package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/notify"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

// wordTokenRe extracts alphanumeric runs as discrete tokens. We intentionally
// split on non-alphanumeric characters (including hyphens) so that OEM part
// numbers like "52960-A6000" don't let a search for "a6000" bleed into unrelated
// categories via substring matching. Unicode letter support matters for OLX BG
// titles and queries.
var wordTokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// queryStopWords are generic words or natural-language price qualifiers that
// should not count toward relevance scoring. They frequently appear in mission
// target queries like "sony a6000 under 500" but carry no product signal.
var queryStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"a": true, "an": true, "of": true, "to": true, "in": true, "on": true,
	"at": true, "is": true, "it": true, "or": true, "by": true,
	"under": true, "below": true, "less": true, "than": true, "max": true,
	"maximum": true, "up": true, "until": true, "upto": true,
	"above": true, "over": true, "more": true, "min": true, "minimum": true,
	"eur": true, "euro": true, "euros": true, "usd": true,
	"used": true, "new": true, "good": true, "like": true, "mint": true,
	"cheap": true, "deal": true, "wanted": true, "buy": true, "buying": true,
}

// cameraIntentTokens indicate camera-focused searches where accessory-only
// listings (bags, cases, straps, etc.) should be filtered unless the query
// explicitly asks for accessories.
var cameraIntentTokens = map[string]bool{
	"camera": true, "mirrorless": true, "dslr": true, "body": true, "kit": true,
	"апарат": true, "камера": true, "фотоапарат": true,
}

// cameraAccessoryTokens identify listing titles that are likely standalone
// accessories. Includes common English, Dutch and Bulgarian marketplace terms.
var cameraAccessoryTokens = map[string]bool{
	"bag": true, "bags": true, "case": true, "cases": true, "cover": true, "covers": true,
	"pouch": true, "pouches": true, "strap": true, "straps": true, "charger": true, "chargers": true,
	"battery": true, "batteries": true, "tripod": true, "tripods": true, "filter": true, "filters": true,
	"adapter": true, "adapters": true, "mount": true, "mounts": true, "hood": true, "hoods": true,
	"cage": true, "cages": true, "cap": true, "caps": true, "grip": true, "grips": true,
	"remote": true, "flash": true, "microphone": true, "mic": true, "housing": true, "underwater": true,
	"tas": true, "hoes": true, "lader": true, "batterij": true, "statief": true,
	"чанта": true, "чанти": true, "калъф": true, "калъфи": true, "каишка": true, "каишки": true,
	"батерия": true, "батерии": true, "зарядно": true, "стойка": true, "филтър": true, "филтри": true,
	"бокс": true, "кутия": true, "подводен": true,
}

// cameraCoreTokens suggest the listing is an actual camera offer (body/kit)
// instead of a standalone accessory. Note: "camera" is intentionally excluded
// because it appears in accessory titles (e.g. "camera bag", "camera case").
var cameraCoreTokens = map[string]bool{
	"body": true, "kit": true, "mirrorless": true, "dslr": true,
	"lens": true, "lenses": true, "toestel": true,
	"апарат": true, "камера": true, "фотоапарат": true,
	"обектив": true, "обектива": true, "обективи": true, "комплект": true, "тяло": true,
}

// phoneIntentTokens indicate phone-focused searches where accessory-only
// listings (cases, chargers, cables, etc.) should be filtered unless the query
// explicitly asks for accessories.
var phoneIntentTokens = map[string]bool{
	"pixel": true, "galaxy": true, "iphone": true, "samsung": true, "oneplus": true,
	"xiaomi": true, "motorola": true, "xperia": true, "huawei": true,
	"smartphone": true, "смартфон": true,
}

// phoneAccessoryTokens identify listing titles that are likely standalone
// phone accessories.
var phoneAccessoryTokens = map[string]bool{
	"case": true, "cases": true, "cover": true, "covers": true,
	"charger": true, "chargers": true, "cable": true, "cables": true,
	"holder": true, "mount": true, "stand": true,
	"калъф": true, "калъфи": true, "протектор": true, "протектори": true,
	"зарядно": true, "кабел": true, "кабели": true, "поставка": true, "стойка": true,
}

// phoneCoreTokens indicate the listing IS a phone device (storage/connectivity specs),
// not a phone accessory that merely mentions the compatible model.
var phoneCoreTokens = map[string]bool{
	"128gb": true, "256gb": true, "64gb": true, "512gb": true, "16gb": true, "32gb": true, "1tb": true,
	"sim": true, "lte": true, "5g": true, "4g": true, "unlocked": true,
	"разблокиран": true,
}

type UserWorker struct {
	db            store.Store
	registry      *marketplace.Registry
	scorer        *scorer.Scorer
	notifier      notify.Dispatcher
	emailNotifier *notify.EmailNotifier
	minScore      float64
}

