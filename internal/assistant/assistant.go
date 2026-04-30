package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/generator"
	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

// ErrAIQuotaExhausted is returned by assistant LLM paths when the W19-23
// global $3/24h AI-spend cap fires before the call could be made. The
// /assistant/converse handler translates this into HTTP 503 with a
// Retry-After header so callers can re-try after the rolling window
// frees up.
//
// QuotaExhaustedError carries the retry-after duration so the API layer
// can render it accurately.
var ErrAIQuotaExhausted = errors.New("assistant: ai quota exhausted")

// QuotaExhaustedError is the typed error returned by assistant LLM paths
// when the global AI-spend cap fires. RetryAfter is the duration until
// the rolling 24h window's oldest entry rolls off.
type QuotaExhaustedError struct {
	RetryAfter time.Duration
}

func (e *QuotaExhaustedError) Error() string {
	return fmt.Sprintf("assistant: ai quota exhausted; retry after %s", e.RetryAfter)
}

func (e *QuotaExhaustedError) Unwrap() error { return ErrAIQuotaExhausted }

// IsAIQuotaExhausted returns true when err (or any wrapped error) is the
// global cap-fire signal. Used by the API layer to render 503.
func IsAIQuotaExhausted(err error) bool {
	return errors.Is(err, ErrAIQuotaExhausted)
}

// pricePhrasePattern strips natural-language budget qualifiers from search
// queries. Budget belongs in BudgetMax; leaving phrases like "under 500" in the
// literal query pollutes marketplace results and defeats title matching.
// Supports EN, NL, and BG Cyrillic prefixes (XOL-39 M3-E).
//
//	EN:  under, below, less than, up to, max, maximum, above, over, more than, min, minimum
//	NL:  onder, tot, maximaal
//	BG:  под (under), до (up to), максимум (maximum), над (above), мин (min)
//
// Currency markers: €, $, лв (BGN lev), BGN, EUR, euro, USD.
// Note: Go RE2 does not support lookbehind — Cyrillic word anchoring relies on
// the surrounding whitespace/start-of-string context via the optional \s* below.
var pricePhrasePattern = regexp.MustCompile(`(?i)(under|below|less\s+than|up\s+to|max(?:imum)?|onder|tot|maximaal|above|over|more\s+than|min(?:imum)?|под|до|максимум|над|мин)\s*[€$]?\s*\d+([.,]\d+)?\s*(лв|bgn|eur|euro|usd|\$|€)?`)

// priceWordPattern catches trailing price hints like "500 eur" or "500 лв"
// that lack a leading qualifier. Supports BGN lev (лв) and BGN (XOL-39 M3-E).
// Note: trailing \b is omitted for Cyrillic currency markers because Go RE2
// uses ASCII-only word boundaries — Cyrillic is not a word character.
var priceWordPattern = regexp.MustCompile(`(?i)\b\d+([.,]\d+)?\s*(лв|bgn|eur|euro|euros|usd|\$|€)`)

// conditionWordPattern removes condition qualifiers — those belong in the
// Condition filter, not the free-text query.
var conditionWordPattern = regexp.MustCompile(`(?i)\b(brand\s+new|like\s+new|new\s+in\s+box|nib|mint|used|second\s*hand|refurbished|fair|good)\b`)

// sanitizeSearchQuery trims price/condition qualifiers and collapses whitespace.
// Returns "" if nothing meaningful remains.
func sanitizeSearchQuery(raw string) string {
	q := strings.ToLower(strings.TrimSpace(raw))
	q = pricePhrasePattern.ReplaceAllString(q, " ")
	q = priceWordPattern.ReplaceAllString(q, " ")
	q = conditionWordPattern.ReplaceAllString(q, " ")
	q = strings.Join(strings.Fields(q), " ")
	return q
}

// isQueryTooBroad rejects category-level queries (single generic noun, or two
// generic words with no distinctive identifier). These produce enormous volumes
// of unrelated results on marketplaces and should be replaced by the main
// target query.
func isQueryTooBroad(q string) bool {
	broad := map[string]bool{
		"laptop": true, "notebook": true, "computer": true, "pc": true,
		"camera": true, "lens": true, "mirrorless": true, "dslr": true,
		"smartphone": true, "phone": true, "tablet": true,
		"tv": true, "television": true, "monitor": true,
		"headphones": true, "earbuds": true, "headset": true,
		"console": true, "gpu": true, "cpu": true,
	}
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return true
	}
	// If every token is broad/generic, reject.
	for _, t := range tokens {
		if !broad[t] {
			return false
		}
	}
	return true
}

const (
	defaultMatchLimit      = 5
	maxListingsToScoreLive = 30 // cap per query for live assistant calls; background workers score all
)

type messageLocale string

const (
	localeEN messageLocale = "en"
	localeNL messageLocale = "nl"
	localeBG messageLocale = "bg"
)

// UsageCallback is called after each LLM request with token counts and timing.
type UsageCallback func(userID string, missionID int64, callType, model string, promptTokens, completionTokens, latencyMs int, success bool, errMsg string)

type Assistant struct {
	cfg      *config.Config
	store    store.Store
	searcher marketplace.Marketplace
	scorer   *scorer.Scorer
	client   *http.Client
	onUsage  UsageCallback
	// Per-call-site model overrides (XOL-60 SUP-9); fall through to cfg.AI.Model.
	modelBrief     string // AI_MODEL_ASSISTANT_BRIEF (parseBriefWithAI)
	modelDraft     string // AI_MODEL_ASSISTANT_DRAFT (draftWithAI)
	modelChat      string // AI_MODEL_ASSISTANT_CHAT  (compareWithAI / chatText)
	modelGenerator string // AI_MODEL_GENERATOR       (EnsureSearchVariants — W19-32)
}

func New(cfg *config.Config, st store.Store, searcher marketplace.Marketplace, sc *scorer.Scorer) *Assistant {
	return &Assistant{
		cfg:            cfg,
		store:          st,
		searcher:       searcher,
		scorer:         sc,
		modelBrief:     cfg.AI.Model,
		modelDraft:     cfg.AI.Model,
		modelChat:      cfg.AI.Model,
		modelGenerator: cfg.AI.Model,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// SetModels sets the per-call-site model overrides for the three assistant
// LLM paths. Empty strings fall through to cfg.AI.Model. Call after New().
// Maps: brief=AI_MODEL_ASSISTANT_BRIEF, draft=AI_MODEL_ASSISTANT_DRAFT,
// chat=AI_MODEL_ASSISTANT_CHAT (XOL-60 SUP-9).
func (a *Assistant) SetModels(brief, draft, chat string) {
	if brief != "" {
		a.modelBrief = brief
	}
	if draft != "" {
		a.modelDraft = draft
	}
	if chat != "" {
		a.modelChat = chat
	}
}

// SetGeneratorModel sets the AI_MODEL_GENERATOR per-call-site override used
// by EnsureSearchVariants. Empty string falls through to cfg.AI.Model.
// Wired alongside SetModels in cmd/server/main.go (XOL-60 SUP-9 / W19-32).
func (a *Assistant) SetGeneratorModel(model string) {
	if model != "" {
		a.modelGenerator = model
	}
}

func (a *Assistant) SetUsageCallback(cb UsageCallback) { a.onUsage = cb }

func (a *Assistant) reportUsage(userID string, missionID int64, callType, model string, prompt, completion, latencyMs int, success bool, errMsg string) {
	if a.onUsage != nil {
		a.onUsage(userID, missionID, callType, model, prompt, completion, latencyMs, success, errMsg)
	}
}

func (a *Assistant) UpsertBrief(ctx context.Context, userID, prompt string) (*models.Mission, error) {
	mission, err := a.parseBrief(ctx, userID, prompt)
	if err != nil {
		return nil, err
	}
	if user, uerr := a.store.GetUserByID(userID); uerr == nil {
		a.applyUserMissionDefaults(user, mission)
	}
	_ = a.EnsureSearchVariants(ctx, mission)

	id, err := a.store.UpsertMission(*mission)
	if err != nil {
		return nil, err
	}
	mission.ID = id
	_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, fmt.Sprintf("mission:%d", id))
	return mission, nil
}

func (a *Assistant) Converse(ctx context.Context, userID, message string) (*models.AssistantReply, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return &models.AssistantReply{
			Message: "What are you shopping for? Give me the item and a rough budget — even a vague idea is enough to start.",
		}, nil
	}

	session, err := a.store.GetAssistantSession(userID)
	if err != nil {
		return nil, err
	}
	if session != nil && session.PendingIntent == models.IntentCreateBrief && session.DraftMission != nil {
		return a.continueBriefConversation(ctx, *session, message)
	}

	lower := strings.ToLower(message)

	// Handle affirmative/negative replies to "want me to show matches?"
	if session != nil && session.PendingIntent == models.IntentShowMatches {
		if isAffirmative(lower) {
			_ = a.store.ClearAssistantSession(userID)
			recs, mission, matchErr := a.FindMatches(ctx, userID, 5, 0)
			if matchErr != nil || mission == nil {
				return &models.AssistantReply{
					Message: "I couldn't pull matches right now — the market may be light. Your hunts will surface deals automatically as they come in.",
				}, nil
			}
			return &models.AssistantReply{
				Message:         renderConversationMatches(mission.Name, recs),
				Intent:          models.IntentShowMatches,
				Mission:         mission,
				Recommendations: recs,
			}, nil
		}
		if isNegative(lower) {
			_ = a.store.ClearAssistantSession(userID)
			return &models.AssistantReply{
				Message: "No problem — your monitors are running. Head to Matches to see deals as they land, or come back here anytime to pull up current matches.",
			}, nil
		}
	}

	switch {
	// W19-33 / XOL-130: split-threshold guard. Bare token "help" is the only
	// ambiguous match — it false-positives on natural phrases like "Help me
	// find a Fujifilm X-T4 in Bulgaria, budget around 1200 euros" which would
	// route to the help-template instead of startBriefConversation. Gate bare
	// "help" on message brevity (≤ 3 words) so longer messages fall through.
	// The multi-word phrases "what can you do" and "how do i use" are
	// unambiguous (complete questions, not partial token matches) and can
	// fire on any-length input that contains them as substrings.
	case (len(strings.Fields(message)) <= 3 && containsAny(lower, "help")) ||
		containsAny(lower, "what can you do", "how do i use"):
		return &models.AssistantReply{
			Message: "I help you find second-hand deals before anyone else does. Tell me what you're after — item, budget, condition — and I'll build a buy mission, scan the market, and tell you which listings are actually worth your time. You can also ask me to show current matches or compare your shortlist.",
			Intent:  models.IntentCreateBrief,
		}, nil
	case containsAny(lower, "show matches", "find matches", "what did you find", "any matches", "matches"):
		recs, mission, err := a.FindMatches(ctx, userID, 5, 0)
		if err != nil {
			return &models.AssistantReply{
				Message:   "I don't have a brief on file yet. Tell me what you're looking for — item, budget, and preferred condition — and I'll get searching.",
				Expecting: true,
				Intent:    models.IntentCreateBrief,
			}, nil
		}
		return &models.AssistantReply{
			Message:         renderConversationMatches(mission.Name, recs),
			Intent:          models.IntentShowMatches,
			Mission:         mission,
			Recommendations: recs,
		}, nil
	case containsAny(lower, "compare shortlist", "compare my shortlist", "compare saved", "compare"):
		comparison, err := a.CompareShortlist(ctx, userID)
		if err != nil {
			return &models.AssistantReply{
				Message:   "Your shortlist is empty — nothing saved yet. Ask me for matches first, save the interesting ones, and then I can compare them for you.",
				Expecting: true,
				Intent:    models.IntentShortlist,
			}, nil
		}
		return &models.AssistantReply{Message: comparison, Intent: models.IntentCompare}, nil
	default:
		return a.startBriefConversation(ctx, userID, message)
	}
}

