package scorer

import (
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

// freshComparables returns n ComparableDeal values all seen within recentDays.
func freshComparables(n, recentDays int) []models.ComparableDeal {
	deals := make([]models.ComparableDeal, n)
	for i := range deals {
		deals[i] = models.ComparableDeal{
			ItemID:   "comp-" + string(rune('a'+i)),
			Price:    50000,
			LastSeen: time.Now().Add(-time.Duration(recentDays) * 24 * time.Hour),
		}
	}
	return deals
}

// staleComparables returns n ComparableDeal values seen staleDays ago.
func staleComparables(n, staleDays int) []models.ComparableDeal {
	return freshComparables(n, staleDays)
}

// TestComputeVerdictPrecedence verifies every rule branch is reachable and the
// SKIP → ASK SELLER → NEGOTIATE → BUY precedence order is respected.
func TestComputeVerdictPrecedence(t *testing.T) {
	enoughComps := freshComparables(6, 30)
	highConf := 0.80  // "high" bucket
	medConf := 0.55   // "medium" bucket
	lowConf := 0.30   // "low" bucket

	cases := []struct {
		name        string
		score       float64
		confidence  float64
		comparables []models.ComparableDeal
		query       string
		priceRatio  float64
		condition   string
		riskFlags   []string
		wantAction  string
	}{
		// ----------------------------------------------------------------
		// SKIP branch 1: price_ratio > 1.30
		// ----------------------------------------------------------------
		{
			name:        "skip/price_ratio_131",
			score:       6.0,
			confidence:  highConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.31,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionSkip,
		},
		{
			name:        "skip/price_ratio_200",
			score:       9.0,
			confidence:  highConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  2.00,
			condition:   "new",
			riskFlags:   nil,
			wantAction:  ActionSkip,
		},
		// ----------------------------------------------------------------
		// SKIP branch 2: hard risk flag present (fraud/safety signal)
		// ----------------------------------------------------------------
		{
			name:        "skip/hard_flag_anomaly_price",
			score:       8.0,
			confidence:  highConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  0.95,
			condition:   "good",
			riskFlags:   []string{"anomaly_price"},
			wantAction:  ActionSkip,
		},
		// ----------------------------------------------------------------
		// SKIP branch 3: condition "fair" AND price_ratio > 1.00
		// ----------------------------------------------------------------
		{
			name:        "skip/fair_condition_overpriced",
			score:       6.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.05,
			condition:   "fair",
			riskFlags:   nil,
			wantAction:  ActionSkip,
		},

		// ----------------------------------------------------------------
		// ASK SELLER branch 1: fewer than 6 comparables
		// ----------------------------------------------------------------
		{
			name:        "ask_seller/too_few_comparables",
			score:       7.0,
			confidence:  medConf,
			comparables: freshComparables(5, 10),
			query:       "sony a6000",
			priceRatio:  0.90,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionAskSeller,
		},
		{
			name:        "ask_seller/zero_comparables",
			score:       8.0,
			confidence:  highConf,
			comparables: nil, // zero comparables — explicit edge case
			query:       "sony a6000",
			priceRatio:  0.90,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionAskSeller,
		},
		// ----------------------------------------------------------------
		// ASK SELLER branch 2: confidence == "low"
		// ----------------------------------------------------------------
		{
			name:        "ask_seller/low_confidence",
			score:       8.0,
			confidence:  lowConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  0.90,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionAskSeller,
		},
		// ----------------------------------------------------------------
		// ASK SELLER branch 3: soft risk flags route to ASK SELLER (not SKIP).
		// Separately test the too-few-comps path as an independent ASK SELLER trigger.
		// ----------------------------------------------------------------
		{
			name:        "ask_seller/five_comps_medium_conf_at_fair",
			score:       8.0,
			confidence:  medConf,
			comparables: freshComparables(5, 10), // < 6 → ASK SELLER
			query:       "sony a6000",
			priceRatio:  0.90,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionAskSeller,
		},
		// ----------------------------------------------------------------
		// ASK SELLER: soft flag at a price that would otherwise be NEGOTIATE
		// ----------------------------------------------------------------
		{
			name:        "ask_seller/soft_flag_missing_photos_at_negotiate_price",
			score:       7.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.20, // would be NEGOTIATE without the flag
			condition:   "good",
			riskFlags:   []string{"missing_key_photos"},
			wantAction:  ActionAskSeller,
		},
		// ----------------------------------------------------------------
		// ASK SELLER: soft flag at a fair price (below 1.00)
		// ----------------------------------------------------------------
		{
			name:        "ask_seller/soft_flag_vague_condition_at_fair_price",
			score:       7.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  0.95, // price below fair, but vague_condition blocks BUY
			condition:   "good",
			riskFlags:   []string{"vague_condition"},
			wantAction:  ActionAskSeller,
		},
		// ----------------------------------------------------------------
		// ASK SELLER: unknown flag defaults to soft (fail-to-soft policy)
		// ----------------------------------------------------------------
		{
			name:        "ask_seller/unknown_flag_defaults_soft",
			score:       7.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  0.95,
			condition:   "good",
			riskFlags:   []string{"future_flag_xyz"}, // not in hard or soft allowlist → treated as soft
			wantAction:  ActionAskSeller,
		},

		// ----------------------------------------------------------------
		// SKIP: hard flag beats a co-present soft flag
		// ----------------------------------------------------------------
		{
			name:        "skip/hard_flag_beats_soft_flag",
			score:       8.0,
			confidence:  highConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  0.95,
			condition:   "good",
			riskFlags:   []string{"anomaly_price", "missing_key_photos"}, // both present → hard wins → SKIP
			wantAction:  ActionSkip,
		},

		// ----------------------------------------------------------------
		// NEGOTIATE
		// ----------------------------------------------------------------
		{
			name:        "negotiate/no_flags_price_in_band",
			score:       7.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.15, // in negotiate band, no flags, medium confidence
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionNegotiate,
		},
		{
			name:        "negotiate/price_slightly_above_fair",
			score:       7.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.10,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionNegotiate,
		},
		{
			name:        "negotiate/price_at_boundary_130",
			score:       7.0,
			confidence:  highConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.30, // exactly 1.30 — NOT > 1.30 so does not skip
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionNegotiate,
		},
		{
			name:        "negotiate/high_confidence",
			score:       7.0,
			confidence:  highConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  1.15,
			condition:   "like_new",
			riskFlags:   nil,
			wantAction:  ActionNegotiate,
		},

		// ----------------------------------------------------------------
		// BUY — all conditions satisfied
		// ----------------------------------------------------------------
		{
			name:        "buy/all_conditions_met",
			score:       9.0,
			confidence:  highConf,
			comparables: freshComparables(6, 30),
			query:       "sony a6000",
			priceRatio:  0.95,
			condition:   "like_new",
			riskFlags:   nil,
			wantAction:  ActionBuy,
		},
		{
			name:        "buy/score_exactly_8",
			score:       8.0,
			confidence:  medConf,
			comparables: freshComparables(6, 45),
			query:       "macbook pro",
			priceRatio:  1.00, // exactly at-or-below fair
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionBuy,
		},
		{
			name:        "buy/medium_confidence_qualifies",
			score:       8.5,
			confidence:  medConf,
			comparables: freshComparables(8, 15),
			query:       "sony a6000",
			priceRatio:  0.80,
			condition:   "new",
			riskFlags:   nil,
			wantAction:  ActionBuy,
		},
		// BUY with low-liquidity 90d freshness fallback
		{
			name:        "buy/low_liquidity_90d_window",
			score:       8.5,
			confidence:  highConf,
			comparables: freshComparables(6, 85), // 85 days — beyond 60d, within 90d
			query:       "camera body only",        // low-liquidity niche keyword
			priceRatio:  0.90,
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionBuy,
		},
		// BUY requires zero flags — all BUY conditions green but one soft flag → ASK SELLER
		{
			name:        "buy/requires_zero_flags",
			score:       9.0,
			confidence:  highConf,
			comparables: freshComparables(6, 20),
			query:       "sony a6000",
			priceRatio:  0.90, // below fair, would be BUY without flag
			condition:   "good",
			riskFlags:   []string{"missing_key_photos"}, // soft flag → blocks BUY, routes to ASK SELLER
			wantAction:  ActionAskSeller,
		},

		// ----------------------------------------------------------------
		// Fallthrough → ASK SELLER
		// ----------------------------------------------------------------
		{
			name:        "fallthrough/no_rule_matches",
			score:       5.0,
			confidence:  medConf,
			comparables: enoughComps,
			query:       "sony a6000",
			priceRatio:  0.0, // price unknown (no fair price)
			condition:   "good",
			riskFlags:   nil,
			wantAction:  ActionAskSeller,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeVerdict(tc.score, tc.confidence, tc.comparables, tc.query, tc.priceRatio, tc.condition, tc.riskFlags)
			if got != tc.wantAction {
				t.Errorf("ComputeVerdict() = %q, want %q\n  score=%.1f conf=%.2f comps=%d query=%q priceRatio=%.2f condition=%q riskFlags=%v",
					got, tc.wantAction, tc.score, tc.confidence, len(tc.comparables), tc.query, tc.priceRatio, tc.condition, tc.riskFlags)
			}
		})
	}
}

