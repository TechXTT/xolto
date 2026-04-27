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

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/modelkey"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/reasoner"
	"github.com/TechXTT/xolto/internal/store"
)

const minOfferCents = 1000 // EUR10 minimum offer

// gpt-5-mini list pricing, as published by OpenAI (verified 2026-04-25).
// Inputs are tokens; outputs are USD per single token at list price.
//
//	input  $0.25 / 1M tokens = $0.00000025 / token
//	output $2.00 / 1M tokens = $0.00000200 / token
//
// These constants are consumed by computeAICostUSD below to derive a per-call
// USD cost from the OpenAI Usage block returned by the chat-completions API.
// They feed the W19-3 anonymous-analyze daily-spend circuit-breaker
// reconciliation: the conservative $0.01/call pre-spend estimate is replaced
// post-call with this real cost. Update both the constants and the
// gpt5MiniPricingAsOf date if OpenAI changes list prices.
const (
	gpt5MiniInputCostPerToken  = 0.25 / 1_000_000
	gpt5MiniOutputCostPerToken = 2.00 / 1_000_000
	gpt5MiniPricingAsOf        = "2026-04-25"
)

// computeAICostUSD turns an analysis's prompt/completion token counts into a
// USD cost at the gpt-5-mini list price. Returns 0 when no LLM call was made
// (heuristic / cache / rate-limited / pre-filter paths report Source != "ai"
// and zero tokens). The value flows into ScoredListing.CostUSD for downstream
// daily-spend reconciliation by the anonymous-analyze breaker.
func computeAICostUSD(analysis models.DealAnalysis) float64 {
	if analysis.PromptTokens <= 0 && analysis.CompletionTokens <= 0 {
		return 0
	}
	return float64(analysis.PromptTokens)*gpt5MiniInputCostPerToken +
		float64(analysis.CompletionTokens)*gpt5MiniOutputCostPerToken
}