// titleMatchesQuery decides whether a listing title plausibly refers to the
// same product category as the query. It tokenizes both sides on word
// boundaries (treating hyphens as separators), drops stopwords and pure numeric
// tokens, and requires EVERY meaningful query token to appear as a discrete
// token in the title. This prevents false matches like the query "sony a6000"
// latching onto a Hyundai wheel-cap title "…52960-A6000 5 gaats met schade"
// just because the OEM part number happens to contain "A6000" as a substring.
func titleMatchesQuery(title, query string) bool {
	titleTokens := tokenizeWords(title)
	queryTokens := meaningfulQueryTokens(query)

	if len(queryTokens) == 0 {
		// Fall back to a permissive match if the query had nothing but
		// stopwords/numbers — otherwise we'd drop every listing.
		return true
	}

	for _, qt := range queryTokens {
		if _, ok := titleTokens[qt]; !ok {
			return false
		}
	}
	return true
}

func listingMatchesSearch(listing models.Listing, spec models.SearchSpec) bool {
	if !titleMatchesQuery(listing.Title, spec.Query) {
		return false
	}
	if shouldRejectAccessoryListing(listing.Title, spec.Query) {
		return false
	}
	return true
}

func shouldRejectAccessoryListing(title, query string) bool {
	queryMeaningful := meaningfulQueryTokens(query)
	if len(queryMeaningful) == 0 {
		return false
	}
	queryTokens := make(map[string]struct{}, len(queryMeaningful))
	for _, tok := range queryMeaningful {
		queryTokens[tok] = struct{}{}
	}

	titleTokens := tokenizeWords(title)

	// Camera accessory check
	if cameraIntentQuery(queryTokens) {
		if containsAnyToken(queryTokens, cameraAccessoryTokens) {
			return false
		}
		if containsAnyToken(titleTokens, cameraAccessoryTokens) {
			return !containsAnyToken(titleTokens, cameraCoreTokens)
		}
	}

	// Phone accessory check
	if phoneIntentQuery(queryTokens) {
		if containsAnyToken(queryTokens, phoneAccessoryTokens) {
			return false
		}
		if phoneIntentQuery(titleTokens) && containsAnyToken(titleTokens, phoneAccessoryTokens) {
			return !containsAnyToken(titleTokens, phoneCoreTokens)
		}
	}

	return false
}

func cameraIntentQuery(tokens map[string]struct{}) bool {
	for tok := range tokens {
		if cameraIntentTokens[tok] {
			return true
		}
		if looksCameraModelToken(tok) {
			return true
		}
	}
	return false
}

func phoneIntentQuery(tokens map[string]struct{}) bool {
	for tok := range tokens {
		if phoneIntentTokens[tok] {
			return true
		}
	}
	return false
}

func looksCameraModelToken(tok string) bool {
	hasDigit := false
	for _, r := range tok {
		if r >= '0' && r <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return false
	}
	return strings.HasPrefix(tok, "a") ||
		strings.HasPrefix(tok, "ilce") ||
		strings.HasPrefix(tok, "eos") ||
		strings.HasPrefix(tok, "rx") ||
		strings.HasPrefix(tok, "zv") ||
		strings.HasPrefix(tok, "d")
}

func containsAnyToken(tokens map[string]struct{}, vocabulary map[string]bool) bool {
	for token := range tokens {
		if vocabulary[token] {
			return true
		}
	}
	return false
}

func tokenizeWords(s string) map[string]struct{} {
	matches := wordTokenRe.FindAllString(strings.ToLower(s), -1)
	out := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		out[m] = struct{}{}
	}
	return out
}