// TestComputeVerdictEdgeCases covers the four named edge cases from the brief.
func TestComputeVerdictEdgeCases(t *testing.T) {
	enoughComps := freshComparables(6, 30)

	t.Run("edge/zero_comparables_listing", func(t *testing.T) {
		// Zero-comparable listing → must be ASK SELLER, not Buy or Negotiate.
		got := ComputeVerdict(9.0, 0.80, nil, "sony a6000", 0.90, "good", nil)
		if got != ActionAskSeller {
			t.Errorf("zero comparables: got %q, want %q", got, ActionAskSeller)
		}
	})

	t.Run("edge/score9_price_ratio_101_must_be_negotiate", func(t *testing.T) {
		// score=9, price_ratio=1.01 → price > 1.00 → NEGOTIATE, not BUY.
		// BUY requires price_ratio <= 1.00; since 1.01 > 1.00, BUY is blocked.
		// NEGOTIATE fires because: price_ratio > 1.00, <= 1.30, confidence medium+, no red flags.
		got := ComputeVerdict(9.0, 0.80, enoughComps, "sony a6000", 1.01, "good", nil)
		if got != ActionNegotiate {
			t.Errorf("score=9 priceRatio=1.01: got %q, want %q", got, ActionNegotiate)
		}
	})

	t.Run("edge/fair_condition_price_ratio_exactly_100_not_skip", func(t *testing.T) {
		// condition="fair" + price_ratio == 1.00 → NOT Skip.
		// SKIP rule is: condition="fair" AND price_ratio > 1.00 (strictly greater).
		// At exactly 1.00 the Skip rule must NOT fire.
		// With 6 comparables, medium+ confidence, price_ratio==1.00, condition fair,
		// the NEGOTIATE rule also won't fire (price_ratio not > 1.00).
		// BUY requires condition in {new, like_new, good} — "fair" disqualifies.
		// Fallthrough → ASK SELLER.
		got := ComputeVerdict(8.0, 0.80, enoughComps, "sony a6000", 1.00, "fair", nil)
		if got == ActionSkip {
			t.Errorf("fair + priceRatio=1.00 must NOT be Skip; got %q", got)
		}
		// Expected: ASK SELLER (fair condition disqualifies BUY; price not > 1.00 so no negotiate)
		if got != ActionAskSeller {
			t.Errorf("fair + priceRatio=1.00: got %q, want %q", got, ActionAskSeller)
		}
	})

	t.Run("edge/hard_flag_score10_still_skip", func(t *testing.T) {
		// hard risk flag (anomaly_price) + score=10 → SKIP (hard flags have highest
		// precedence after price > 1.30, regardless of score).
		got := ComputeVerdict(10.0, 0.95, enoughComps, "sony a6000", 0.80, "new", []string{"anomaly_price"})
		if got != ActionSkip {
			t.Errorf("hard_flag + score=10: got %q, want %q", got, ActionSkip)
		}
	})
}