func (a *Assistant) GetActiveMission(userID string) (*models.Mission, error) {
	return a.store.GetActiveMission(userID)
}

func (a *Assistant) GetActiveProfile(userID string) (*models.Mission, error) {
	return a.GetActiveMission(userID)
}

func (a *Assistant) FindMatches(ctx context.Context, userID string, limit int, missionID int64) ([]models.Recommendation, *models.Mission, error) {
	var (
		mission *models.Mission
		err     error
	)
	if missionID > 0 {
		mission, err = a.store.GetMission(missionID)
	} else {
		mission, err = a.store.GetActiveMission(userID)
	}
	if err != nil {
		return nil, nil, err
	}
	if mission == nil {
		return nil, nil, fmt.Errorf("no active shopping brief found")
	}
	if mission.UserID != "" && mission.UserID != userID {
		return nil, nil, fmt.Errorf("mission does not belong to user")
	}
	if limit <= 0 {
		limit = defaultMatchLimit
	}

	searches := a.searchConfigsForMission(*mission)
	seen := map[string]struct{}{}
	var recs []models.Recommendation
	for _, searchCfg := range searches {
		listings, err := a.searcher.Search(ctx, searchCfg)
		if err != nil {
			continue
		}
		if len(listings) > maxListingsToScoreLive {
			listings = listings[:maxListingsToScoreLive]
		}
		for _, listing := range listings {
			if _, exists := seen[listing.ItemID]; exists {
				continue
			}
			seen[listing.ItemID] = struct{}{}
			listing.ProfileID = mission.ID
			scored := a.scorer.Score(ctx, listing, searchCfg)
			rec := buildRecommendation(scored, *mission)
			if rec.Label == models.RecommendationSkip {
				continue
			}
			recs = append(recs, rec)
		}
	}

	slices.SortFunc(recs, func(a, b models.Recommendation) int {
		if a.Scored.Score == b.Scored.Score {
			if a.FitScore > b.FitScore {
				return -1
			}
			if a.FitScore < b.FitScore {
				return 1
			}
			return 0
		}
		if a.Scored.Score > b.Scored.Score {
			return -1
		}
		return 1
	})

	if len(recs) > limit {
		recs = recs[:limit]
	}
	return recs, mission, nil
}

func (a *Assistant) ExplainListing(ctx context.Context, userID, itemID string) (string, error) {
	rec, _, err := a.findRecommendationByItemID(ctx, userID, itemID, 0)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", fmt.Errorf("listing %s not found in active matches", itemID)
	}

	return formatRecommendationDetail(*rec), nil
}