// offPlatformPhoneRe matches Bulgarian mobile phone patterns used in scam listings.
// Compiled once at package level (XOL-80).
var offPlatformPhoneRe = regexp.MustCompile(`\+?359\s*\d{8,9}|0[87]\d{7,8}`)

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

	marketAvg, hasMarket, err := sc.store.GetMarketAverage(search.Query, modelkey.Normalize(search.Query), search.CategoryID, search.MarketplaceID, sc.scoringCfg.MarketSampleSize)
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
				// W19-23 global AI-spend cap. Pre-spend gate: project the
				// conservative $0.01/call estimate into the rolling 24h
				// total; if it would breach the founder-locked $3 ceiling
				// we fall back to heuristic-only and tag the analysis so
				// VAL-1 calibration can filter the contaminated row out.
				// Reconcile post-call with the real W19-3 cost so the
				// rolling sum stays honest.
				budget := aibudget.Global()
				budgetGated := false
				if budget != nil {
					if allowed, _ := budget.Allow(ctx, "scorer", aibudget.EstimatedCostPerCallUSD); !allowed {
						budgetGated = true
					}
				}
				if budgetGated {
					analysis = heuristic
					analysis.AIPath = models.AIPathHeuristicFallback
					slog.Warn("global AI budget exhausted; scorer falling back to heuristic", "item", listing.ItemID)
				} else {
					analysis, err = sc.reasoner.Analyze(ctx, listing, search, marketAvg, comparables)
					if err != nil {
						if budget != nil {
							budget.Rollback(ctx, aibudget.EstimatedCostPerCallUSD)
						}
						analysis = heuristic
						slog.Warn("ai reasoning failed, using heuristic fallback", "item", listing.ItemID, "error", err)
					} else if analysis.Source == "ai" && cacheKey != "" {
						if cacheErr := sc.store.SetAIScoreCache(cacheKey, float64(analysis.FairPrice), encodeAIScoreCachePayload(analysis), promptVersion); cacheErr != nil {
							slog.Warn("failed to persist ai score cache", "item", listing.ItemID, "error", cacheErr)
						}
					}
					// Stamp the per-call USD cost from the LLM response's usage
					// block. Heuristic / rate-limited fallbacks (Source != "ai")
					// have zero tokens and therefore zero cost, which is correct.
					analysis.CostUSD = computeAICostUSD(analysis)
					if budget != nil {
						// Reconcile against the real spend (delta-charge or
						// refund). For non-"ai" sources (rate-limited /
						// heuristic-confident) CostUSD is 0 → fully refunds
						// the pre-spend estimate, which is correct because no
						// LLM call was actually paid for.
						budget.Reconcile(analysis.CostUSD)
					}
					// Successful LLM path: tag the analysis as a real "ai"
					// row so VAL-1 calibration aggregates it normally.
					if analysis.Source == "ai" {
						analysis.AIPath = models.AIPathAI
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
		fmt.Fprintf(&reason, "%.0f%% of fair value (%s)", ratio*100, format.Euro(referencePrice))
	} else if search.MaxPrice > 0 {
		ratio := float64(listing.Price) / float64(search.MaxPrice)
		score = clamp(10.0-8.0*ratio, 1, 10)
		fmt.Fprintf(&reason, "%.0f%% of max budget", ratio*100)
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
	case "fair":
		score -= 0.3
		reason.WriteString(", fair condition")
	case "for_parts":
		score -= 2.0
		reason.WriteString(", for-parts listing")
	case "unknown":
		reason.WriteString(", condition unknown")
	// "used" and "good": no score adjustment — neutral baseline
	}

	score = clamp(score, 1, 10)

	// Category-specific condition adjustment (XOL-86 C-9 Phase 3).
	switch strings.ToLower(listing.Condition) {
	case "fair":
		switch search.Category {
		case "camera":
			score -= 0.3
			reason.WriteString(" (camera penalty)")
		case "phone":
			score -= 0.2                              // XOL-98
			reason.WriteString(" (phone fair penalty)") // XOL-98
		case "laptop":
			score += 0.1
		}
	case "used":
		switch search.Category {
		case "camera":
			score -= 0.2
			reason.WriteString(", used camera")
		}
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
			// AI did make a paid call to reach an "irrelevant" verdict, so
			// the cost is non-zero on the "ai" branch. "ai-cache" has zero
			// tokens (cache hits skip the LLM entirely) and therefore zero
			// cost, which is what we want.
			CostUSD: analysis.CostUSD,
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

	mustHaveMatches := ScoreMustHaves(listing, search.MustHaves)
	missedMustHaveCount := 0
	for _, m := range mustHaveMatches {
		if m.Status == MustHaveStatusMissed {
			missedMustHaveCount++
		}
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
		missedMustHaveCount,
	)

	if analysis.Reason != "" {
		reason.WriteString(" | ")
		reason.WriteString(analysis.Reason)
	}

	compCount, compMedian := computeComparableStats(comparables)
	scoreContribs := computeScoreContributions(listing, search, analysis, referencePrice, score)
	// Emit attribution into the shadow log (VAL-2). This feeds the VAL-1
	// calibration dashboard via Railway log aggregation. slog.Info so it
	// flows in production (LevelInfo is the production floor per XOL-105).
	slog.Info("score_attribution",
		"item_id", listing.ItemID,
		"marketplace", listing.MarketplaceID,
		"score", score,
		"verdict", recommendedAction,
		"comparables", scoreContribs["comparables"],
		"confidence", scoreContribs["confidence"],
		"negotiable", scoreContribs["negotiable"],
		"recency", scoreContribs["recency"],
		"condition", scoreContribs["condition"],
		"category_condition", scoreContribs["category_condition"],
		"reasoning_source", analysis.Source,
	)
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
		MustHaves:                mustHaveMatches,
		ScoreContributions:       scoreContribs,
		CostUSD:                  analysis.CostUSD,
		AIPath:                   analysis.AIPath,
	}
}