// TestComputeVerdictBuyFreshnessGate ensures the 60-day freshness gate on BUY
// works correctly and the 90-day fallback fires only for low-liquidity niches.
func TestComputeVerdictBuyFreshnessGate(t *testing.T) {
	highConf := 0.80

	t.Run("buy/stale_61d_normal_niche_not_buy", func(t *testing.T) {
		// Standard niche: 61-day-old comparables → freshness > 60d → BUY blocked.
		comps := staleComparables(6, 61)
		got := ComputeVerdict(9.0, highConf, comps, "sony a6000", 0.90, "good", nil)
		// 61 days > 60d limit for normal niche; BUY is blocked.
		// NEGOTIATE: price_ratio=0.90 which is NOT > 1.00 → Negotiate won't fire.
		// Fallthrough → ASK SELLER.
		if got == ActionBuy {
			t.Errorf("stale 61d normal niche: got Buy, should not be Buy")
		}
	})

	t.Run("buy/stale_61d_low_liquidity_niche_is_buy", func(t *testing.T) {
		// Low-liquidity camera body niche: 61-day comparables → within 90d window → BUY.
		comps := staleComparables(6, 61)
		got := ComputeVerdict(9.0, highConf, comps, "camera body only", 0.90, "good", nil)
		if got != ActionBuy {
			t.Errorf("stale 61d low-liquidity niche: got %q, want buy", got)
		}
	})

	t.Run("buy/stale_91d_low_liquidity_niche_not_buy", func(t *testing.T) {
		// Even low-liquidity niche: 91-day comparables → beyond 90d window → BUY blocked.
		comps := staleComparables(6, 91)
		got := ComputeVerdict(9.0, highConf, comps, "camera body only", 0.90, "good", nil)
		if got == ActionBuy {
			t.Errorf("stale 91d even low-liquidity: got Buy, should not be Buy")
		}
	})
}