func (a *Assistant) SaveToShortlist(ctx context.Context, userID, itemID string) (*models.ShortlistEntry, error) {
	rec, mission, err := a.resolveRecommendation(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	if rec == nil || mission == nil {
		return nil, fmt.Errorf("listing %s not found in active matches", itemID)
	}

	entry := models.ShortlistEntry{
		UserID:              userID,
		MissionID:           mission.ID,
		ItemID:              rec.Listing.ItemID,
		Title:               rec.Listing.Title,
		URL:                 rec.Listing.URL,
		RecommendationLabel: rec.Label,
		RecommendationScore: rec.Scored.Score,
		AskPrice:            rec.Listing.Price,
		FairPrice:           rec.Scored.FairPrice,
		Verdict:             rec.Verdict,
		Concerns:            rec.Concerns,
		SuggestedQuestions:  rec.NextQuestions,
		Status:              "watching",
	}
	if err := a.store.SaveShortlistEntry(entry); err != nil {
		return nil, err
	}
	return a.store.GetShortlistEntry(userID, itemID)
}

// resolveRecommendation resolves a listing itemID to a Recommendation, preferring
// the persisted listings table over a live marketplace scrape. The live scrape
// path only surfaces the top N matches within the current polling window, so
// listings the user sees in the UI (loaded from the DB) may not be present in
// that live view. Falling back to live scrape covers the edge case where a
// listing was never persisted.
func (a *Assistant) resolveRecommendation(ctx context.Context, userID, itemID string) (*models.Recommendation, *models.Mission, error) {
	listing, err := a.store.GetListing(userID, itemID)
	if err == nil && listing != nil {
		var mission *models.Mission
		if listing.ProfileID > 0 {
			if loaded, err := a.store.GetMission(listing.ProfileID); err == nil && loaded != nil && loaded.UserID == userID {
				mission = loaded
			}
		}
		if mission == nil {
			mission, _ = a.store.GetActiveMission(userID)
		}
		if mission != nil {
			scored := models.ScoredListing{
				Listing:    *listing,
				Score:      listing.Score,
				FairPrice:  listing.FairPrice,
				OfferPrice: listing.OfferPrice,
				Confidence: listing.Confidence,
				Reason:     listing.Reason,
				RiskFlags:  listing.RiskFlags,
			}
			rec := buildRecommendation(scored, *mission)
			return &rec, mission, nil
		}
	}
	return a.findRecommendationByItemID(ctx, userID, itemID, 0)
}

func (a *Assistant) ListShortlist(userID string) ([]models.ShortlistEntry, error) {
	return a.store.GetShortlist(userID)
}

func (a *Assistant) CompareShortlist(ctx context.Context, userID string) (string, error) {
	entries, err := a.store.GetShortlist(userID)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("shortlist is empty")
	}

	if a.aiEnabled() {
		if comparison, err := a.compareWithAI(ctx, userID, entries); err == nil && comparison != "" {
			return comparison, nil
		}
	}

	var b strings.Builder
	b.WriteString("Shortlist comparison:\n")
	for i, entry := range entries {
		fmt.Fprintf(&b, "%d. %s [%s] ask=%s fair=%s\n", i+1, entry.Title, entry.RecommendationLabel, formatEuro(entry.AskPrice), formatEuro(entry.FairPrice))
		if entry.Verdict != "" {
			fmt.Fprintf(&b, "   %s\n", entry.Verdict)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func (a *Assistant) DraftSellerMessage(ctx context.Context, userID, itemID string) (*models.ActionDraft, error) {
	entry, err := a.store.GetShortlistEntry(userID, itemID)
	if err != nil {
		return nil, err
	}
	if entry == nil {
		if _, err := a.SaveToShortlist(ctx, userID, itemID); err != nil {
			return nil, err
		}
		entry, err = a.store.GetShortlistEntry(userID, itemID)
		if err != nil {
			return nil, err
		}
	}

	var mission *models.Mission
	if entry.MissionID > 0 {
		if loaded, err := a.store.GetMission(entry.MissionID); err == nil && loaded != nil && loaded.UserID == userID {
			mission = loaded
		}
	}

	marketplaceID := detectEntryMarketplaceID(*entry)
	locale := localeForMarketplace(marketplaceID)
	customTemplate := a.resolveMessageTemplate(userID, entry.MissionID, marketplaceID)

	content := buildHeuristicDraft(*entry, mission, locale)
	if customTemplate != "" {
		if rendered, err := renderSellerTemplate(customTemplate, *entry); err == nil && rendered != "" {
			content = rendered
		}
	} else if a.aiEnabled() {
		var riskFlags []string
		if listing, lookupErr := a.store.GetListing(userID, entry.ItemID); lookupErr == nil {
			riskFlags = listing.RiskFlags
		}
		if aiDraft, err := a.draftWithAI(ctx, userID, *entry, marketplaceID, locale, riskFlags); err == nil && aiDraft != "" {
			content = aiDraft
		}
	}

	draft := models.ActionDraft{
		UserID:     userID,
		ItemID:     itemID,
		ActionType: "seller_message_draft",
		Content:    content,
		Status:     "draft",
	}
	if err := a.store.SaveActionDraft(draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

func (a *Assistant) resolveMessageTemplate(userID string, missionID int64, marketplaceID string) string {
	specs, err := a.store.GetSearchConfigs(userID)
	if err != nil {
		return ""
	}
	mp := marketplace.NormalizeMarketplaceID(marketplaceID)
	if mp == "" {
		return ""
	}

	// Prefer exact mission + exact marketplace, then broader fallbacks.
	for _, spec := range specs {
		if spec.ProfileID == missionID && marketplace.NormalizeMarketplaceID(spec.MarketplaceID) == mp {
			if tmpl := searchTemplateForMarketplace(spec, mp); tmpl != "" {
				return tmpl
			}
		}
	}
	for _, spec := range specs {
		if marketplace.NormalizeMarketplaceID(spec.MarketplaceID) == mp {
			if tmpl := searchTemplateForMarketplace(spec, mp); tmpl != "" {
				return tmpl
			}
		}
	}
	if missionID > 0 {
		for _, spec := range specs {
			if spec.ProfileID == missionID {
				if tmpl := searchTemplateForMarketplace(spec, mp); tmpl != "" {
					return tmpl
				}
			}
		}
	}
	return ""
}

func searchTemplateForMarketplace(spec models.SearchSpec, marketplaceID string) string {
	keys := []string{
		"message_template_" + marketplaceID,
	}
	if strings.HasPrefix(marketplaceID, "vinted_") {
		keys = append(keys, "message_template_vinted")
	}
	switch localeForMarketplace(marketplaceID) {
	case localeNL:
		keys = append(keys, "message_template_nl")
	case localeBG:
		keys = append(keys, "message_template_bg")
	}
	for _, key := range keys {
		if spec.Attributes == nil {
			break
		}
		if value := strings.TrimSpace(spec.Attributes[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(spec.MessageTemplate)
}

func detectEntryMarketplaceID(entry models.ShortlistEntry) string {
	rawURL := strings.ToLower(strings.TrimSpace(entry.URL))
	switch {
	case strings.Contains(rawURL, "marktplaats.nl"):
		return "marktplaats"
	case strings.Contains(rawURL, "olx.bg"):
		return "olxbg"
	case strings.Contains(rawURL, "vinted.nl"):
		return "vinted_nl"
	case strings.Contains(rawURL, "vinted.dk"):
		return "vinted_dk"
	case strings.Contains(rawURL, "vinted."):
		return "vinted"
	}

	itemID := strings.ToLower(strings.TrimSpace(entry.ItemID))
	switch {
	case strings.HasPrefix(itemID, "olxbg_"):
		return "olxbg"
	case strings.HasPrefix(itemID, "vinted_nl_"):
		return "vinted_nl"
	case strings.HasPrefix(itemID, "vinted_dk_"):
		return "vinted_dk"
	case strings.HasPrefix(itemID, "vinted_"):
		return "vinted"
	default:
		return "marktplaats"
	}
}

func localeForMarketplace(marketplaceID string) messageLocale {
	switch marketplace.NormalizeMarketplaceID(marketplaceID) {
	case "marktplaats", "vinted_nl":
		return localeNL
	case "olxbg":
		return localeBG
	default:
		return localeEN
	}
}

func localeLabel(locale messageLocale) string {
	switch locale {
	case localeNL:
		return "Dutch"
	case localeBG:
		return "Bulgarian"
	default:
		return "English"
	}
}

func renderSellerTemplate(tmpl string, entry models.ShortlistEntry) (string, error) {
	t, err := template.New("seller-message").Parse(strings.TrimSpace(tmpl))
	if err != nil {
		return "", err
	}
	offerAmt := suggestedOfferAmount(entry)
	data := map[string]string{
		"Title":          entry.Title,
		"OfferPrice":     fmt.Sprintf("%.2f", float64(offerAmt)/100),
		"OfferPriceEuro": formatEuro(offerAmt),
		"AskPrice":       fmt.Sprintf("%.2f", float64(entry.AskPrice)/100),
		"AskPriceEuro":   formatEuro(entry.AskPrice),
		"FairPrice":      fmt.Sprintf("%.2f", float64(entry.FairPrice)/100),
		"FairPriceEuro":  formatEuro(entry.FairPrice),
		"Score":          fmt.Sprintf("%.1f", entry.RecommendationScore),
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

func suggestedOfferAmount(entry models.ShortlistEntry) int {
	offerAmt := entry.FairPrice
	if offerAmt <= 0 {
		offerAmt = entry.AskPrice
	}
	if entry.AskPrice > 0 && entry.AskPrice < offerAmt {
		offerAmt = entry.AskPrice
	}
	return offerAmt
}

func (a *Assistant) startBriefConversation(ctx context.Context, userID, prompt string) (*models.AssistantReply, error) {
	mission, err := a.parseBrief(ctx, userID, prompt)
	if err != nil {
		return nil, err
	}
	if user, uerr := a.store.GetUserByID(userID); uerr == nil {
		a.applyUserMissionDefaults(user, mission)
	}
	if mission.BudgetStretch == 0 && mission.BudgetMax > 0 {
		mission.BudgetStretch = mission.BudgetMax
	}

	// W19-34 / XOL-131: diagnostic for slot-extraction failures during VAL-3
	// cohort. When the brief parser asks for a slot the user already provided
	// in the raw input, we need attribution signal in production logs.
	question, key := nextProfileQuestion(*mission)
	if question != "" {
		slog.Info("brief parser asking for missing slot",
			"op", "assistant.startBriefConversation.slot_missing",
			"user_id", userID,
			"missing_slot", key,
			"raw_input_chars", len(prompt),
			"extracted_budget_max", mission.BudgetMax,
			"extracted_target_query", mission.TargetQuery,
			"ai_path_used", a.aiEnabled())
		session := models.AssistantSession{
			UserID:           userID,
			PendingIntent:    models.IntentCreateBrief,
			PendingQuestion:  key,
			DraftMission:     mission,
			LastAssistantMsg: question,
		}
		if err := a.store.SaveAssistantSession(session); err != nil {
			return nil, err
		}
		_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, question)
		return &models.AssistantReply{
			Message:   question,
			Expecting: true,
			Intent:    models.IntentCreateBrief,
			Mission:   mission,
		}, nil
	}
	_ = a.EnsureSearchVariants(ctx, mission)

	id, err := a.store.UpsertMission(*mission)
	if err != nil {
		return nil, err
	}
	mission.ID = id

	huntCount, _ := a.AutoDeployHunts(ctx, userID, *mission)
	recs, _, _ := a.FindMatches(ctx, userID, defaultMatchLimit, mission.ID)

	var huntMsg string
	switch {
	case huntCount == 1:
		huntMsg = "I've activated 1 monitor. It will scan every few minutes."
	case huntCount > 1:
		huntMsg = fmt.Sprintf("I've activated %d monitors across the market.", huntCount)
	default:
		huntMsg = "Your existing monitors will pick this up automatically."
	}

	reply := fmt.Sprintf("Mission saved for %s. %s\n\nHere's what's available right now:", mission.Name, huntMsg)
	_ = a.store.ClearAssistantSession(userID)
	_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, reply)
	return &models.AssistantReply{
		Message:         reply,
		Expecting:       false,
		Intent:          models.IntentShowMatches,
		Mission:         mission,
		Recommendations: recs,
	}, nil
}

func (a *Assistant) continueBriefConversation(ctx context.Context, session models.AssistantSession, answer string) (*models.AssistantReply, error) {
	mission := session.DraftMission
	if mission == nil {
		return a.startBriefConversation(ctx, session.UserID, answer)
	}

	applyAnswerToProfile(mission, session.PendingQuestion, answer, a.cfg.Marktplaats)
	if user, uerr := a.store.GetUserByID(session.UserID); uerr == nil {
		a.applyUserMissionDefaults(user, mission)
	}
	// W19-34 / XOL-131: diagnostic for slot-extraction failures during VAL-3
	// cohort. When the brief parser asks for a slot the user already provided
	// in the raw input, we need attribution signal in production logs.
	question, key := nextProfileQuestion(*mission)
	if question != "" {
		slog.Info("brief parser asking for missing slot",
			"op", "assistant.continueBriefConversation.slot_missing",
			"user_id", session.UserID,
			"missing_slot", key,
			"raw_input_chars", len(answer),
			"extracted_budget_max", mission.BudgetMax,
			"extracted_target_query", mission.TargetQuery,
			"ai_path_used", a.aiEnabled())
		session.PendingQuestion = key
		session.DraftMission = mission
		session.LastAssistantMsg = question
		if err := a.store.SaveAssistantSession(session); err != nil {
			return nil, err
		}
		_ = a.store.SaveConversationArtifact(session.UserID, models.IntentRefineBrief, answer, question)
		return &models.AssistantReply{
			Message:   question,
			Expecting: true,
			Intent:    models.IntentRefineBrief,
			Mission:   mission,
		}, nil
	}
	_ = a.EnsureSearchVariants(ctx, mission)

	id, err := a.store.UpsertMission(*mission)
	if err != nil {
		return nil, err
	}
	mission.ID = id
	_ = a.store.ClearAssistantSession(session.UserID)

	huntCount, _ := a.AutoDeployHunts(ctx, session.UserID, *mission)
	recs, _, matchErr := a.FindMatches(ctx, session.UserID, 3, mission.ID)
	reply := fmt.Sprintf("Done — mission locked in for %s.", mission.Name)
	if huntCount > 0 {
		reply += fmt.Sprintf(" I activated %d monitor(s) for it.", huntCount)
	}
	if matchErr == nil && len(recs) > 0 {
		reply += "\n\n" + renderConversationMatches(mission.Name, recs)
		reply += "\n\nLet me know if you want to save any of these, tighten the budget, or focus on a specific model."
	} else {
		reply += " The monitors are set up — head to Matches to catch new listings as they come in."
	}
	_ = a.store.SaveConversationArtifact(session.UserID, models.IntentRefineBrief, answer, reply)
	return &models.AssistantReply{
		Message:         reply,
		Intent:          models.IntentShowMatches,
		Mission:         mission,
		Recommendations: recs,
	}, nil
}

func (a *Assistant) searchConfigsForMission(mission models.Mission) []models.SearchSpec {
	queries := mission.SearchQueries
	if len(queries) == 0 && mission.TargetQuery != "" {
		queries = []string{mission.TargetQuery}
	}
	if len(queries) == 0 {
		queries = []string{mission.Name}
	}

	conditions := mission.PreferredCondition
	if len(conditions) == 0 {
		conditions = []string{"good", "like_new"}
	}

	searches := make([]models.SearchSpec, 0, len(queries))
	scope := mission.MarketplaceScope
	if len(scope) == 0 {
		scope = marketplace.ValidateScope(mission.CountryCode, mission.CrossBorderEnabled, nil)
	}
	for _, query := range queries {
		for _, marketplaceID := range scope {
			searches = append(searches, models.SearchSpec{
				Name:            mission.Name,
				Query:           query,
				MarketplaceID:   marketplaceID,
				ProfileID:       mission.ID,
				CountryCode:     mission.CountryCode,
				City:            mission.City,
				PostalCode:      mission.PostalCode,
				RadiusKm:        mission.TravelRadius,
				CategoryID:      mission.CategoryID,
				Category:        mission.Category,
				MaxPrice:        mission.BudgetStretch * 100,
				MinPrice:        0,
				Condition:       conditions,
				OfferPercentage: 72,
				AutoMessage:     false,
			})
		}
	}
	return searches
}

// AutoDeployHunts creates SearchSpec records for a mission.
// It skips query+marketplace combinations that already exist.
func (a *Assistant) AutoDeployHunts(ctx context.Context, userID string, mission models.Mission) (int, error) {
	_ = ctx
	user, err := a.store.GetUserByID(userID)
	if err != nil || user == nil {
		return 0, err
	}
	a.applyUserMissionDefaults(user, &mission)

	// W19-35 / XOL-132: dedup scope is (mission_id, query, marketplace), NOT
	// (user_id, query, marketplace). Two distinct missions for the same user
	// can legitimately have overlapping queries (e.g. user creates one mission
	// for "canon eos r6" and another for "canon eos r6 mark ii" — the
	// "canon eos r6" query appears in both). The old user-wide dedup made
	// mission_2 silently lose its search_configs. Scope to the current mission
	// so AutoDeployHunts only suppresses re-creating the SAME search inside
	// the SAME mission across multiple deploys (e.g. mission edit + re-deploy).
	existing, _ := a.store.GetSearchConfigs(userID)
	existingKeys := make(map[string]bool, len(existing))
	for _, s := range existing {
		// Only dedupe against search_configs for THIS mission (ProfileID == mission.ID).
		if s.ProfileID == mission.ID {
			existingKeys[strings.ToLower(s.Query)+"|"+marketplace.NormalizeMarketplaceID(s.MarketplaceID)] = true
		}
	}

	rawQueries := mission.SearchQueries
	if len(rawQueries) == 0 && mission.TargetQuery != "" {
		rawQueries = []string{mission.TargetQuery}
	}
	if len(rawQueries) == 0 {
		rawQueries = []string{mission.Name}
	}

	// Always include the mission's primary target query — it is the most
	// specific anchor and the LLM sometimes omits it from search_queries.
	if strings.TrimSpace(mission.TargetQuery) != "" {
		rawQueries = append([]string{mission.TargetQuery}, rawQueries...)
	}

	// Sanitize + dedupe. Drop broad category-only queries that produce noise.
	queries := make([]string, 0, len(rawQueries))
	seenQ := make(map[string]bool, len(rawQueries))
	for _, q := range rawQueries {
		cleaned := sanitizeSearchQuery(q)
		if cleaned == "" || isQueryTooBroad(cleaned) {
			continue
		}
		if seenQ[cleaned] {
			continue
		}
		seenQ[cleaned] = true
		queries = append(queries, cleaned)
	}
	if len(queries) == 0 {
		// Last-ditch fallback so the mission still deploys something useful.
		fallback := sanitizeSearchQuery(mission.Name)
		if fallback != "" {
			queries = []string{fallback}
		}
	}

	interval := intervalForTier(user.Tier)
	marketplaces := mission.MarketplaceScope
	if len(marketplaces) == 0 {
		marketplaces = marketplace.ValidateScope(mission.CountryCode, mission.CrossBorderEnabled, nil)
	}

	count := 0
	for _, query := range queries {
		for _, mp := range marketplaces {
			mp = marketplace.NormalizeMarketplaceID(mp)
			key := strings.ToLower(query) + "|" + mp
			if existingKeys[key] {
				continue
			}
			maxPrice := mission.BudgetStretch
			if maxPrice == 0 {
				maxPrice = mission.BudgetMax
			}
			spec := models.SearchSpec{
				UserID:          userID,
				ProfileID:       mission.ID,
				Name:            mission.Name,
				Query:           query,
				MarketplaceID:   mp,
				CountryCode:     mission.CountryCode,
				City:            mission.City,
				PostalCode:      mission.PostalCode,
				RadiusKm:        mission.TravelRadius,
				CategoryID:      mission.CategoryID,
				Category:        mission.Category,
				MaxPrice:        maxPrice * 100,
				Condition:       mission.PreferredCondition,
				CheckInterval:   interval,
				NextRunAt:       time.Now().UTC(),
				OfferPercentage: 72,
				Enabled:         true,
			}
			if _, err := a.store.CreateSearchConfig(spec); err == nil {
				count++
			}
		}
	}
	return count, nil
}

func intervalForTier(tier string) time.Duration {
	return time.Duration(billing.LimitsFor(tier).MinCheckIntervalMins) * time.Minute
}

// EnsureSearchVariants populates mission.SearchQueries with 3-5 variants when
// adequate coverage is missing. Mutates the mission in-place. Called from both
// the HTTP handler path (handleMissions POST) and the assistant chat path
// (parseBrief / startBrief / continueBrief) so all mission-creation paths
// produce the same auto-expanded shape.
//
// Contract:
//   - If len(mission.SearchQueries) >= 3, return nil (already adequate)
//   - If mission.TargetQuery == "", return nil (nothing to expand from)
//   - Else: invoke generator.GenerateSearches(ctx, mission.TargetQuery), dedupe
//     against existing entries (case-insensitive trim), take up to 5 total
//   - Hard-cap at 5 (founder-locked, W19-31 directive)
//   - On error (cap-fire / LLM failure): return nil after slog.Warn for
//     VAL-3 cohort attribution; mission still creates with whatever
//     SearchQueries it had (graceful degradation per W19-23 contract)
//
// AI-budget gate: handled internally by generator.GenerateSearches via
// aibudget.Allow("generator", ...) at internal/generator/generator.go:160.
func (a *Assistant) EnsureSearchVariants(ctx context.Context, mission *models.Mission) error {
	if mission == nil {
		return nil
	}
	if len(mission.SearchQueries) >= 3 {
		return nil
	}
	target := strings.TrimSpace(mission.TargetQuery)
	if target == "" {
		return nil
	}
	aiCfg := config.AIConfig{
		Enabled: a.cfg.AI.APIKey != "",
		BaseURL: a.cfg.AI.BaseURL,
		APIKey:  a.cfg.AI.APIKey,
		Model:   a.cfg.AI.Model,
	}
	gen := generator.New(aiCfg)
	// Preserve the AI_MODEL_GENERATOR per-call-site override (XOL-60 SUP-9).
	// PR #48 wired this at the handler call site; the W19-32 refactor lost it
	// and pr-reviewer flagged the regression. Empty string is fine — generator
	// falls back to aiCfg.Model.
	if a.modelGenerator != "" {
		gen.SetModel(a.modelGenerator)
	}
	if a.onUsage != nil {
		cb := a.onUsage
		userID := mission.UserID
		missionID := mission.ID
		gen.SetUsageCallback(func(callType, model string, prompt, completion, latencyMs int, success bool, errMsg string) {
			cb(userID, missionID, callType, model, prompt, completion, latencyMs, success, errMsg)
		})
	}
	genSearches, err := gen.GenerateSearches(ctx, target)
	if err != nil {
		// W19-37 / XOL-134: gen.GenerateSearches returns BOTH the fallback
		// entries AND a wrapped error when AI fails (see generator.go:62-67).
		// The prior implementation discarded the fallback — leaving missions
		// with 1 chip when AI was flaky. Now: log the warning, then USE the
		// fallback if it's non-empty. Only skip when there's truly nothing
		// to expand from (genSearches is nil or empty).
		slog.Warn("mission auto-expand AI path failed; using fallback",
			"op", "assistant.EnsureSearchVariants",
			"user_id", mission.UserID,
			"target_query", target,
			"fallback_count", len(genSearches),
			"error", err.Error())
		if len(genSearches) == 0 {
			return nil
		}
		// Fall through to the dedup loop below — fallback entries get
		// merged into out the same way AI-success entries would.
	}
	// Dedupe against existing SearchQueries (case-insensitive trim).
	seen := make(map[string]bool, len(mission.SearchQueries)+len(genSearches))
	out := make([]string, 0, 5)
	for _, q := range mission.SearchQueries {
		cleaned := strings.TrimSpace(q)
		if cleaned == "" {
			continue
		}
		key := strings.ToLower(cleaned)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cleaned)
	}
	for _, sc := range genSearches {
		cleaned := strings.TrimSpace(sc.Query)
		if cleaned == "" {
			continue
		}
		key := strings.ToLower(cleaned)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cleaned)
		if len(out) >= 5 {
			break
		}
	}
	// W19-37 / XOL-134: floor enforcement. Brief locks 3-5 variants per
	// mission. If dedup left us with <3 entries (common when generator
	// returns 1 generic entry, or fallback returned a small set), synthesize
	// trailing variants from common buying-context tokens. Deterministic,
	// no LLM call, no aibudget cost. Skip synthesis when target is empty
	// (defensive: shouldn't happen since we checked at function entry).
	if len(out) < 3 && target != "" {
		floorTokens := []string{"used", "for sale", "deals"}
		for _, token := range floorTokens {
			if len(out) >= 3 {
				break
			}
			candidate := strings.TrimSpace(target + " " + token)
			key := strings.ToLower(candidate)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, candidate)
		}
	}
	if len(out) > 5 {
		out = out[:5]
	}
	if len(out) > 0 {
		mission.SearchQueries = out
	}
	return nil
}

func (a *Assistant) applyUserMissionDefaults(user *models.User, mission *models.Mission) {
	if user == nil || mission == nil {
		return
	}
	if mission.CountryCode == "" {
		mission.CountryCode = user.CountryCode
	}
	if mission.Region == "" {
		mission.Region = user.Region
	}
	if mission.City == "" {
		mission.City = user.City
	}
	if mission.PostalCode == "" {
		mission.PostalCode = user.PostalCode
	}
	if mission.ZipCode == "" {
		if mission.PostalCode != "" {
			mission.ZipCode = mission.PostalCode
		} else {
			mission.ZipCode = a.cfg.Marktplaats.ZipCode
		}
	}
	if mission.TravelRadius == 0 {
		switch {
		case user.PreferredRadiusKm > 0:
			mission.TravelRadius = user.PreferredRadiusKm
		case mission.Distance > 0:
			mission.TravelRadius = mission.Distance / 1000
		case a.cfg.Marktplaats.Distance > 0:
			mission.TravelRadius = a.cfg.Marktplaats.Distance / 1000
		}
	}
	if mission.Distance == 0 && mission.TravelRadius > 0 {
		mission.Distance = mission.TravelRadius * 1000
	}
	if len(mission.MarketplaceScope) == 0 {
		mission.MarketplaceScope = marketplace.ValidateScope(mission.CountryCode, mission.CrossBorderEnabled, nil)
	}
}

func (a *Assistant) findRecommendationByItemID(ctx context.Context, userID, itemID string, missionID int64) (*models.Recommendation, *models.Mission, error) {
	recs, mission, err := a.FindMatches(ctx, userID, 15, missionID)
	if err != nil {
		return nil, nil, err
	}
	for _, rec := range recs {
		if rec.Listing.ItemID == itemID {
			recCopy := rec
			return &recCopy, mission, nil
		}
	}
	return nil, mission, nil
}

func buildRecommendation(scored models.ScoredListing, mission models.Mission) models.Recommendation {
	fitScore := scoreFit(scored.Listing, mission)
	concerns := collectConcerns(scored.Listing, mission, scored)
	questions := buildQuestions(scored.Listing, concerns)

	label := models.RecommendationSkip
	switch {
	case scored.Listing.Price <= 0:
		label = models.RecommendationSkip
	case scored.Score >= 8.0 && fitScore >= 0.65 && len(concerns) <= 1:
		label = models.RecommendationBuyNow
	case scored.Score >= 6.5 && fitScore >= 0.45:
		label = models.RecommendationWatch
	case fitScore >= 0.4:
		label = models.RecommendationAskQuestions
	default:
		label = models.RecommendationSkip
	}

	if len(concerns) > 0 && label == models.RecommendationBuyNow {
		label = models.RecommendationAskQuestions
	}

	verdict := buildVerdict(label, fitScore, scored, concerns)
	return models.Recommendation{
		Listing:        scored.Listing,
		Scored:         scored,
		Mission:        mission,
		Label:          label,
		FitScore:       fitScore,
		Verdict:        verdict,
		Concerns:       concerns,
		NextQuestions:  questions,
		SuggestedOffer: scored.OfferPrice,
	}
}

func scoreFit(listing models.Listing, mission models.Mission) float64 {
	score := 0.4
	text := strings.ToLower(listing.Title + " " + listing.Description)

	for _, feature := range mission.RequiredFeatures {
		if strings.Contains(text, strings.ToLower(feature)) {
			score += 0.15
		} else {
			score -= 0.15
		}
	}
	for _, feature := range mission.NiceToHave {
		if strings.Contains(text, strings.ToLower(feature)) {
			score += 0.05
		}
	}
	if mission.BudgetMax > 0 && listing.Price > 0 && listing.Price <= mission.BudgetMax*100 {
		score += 0.2
	}
	condition := strings.ToLower(listing.Condition)
	for _, preferred := range mission.PreferredCondition {
		if strings.EqualFold(condition, preferred) {
			score += 0.1
			break
		}
	}
	return math.Max(0, math.Min(1, score))
}

func collectConcerns(listing models.Listing, mission models.Mission, scored models.ScoredListing) []string {
	var concerns []string
	text := strings.ToLower(listing.Title + " " + listing.Description)

	if scored.Confidence < 0.5 {
		concerns = append(concerns, "confidence is limited because comparable data is weak")
	}
	if listing.PriceType == "reserved" || listing.PriceType == "fast-bid" || listing.PriceType == "bidding" {
		concerns = append(concerns, "listing does not have a straightforward fixed asking price")
	}
	if strings.Contains(text, "defect") || strings.Contains(text, "not working") ||
		strings.Contains(text, "broken") || strings.Contains(text, "fault") ||
		strings.Contains(text, "gaat niet aan") || strings.Contains(text, "kapot") {
		concerns = append(concerns, "listing may be defective or incomplete")
	}
	for _, required := range mission.RequiredFeatures {
		if !strings.Contains(text, strings.ToLower(required)) {
			concerns = append(concerns, fmt.Sprintf("required feature not clearly confirmed: %s", required))
		}
	}
	if mission.BudgetStretch > 0 && listing.Price > mission.BudgetStretch*100 {
		concerns = append(concerns, "listing is above your stretch budget")
	}
	return concerns
}

func buildQuestions(_ models.Listing, concerns []string) []string {
	questions := []string{
		"Can you confirm everything works as expected — no faults or missing parts?",
		"What accessories and original packaging are included?",
	}
	for _, concern := range concerns {
		switch {
		case strings.Contains(concern, "defective"):
			questions = append(questions, "What exactly is the defect, and what have you already tested?")
		case strings.Contains(concern, "required feature"):
			questions = append(questions, "Could you send a photo or confirmation of the feature I mentioned?")
		case strings.Contains(concern, "above your stretch budget"):
			questions = append(questions, "Is there any flexibility on the asking price?")
		}
	}
	return dedupeStrings(questions)
}

func formatRecommendationDetail(rec models.Recommendation) string {
	lines := []string{
		rec.Listing.Title,
		fmt.Sprintf("Recommendation: %s", formatRecommendationLabel(rec.Label)),
		fmt.Sprintf("Ask price: %s", formatEuro(rec.Listing.Price)),
		fmt.Sprintf("Estimated fair price: %s", formatEuro(rec.Scored.FairPrice)),
		fmt.Sprintf("Fit: %.0f%%", rec.FitScore*100),
		fmt.Sprintf("Why: %s", rec.Verdict),
	}
	if len(rec.Concerns) > 0 {
		lines = append(lines, "Things to check:")
		for _, concern := range rec.Concerns[:minInt(3, len(rec.Concerns))] {
			lines = append(lines, "- "+humanizeConcern(concern))
		}
	}
	if len(rec.NextQuestions) > 0 {
		lines = append(lines, "Suggested seller questions:")
		for _, question := range rec.NextQuestions[:minInt(3, len(rec.NextQuestions))] {
			lines = append(lines, "- "+question)
		}
	}
	return strings.Join(lines, "\n")
}

func renderConversationMatches(missionName string, recs []models.Recommendation) string {
	if len(recs) == 0 {
		return "Nothing strong is showing up for " + missionName + " right now. The market might be thin — keep the monitors running and I'll alert you when something comes in."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Here's what I found for %s:\n\n", missionName)
	for i, rec := range recs {
		label := formatRecommendationLabel(rec.Label)
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, rec.Listing.Title, formatEuro(rec.Listing.Price))
		if rec.Scored.FairPrice > 0 {
			fmt.Fprintf(&b, "   Fair value ≈ %s · %s\n", formatEuro(rec.Scored.FairPrice), label)
		} else {
			fmt.Fprintf(&b, "   %s\n", label)
		}
		if rec.Verdict != "" {
			fmt.Fprintf(&b, "   %s\n", rec.Verdict)
		}
		if len(rec.Concerns) > 0 {
			fmt.Fprintf(&b, "   Worth checking: %s\n", humanizeConcern(rec.Concerns[0]))
		}
		if i < len(recs)-1 {
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func buildHeuristicDraft(entry models.ShortlistEntry, mission *models.Mission, locale messageLocale) string {
	offerAmt := suggestedOfferAmount(entry)
	question := categorySpecificQuestion(mission, entry.Title, locale)
	// SuggestedQuestions are hardcoded English in buildQuestions; only substitute
	// them into the draft when the target locale is English to avoid mixed-language
	// messages on non-EN marketplaces (e.g. OLX BG, Marktplaats).
	if locale == localeEN && len(entry.SuggestedQuestions) > 0 {
		question = entry.SuggestedQuestions[0]
	}
	switch locale {
	case localeNL:
		return strings.TrimSpace(fmt.Sprintf(
			"Hoi! Ik heb interesse in je %s. %s Als alles in orde is, zou je %s overwegen?",
			entry.Title,
			question,
			formatEuro(offerAmt),
		))
	case localeBG:
		return strings.TrimSpace(fmt.Sprintf(
			"Здравейте! Интересувам се от %s. %s Ако всичко е наред, бихте ли приели %s?",
			entry.Title,
			question,
			formatEuro(offerAmt),
		))
	default:
		return strings.TrimSpace(fmt.Sprintf(
			"Hi! I'm interested in your %s. %s If all checks out, would you consider %s?",
			entry.Title,
			question,
			formatEuro(offerAmt),
		))
	}
}

func categorySpecificQuestion(mission *models.Mission, title string, locale messageLocale) string {
	category := ""
	if mission != nil {
		category = strings.ToLower(strings.TrimSpace(mission.Category))
	}
	lowerTitle := strings.ToLower(title)
	if category == "" || category == "other" {
		switch {
		case containsAny(lowerTitle, "iphone", "pixel", "samsung", "oneplus", "smartphone", "phone"):
			category = "phone"
		case containsAny(lowerTitle, "camera", "sony", "canon", "nikon", "fujifilm", "lens"):
			category = "camera"
		case containsAny(lowerTitle, "laptop", "macbook", "thinkpad", "notebook"):
			category = "laptop"
		}
	}
	switch category {
	case "phone":
		switch locale {
		case localeNL:
			return "Kun je de batterijgezondheid delen en bevestigen of er krassen op scherm of frame zijn?"
		case localeBG:
			return "Може ли да споделите процента здраве на батерията и дали има следи по екрана или рамката?"
		default:
			return "Could you share the battery health percentage and confirm whether there are any screen or frame marks?"
		}
	case "camera":
		switch locale {
		case localeNL:
			return "Kun je de huidige shutter count delen en bevestigen dat sensor en lensvatting schoon zijn?"
		case localeBG:
			return "Може ли да споделите текущия shutter count и дали сензорът и байонетът са чисти?"
		default:
			return "Could you share the current shutter count and confirm whether the sensor and lens mount are clean?"
		}
	case "laptop":
		switch locale {
		case localeNL:
			return "Kun je de staat van batterij, toetsenbord en scherm bevestigen, en of er dode pixels zijn?"
		case localeBG:
			return "Може ли да потвърдите състоянието на батерията, клавиатурата и екрана, и дали има мъртви пиксели?"
		default:
			return "Could you confirm battery condition, keyboard/screen condition, and if there are any dead pixels?"
		}
	default:
		switch locale {
		case localeNL:
			return "Kun je bevestigen dat alles goed werkt en dat er niets ontbreekt?"
		case localeBG:
			return "Може ли да потвърдите, че всичко работи и няма липсващи части?"
		default:
			return "Can you confirm everything is in good working order and nothing is missing?"
		}
	}
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func formatEuro(cents int) string {
	return format.Euro(cents)
}

func buildVerdict(label models.RecommendationLabel, _ float64, scored models.ScoredListing, concerns []string) string {
	switch label {
	case models.RecommendationBuyNow:
		if len(concerns) == 0 {
			return "Looks clean — priced well and nothing flags up. I'd move on this."
		}
		return "Solid price, but worth a quick check with the seller before committing."
	case models.RecommendationWatch:
		if scored.Confidence < 0.5 {
			return "Hard to judge without more comparable data, but the price looks reasonable. Worth watching."
		}
		return "Decent option. Not the sharpest deal out there, but nothing wrong with it either."
	case models.RecommendationAskQuestions:
		if len(concerns) > 0 {
			return "Could work — but I'd get answers to a couple of questions before saying yes."
		}
		return "Interesting, but I'd probe a bit before committing."
	default:
		return "Doesn't clear the bar right now — skip it."
	}
}

func formatRecommendationLabel(label models.RecommendationLabel) string {
	switch label {
	case models.RecommendationBuyNow:
		return "Buy now"
	case models.RecommendationWatch:
		return "Worth watching"
	case models.RecommendationAskQuestions:
		return "Ask questions first"
	default:
		return "Skip"
	}
}

func humanizeConcern(concern string) string {
	switch concern {
	case "confidence is limited because comparable data is weak":
		return "I do not have strong comparable pricing data yet"
	case "listing does not have a straightforward fixed asking price":
		return "The listing does not have a clear fixed asking price"
	case "listing may be defective or incomplete":
		return "The description suggests it may be defective or incomplete"
	case "listing is above your stretch budget":
		return "It is above your stretch budget"
	default:
		return concern
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *Assistant) parseBrief(ctx context.Context, userID, prompt string) (*models.Mission, error) {
	mission := heuristicProfileFromPrompt(userID, prompt, a.cfg.Marktplaats)
	if !a.aiEnabled() {
		return mission, nil
	}

	aiProfile, err := a.parseBriefWithAI(ctx, userID, prompt)
	if err != nil {
		// W19-23 cap-fire must propagate so the API layer renders 503 + Retry-After.
		// Pro/Buyer tiers paid for AI; silent heuristic fallback would be a billing-trust
		// regression. Other (network/parse/provider) errors still degrade gracefully to
		// the heuristic mission per the original design.
		if IsAIQuotaExhausted(err) {
			return nil, err
		}
		return mission, nil
	}
	return aiProfile, nil
}

func heuristicProfileFromPrompt(userID, prompt string, mpCfg config.MarktplaatsConfig) *models.Mission {
	text := strings.TrimSpace(prompt)
	lower := strings.ToLower(text)
	categoryID := 0
	if containsAny(lower, "sony", "camera", "a7", "a6400", "a6700", "lens", "fe ", "gm", "16-35", "24-70") {
		categoryID = 487
		if containsAny(lower, "lens", "16-35", "24-70", "50mm", "85mm", "70-200", "fe ", "gm") {
			categoryID = 495
		}
	}

	// W19-38 / XOL-135: extract a clean item name from the raw prompt so
	// downstream Mission.Name + TargetQuery + SearchQueries don't carry
	// the user's filler ("help me find a used") + price/location context.
	// Falls back to raw text on degenerate input (defensive guard inside
	// extractItemName).
	cleanItem := extractItemName(text)
	name := cleanItem
	if len(name) > 40 {
		name = name[:40]
	}
	return &models.Mission{
		UserID:             userID,
		Name:               name,
		TargetQuery:        cleanItem,
		CategoryID:         categoryID,
		BudgetMax:          extractBudget(lower),
		BudgetStretch:      extractStretchBudget(lower),
		PreferredCondition: []string{"good", "like_new"},
		RequiredFeatures:   nil,
		NiceToHave:         nil,
		RiskTolerance:      "balanced",
		ZipCode:            mpCfg.ZipCode,
		Distance:           mpCfg.Distance,
		SearchQueries:      []string{cleanItem},
		Status:             "active",
		Urgency:            "flexible",
		TravelRadius:       mpCfg.Distance / 1000,
		Category:           detectMissionCategory(lower),
		Active:             true,
	}
}

func nextProfileQuestion(profile models.Mission) (string, string) {
	if strings.TrimSpace(profile.TargetQuery) == "" || len(profile.SearchQueries) == 0 {
		return "What are you after? A specific make and model works best — the more precise, the better the matches.", "target_query"
	}
	if profile.BudgetMax == 0 {
		return "What's your budget? I'll find deals under that and flag anything that looks like a steal.", "budget_max"
	}
	if len(profile.PreferredCondition) == 0 {
		return "How fussy are you about condition? I can stick to like-new only, or cast a wider net and flag the risks on each one.", "condition"
	}
	return "", ""
}

func applyAnswerToProfile(profile *models.Mission, questionKey, answer string, mpCfg config.MarktplaatsConfig) {
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)
	switch questionKey {
	case "target_query":
		profile.TargetQuery = answer
		profile.Name = answer
		profile.SearchQueries = []string{answer}
		if profile.CategoryID == 0 {
			profile.CategoryID = detectCategory(answer)
		}
		if strings.TrimSpace(profile.Category) == "" || strings.EqualFold(profile.Category, "other") {
			profile.Category = detectMissionCategory(answer)
		}
	case "category":
		profile.CategoryID = detectCategory(answer)
		profile.Category = detectMissionCategory(answer)
	case "budget_max":
		profile.BudgetMax = extractFirstInteger(answer)
		profile.BudgetStretch = profile.BudgetMax // treat max as stretch for simplicity
	case "budget_stretch":
		profile.BudgetStretch = extractFirstInteger(answer)
	case "condition":
		profile.PreferredCondition = parseConditions(answer)
	}
	if profile.ZipCode == "" {
		profile.ZipCode = mpCfg.ZipCode
	}
	if profile.Distance == 0 {
		profile.Distance = mpCfg.Distance
	}
	if profile.RiskTolerance == "" {
		if containsAny(lower, "safe", "careful", "low risk") {
			profile.RiskTolerance = "cautious"
		} else {
			profile.RiskTolerance = "balanced"
		}
	}
	if profile.TravelRadius == 0 && profile.Distance > 0 {
		profile.TravelRadius = profile.Distance / 1000
	}
	if len(profile.SearchQueries) == 0 && profile.TargetQuery != "" {
		profile.SearchQueries = []string{profile.TargetQuery}
	}
}

func detectCategory(text string) int {
	lower := strings.ToLower(text)
	if containsAny(lower, "lens", "16-35", "24-70", "50mm", "85mm", "70-200", "fe ", "gm", "sel") {
		return 495
	}
	if containsAny(lower, "camera", "body", "sony", "a7", "a6400", "a6700", "zv-e10", "alpha") {
		return 487
	}
	return 0
}

func detectMissionCategory(text string) string {
	lower := strings.ToLower(text)
	switch {
	case containsAny(lower, "iphone", "pixel", "samsung", "oneplus", "smartphone", "phone"):
		return "phone"
	case containsAny(lower, "laptop", "macbook", "thinkpad", "notebook", "chromebook"):
		return "laptop"
	case containsAny(lower, "camera", "sony", "canon", "nikon", "fujifilm", "lens", "alpha", "a7", "a6"):
		return "camera"
	default:
		return "other"
	}
}

func extractFirstInteger(text string) int {
	var value int
	fmt.Sscanf(strings.TrimSpace(text), "%d", &value)
	if value > 0 {
		return value
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			continue
		}
		fmt.Sscanf(text[i:], "%d", &value)
		if value > 0 {
			return value
		}
	}
	return 0
}

func parseConditions(text string) []string {
	lower := strings.ToLower(text)
	var conditions []string
	if containsAny(lower, "like new", "like-new", "very good", "mint", "excellent", "zo goed als nieuw") {
		conditions = append(conditions, "like_new")
	}
	if containsAny(lower, "used", "good", "gebruikt") {
		conditions = append(conditions, "good")
	}
	if containsAny(lower, "new", "nieuw") {
		conditions = append(conditions, "new")
	}
	if len(conditions) == 0 {
		conditions = []string{"good", "like_new"}
	}
	return dedupeStrings(conditions)
}

// extractItemName pulls a clean item name from a natural-language buying
// prompt. Strips leading filler ("help me find", "looking for", etc.),
// trailing budget/location context (", budget around X EUR", " in Sofia"),
// and condition words ("used", "new", "like new"). Returns the cleaned
// candidate, or the original text if cleaning would produce <3 chars.
//
// W19-38 / XOL-135: heuristicProfileFromPrompt previously used the raw
// user prompt as Mission.Name + TargetQuery, producing names like
// "Help me find a used Fujifilm X-T4 in Bul" on the chat path. With this
// helper, "Help me find a used Fujifilm X-T4 in Bulgaria, budget around
// 1200 euros" → "Fujifilm X-T4".
//
// Defensive fallback to raw text on degenerate input (≤3 chars after
// cleaning) so missions never end up with empty Name.
func extractItemName(rawPrompt string) string {
	text := strings.TrimSpace(rawPrompt)
	if text == "" {
		return ""
	}
	cleaned := text

	// Strip leading filler (case-insensitive). EN + BG.
	leadingPatterns := []string{
		"help me find a used ", "help me find a ", "help me find ",
		"can you help me find ", "can you find ", "can you find me ",
		"looking for a used ", "looking for a ", "looking for ",
		"i need a used ", "i need a ", "i need ",
		"i want a used ", "i want a ", "i want ",
		"show me ", "find me a ", "find me ", "find a ", "find ",
		"търся ",     // BG: "I'm looking for"
		"трябва ми ", // BG: "I need"
		"искам ",     // BG: "I want"
	}
	lower := strings.ToLower(cleaned)
	for _, pat := range leadingPatterns {
		if strings.HasPrefix(lower, pat) {
			cleaned = strings.TrimSpace(cleaned[len(pat):])
			lower = strings.ToLower(cleaned)
			break
		}
	}

	// Strip condition words at start of remainder.
	conditionPrefixes := []string{
		"used ", "new ", "like new ", "second-hand ", "secondhand ",
		"употребяван ", // BG: "used"
		"нов ",         // BG: "new"
	}
	for _, pat := range conditionPrefixes {
		if strings.HasPrefix(lower, pat) {
			cleaned = strings.TrimSpace(cleaned[len(pat):])
			lower = strings.ToLower(cleaned)
			break
		}
	}

	// Strip trailing context. Cut at the FIRST occurrence of any of:
	//   - ", budget" / ", under" / ", max" / ", with budget" (price clauses)
	//   - " in <location>" — match " in " followed by a country/city word
	//   - " under " / " budget " (mid-clause price markers)
	//   - " за " / " под " / " бюджет " (BG price markers)
	trailingMarkers := []string{
		", budget", ", under", ", max", ", with budget", ", for ",
		" budget around ", " budget ",
		" under ", " up to ", " max ",
		" за ", " под ", " бюджет ",
	}
	for _, marker := range trailingMarkers {
		if idx := strings.Index(lower, marker); idx > 0 {
			cleaned = strings.TrimSpace(cleaned[:idx])
			lower = strings.ToLower(cleaned)
		}
	}

	// Strip " in <location>" — cut at " in " when the remainder looks
	// place-like: short, likely a country/city (≤30 chars, ≤3 words).
	if idx := strings.LastIndex(lower, " in "); idx > 0 {
		remainder := strings.TrimSpace(cleaned[idx+4:])
		if remainder != "" {
			if len(remainder) <= 30 && len(strings.Fields(remainder)) <= 3 {
				cleaned = strings.TrimSpace(cleaned[:idx])
			}
		}
	}

	cleaned = strings.TrimRight(cleaned, ",.;: ")
	cleaned = strings.TrimSpace(cleaned)

	// Defensive fallback: if cleaning collapsed the result too aggressively,
	// return the original text. Prevents missions ending up with empty Name.
	if len(cleaned) < 3 {
		return text
	}
	return cleaned
}

// extractBudget pulls a budget integer out of natural-language buying input.
// W19-34 / XOL-131: rewritten 2026-04-30 to handle natural phrasings like
// "budget around 1200 euros" — the prior implementation used Sscanf("%d")
// which required the integer at position 0 of `after`. Founder live-verify
// caught the brief parser asking "What's your budget?" for an input that
// explicitly stated 1200 EUR.
//
// Strategy: find any of the budget markers (EN + BG/Cyrillic) and scan the
// 30-character window AFTER the marker for the first 2-5 digit integer.
// Also scans the 30-character window BEFORE the marker so phrasings like
// "1200 euro budget" / "1500 лв бюджет" work. Returns 0 if no marker is
// present in the text or if no plausible budget integer is found within
// any marker's window.
func extractBudget(text string) int {
	// EN, NL, and BG Cyrillic budget markers (XOL-39 M3-E).
	// "под" = under, "до" = up to, "максимум" = maximum, "бюджет" = budget.
	markers := []string{
		"under ", "max ", "budget ",
		"под ", "до ", "максимум ", "бюджет ",
	}
	// Match 2-5 digit integers (€10 minimum to €99,999 maximum — typical
	// used-electronics budget range). Anchored on word boundaries so we
	// don't pick up partial digits from larger numbers.
	intRe := budgetIntRegex
	for _, marker := range markers {
		// Search for the marker without its trailing space so phrasings like
		// "1200 euro budget" (marker at end of string, no trailing space) are
		// also detected by the before-window path.
		searchMarker := strings.TrimRight(marker, " ")
		idx := strings.Index(text, searchMarker)
		if idx < 0 {
			continue
		}
		afterStart := idx + len(searchMarker)
		// Skip any whitespace between the marker and the next token.
		for afterStart < len(text) && text[afterStart] == ' ' {
			afterStart++
		}
		afterEnd := afterStart + 30
		if afterEnd > len(text) {
			afterEnd = len(text)
		}
		if match := intRe.FindString(text[afterStart:afterEnd]); match != "" {
			if v, err := strconv.Atoi(match); err == nil && v > 0 {
				return v
			}
		}
		// Also check the 30-character window BEFORE the marker for phrasings
		// like "1200 euro budget" or "1500 лв бюджет".
		beforeStart := idx - 30
		if beforeStart < 0 {
			beforeStart = 0
		}
		if match := intRe.FindString(text[beforeStart:idx]); match != "" {
			if v, err := strconv.Atoi(match); err == nil && v > 0 {
				return v
			}
		}
	}
	return 0
}

func extractStretchBudget(text string) int {
	return extractBudget(text)
}

// budgetIntRegex matches a 2-5 digit positive integer with word boundaries.
// Pre-compiled at package init so the hot path doesn't repay compilation cost
// on every call.
var budgetIntRegex = regexp.MustCompile(`\b\d{2,5}\b`)

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func isAffirmative(lower string) bool {
	return containsAny(lower,
		"yes", "yeah", "yep", "yup", "sure", "ok", "okay", "go for it", "go ahead",
		"show me", "show matches", "find matches", "let's go", "lets go", "do it",
		"please", "sounds good", "great", "perfect", "absolutely",
	)
}

func isNegative(lower string) bool {
	return containsAny(lower,
		"no", "nope", "nah", "not now", "later", "skip", "cancel", "nevermind", "never mind",
	)
}

func (a *Assistant) aiEnabled() bool {
	return a.cfg.AI.Enabled && a.cfg.AI.APIKey != "" && a.cfg.AI.Model != ""
}

func (a *Assistant) parseBriefWithAI(ctx context.Context, userID, prompt string) (*models.Mission, error) {
	type profileResponse struct {
		Name               string   `json:"name"`
		TargetQuery        string   `json:"target_query"`
		CategoryID         int      `json:"category_id"`
		Category           string   `json:"category"`
		BudgetMax          int      `json:"budget_max"`
		BudgetStretch      int      `json:"budget_stretch"`
		PreferredCondition []string `json:"preferred_condition"`
		RequiredFeatures   []string `json:"required_features"`
		NiceToHave         []string `json:"nice_to_have"`
		RiskTolerance      string   `json:"risk_tolerance"`
		SearchQueries      []string `json:"search_queries"`
	}

	// Strict json_schema response_format for the brief parser (XOL-60 SUP-9).
	// Schema derived from profileResponse struct fields.
	briefSchema := map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "buying_brief",
			"strict": true,
			"schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":                map[string]any{"type": "string"},
					"target_query":        map[string]any{"type": "string"},
					"category_id":         map[string]any{"type": "integer"},
					"category":            map[string]any{"type": "string"},
					"budget_max":          map[string]any{"type": "integer"},
					"budget_stretch":      map[string]any{"type": "integer"},
					"preferred_condition": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"required_features":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"nice_to_have":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"risk_tolerance":      map[string]any{"type": "string"},
					"search_queries":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required":             []string{"name", "target_query", "category_id", "category", "budget_max", "budget_stretch", "preferred_condition", "required_features", "nice_to_have", "risk_tolerance", "search_queries"},
				"additionalProperties": false,
			},
		},
	}

	payload := map[string]any{
		"model":           a.modelBrief,
		"temperature":     0.2,
		"response_format": briefSchema,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "You are an expert buying assistant helping users find great second-hand deals on European marketplaces (Marktplaats, Vinted, OLX). " +
					"Extract the user's buying intent into a structured JSON profile. Rules:\n" +
					"- name: short human-readable label (e.g. 'Sony A6700', 'Camera + 2 lenses', 'Vintage Levi jacket'). No slugs, no hyphens, no URL encoding.\n" +
					"- search_queries: 1 to 3 SPECIFIC product queries. Each entry must identify the exact product — include brand + model (or distinctive identifier). Include at most one common abbreviation or alternate spelling. DO NOT emit broad category queries like 'mirrorless camera', 'laptop', or 'smartphone' — those return unrelated noise. DO NOT include price qualifiers like 'under 500' or 'max 300 eur' — budget belongs in budget_max. DO NOT include condition words ('new', 'used', 'like new') — condition belongs in preferred_condition.\n" +
					"- target_query: the single most representative specific query (brand + model). Strip price and condition words.\n" +
					"- budget values are whole euros (integers). budget_stretch = budget_max if not specified.\n" +
					"- preferred_condition: 'new', 'like_new', 'good', 'fair'. Default to ['like_new','good'] if unspecified.\n" +
					"Return ONLY valid JSON — no explanation, no markdown fences.",
			},
			{
				"role": "user",
				"content": "Extract a buying brief from this request. Schema: " +
					`{"name":"","target_query":"","category_id":0,"budget_max":0,"budget_stretch":0,"preferred_condition":[],"required_features":[],"nice_to_have":[],"risk_tolerance":"balanced","search_queries":[]}` +
					"\n\nRequest: " + prompt,
			},
		},
	}
	// W19-23 global AI-spend cap. Pre-spend gate.
	budget := aibudget.Global()
	if budget != nil {
		if allowed, retry := budget.Allow(ctx, "assistant.brief_parser", aibudget.EstimatedCostPerCallUSD); !allowed {
			return nil, &QuotaExhaustedError{RetryAfter: retry}
		}
	}

	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.AI.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.AI.APIKey)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := a.client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		a.reportUsage(userID, 0, "brief_parser", a.modelBrief, 0, 0, latencyMs, false, err.Error())
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("ai provider returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		a.reportUsage(userID, 0, "brief_parser", a.modelBrief, 0, 0, latencyMs, false, errMsg)
		return nil, fmt.Errorf("%s", errMsg)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		a.reportUsage(userID, 0, "brief_parser", a.modelBrief, 0, 0, latencyMs, false, err.Error())
		return nil, err
	}
	a.reportUsage(userID, 0, "brief_parser", a.modelBrief, completion.Usage.PromptTokens, completion.Usage.CompletionTokens, latencyMs, true, "")
	if budget != nil {
		actualCostUSD := float64(completion.Usage.PromptTokens)*0.25/1_000_000 +
			float64(completion.Usage.CompletionTokens)*2.00/1_000_000
		budget.Reconcile(actualCostUSD)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no ai choices")
	}
	// With strict json_schema mode, the model returns valid JSON directly.
	// No extractJSON fallback — surface parse failures as typed errors.
	var parsed profileResponse
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &parsed); err != nil {
		return nil, fmt.Errorf("parse ai brief json: %w", err)
	}
	if parsed.BudgetStretch == 0 {
		parsed.BudgetStretch = parsed.BudgetMax
	}
	// Cap search queries defensively — the model sometimes ignores the limit.
	if len(parsed.SearchQueries) > 5 {
		parsed.SearchQueries = parsed.SearchQueries[:5]
	}
	// Sanitize name: replace hyphens/underscores used as slugs with spaces and trim.
	parsed.Name = strings.NewReplacer("-", " ", "_", " ", "+", " ").Replace(parsed.Name)
	parsed.Name = strings.Join(strings.Fields(parsed.Name), " ")
	if parsed.Name == "" && parsed.TargetQuery != "" {
		parsed.Name = parsed.TargetQuery
	}
	return &models.Mission{
		UserID:             userID,
		Name:               parsed.Name,
		TargetQuery:        parsed.TargetQuery,
		CategoryID:         parsed.CategoryID,
		BudgetMax:          parsed.BudgetMax,
		BudgetStretch:      parsed.BudgetStretch,
		PreferredCondition: parsed.PreferredCondition,
		RequiredFeatures:   parsed.RequiredFeatures,
		NiceToHave:         parsed.NiceToHave,
		RiskTolerance:      parsed.RiskTolerance,
		ZipCode:            a.cfg.Marktplaats.ZipCode,
		Distance:           a.cfg.Marktplaats.Distance,
		SearchQueries:      parsed.SearchQueries,
		Status:             "active",
		Urgency:            "flexible",
		TravelRadius:       a.cfg.Marktplaats.Distance / 1000,
		Category:           missionCategoryFromParsed(parsed.Category, parsed.TargetQuery, parsed.Name),
		Active:             true,
	}, nil
}

func missionCategoryFromParsed(parsedCategory, targetQuery, name string) string {
	value := strings.ToLower(strings.TrimSpace(parsedCategory))
	switch value {
	case "phone", "laptop", "camera", "other":
		return value
	default:
		return detectMissionCategory(targetQuery + " " + name)
	}
}

func (a *Assistant) compareWithAI(ctx context.Context, userID string, entries []models.ShortlistEntry) (string, error) {
	payload := map[string]any{
		"model":       a.modelChat,
		"temperature": 0.5,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "You are a knowledgeable buying assistant helping a user decide between second-hand items they've shortlisted. " +
					"Compare the options like a trusted friend who knows the market: weigh price against fair value, highlight any concerns, and make a clear recommendation. " +
					"Be direct and conversational — not a bullet-point report. 3-5 sentences max.",
			},
			{"role": "user", "content": "Compare these shortlisted deals and tell me which one to go for:\n" + mustJSON(entries)},
		},
	}
	missionID := int64(0)
	if len(entries) > 0 {
		candidate := entries[0].MissionID
		if candidate > 0 {
			sameMission := true
			for _, entry := range entries[1:] {
				if entry.MissionID != candidate {
					sameMission = false
					break
				}
			}
			if sameMission {
				missionID = candidate
			}
		}
	}
	return a.chatText(ctx, userID, missionID, "compare", payload)
}