// computeScoreContributions re-runs the scoring steps independently of the
// main scoring loop to produce a signed-delta attribution map for each
// component. This is internal-only (VAL-2) and must never flow into the public
// /matches response shape.
//
// Components:
//
//	"comparables"       — base score derived from reference price vs asking price
//	                      (or max budget when no reference price is available).
//	                      Value is the unclamped base score before any adjustments.
//	"confidence"        — +0.4 (high) / 0.0 (medium) / -0.3 (low) from analysis.Confidence.
//	"negotiable"        — +1.0 when listing.PriceType == "negotiable"; 0 otherwise.
//	"recency"           — +0.5 for listings posted within the last hour; 0 otherwise.
//	"condition"         — condition-tier delta (e.g. like_new: +0.5, for_parts: -2.0).
//	"category_condition"— XOL-86 category-specific condition penalty/bonus; 0 when n/a.
//
// The sum of all component deltas equals the final (clamped) Score within a
// floating-point tolerance (the clamping means the algebraic sum of deltas may
// differ from Score when the raw sum was outside [1, 10]).
//
// Returns nil when referencePrice == 0 and search.MaxPrice == 0 (no basis for
// the base score; only the "no market data" flat 5.0 path applies — in that
// case the contributions are populated with comparables=5.0 and all others 0).
func computeScoreContributions(
	listing models.Listing,
	search models.SearchSpec,
	analysis models.DealAnalysis,
	referencePrice int,
	finalScore float64,
) map[string]float64 {
	contribs := map[string]float64{
		"comparables":        0,
		"confidence":         0,
		"negotiable":         0,
		"recency":            0,
		"condition":          0,
		"category_condition": 0,
	}

	// --- Base score ("comparables") ---
	var baseScore float64
	if referencePrice > 0 {
		ratio := float64(listing.Price) / float64(referencePrice)
		baseScore = clamp(10.0-10.0*ratio+5.0, 1, 10)
	} else if search.MaxPrice > 0 {
		ratio := float64(listing.Price) / float64(search.MaxPrice)
		baseScore = clamp(10.0-8.0*ratio, 1, 10)
	} else {
		baseScore = 5.0
	}
	contribs["comparables"] = baseScore

	// --- Confidence delta ---
	if analysis.Confidence >= 0.75 {
		contribs["confidence"] = 0.4
	} else if analysis.Confidence < 0.4 {
		contribs["confidence"] = -0.3
	}

	// --- Negotiable delta ---
	if listing.PriceType == "negotiable" {
		contribs["negotiable"] = 1.0
	}

	// --- Recency delta ---
	if !listing.Date.IsZero() && time.Since(listing.Date) < time.Hour {
		contribs["recency"] = 0.5
	}

	// --- Condition delta (base condition tier) ---
	switch strings.ToLower(listing.Condition) {
	case "like_new", "new":
		contribs["condition"] = 0.5
	case "fair":
		contribs["condition"] = -0.3
	case "for_parts":
		contribs["condition"] = -2.0
	}

	// --- Category-specific condition delta (XOL-86) ---
	switch strings.ToLower(listing.Condition) {
	case "fair":
		switch search.Category {
		case "camera":
			contribs["category_condition"] = -0.3
		case "phone":
			contribs["category_condition"] = -0.2
		case "laptop":
			contribs["category_condition"] = 0.1
		}
	case "used":
		switch search.Category {
		case "camera":
			contribs["category_condition"] = -0.2
		}
	}

	// Reconcile: the sum of (comparables + all deltas) may exceed [1,10] before
	// clamping. Adjust the "comparables" base so the sum of contributions matches
	// the actual clamped finalScore. This keeps the invariant:
	//   sum(contribs.values()) == finalScore
	// within float64 precision.
	deltaSum := contribs["confidence"] + contribs["negotiable"] +
		contribs["recency"] + contribs["condition"] + contribs["category_condition"]
	contribs["comparables"] = finalScore - deltaSum

	return contribs
}

