package scorer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/reasoner"
	"github.com/TechXTT/xolto/internal/store"
)

const minOfferCents = 1000 // EUR10 minimum offer

// accessoryTitleRe matches ASCII/Latin accessory terms using \b word boundaries.
// Covers EN and NL terms (XOL-21).
var accessoryTitleRe = regexp.MustCompile(
	`(?i)\b(adapter|charger|battery|accu|oplader|netvoeding|onderdelen|` +
		`bag|sleeve|strap|holster|tripod|monopod)\b`,
)

// accessoryTitleCyrillicRe matches BG Cyrillic accessory terms. RE2 \b does not
// recognise Unicode word characters, and (?i) does not fold Cyrillic case, so we
// enumerate both lowercase and title-case forms and use a leading non-Cyrillic-
// letter assertion as a pseudo-boundary (XOL-21).
var accessoryTitleCyrillicRe = regexp.MustCompile(
	`(?:^|[^а-яА-ЯёЁ])(зарядно|Зарядно|батерия|Батерия|зарядното|Зарядното|` +
		`части|Части|аксесоар|Аксесоар|аксесоари|Аксесоари)`,
)

// isAccessoryTitle returns true when the listing title is dominated by accessory
// terms. Only filters obvious accessory-only listings; items with "charger
// included" alongside a primary device name are not filtered (word boundary match
// does not check position, so keep the pattern conservative).
func isAccessoryTitle(title string) bool {
	return accessoryTitleRe.MatchString(title) || accessoryTitleCyrillicRe.MatchString(title)
}