func (a *Assistant) draftWithAI(ctx context.Context, userID string, entry models.ShortlistEntry, marketplaceID string, locale messageLocale, riskFlags []string) (string, error) {
	language := localeLabel(locale)
	// Reference data (Concerns, SuggestedQuestions, Verdict) is generated in
	// English by the scorer/buildQuestions. Stripping those fields for non-EN
	// locales prevents the model from echoing English phrasing into the draft.
	entryForPrompt := entry
	if locale != localeEN {
		entryForPrompt.SuggestedQuestions = nil
		entryForPrompt.Concerns = nil
		entryForPrompt.Verdict = ""
	}
	payload := map[string]any{
		"model":       a.modelDraft,
		"temperature": 0.5,
		"messages": []map[string]string{
			{
				"role": "system",
				"content": "You help buyers draft seller messages on European secondhand marketplaces (Marktplaats, Vinted, OLX BG). " +
					"Write the entire message in " + language + ". The reference data may be in English — ignore its phrasing and produce natural " + language + " prose. " +
					"Match the marketplace tone and language expectations for " + marketplaceID + ". " +
					"Include: a brief mention of what appeals about the listing, one question about condition or completeness if relevant, " +
					"and the suggested offer phrased naturally as 'would you consider X?'. " +
					"Keep it to 2-3 sentences. Do not commit to buying. Do not be pushy. " +
					"Output only the message body — no preamble, no translation notes. " +
					"If risk_flags contains 'stale_listing', explicitly ask whether the item is still available. " +
					"If risk_flags contains 'no_battery_health', ask about the battery health or capacity. ",
			},
			{
				"role": "user",
				"content": "Draft a seller message for this listing:\n" + mustJSON(map[string]any{
					"marketplace": marketplaceID,
					"language":    string(locale),
					"entry":       entryForPrompt,
					"risk_flags":  riskFlags,
				}),
			},
		},
	}
	return a.chatText(ctx, userID, entry.MissionID, "draft", payload)
}