// TestConfidenceBucket validates the internal threshold mapping.
func TestConfidenceBucket(t *testing.T) {
	cases := []struct {
		conf float64
		want string
	}{
		{0.00, "low"},
		{0.35, "low"},
		{0.39, "low"},
		{0.40, "medium"},
		{0.60, "medium"},
		{0.74, "medium"},
		{0.75, "high"},
		{0.90, "high"},
		{1.00, "high"},
	}
	for _, tc := range cases {
		got := confidenceBucket(tc.conf)
		if got != tc.want {
			t.Errorf("confidenceBucket(%.2f) = %q, want %q", tc.conf, got, tc.want)
		}
	}
}

// TestIsLowLiquidityNiche validates the 90-day freshness allowlist.
func TestIsLowLiquidityNiche(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{"camera body only", true},
		{"camera body-only mirrorless", true},
		{"sony a6000 body only", true},
		{"nikon d750 body only", true},
		{"canon 5d body only", true},
		// Lens included → not low-liquidity camera-body niche
		{"sony a6000 with 50mm lens", false},
		// Discontinued laptops
		{"thinkpad x220", true},
		{"thinkpad x230 i7", true},
		{"macbook pro 2015 retina", true},
		{"macbook air 2014", true},
		// Current models → not low-liquidity
		{"macbook pro 2023", false},
		{"thinkpad x1 carbon", false},
		// Other electronics → not low-liquidity
		{"sony a7 iii", false},
		{"laptop hp elitebook", false},
	}
	for _, tc := range cases {
		got := isLowLiquidityNiche(tc.query)
		if got != tc.want {
			t.Errorf("isLowLiquidityNiche(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
}

// TestMaxComparableAgeDays validates the freshness calculation.
func TestMaxComparableAgeDays(t *testing.T) {
	t.Run("empty_comparables_returns_zero", func(t *testing.T) {
		got := maxComparableAgeDays(nil)
		if got != 0 {
			t.Errorf("empty comparables: got %d, want 0", got)
		}
	})

	t.Run("single_comparable_30_days_old", func(t *testing.T) {
		comps := freshComparables(1, 30)
		got := maxComparableAgeDays(comps)
		// Allow ±1 day for clock jitter in tests.
		if got < 29 || got > 31 {
			t.Errorf("30-day comparable: got %d days, want ~30", got)
		}
	})

	t.Run("mixed_ages_returns_max", func(t *testing.T) {
		comps := []models.ComparableDeal{
			{LastSeen: time.Now().Add(-10 * 24 * time.Hour)},
			{LastSeen: time.Now().Add(-50 * 24 * time.Hour)},
			{LastSeen: time.Now().Add(-20 * 24 * time.Hour)},
		}
		got := maxComparableAgeDays(comps)
		if got < 49 || got > 51 {
			t.Errorf("mixed ages: got %d days, want ~50", got)
		}
	})

	t.Run("zero_time_comparable_ignored", func(t *testing.T) {
		comps := []models.ComparableDeal{
			{LastSeen: time.Time{}},   // zero value — must be skipped
			{LastSeen: time.Now().Add(-20 * 24 * time.Hour)},
		}
		got := maxComparableAgeDays(comps)
		if got < 19 || got > 21 {
			t.Errorf("zero-time comparable: got %d days, want ~20", got)
		}
	})
}

// TestComputeVerdictNegotiateBoundaryNotSkip verifies the price_ratio=1.30
// boundary: exactly 1.30 is Negotiate (not Skip) because Skip requires > 1.30.
func TestComputeVerdictNegotiateBoundaryNotSkip(t *testing.T) {
	comps := freshComparables(6, 20)
	// price_ratio exactly 1.30: NOT > 1.30, so SKIP branch 1 does not fire.
	got := ComputeVerdict(7.0, 0.80, comps, "sony a6000", 1.30, "good", nil)
	if got == ActionSkip {
		t.Errorf("priceRatio=1.30 must NOT be Skip (boundary is > 1.30); got %q", got)
	}
	if got != ActionNegotiate {
		t.Errorf("priceRatio=1.30: got %q, want negotiate", got)
	}
}

// TestComputeVerdictSkipBoundaryAbove130 verifies price_ratio=1.301 is Skip.
func TestComputeVerdictSkipBoundaryAbove130(t *testing.T) {
	comps := freshComparables(6, 20)
	got := ComputeVerdict(7.0, 0.80, comps, "sony a6000", 1.301, "good", nil)
	if got != ActionSkip {
		t.Errorf("priceRatio=1.301: got %q, want skip", got)
	}
}

// TestComputeVerdictBuyConditionGate verifies "fair" condition blocks BUY.
func TestComputeVerdictBuyConditionGate(t *testing.T) {
	comps := freshComparables(6, 20)
	// All other BUY conditions met, but condition="fair" → NOT Buy.
	got := ComputeVerdict(9.0, 0.80, comps, "sony a6000", 0.90, "fair", nil)
	if got == ActionBuy {
		t.Errorf("condition=fair must not produce Buy; got %q", got)
	}
}

// TestComputeVerdictBuyExactlyAtFair verifies price_ratio=1.00 qualifies for BUY.
func TestComputeVerdictBuyExactlyAtFair(t *testing.T) {
	comps := freshComparables(6, 30)
	// price_ratio exactly 1.00 — "at or below fair" → BUY qualifies.
	got := ComputeVerdict(9.0, 0.80, comps, "sony a6000", 1.00, "good", nil)
	if got != ActionBuy {
		t.Errorf("priceRatio=1.00: got %q, want buy", got)
	}
}

// TestComputeVerdictEnumValues ensures only the four expected string values are emitted.
func TestComputeVerdictEnumValues(t *testing.T) {
	validActions := map[string]bool{
		ActionBuy:       true,
		ActionNegotiate: true,
		ActionAskSeller: true,
		ActionSkip:      true,
	}

	// Exercise each rule branch once and verify the emitted string is valid.
	cases := []struct {
		name        string
		score       float64
		confidence  float64
		comparables []models.ComparableDeal
		priceRatio  float64
		condition   string
		riskFlags   []string
	}{
		{"skip_high_price", 9.0, 0.9, freshComparables(6, 10), 1.50, "good", nil},
		{"ask_few_comps", 8.0, 0.8, freshComparables(3, 10), 0.80, "good", nil},
		{"negotiate", 7.0, 0.7, freshComparables(6, 10), 1.15, "good", nil},
		{"buy", 9.0, 0.8, freshComparables(6, 10), 0.90, "good", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeVerdict(tc.score, tc.confidence, tc.comparables, "sony a6000", tc.priceRatio, tc.condition, tc.riskFlags)
			if !validActions[got] {
				t.Errorf("ComputeVerdict emitted invalid action %q (must be one of buy|negotiate|ask_seller|skip)", got)
			}
		})
	}
}
