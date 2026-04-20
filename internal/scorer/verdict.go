package scorer

import (
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

// Recommended action enum values — stable contract consumed by the dash UI.
// Dash must treat these as opaque enum strings and must NOT hardcode thresholds.
const (
	ActionBuy       = "buy"
	ActionNegotiate = "negotiate"
	ActionAskSeller = "ask_seller"
	ActionSkip      = "skip"
)

// hardRiskFlags are fraud/safety signals that disqualify a listing
// entirely — any match routes to SKIP regardless of other signals.
var hardRiskFlags = map[string]struct{}{
	"anomaly_price":        {},
	"off_platform_redirect": {}, // XOL-80: contact redirect = #1 OLX.bg scam vector
	"stolen_risk":          {}, // reserved for future scorer signals
	"identity_suspect":     {}, // reserved for future scorer signals
}

// softRiskFlags are missing-info signals the buyer should resolve by
// messaging the seller — any match routes to ASK SELLER (unless a
// harder rule already disqualifies).
var softRiskFlags = map[string]struct{}{
	"vague_condition":       {},
	"unclear_bundle":        {},
	"no_model_id":           {},
	"missing_key_photos":    {},
	"no_battery_health":     {},
	"refurbished_ambiguity": {},
	// brief-named aliases (kept for forward-compat if scorer adds them)
	"too_few_photos":        {},
	"condition_unclear":     {},
	"weak_comparable_basis": {},
}

// hasHardRiskFlag returns true if any flag in the slice is a hard (fraud/safety) signal.
func hasHardRiskFlag(flags []string) bool {
	for _, f := range flags {
		if _, ok := hardRiskFlags[f]; ok {
			return true
		}
	}
	return false
}

// hasSoftRiskFlag returns true if any flag in the slice is a soft (missing-info) signal
// or an unknown flag (unknown flags default to soft — fail-closed on unknown flags would
// route to SKIP and dismiss listings over signals we haven't audited).
func hasSoftRiskFlag(flags []string) bool {
	for _, f := range flags {
		if _, ok := hardRiskFlags[f]; ok {
			continue // hard flag, not soft
		}
		// Flag is either explicitly soft or unknown — both treated as soft.
		return true
	}
	return false
}

// confidenceBucket converts the float64 confidence value (0–1) used internally
// by the scorer into the three-bucket string the mapping rules operate on.
//
// Thresholds mirror the existing scorer.go bonus/penalty breakpoints:
//
//	>= 0.75 → "high"    (matches "strong comparable confidence" bonus)
//	>= 0.40 → "medium"
//	<  0.40 → "low"     (matches "weak comparable confidence" penalty)
func confidenceBucket(c float64) string {
	if c >= 0.75 {
		return "high"
	}
	if c >= 0.40 {
		return "medium"
	}
	return "low"
}

// isLowLiquidityNiche returns true for category + query combinations that
// qualify for the 90-day comparable freshness fallback per the locked taxonomy.
// Wedge categories: camera bodies without a lens, discontinued laptop lines.
func isLowLiquidityNiche(query string) bool {
	lower := strings.ToLower(query)
	// Camera body without lens: the query mentions a camera body keyword but
	// not a lens keyword.
	cameraBody := containsAny(lower, "camera", "body only", "body-only", "boitier")
	lens := containsAny(lower, "lens", "mm", "objectif", "zoom")
	if cameraBody && !lens {
		return true
	}
	// Discontinued laptop lines.
	discontinued := []string{
		"thinkpad x220", "thinkpad x230", "thinkpad x240", "thinkpad t420", "thinkpad t430",
		"macbook pro 2015", "macbook pro 2014", "macbook pro 2013",
		"macbook air 2015", "macbook air 2014", "macbook air 2013",
		"macbook 2017", "macbook 2016", "macbook 2015",
	}
	for _, d := range discontinued {
		if strings.Contains(lower, d) {
			return true
		}
	}
	return false
}

// maxComparableFreshnessdays returns the maximum age in days among the given
// comparables. Returns 0 if comparables is empty.
func maxComparableAgeDays(comparables []models.ComparableDeal) int {
	if len(comparables) == 0 {
		return 0
	}
	var maxAge int
	now := time.Now()
	for _, c := range comparables {
		if c.LastSeen.IsZero() {
			continue
		}
		days := int(now.Sub(c.LastSeen).Hours() / 24)
		if days > maxAge {
			maxAge = days
		}
	}
	return maxAge
}

// ComputeVerdict derives the recommended_action verdict from the scorer
// signals. It is the single source of truth for the mapping — the dash UI
// consumes the emitted string and must not re-implement this logic.
//
// Precedence (first match wins): SKIP → ASK SELLER → NEGOTIATE → BUY.
// Fallthrough default: ask_seller (trust-preservation bias when signals thin).
//
// Inputs:
//
//	marketplaceID      — marketplace identifier (e.g. "olxbg"); selects threshold set
//	score              — scored value 0–10
//	confidence         — float64 0–1 (internally bucketed to low/medium/high)
//	comparables        — slice used to derive count and freshness
//	query              — used to detect low-liquidity niches for the 90d window
//	priceRatio         — askingPrice / fairPrice (0 when fairPrice is unknown)
//	condition          — listing condition string (new/like_new/good/fair/unknown/"")
//	riskFlags          — trust-signal keys already computed by computeRiskFlags
//	missedMustHaveCount — number of must-haves with status "missed"; "unknown" does not count
func ComputeVerdict(
	marketplaceID string,
	score float64,
	confidence float64,
	comparables []models.ComparableDeal,
	query string,
	priceRatio float64,
	condition string,
	riskFlags []string,
	missedMustHaveCount int,
) string {
	t := ThresholdsFor(marketplaceID)
	confBucket := confidenceBucket(confidence)
	comparableCount := len(comparables)
	maxAgeDays := maxComparableAgeDays(comparables)
	condLower := strings.ToLower(strings.TrimSpace(condition))

	// ----------------------------------------------------------------
	// SKIP — evaluate first; highest precedence
	// ----------------------------------------------------------------
	// 1. price_ratio > MaxPriceRatioSkip
	if priceRatio > t.MaxPriceRatioSkip {
		return ActionSkip
	}
	// 2. any HARD risk flag present (fraud/safety signals)
	if hasHardRiskFlag(riskFlags) {
		return ActionSkip
	}
	// 3. condition "fair" AND price_ratio > 1.00 (strictly above fair)
	if condLower == "fair" && priceRatio > 1.00 {
		return ActionSkip
	}

	// ----------------------------------------------------------------
	// ASK SELLER
	// ----------------------------------------------------------------
	// 1. any SOFT risk flag present (or unknown flag — defaults to soft)
	if hasSoftRiskFlag(riskFlags) {
		return ActionAskSeller
	}
	// 2a. at least one must-have is explicitly missed — stated requirements unconfirmed.
	// "unknown" (listing silent) does not trigger; only explicit "missed" does.
	if missedMustHaveCount > 0 {
		return ActionAskSeller
	}
	// 2. fewer than MinComparables comparables
	if comparableCount < t.MinComparables {
		return ActionAskSeller
	}
	// 3. confidence is low
	if confBucket == "low" {
		return ActionAskSeller
	}

	// ----------------------------------------------------------------
	// NEGOTIATE
	// ----------------------------------------------------------------
	// price_ratio > 1.00 AND <= MaxPriceRatioNegotiate AND confidence medium/high AND
	// no hard flag AND no soft flag
	if priceRatio > 1.00 && priceRatio <= t.MaxPriceRatioNegotiate &&
		(confBucket == "medium" || confBucket == "high") &&
		!hasHardRiskFlag(riskFlags) && !hasSoftRiskFlag(riskFlags) {
		return ActionNegotiate
	}

	// ----------------------------------------------------------------
	// BUY
	// ----------------------------------------------------------------
	// score >= MinScoreForBuy AND confidence medium/high AND comparable_count >= MinComparables AND
	// freshness <= FreshnessDaysDefault (or <= FreshnessDaysLowLiquidity for low-liquidity niche) AND
	// price_ratio <= 1.00 AND condition in {new, like_new, good} AND
	// no risk flags of any kind (both hard and soft disqualify Buy)
	if score >= t.MinScoreForBuy &&
		(confBucket == "medium" || confBucket == "high") &&
		comparableCount >= t.MinComparables &&
		priceRatio <= 1.00 &&
		(condLower == "new" || condLower == "like_new" || condLower == "good") &&
		len(riskFlags) == 0 {

		freshnessLimit := t.FreshnessDaysDefault
		if isLowLiquidityNiche(query) {
			freshnessLimit = t.FreshnessDaysLowLiquidity
		}
		if maxAgeDays <= freshnessLimit {
			return ActionBuy
		}
	}

	// ----------------------------------------------------------------
	// Fallthrough — trust-preservation default
	// ----------------------------------------------------------------
	return ActionAskSeller
}