func (a *Assistant) chatText(ctx context.Context, userID string, missionID int64, callType string, payload map[string]any) (string, error) {
	// Extract the model from the payload for usage reporting (set by caller).
	model, _ := payload["model"].(string)
	if model == "" {
		model = a.cfg.AI.Model
	}

	// W19-23 global AI-spend cap. Pre-spend gate: if the projection would
	// breach the founder-locked $3/24h ceiling, abort with the typed
	// quota-exhausted error so the API layer can render 503 + Retry-After.
	budget := aibudget.Global()
	if budget != nil {
		if allowed, retry := budget.Allow(ctx, "assistant."+callType, aibudget.EstimatedCostPerCallUSD); !allowed {
			return "", &QuotaExhaustedError{RetryAfter: retry}
		}
	}

	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.AI.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.AI.APIKey)
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	resp, err := a.client.Do(req)
	latencyMs := int(time.Since(start).Milliseconds())
	if err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		a.reportUsage(userID, missionID, callType, model, 0, 0, latencyMs, false, err.Error())
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		body, _ := io.ReadAll(resp.Body)
		errMsg := fmt.Sprintf("ai provider returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		a.reportUsage(userID, missionID, callType, model, 0, 0, latencyMs, false, errMsg)
		return "", fmt.Errorf("%s", errMsg)
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		if budget != nil {
			budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
		}
		a.reportUsage(userID, missionID, callType, model, 0, 0, latencyMs, false, err.Error())
		return "", err
	}
	a.reportUsage(userID, missionID, callType, model, completion.Usage.PromptTokens, completion.Usage.CompletionTokens, latencyMs, true, "")
	// Reconcile against the real spend at gpt-5-mini list price. The
	// constants live in scorer.go; reconcile takes the actual USD so we
	// inline-compute the same formula here to avoid an scorer ↔ assistant
	// import cycle. Pricing as-of 2026-04-25.
	if budget != nil {
		actualCostUSD := float64(completion.Usage.PromptTokens)*0.25/1_000_000 +
			float64(completion.Usage.CompletionTokens)*2.00/1_000_000
		budget.Reconcile(actualCostUSD)
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("no ai choices")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func mustJSON(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