type aiScoreCachePayload struct {
	Relevant     bool    `json:"relevant"`
	FairPrice    int     `json:"fair_price"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
	SearchAdvice string  `json:"search_advice,omitempty"`
}

type scoreStore interface {
	store.Reader
	SetAIScoreCache(cacheKey string, score float64, reasoning string, promptVersion int) error
}

type Scorer struct {
	store      scoreStore
	scoringCfg struct {
		MinScore         float64
		MarketSampleSize int
	}
	reasoner *reasoner.Reasoner
}

func (sc *Scorer) promptVersion() int {
	if sc.reasoner != nil {
		return sc.reasoner.PromptVersion()
	}
	return 1
}

func (sc *Scorer) shouldSkipLLM(listing models.Listing, search models.SearchSpec, heuristic models.DealAnalysis) bool {
	if search.MaxPrice > 0 && listing.Price > search.MaxPrice*3/2 {
		return true
	}
	if heuristic.Confidence >= 0.70 && heuristic.FairPrice > 0 {
		ratio := float64(listing.Price) / float64(heuristic.FairPrice)
		score := clamp(10.0-10.0*ratio+5.0, 1, 10)
		if score < 3.0 {
			return true
		}
	}
	return false
}

func New(s scoreStore, cfg interface {
	GetMinScore() float64
	GetMarketSampleSize() int
}, rsn *reasoner.Reasoner) *Scorer {
	return &Scorer{
		store: s,
		scoringCfg: struct {
			MinScore         float64
			MarketSampleSize int
		}{
			MinScore:         cfg.GetMinScore(),
			MarketSampleSize: cfg.GetMarketSampleSize(),
		},
		reasoner: rsn,
	}
}

// Score evaluates a listing and returns a ScoredListing with score and offer price.
func (sc *Scorer) Score(ctx context.Context, listing models.Listing, search models.SearchSpec) models.ScoredListing {
	var score float64
	var reason strings.Builder

	if !hasActionablePrice(listing) {
		reason.WriteString("listing has no actionable asking price")
		if listing.PriceType != "" {
			reason.WriteString(" (")
			reason.WriteString(listing.PriceType)
			reason.WriteString(")")
		}

		return models.ScoredListing{
			Listing:                  listing,
			Score:                    1.0,
			OfferPrice:               0,
			FairPrice:                0,
			MarketAverage:            0,
			Confidence:               0,
			Reason:                   reason.String(),
			ReasoningSource:          "rule",
			RecommendedAction:        ActionAskSeller,
			ComparablesCount:         0,
			ComparablesMedianAgeDays: 0,
			MustHaves:                ScoreMustHaves(listing, search.MustHaves),
		}
	}

	// Accessory pre-filter: skip listings whose title indicates they are standalone
	// accessories (charger, battery, adapter, etc.) — saves an LLM call and keeps
	// the feed focused on primary devices (XOL-21).
	if isAccessoryTitle(listing.Title) {
		return models.ScoredListing{
			Listing:           listing,
			Score:             1.0,
			Reason:            "accessory pre-filtered",
			ReasoningSource:   "accessory-prefilter",
			RecommendedAction: ActionSkip,
			MustHaves:         ScoreMustHaves(listing, search.MustHaves),
		}
	}

	marketAvg, hasMarket, err := sc.store.GetMarketAverage(search.Query, search.CategoryID, sc.scoringCfg.MarketSampleSize)
	if err != nil {
		slog.Warn("failed to load market average", "query", search.Query, "error", err)
	}

	comparables, err := sc.store.GetComparableDeals(search.UserID, search.Query, listing.ItemID, 50)
	if err != nil {
		slog.Warn("failed to load comparable deals", "item", listing.ItemID, "error", err)
	}

	// Prepend user-approved matches from this mission. They're ground-truth
	// relevant items, so they anchor both fair-price calibration and the
	// reasoner's relevance judgement. Deduped by ItemID against `comparables`
	// so an already-seen listing doesn't appear twice.
	if search.ProfileID > 0 {
		approved, aerr := sc.store.GetApprovedComparables(search.UserID, search.ProfileID, 10)
		if aerr != nil {
			slog.Warn("failed to load approved comparables", "mission", search.ProfileID, "error", aerr)
		} else if len(approved) > 0 {
			seen := make(map[string]bool, len(comparables))
			for _, c := range comparables {
				seen[c.ItemID] = true
			}
			merged := make([]models.ComparableDeal, 0, len(approved)+len(comparables))
			for _, a := range approved {
				if a.ItemID == listing.ItemID || seen[a.ItemID] {
					continue
				}
				merged = append(merged, a)
			}
			comparables = append(merged, comparables...)
		}
	}

	analysis := models.DealAnalysis{
		FairPrice:  marketAvg,
		Confidence: 0.35,
		Reason:     "market-average fallback",
		Source:     "heuristic",
	}
	if sc.reasoner != nil {
		heuristic := sc.reasoner.HeuristicAnalysis(listing, search, marketAvg, comparables)
		analysis = heuristic
		if sc.shouldSkipLLM(listing, search, heuristic) {
			analysis.Source = "prefilter"
		} else {
			promptVersion := sc.promptVersion()
			cacheKey := aiScoreCacheKey(listing.ItemID, listing.Price, promptVersion)
			cacheHit := false
			if cacheKey != "" {
				cachedScore, cachedReasoning, found, cacheErr := sc.store.GetAIScoreCache(cacheKey, promptVersion)
				if cacheErr != nil {
					slog.Warn("failed to load ai score cache", "item", listing.ItemID, "error", cacheErr)
				} else if found {
					cacheHit = true
					cachedAnalysis, ok := decodeAIScoreCachePayload(cachedScore, cachedReasoning)
					if ok {
						analysis = cachedAnalysis
					} else {
						if cachedScore > 0 {
							analysis.FairPrice = int(cachedScore)
						}
						if strings.TrimSpace(cachedReasoning) != "" {
							analysis.Reason = strings.TrimSpace(cachedReasoning)
						}
						analysis.Source = "ai-cache"
					}
				}
			}
			if !cacheHit {
				analysis, err = sc.reasoner.Analyze(ctx, listing, search, marketAvg, comparables)
				if err != nil {
					analysis = heuristic
					slog.Warn("ai reasoning failed, using heuristic fallback", "item", listing.ItemID, "error", err)
				} else if analysis.Source == "ai" && cacheKey != "" {
					if cacheErr := sc.store.SetAIScoreCache(cacheKey, float64(analysis.FairPrice), encodeAIScoreCachePayload(analysis), promptVersion); cacheErr != nil {
						slog.Warn("failed to persist ai score cache", "item", listing.ItemID, "error", cacheErr)
					}
				}
			}
		}
	}

	referencePrice := analysis.FairPrice
	if referencePrice <= 0 && hasMarket {
		referencePrice = marketAvg
	}

	if referencePrice > 0 {
		ratio := float64(listing.Price) / float64(referencePrice)
		score = clamp(10.0-10.0*ratio+5.0, 1, 10)
		reason.WriteString(fmt.Sprintf("%.0f%% of fair value (%s)", ratio*100, format.Euro(referencePrice)))
	} else if search.MaxPrice > 0 {
		ratio := float64(listing.Price) / float64(search.MaxPrice)
		score = clamp(10.0-8.0*ratio, 1, 10)
		reason.WriteString(fmt.Sprintf("%.0f%% of max budget", ratio*100))
	} else {
		score = 5.0
		reason.WriteString("no market data")
	}

	if analysis.Confidence >= 0.75 {
		score += 0.4
		reason.WriteString(", strong comparable confidence")
	} else if analysis.Confidence < 0.4 {
		score -= 0.3
		reason.WriteString(", weak comparable confidence")
	}

	if listing.PriceType == "negotiable" {
		score += 1.0
		reason.WriteString(", negotiable")
	}

	if !listing.Date.IsZero() && time.Since(listing.Date) < time.Hour {
		score += 0.5
		reason.WriteString(", fresh listing")
	}

	switch strings.ToLower(listing.Condition) {
	case "like_new":
		score += 0.5
		reason.WriteString(", like new")
	case "new":
		score += 0.5
		reason.WriteString(", new condition")
	}

	score = clamp(score, 1, 10)

	// AI explicitly judged the listing as irrelevant to the search query.
	if (analysis.Source == "ai" || analysis.Source == "ai-cache") && !analysis.Relevant {
		compCount, compMedian := computeComparableStats(comparables)
		return models.ScoredListing{
			Listing:                  listing,
			Score:                    1.0,
			OfferPrice:               0,
			Reason:                   "not relevant to search: " + analysis.Reason,
			ReasoningSource:          analysis.Source,
			RecommendedAction:        ActionSkip,
			ComparablesCount:         compCount,
			ComparablesMedianAgeDays: compMedian,
			MustHaves:                ScoreMustHaves(listing, search.MustHaves),
		}
	}

	riskFlags := computeRiskFlags(listing, analysis.FairPrice)
	offerPrice := calculateOffer(listing.Price, analysis.FairPrice, marketAvg, hasMarket, search.OfferPercentage)

	// Compute price_ratio for the verdict mapper. Use the same reference price
	// hierarchy as the scoring formula above: AI fair price > market average.
	// When no reference price is available, priceRatio = 0 (unknown) — the
	// mapper will not fire SKIP/NEGOTIATE/BUY price rules and will fall through
	// to ASK SELLER (appropriate for evidence-thin listings).
	priceRatio := 0.0
	if referencePrice > 0 && listing.Price > 0 {
		priceRatio = float64(listing.Price) / float64(referencePrice)
	}

	recommendedAction := ComputeVerdict(
		listing.MarketplaceID,
		score,
		analysis.Confidence,
		comparables,
		search.Query,
		priceRatio,
		listing.Condition,
		riskFlags,
	)

	if analysis.Reason != "" {
		reason.WriteString(" | ")
		reason.WriteString(analysis.Reason)
	}

	compCount, compMedian := computeComparableStats(comparables)
	return models.ScoredListing{
		Listing:                  listing,
		Score:                    score,
		OfferPrice:               offerPrice,
		FairPrice:                analysis.FairPrice,
		MarketAverage:            marketAvg,
		Confidence:               analysis.Confidence,
		Reason:                   reason.String(),
		ReasoningSource:          analysis.Source,
		SearchAdvice:             analysis.SearchAdvice,
		ComparableDeals:          analysis.ComparableDeals,
		RiskFlags:                riskFlags,
		RecommendedAction:        recommendedAction,
		ComparablesCount:         compCount,
		ComparablesMedianAgeDays: compMedian,
		MustHaves:                ScoreMustHaves(listing, search.MustHaves),
	}
}

// computeComparableStats returns (count, medianAgeDays) for a slice of
// ComparableDeals. Comparables with a zero LastSeen are excluded from both
// count and median. Negative ages (LastSeen in the future) are treated as 0.
func computeComparableStats(comparables []models.ComparableDeal) (count int, medianDays int) {
	now := time.Now()
	var ages []int
	for _, c := range comparables {
		if c.LastSeen.IsZero() {
			continue
		}
		age := int(now.Sub(c.LastSeen).Hours() / 24)
		if age < 0 {
			age = 0
		}
		ages = append(ages, age)
	}
	n := len(ages)
	if n == 0 {
		return 0, 0
	}
	sort.Ints(ages)
	var median int
	if n%2 == 1 {
		median = ages[n/2]
	} else {
		median = (ages[n/2-1] + ages[n/2] + 1) / 2 // round half-up
	}
	return n, median
}

func aiScoreCacheKey(itemID string, price, promptVersion int) string {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" || price <= 0 {
		return ""
	}
	if promptVersion <= 0 {
		promptVersion = 1
	}
	return fmt.Sprintf("%s:%d:%d", itemID, price, promptVersion)
}

func encodeAIScoreCachePayload(analysis models.DealAnalysis) string {
	payload := aiScoreCachePayload{
		Relevant:     analysis.Relevant,
		FairPrice:    analysis.FairPrice,
		Confidence:   analysis.Confidence,
		Reason:       strings.TrimSpace(analysis.Reason),
		SearchAdvice: strings.TrimSpace(analysis.SearchAdvice),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return strings.TrimSpace(analysis.Reason)
	}
	return string(raw)
}

func decodeAIScoreCachePayload(score float64, raw string) (models.DealAnalysis, bool) {
	payload := aiScoreCachePayload{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err != nil {
		return models.DealAnalysis{}, false
	}
	if payload.FairPrice <= 0 && score > 0 {
		payload.FairPrice = int(score)
	}
	if payload.Confidence <= 0 {
		payload.Confidence = 0.7
	}
	return models.DealAnalysis{
		Relevant:        payload.Relevant,
		FairPrice:       payload.FairPrice,
		Confidence:      clamp(payload.Confidence, 0.05, 0.99),
		Reason:          strings.TrimSpace(payload.Reason),
		Source:          "ai-cache",
		SearchAdvice:    strings.TrimSpace(payload.SearchAdvice),
		ComparableDeals: nil,
	}, true
}

func calculateOffer(askingPrice, fairPrice, marketAvg int, hasMarket bool, offerPct int) int {
	base := askingPrice
	if fairPrice > 0 {
		base = fairPrice
	} else if hasMarket && marketAvg > 0 {
		base = marketAvg
	}

	offer := base * offerPct / 100
	if offer < minOfferCents {
		offer = minOfferCents
	}
	if offer > askingPrice {
		offer = askingPrice
	}
	return offer
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func hasActionablePrice(listing models.Listing) bool {
	if listing.Price <= 0 {
		return false
	}

	switch listing.PriceType {
	case "reserved", "see-description", "exchange", "free":
		return false
	default:
		return true
	}
}

// computeRiskFlags returns trust-signal flags for a listing.
func computeRiskFlags(listing models.Listing, fairPrice int) []string {
	var flags []string
	lower := strings.ToLower(listing.Title + " " + listing.Description)
	electronics := isElectronicsListing(lower)

	if fairPrice > 0 && listing.Price > 0 && listing.Price < fairPrice/2 {
		flags = append(flags, "anomaly_price")
	}

	vagueTerms := []string{"as is", "as-is", "untested", "for parts", "sold as seen", "no returns", "working condition", "not working"}
	for _, term := range vagueTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "vague_condition")
			break
		}
	}

	bundleTerms := []string{"bundle", " lot ", "complete set", "collection"}
	for _, term := range bundleTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "unclear_bundle")
			break
		}
	}

	if electronics {
		hasDigit := false
		for _, c := range listing.Title {
			if c >= '0' && c <= '9' {
				hasDigit = true
				break
			}
		}
		if !hasDigit {
			flags = append(flags, "no_model_id")
		}
	}

	if electronics && len(listing.ImageURLs) < 3 {
		flags = append(flags, "missing_key_photos")
	}

	if electronics && isPhoneOrLaptop(lower) && !hasBatteryHealthSignal(lower) {
		flags = append(flags, "no_battery_health")
	}

	if hasRefurbishedAmbiguity(lower) {
		flags = append(flags, "refurbished_ambiguity")
	}

	return flags
}

func isElectronicsListing(text string) bool {
	lower := strings.ToLower(text)
	keywords := []string{
		// EN/NL terms (existing)
		"camera", "lens", "laptop", "macbook", "iphone", "ipad", "samsung", "pixel",
		"sony", "nikon", "canon", "fuji", "fujifilm", "gpu", "cpu", "graphics card",
		"smartphone", "tablet", "notebook", "thinkpad", "surface", "playstation", "xbox", "nintendo",
		"monitor", "television", "tv", "router", "modem", "headphone", "airpods", "charger", "battery",
		// BG Cyrillic terms — added for OLX.bg wedge (XOL-35 M3-A)
		"фотоапарат", "камера", "обектив", "лаптоп", "компютър",
		"слушалки", "телефон", "таблет",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

func isPhoneOrLaptop(text string) bool {
	return containsAny(text,
		"iphone", "samsung", "pixel", "oneplus", "smartphone", "phone",
		"laptop", "macbook", "notebook", "thinkpad", "surface",
	)
}

func hasBatteryHealthSignal(text string) bool {
	return containsAny(text,
		// EN terms (existing)
		"battery health", "battery capacity", "battery condition", "cycle count", "battery cycles",
		// NL terms (existing)
		"battery capaciteit", "accu", "accuprestatie", "batterijconditie",
		// BG Cyrillic terms — added for OLX.bg wedge (XOL-35 M3-A)
		"батерия", "акумулатор", "капацитет",
	)
}

func hasRefurbishedAmbiguity(text string) bool {
	if !containsAny(text,
		// EN terms (existing)
		"refurb", "renewed", "reconditioned",
		// NL terms (existing)
		"gereviseerd",
		// BG Cyrillic terms — added for OLX.bg wedge (XOL-35 M3-A)
		"рециклиран", "възстановен", "ремонтиран",
	) {
		return false
	}
	if containsAny(text, "grade a", "grade b", "grade c", "warranty", "garantie", "12 month", "24 month") {
		return false
	}
	return true
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
