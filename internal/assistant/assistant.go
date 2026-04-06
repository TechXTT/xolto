package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/format"
	"github.com/TechXTT/marktbot/internal/marketplace"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/scorer"
	"github.com/TechXTT/marktbot/internal/store"
)

const (
	defaultMatchLimit = 5
)

type Assistant struct {
	cfg      *config.Config
	store    store.Store
	searcher marketplace.Marketplace
	scorer   *scorer.Scorer
	client   *http.Client
}

func New(cfg *config.Config, st store.Store, searcher marketplace.Marketplace, sc *scorer.Scorer) *Assistant {
	return &Assistant{
		cfg:      cfg,
		store:    st,
		searcher: searcher,
		scorer:   sc,
		client: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (a *Assistant) UpsertBrief(ctx context.Context, userID, prompt string) (*models.ShoppingProfile, error) {
	profile, err := a.parseBrief(ctx, userID, prompt)
	if err != nil {
		return nil, err
	}

	id, err := a.store.UpsertShoppingProfile(*profile)
	if err != nil {
		return nil, err
	}
	profile.ID = id
	_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, fmt.Sprintf("profile:%d", id))
	return profile, nil
}

func (a *Assistant) Converse(ctx context.Context, userID, message string) (*models.AssistantReply, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return &models.AssistantReply{
			Message: "Tell me what you want to buy and I’ll help narrow it down. For example: I want a Sony A6400 under 800 euro in very good condition.",
		}, nil
	}

	session, err := a.store.GetAssistantSession(userID)
	if err != nil {
		return nil, err
	}
	if session != nil && session.PendingIntent == models.IntentCreateBrief && session.DraftProfile != nil {
		return a.continueBriefConversation(ctx, *session, message)
	}

	lower := strings.ToLower(message)
	switch {
	case containsAny(lower, "help", "what can you do", "how do i use"):
		return &models.AssistantReply{
			Message: "I can help you build a shopping brief, find matches, compare shortlist items, explain a listing, and draft seller questions. Tell me what you want to buy, or ask me to show matches or compare your shortlist.",
			Intent:  models.IntentCreateBrief,
		}, nil
	case containsAny(lower, "show matches", "find matches", "what did you find", "matches"):
		recs, profile, err := a.FindMatches(ctx, userID, 5)
		if err != nil {
			return &models.AssistantReply{
				Message:   "I need a shopping brief first. Tell me what you want to buy, your budget, and any must-have features.",
				Expecting: true,
				Intent:    models.IntentCreateBrief,
			}, nil
		}
		return &models.AssistantReply{
			Message:         renderConversationMatches(profile.Name, recs),
			Intent:          models.IntentShowMatches,
			Profile:         profile,
			Recommendations: recs,
		}, nil
	case containsAny(lower, "compare shortlist", "compare my shortlist", "compare"):
		comparison, err := a.CompareShortlist(ctx, userID)
		if err != nil {
			return &models.AssistantReply{
				Message:   "Your shortlist is empty right now. Ask me for matches first, then save the interesting ones.",
				Expecting: true,
				Intent:    models.IntentShortlist,
			}, nil
		}
		return &models.AssistantReply{Message: comparison, Intent: models.IntentCompare}, nil
	default:
		return a.startBriefConversation(ctx, userID, message)
	}
}

func (a *Assistant) GetActiveProfile(userID string) (*models.ShoppingProfile, error) {
	return a.store.GetActiveShoppingProfile(userID)
}