func meaningfulQueryTokens(query string) []string {
	raw := wordTokenRe.FindAllString(strings.ToLower(query), -1)
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, tok := range raw {
		if queryStopWords[tok] {
			continue
		}
		if len(tok) < 2 {
			continue
		}
		// Drop pure numeric tokens — price hints like "500" in "under 500"
		// shouldn't be treated as product identifiers. Alphanumeric tokens
		// like "a6000" or "rtx3080" remain distinctive model identifiers.
		if isAllDigits(tok) {
			continue
		}
		if seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (w *UserWorker) RunTask(ctx context.Context, task candidate, queueWait time.Duration) error {
	spec := task.spec
	user := task.user
	startedAt := time.Now().UTC()
	logEntry := models.SearchRunLog{
		SearchConfigID:  spec.ID,
		UserID:          spec.UserID,
		MissionID:       spec.ProfileID,
		Plan:            billing.NormalizeTier(user.Tier),
		MarketplaceID:   spec.MarketplaceID,
		CountryCode:     spec.CountryCode,
		StartedAt:       startedAt,
		QueueWaitMs:     int(queueWait / time.Millisecond),
		Priority:        task.priority,
		SearchesAvoided: task.searchesAvoided,
	}

	finish := func(status string, errCode string, err error) error {
		finishedAt := time.Now().UTC()
		logEntry.FinishedAt = finishedAt
		logEntry.Status = status
		logEntry.ErrorCode = errCode
		spec.PriorityClass = task.priority
		spec.LastRunAt = finishedAt
		if errCode != "" {
			spec.ConsecutiveFailures++
			spec.LastErrorAt = finishedAt
		} else {
			spec.ConsecutiveFailures = 0
			spec.LastErrorAt = time.Time{}
		}
		spec.NextRunAt = nextRunAtAfter(spec, user, finishedAt)

		if updateErr := w.db.UpdateSearchRuntime(spec); updateErr != nil {
			slog.Warn("failed to update search runtime", "search_id", spec.ID, "error", updateErr)
			if err == nil {
				err = updateErr
			}
		}
		if logErr := w.db.RecordSearchRun(logEntry); logErr != nil {
			slog.Warn("failed to record search run", "search_id", spec.ID, "error", logErr)
			if err == nil {
				err = logErr
			}
		}
		return err
	}

	if task.mission != nil {
		scope, err := missionScopeForCandidate(user, task.mission)
		if err != nil {
			return finish("invalid_scope", "invalid_scope", err)
		}
		if len(scope) > 0 && !scopeContains(scope, spec.MarketplaceID) {
			logEntry.Throttled = true
			return finish("out_of_scope", "out_of_scope", nil)
		}
	}

	mp, ok := w.registry.Get(spec.MarketplaceID)
	if !ok {
		return finish("unknown_marketplace", "unknown_marketplace", fmt.Errorf("unknown marketplace %q", spec.MarketplaceID))
	}

	listings, err := mp.Search(ctx, spec)
	if err != nil {
		slog.Warn("worker search failed", "marketplace", spec.MarketplaceID, "query", spec.Query, "error", err)
		return finish("search_failed", "search_failed", err)
	}

	for _, listing := range listings {
		if !listingMatchesSearch(listing, spec) {
			continue
		}
		logEntry.ResultsFound++
		listing.ProfileID = spec.ProfileID
		if listing.Price > 0 {
			_ = w.db.RecordPrice(spec.Query, spec.CategoryID, spec.MarketplaceID, listing.Price)
		}
		isNew, _ := w.db.IsNew(spec.UserID, listing.ItemID)
		prevScore, hadPrev, _ := w.db.GetListingScore(spec.UserID, listing.ItemID)
		if !isNew {
			storedPrice, storedSource, storedComparablesCount, found, err := w.db.GetListingScoringState(spec.UserID, listing.ItemID)
			if err != nil {
				slog.Warn("failed to load listing scoring state", "item", listing.ItemID, "error", err)
			} else if found && storedPrice == listing.Price && storedSource == "ai" && storedComparablesCount > 0 {
				// Skip re-scoring only when the listing was previously scored with AI
				// at the same price AND comparables_count was already populated.
				// If comparables_count==0 the listing was saved before XOL-17 populated
				// this field, so we must re-score to fill it.
				if err := w.db.TouchListing(spec.UserID, listing.ItemID); err != nil {
					slog.Warn("failed to touch cached listing", "item", listing.ItemID, "error", err)
				}
				continue
			}
		}

		scored := w.scorer.Score(ctx, listing, spec)
		if err := w.db.SaveListing(spec.UserID, listing, spec.Query, scored); err != nil {
			slog.Warn("failed to save listing", "item", listing.ItemID, "error", err)
		}
		if isNew {
			logEntry.NewListings++
		}

		crossed := !isNew && hadPrev && prevScore < w.minScore && scored.Score >= w.minScore
		if !isNew && !crossed {
			continue
		}
		if scored.Score < w.minScore || scored.OfferPrice <= 0 {
			continue
		}

		logEntry.DealHits++
		spec.LastSignalAt = time.Now().UTC()
		if w.notifier != nil {
			payload, _ := json.Marshal(map[string]any{
				"type":      "deal_found",
				"userID":    spec.UserID,
				"missionID": spec.ProfileID,
				"search":    spec.Name,
				"deal":      scored,
			})
			w.notifier.Publish(spec.UserID, string(payload))
		}
		if w.emailNotifier != nil && w.emailNotifier.Enabled() && user.Email != "" {
			_ = w.emailNotifier.SendDealAlert(user.Email, listing, scored.Score)
		}
		slog.Info("worker deal found", "user", spec.UserID, "title", listing.Title, "score", fmt.Sprintf("%.1f", scored.Score))
	}

	spec.LastResultCount = logEntry.ResultsFound
	if logEntry.ResultsFound > 0 {
		spec.ConsecutiveEmptyRuns = 0
		if spec.LastSignalAt.IsZero() {
			spec.LastSignalAt = time.Now().UTC()
		}
	} else {
		spec.ConsecutiveEmptyRuns++
	}

	return finish("success", "", nil)
}