// ComputeAttributionFromListing reconstructs the score attribution from a
// stored models.Listing. This is the VAL-2 read-path helper — it is called by
// the /matches handler when the internal attribution debug gate is open.
//
// Because some scoring inputs (market average, original posting date) are not
// persisted, the reconstruction is approximate:
//   - "comparables" is derived as: finalScore - sum(all other deltas).
//     This is always exact because it absorbs any rounding/clamping.
//   - "recency" is always 0 for stored listings (posting date not persisted
//     separately from last_seen; will be 0 for listings older than 1 hour).
//   - "negotiable" is derived from listing.PriceType.
//   - "condition" and "category_condition" are derived from listing.Condition
//     and a best-effort search.Category value. Pass searchCategory="" when the
//     category is unknown at read time (contribution will be 0).
//
// The returned map always contains exactly the same component keys as
// computeScoreContributions:
//
//	"comparables", "confidence", "negotiable", "recency",
//	"condition", "category_condition"
func ComputeAttributionFromListing(listing models.Listing, searchCategory string) map[string]float64 {
	contribs := map[string]float64{
		"comparables":        0,
		"confidence":         0,
		"negotiable":         0,
		"recency":            0,
		"condition":          0,
		"category_condition": 0,
	}

	// --- Confidence delta ---
	if listing.Confidence >= 0.75 {
		contribs["confidence"] = 0.4
	} else if listing.Confidence < 0.4 {
		contribs["confidence"] = -0.3
	}

	// --- Negotiable delta ---
	if listing.PriceType == "negotiable" {
		contribs["negotiable"] = 1.0
	}

	// --- Recency: always 0 for stored listings (date not persisted) ---

	// --- Condition delta (base condition tier) ---
	switch strings.ToLower(listing.Condition) {
	case "like_new", "new":
		contribs["condition"] = 0.5
	case "fair":
		contribs["condition"] = -0.3
	case "for_parts":
		contribs["condition"] = -2.0
	}

	// --- Category-specific condition delta (XOL-86) ---
	switch strings.ToLower(listing.Condition) {
	case "fair":
		switch searchCategory {
		case "camera":
			contribs["category_condition"] = -0.3
		case "phone":
			contribs["category_condition"] = -0.2
		case "laptop":
			contribs["category_condition"] = 0.1
		}
	case "used":
		switch searchCategory {
		case "camera":
			contribs["category_condition"] = -0.2
		}
	}

	// --- Base (comparables): final score minus all other deltas ---
	deltaSum := contribs["confidence"] + contribs["negotiable"] +
		contribs["recency"] + contribs["condition"] + contribs["category_condition"]
	contribs["comparables"] = listing.Score - deltaSum

	return contribs
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
		age := max(int(now.Sub(c.LastSeen).Hours()/24), 0)
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

	return min(max(base*offerPct/100, minOfferCents), askingPrice)
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

	// Staleness check — listing not seen in search results for 3+ days.
	if !listing.Date.IsZero() && time.Since(listing.Date) > 72*time.Hour {
		flags = append(flags, "stale_listing")
	}

	// Condition-field check — catches structured OLX.bg params not repeated in description.
	switch listing.Condition {
	case "for_parts", "unknown":
		flags = append(flags, "vague_condition")
	}

	vagueTerms := []string{
		// EN/NL terms (existing)
		"as is", "as-is", "untested", "for parts", "sold as seen", "no returns", "working condition", "not working",
		// BG terms — added for OLX.bg wedge (XOL-80)
		"за части", "за ремонт", "не работи", "без гаранция", "като е", "проблем с",
	}
	for _, term := range vagueTerms {
		if strings.Contains(lower, term) {
			flags = append(flags, "vague_condition")
			break
		}
	}

	bundleTerms := []string{
		"bundle", " lot ", "complete set", "collection",
		// BG Cyrillic — OLX.bg wedge (XOL-88)
		"комплект", " лот ", "с аксесоари",
	}
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

	// off_platform_redirect: detect scam contact redirect attempts in description only (XOL-80).
	// Check description only (not title) to reduce false positives.
	descLower := strings.ToLower(listing.Description)
	offPlatformASCII := []string{"whatsapp", "viber", "telegram", "signal"}
	offPlatformCyrillic := []string{"пишете на", "пишете ми", "обадете се", "пиши на", "обади се"}
	offPlatformTriggered := false
	for _, term := range offPlatformASCII {
		if strings.Contains(descLower, term) {
			offPlatformTriggered = true
			break
		}
	}
	if !offPlatformTriggered {
		for _, term := range offPlatformCyrillic {
			if strings.Contains(listing.Description, term) {
				offPlatformTriggered = true
				break
			}
		}
	}
	if !offPlatformTriggered && offPlatformPhoneRe.MatchString(listing.Description) {
		offPlatformTriggered = true
	}
	if offPlatformTriggered {
		flags = append(flags, "off_platform_redirect")
	}

	// carrier_locked: phone-only risk flag. Detects carrier/network locking signals
	// in BG Cyrillic and EN (XOL-98).
	if isPhoneOrLaptop(lower) {
		carrierLockedTerms := []string{
			// BG Cyrillic
			"заключен за",
			"заключен към",
			" лок ", // spaces prevent false positives inside words like "локален"
			// EN
			"carrier locked",
			"network locked",
			"sim locked",
			"sim lock",
			"locked to",
		}
		for _, term := range carrierLockedTerms {
			if strings.Contains(lower, term) {
				flags = append(flags, "carrier_locked") // XOL-98
				break
			}
		}
	}

	// screen_replaced: phone-only risk flag. Detects aftermarket or replaced
	// display signals in BG Cyrillic and EN (XOL-98).
	if isPhoneOrLaptop(lower) {
		screenReplacedTerms := []string{
			// BG Cyrillic
			"сменен дисплей",
			"сменен екран",
			"нов дисплей",
			"нов екран",
			"смяна на дисплей",
			// EN
			"display replaced",
			"screen replaced",
			"aftermarket screen",
			"non-original display",
		}
		for _, term := range screenReplacedTerms {
			if strings.Contains(lower, term) {
				flags = append(flags, "screen_replaced") // XOL-98
				break
			}
		}
	}

	seen := make(map[string]bool, len(flags))
	deduped := flags[:0]
	for _, f := range flags {
		if !seen[f] {
			seen[f] = true
			deduped = append(deduped, f)
		}
	}
	return deduped
}

