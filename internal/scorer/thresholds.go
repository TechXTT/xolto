package scorer

import "strings"

// Thresholds bundles the knobs ComputeVerdict uses. Kept in a struct so the
// dispatch layer can return a marketplace-specific set without scattering
// literals through the codebase.
type Thresholds struct {
	// MinComparables is the minimum comparables count required for BUY and to
	// exit the low-comparables ASK SELLER gate.
	MinComparables int
	// MinScoreForBuy is the minimum score required for BUY.
	MinScoreForBuy float64
	// MaxPriceRatioSkip triggers SKIP when priceRatio is strictly above this.
	MaxPriceRatioSkip float64
	// MaxPriceRatioNegotiate is the upper bound for the NEGOTIATE price band.
	MaxPriceRatioNegotiate float64
	// FreshnessDaysDefault is the comparable freshness limit for BUY in
	// non-low-liquidity niches.
	FreshnessDaysDefault int
	// FreshnessDaysLowLiquidity is the comparable freshness limit for BUY in
	// low-liquidity niches (camera bodies, discontinued laptop lines).
	FreshnessDaysLowLiquidity int
}

// defaultThresholds preserves the pre-XOL-36 Marktplaats-tuned behavior.
// All non-BG marketplaces fall through to this. Do not change these values
// without justifying the NL regression impact in the PR body.
var defaultThresholds = Thresholds{
	MinComparables:            6,
	MinScoreForBuy:            8.0,
	MaxPriceRatioSkip:         1.30,
	MaxPriceRatioNegotiate:    1.30,
	FreshnessDaysDefault:      60,
	FreshnessDaysLowLiquidity: 90,
}

// bgThresholds reflects OLX BG's thinner query liquidity. The comparable
// floor is relaxed so that decisional clarity is restored on BG verdicts;
// score, price, and freshness floors are kept identical to NL so the quality
// bar is unchanged.
var bgThresholds = Thresholds{
	MinComparables:            3,
	MinScoreForBuy:            8.0,
	MaxPriceRatioSkip:         1.30,
	MaxPriceRatioNegotiate:    1.30,
	FreshnessDaysDefault:      60,
	FreshnessDaysLowLiquidity: 90,
}

// ThresholdsFor returns the Thresholds for the given marketplace id. Unknown
// or empty ids fall through to defaultThresholds.
func ThresholdsFor(marketplaceID string) Thresholds {
	switch normalizeMarketplaceID(marketplaceID) {
	case "olxbg":
		return bgThresholds
	default:
		return defaultThresholds
	}
}

// normalizeMarketplaceID lowercases, trims, and collapses common variants
// (olx-bg, olx.bg → olxbg). Kept defensive because ingest is not strictly
// normalized yet.
func normalizeMarketplaceID(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}