func (a *Assistant) FindMatches(ctx context.Context, userID string, limit int) ([]models.Recommendation, *models.ShoppingProfile, error) {
	profile, err := a.store.GetActiveShoppingProfile(userID)
	if err != nil {
		return nil, nil, err
	}
	if profile == nil {
		return nil, nil, fmt.Errorf("no active shopping brief found")
	}
	if limit <= 0 {
		limit = defaultMatchLimit
	}

	searches := a.searchConfigsForProfile(*profile)
	seen := map[string]struct{}{}
	var recs []models.Recommendation
	for _, searchCfg := range searches {
		listings, err := a.searcher.Search(ctx, searchCfg)
		if err != nil {
			continue
		}
		for _, listing := range listings {
			if _, exists := seen[listing.ItemID]; exists {
				continue
			}
			seen[listing.ItemID] = struct{}{}
			scored := a.scorer.Score(ctx, listing, searchCfg)
			rec := buildRecommendation(scored, *profile)
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
	return recs, profile, nil
}

func (a *Assistant) ExplainListing(ctx context.Context, userID, itemID string) (string, error) {
	rec, _, err := a.findRecommendationByItemID(ctx, userID, itemID)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", fmt.Errorf("listing %s not found in active matches", itemID)
	}

	return formatRecommendationDetail(*rec), nil
}

func (a *Assistant) SaveToShortlist(ctx context.Context, userID, itemID string) (*models.ShortlistEntry, error) {
	rec, profile, err := a.findRecommendationByItemID(ctx, userID, itemID)
	if err != nil {
		return nil, err
	}
	if rec == nil || profile == nil {
		return nil, fmt.Errorf("listing %s not found in active matches", itemID)
	}

	entry := models.ShortlistEntry{
		UserID:              userID,
		ProfileID:           profile.ID,
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
		if comparison, err := a.compareWithAI(ctx, entries); err == nil && comparison != "" {
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

	content := buildHeuristicDraft(*entry)
	if a.aiEnabled() {
		if aiDraft, err := a.draftWithAI(ctx, *entry); err == nil && aiDraft != "" {
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

func (a *Assistant) startBriefConversation(ctx context.Context, userID, prompt string) (*models.AssistantReply, error) {
	profile, err := a.parseBrief(ctx, userID, prompt)
	if err != nil {
		return nil, err
	}
	if profile.BudgetStretch == 0 && profile.BudgetMax > 0 {
		profile.BudgetStretch = profile.BudgetMax
	}

	if question, key := nextProfileQuestion(*profile); question != "" {
		session := models.AssistantSession{
			UserID:           userID,
			PendingIntent:    models.IntentCreateBrief,
			PendingQuestion:  key,
			DraftProfile:     profile,
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
			Profile:   profile,
		}, nil
	}

	id, err := a.store.UpsertShoppingProfile(*profile)
	if err != nil {
		return nil, err
	}
	profile.ID = id
	_ = a.store.ClearAssistantSession(userID)
	reply := fmt.Sprintf("Saved your shopping brief for %s. Want me to show the best matches now?", profile.Name)
	_ = a.store.SaveConversationArtifact(userID, models.IntentCreateBrief, prompt, reply)
	return &models.AssistantReply{
		Message:   reply,
		Expecting: true,
		Intent:    models.IntentShowMatches,
		Profile:   profile,
	}, nil
}

func (a *Assistant) continueBriefConversation(ctx context.Context, session models.AssistantSession, answer string) (*models.AssistantReply, error) {
	profile := session.DraftProfile
	if profile == nil {
		return a.startBriefConversation(ctx, session.UserID, answer)
	}

	applyAnswerToProfile(profile, session.PendingQuestion, answer, a.cfg.Marktplaats)
	if question, key := nextProfileQuestion(*profile); question != "" {
		session.PendingQuestion = key
		session.DraftProfile = profile
		session.LastAssistantMsg = question
		if err := a.store.SaveAssistantSession(session); err != nil {
			return nil, err
		}
		_ = a.store.SaveConversationArtifact(session.UserID, models.IntentRefineBrief, answer, question)
		return &models.AssistantReply{
			Message:   question,
			Expecting: true,
			Intent:    models.IntentRefineBrief,
			Profile:   profile,
		}, nil
	}

	id, err := a.store.UpsertShoppingProfile(*profile)
	if err != nil {
		return nil, err
	}
	profile.ID = id
	_ = a.store.ClearAssistantSession(session.UserID)

	recs, _, matchErr := a.FindMatches(ctx, session.UserID, 3)
	reply := fmt.Sprintf("Perfect. I saved your brief for %s.", profile.Name)
	if matchErr == nil && len(recs) > 0 {
		reply += "\n\n" + renderConversationMatches(profile.Name, recs)
		reply += "\n\nTell me if you want to tighten the budget, focus on one model, or shortlist one of these item IDs."
	} else {
		reply += "\nYou can now ask me to show matches, compare options, or refine the brief."
	}
	_ = a.store.SaveConversationArtifact(session.UserID, models.IntentRefineBrief, answer, reply)
	return &models.AssistantReply{
		Message:         reply,
		Intent:          models.IntentShowMatches,
		Profile:         profile,
		Recommendations: recs,
	}, nil
}

func (a *Assistant) searchConfigsForProfile(profile models.ShoppingProfile) []models.SearchSpec {
	queries := profile.SearchQueries
	if len(queries) == 0 && profile.TargetQuery != "" {
		queries = []string{profile.TargetQuery}
	}
	if len(queries) == 0 {
		queries = []string{profile.Name}
	}

	conditions := profile.PreferredCondition
	if len(conditions) == 0 {
		conditions = []string{"good", "like_new"}
	}

	searches := make([]models.SearchSpec, 0, len(queries))
	for _, query := range queries {
		searches = append(searches, models.SearchSpec{
			Name:            profile.Name,
			Query:           query,
			MarketplaceID:   "marktplaats",
			CategoryID:      profile.CategoryID,
			MaxPrice:        profile.BudgetStretch * 100,
			MinPrice:        0,
			Condition:       conditions,
			OfferPercentage: 72,
			AutoMessage:     false,
		})
	}
	return searches
}

func (a *Assistant) findRecommendationByItemID(ctx context.Context, userID, itemID string) (*models.Recommendation, *models.ShoppingProfile, error) {
	recs, profile, err := a.FindMatches(ctx, userID, 15)
	if err != nil {
		return nil, nil, err
	}
	for _, rec := range recs {
		if rec.Listing.ItemID == itemID {
			recCopy := rec
			return &recCopy, profile, nil
		}
	}
	return nil, profile, nil
}

func buildRecommendation(scored models.ScoredListing, profile models.ShoppingProfile) models.Recommendation {
	fitScore := scoreFit(scored.Listing, profile)
	concerns := collectConcerns(scored.Listing, profile, scored)
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
		Profile:        profile,
		Label:          label,
		FitScore:       fitScore,
		Verdict:        verdict,
		Concerns:       concerns,
		NextQuestions:  questions,
		SuggestedOffer: scored.OfferPrice,
	}
}

func scoreFit(listing models.Listing, profile models.ShoppingProfile) float64 {
	score := 0.4
	text := strings.ToLower(listing.Title + " " + listing.Description)

	for _, feature := range profile.RequiredFeatures {
		if strings.Contains(text, strings.ToLower(feature)) {
			score += 0.15
		} else {
			score -= 0.15
		}
	}
	for _, feature := range profile.NiceToHave {
		if strings.Contains(text, strings.ToLower(feature)) {
			score += 0.05
		}
	}
	if profile.BudgetMax > 0 && listing.Price > 0 && listing.Price <= profile.BudgetMax*100 {
		score += 0.2
	}
	condition := strings.ToLower(listing.Attributes["condition"])
	for _, preferred := range profile.PreferredCondition {
		if strings.EqualFold(condition, preferred) {
			score += 0.1
			break
		}
	}
	return math.Max(0, math.Min(1, score))
}

func collectConcerns(listing models.Listing, profile models.ShoppingProfile, scored models.ScoredListing) []string {
	var concerns []string
	text := strings.ToLower(listing.Title + " " + listing.Description)

	if scored.Confidence < 0.5 {
		concerns = append(concerns, "confidence is limited because comparable data is weak")
	}
	if listing.PriceType == "reserved" || listing.PriceType == "fast-bid" || listing.PriceType == "bidding" {
		concerns = append(concerns, "listing does not have a straightforward fixed asking price")
	}
	if strings.Contains(text, "defect") || strings.Contains(text, "gaat niet aan") || strings.Contains(text, "kapot") {
		concerns = append(concerns, "listing may be defective or incomplete")
	}
	for _, required := range profile.RequiredFeatures {
		if !strings.Contains(text, strings.ToLower(required)) {
			concerns = append(concerns, fmt.Sprintf("required feature not clearly confirmed: %s", required))
		}
	}
	if profile.BudgetStretch > 0 && listing.Price > profile.BudgetStretch*100 {
		concerns = append(concerns, "listing is above your stretch budget")
	}
	return concerns
}

func buildQuestions(listing models.Listing, concerns []string) []string {
	questions := []string{
		"Kun je bevestigen dat alles technisch goed werkt?",
		"Wat is de staat van de sensor, body en accessoires?",
	}
	for _, concern := range concerns {
		switch {
		case strings.Contains(concern, "defective"):
			questions = append(questions, "Wat is precies het defect en wat is al getest?")
		case strings.Contains(concern, "required feature"):
			questions = append(questions, "Kun je een foto of bevestiging sturen van de ontbrekende specificatie?")
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

func renderConversationMatches(profileName string, recs []models.Recommendation) string {
	if len(recs) == 0 {
		return "I couldn't find any suitable matches right now."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Here are the best matches I found for %s:\n\n", profileName)
	for i, rec := range recs {
		fmt.Fprintf(&b, "%d. **%s**\n", i+1, rec.Listing.Title)
		fmt.Fprintf(&b, "   %s\n", formatRecommendationLabel(rec.Label))
		fmt.Fprintf(&b, "   Ask: %s\n", formatEuro(rec.Listing.Price))
		fmt.Fprintf(&b, "   Fair value: %s\n", formatEuro(rec.Scored.FairPrice))
		fmt.Fprintf(&b, "   Item ID: `%s`\n", rec.Listing.ItemID)
		fmt.Fprintf(&b, "   Summary: %s\n", rec.Verdict)
		if len(rec.Concerns) > 0 {
			fmt.Fprintf(&b, "   Watch-outs: %s\n", strings.Join(humanizeConcerns(rec.Concerns[:minInt(2, len(rec.Concerns))]), "; "))
		}
		if i < len(recs)-1 {
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func buildHeuristicDraft(entry models.ShortlistEntry) string {
	questions := strings.Join(entry.SuggestedQuestions, " ")
	return strings.TrimSpace(fmt.Sprintf(
		"Hoi! Ik heb interesse in %s. Kun je me nog iets meer vertellen over de staat en of alles goed werkt? %s Als alles klopt, zou een prijs rond %s voor jou bespreekbaar zijn?",
		entry.Title,
		questions,
		formatEuro(entry.FairPrice),
	))
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

func buildVerdict(label models.RecommendationLabel, fitScore float64, scored models.ScoredListing, concerns []string) string {
	var summary string
	switch label {
	case models.RecommendationBuyNow:
		summary = "Strong match with solid pricing."
	case models.RecommendationWatch:
		summary = "Promising option worth watching."
	case models.RecommendationAskQuestions:
		summary = "Potential fit, but I would ask a few questions first."
	default:
		summary = "Not a strong enough match right now."
	}

	if scored.Confidence < 0.5 {
		summary += " Pricing confidence is limited."
	}
	if len(concerns) > 0 && label != models.RecommendationSkip {
		summary += " There are a couple of things to verify."
	}
	summary += fmt.Sprintf(" Fit looks like %.0f%%.", fitScore*100)
	return summary
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

func humanizeConcerns(concerns []string) []string {
	out := make([]string, 0, len(concerns))
	for _, concern := range concerns {
		out = append(out, humanizeConcern(concern))
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (a *Assistant) parseBrief(ctx context.Context, userID, prompt string) (*models.ShoppingProfile, error) {
	profile := heuristicProfileFromPrompt(userID, prompt, a.cfg.Marktplaats)
	if !a.aiEnabled() {
		return profile, nil
	}

	aiProfile, err := a.parseBriefWithAI(ctx, userID, prompt)
	if err != nil {
		return profile, nil
	}
	return aiProfile, nil
}

func heuristicProfileFromPrompt(userID, prompt string, mpCfg config.MarktplaatsConfig) *models.ShoppingProfile {
	text := strings.TrimSpace(prompt)
	lower := strings.ToLower(text)
	categoryID := 0
	if containsAny(lower, "sony", "camera", "a7", "a6400", "a6700", "lens", "fe ", "gm", "16-35", "24-70") {
		categoryID = 487
		if containsAny(lower, "lens", "16-35", "24-70", "50mm", "85mm", "70-200", "fe ", "gm") {
			categoryID = 495
		}
	}

	name := text
	if len(name) > 40 {
		name = name[:40]
	}
	return &models.ShoppingProfile{
		UserID:             userID,
		Name:               name,
		TargetQuery:        text,
		CategoryID:         categoryID,
		BudgetMax:          extractBudget(lower),
		BudgetStretch:      extractStretchBudget(lower),
		PreferredCondition: []string{"Gebruikt", "Zo goed als nieuw"},
		RequiredFeatures:   nil,
		NiceToHave:         nil,
		RiskTolerance:      "balanced",
		ZipCode:            mpCfg.ZipCode,
		Distance:           mpCfg.Distance,
		SearchQueries:      []string{text},
		Active:             true,
	}
}

func nextProfileQuestion(profile models.ShoppingProfile) (string, string) {
	if strings.TrimSpace(profile.TargetQuery) == "" || len(profile.SearchQueries) == 0 {
		return "What exactly are you shopping for? A specific model is best.", "target_query"
	}
	if profile.CategoryID == 0 {
		return "Is this a camera body, a lens, or something else?", "category"
	}
	if profile.BudgetMax == 0 {
		return "What is your target budget in euros?", "budget_max"
	}
	if profile.BudgetStretch == 0 {
		return "What is the highest stretch budget you would still consider in euros?", "budget_stretch"
	}
	if len(profile.PreferredCondition) == 0 {
		return "What condition do you prefer? For example: gebruikt, zo goed als nieuw, or new.", "condition"
	}
	return "", ""
}

func applyAnswerToProfile(profile *models.ShoppingProfile, questionKey, answer string, mpCfg config.MarktplaatsConfig) {
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
	case "category":
		profile.CategoryID = detectCategory(answer)
	case "budget_max":
		profile.BudgetMax = extractFirstInteger(answer)
		if profile.BudgetStretch == 0 {
			profile.BudgetStretch = profile.BudgetMax
		}
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
	if containsAny(lower, "zo goed als nieuw", "very good", "mint", "excellent") {
		conditions = append(conditions, "Zo goed als nieuw")
	}
	if containsAny(lower, "gebruikt", "used") {
		conditions = append(conditions, "Gebruikt")
	}
	if containsAny(lower, "new", "nieuw") {
		conditions = append(conditions, "Nieuw")
	}
	if len(conditions) == 0 {
		conditions = []string{"Gebruikt", "Zo goed als nieuw"}
	}
	return dedupeStrings(conditions)
}

func extractBudget(text string) int {
	for _, marker := range []string{"under ", "max ", "budget "} {
		idx := strings.Index(text, marker)
		if idx >= 0 {
			var value int
			fmt.Sscanf(text[idx+len(marker):], "%d", &value)
			if value > 0 {
				return value
			}
		}
	}
	return 0
}

func extractStretchBudget(text string) int {
	return extractBudget(text)
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func (a *Assistant) aiEnabled() bool {
	return a.cfg.AI.Enabled && a.cfg.AI.APIKey != "" && a.cfg.AI.Model != ""
}

func (a *Assistant) parseBriefWithAI(ctx context.Context, userID, prompt string) (*models.ShoppingProfile, error) {
	type profileResponse struct {
		Name               string   `json:"name"`
		TargetQuery        string   `json:"target_query"`
		CategoryID         int      `json:"category_id"`
		BudgetMax          int      `json:"budget_max"`
		BudgetStretch      int      `json:"budget_stretch"`
		PreferredCondition []string `json:"preferred_condition"`
		RequiredFeatures   []string `json:"required_features"`
		NiceToHave         []string `json:"nice_to_have"`
		RiskTolerance      string   `json:"risk_tolerance"`
		SearchQueries      []string `json:"search_queries"`
	}

	payload := map[string]any{
		"model":       a.cfg.AI.Model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": "You convert shopping requests into a structured Marktplaats shopping profile. Return JSON only."},
			{"role": "user", "content": "Parse this shopping request into JSON: " +
				`{"name":"","target_query":"","category_id":0,"budget_max":0,"budget_stretch":0,"preferred_condition":[],"required_features":[],"nice_to_have":[],"risk_tolerance":"balanced","search_queries":[]}` +
				"\nRequest: " + prompt},
		},
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.AI.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.AI.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ai provider returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return nil, err
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no ai choices")
	}
	content := extractJSON(completion.Choices[0].Message.Content)
	var parsed profileResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, err
	}
	if parsed.BudgetStretch == 0 {
		parsed.BudgetStretch = parsed.BudgetMax
	}
	return &models.ShoppingProfile{
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
		Active:             true,
	}, nil
}

func (a *Assistant) compareWithAI(ctx context.Context, entries []models.ShortlistEntry) (string, error) {
	payload := map[string]any{
		"model":       a.cfg.AI.Model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": "Compare shortlisted marketplace options for a buyer. Be concise and practical."},
			{"role": "user", "content": "Compare these shortlist entries and recommend the best one:\n" + mustJSON(entries)},
		},
	}
	return a.chatText(ctx, payload)
}

func (a *Assistant) draftWithAI(ctx context.Context, entry models.ShortlistEntry) (string, error) {
	payload := map[string]any{
		"model":       a.cfg.AI.Model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "system", "content": "Draft a polite Dutch seller message. Advisory only. Do not confirm purchase."},
			{"role": "user", "content": "Draft a concise Dutch message for this shortlist entry:\n" + mustJSON(entry)},
		},
	}
	return a.chatText(ctx, payload)
}

func (a *Assistant) chatText(ctx context.Context, payload map[string]any) (string, error) {
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.AI.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.AI.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ai provider returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return "", err
	}
	if len(completion.Choices) == 0 {
		return "", fmt.Errorf("no ai choices")
	}
	return strings.TrimSpace(completion.Choices[0].Message.Content), nil
}

func extractJSON(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "{") {
		return value
	}
	start := strings.IndexByte(value, '{')
	end := strings.LastIndexByte(value, '}')
	if start >= 0 && end > start {
		return value[start : end+1]
	}
	return value
}

func mustJSON(v any) string {
	raw, _ := json.MarshalIndent(v, "", "  ")
	return string(raw)
}