func isElectronicsListing(text string) bool {
	lower := strings.ToLower(text)
	keywords := []string{
		// EN/NL terms (existing)
		"camera", "lens", "laptop", "macbook", "iphone", "ipad", "samsung", "pixel",
		"sony", "nikon", "canon", "fuji", "fujifilm", "gpu", "cpu", "graphics card",
		"smartphone", "tablet", "notebook", "thinkpad", "surface", "playstation", "xbox", "nintendo",
		"monitor", "television", "tv", "router", "modem", "headphone", "airpods", "charger", "battery",
		// BG Cyrillic terms — added for OLX.bg wedge (XOL-35 M3-A); принтер added XOL-80
		"фотоапарат", "камера", "обектив", "лаптоп", "компютър",
		"слушалки", "телефон", "таблет", "принтер",
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
		// EN terms (existing)
		"iphone", "samsung", "pixel", "oneplus", "smartphone", "phone",
		"laptop", "macbook", "notebook", "thinkpad", "surface",
		// BG Cyrillic — OLX.bg wedge (XOL-88)
		"телефон", "смартфон", "лаптоп",
		// BG Cyrillic brand terms — XOL-98
		"айфон",    // iPhone in Bulgarian
		"самсунг",  // Samsung in Cyrillic
		"хуауей",   // Huawei
		"моторола", // Motorola
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
