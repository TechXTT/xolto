package scorer

import "testing"

// TestThresholdsFor verifies that ThresholdsFor dispatches correctly for
// canonical ids, common variant spellings, unknown ids, and empty input.
func TestThresholdsFor(t *testing.T) {
	cases := []struct {
		name           string
		id             string
		wantMinComps   int
		wantMinScore   float64
		wantBGVariant  bool // true when we expect bgThresholds
	}{
		// Canonical BG identifier.
		{
			name:          "canonical_olxbg",
			id:            "olxbg",
			wantMinComps:  3,
			wantMinScore:  8.0,
			wantBGVariant: true,
		},
		// Common variant with hyphen.
		{
			name:          "variant_olx_dash_bg",
			id:            "OLX-BG",
			wantMinComps:  3,
			wantMinScore:  8.0,
			wantBGVariant: true,
		},
		// Dot-separated variant.
		{
			name:          "variant_olx_dot_bg",
			id:            "olx.bg",
			wantMinComps:  3,
			wantMinScore:  8.0,
			wantBGVariant: true,
		},
		// Unknown marketplace falls through to default.
		{
			name:          "unknown_marktplaats",
			id:            "marktplaats",
			wantMinComps:  6,
			wantMinScore:  8.0,
			wantBGVariant: false,
		},
		// Empty string falls through to default.
		{
			name:          "empty_string",
			id:            "",
			wantMinComps:  6,
			wantMinScore:  8.0,
			wantBGVariant: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ThresholdsFor(tc.id)
			if got.MinComparables != tc.wantMinComps {
				t.Errorf("ThresholdsFor(%q).MinComparables = %d, want %d", tc.id, got.MinComparables, tc.wantMinComps)
			}
			if got.MinScoreForBuy != tc.wantMinScore {
				t.Errorf("ThresholdsFor(%q).MinScoreForBuy = %f, want %f", tc.id, got.MinScoreForBuy, tc.wantMinScore)
			}
			// Verify the full struct matches the expected preset so we catch any
			// future partial-mutation bugs.
			if tc.wantBGVariant {
				if got != bgThresholds {
					t.Errorf("ThresholdsFor(%q) = %+v, want bgThresholds %+v", tc.id, got, bgThresholds)
				}
			} else {
				if got != defaultThresholds {
					t.Errorf("ThresholdsFor(%q) = %+v, want defaultThresholds %+v", tc.id, got, defaultThresholds)
				}
			}
		})
	}
}

// TestNormalizeMarketplaceID verifies individual normalization edge cases.
func TestNormalizeMarketplaceID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"olxbg", "olxbg"},
		{"OLX-BG", "olxbg"},
		{"olx.bg", "olxbg"},
		{"OLX_BG", "olxbg"},
		{"  OLX-BG  ", "olxbg"},
		{"marktplaats", "marktplaats"},
		{"", ""},
	}
	for _, tc := range cases {
		got := normalizeMarketplaceID(tc.in)
		if got != tc.want {
			t.Errorf("normalizeMarketplaceID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
