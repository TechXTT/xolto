package assistant

import (
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

func TestBuildRecommendationBuyNow(t *testing.T) {
	rec := buildRecommendation(models.ScoredListing{
		Listing: models.Listing{
			ItemID: "1",
			Title:  "Sony A7 III body with battery",
			Price:  85000,
			Attributes: map[string]string{
				"condition": "Zo goed als nieuw",
			},
		},
		Score:      8.5,
		FairPrice:  95000,
		Confidence: 0.72,
		Reason:     "strong value",
	}, models.ShoppingProfile{
		Name:               "Sony A7 III",
		TargetQuery:        "sony a7 iii",
		BudgetMax:          900,
		BudgetStretch:      1000,
		PreferredCondition: []string{"Zo goed als nieuw"},
	})

	if rec.Label != models.RecommendationBuyNow && rec.Label != models.RecommendationWatch {
		t.Fatalf("expected buy_now or worth_watching, got %s", rec.Label)
	}
}

func TestBuildRecommendationSkipsNoPrice(t *testing.T) {
	rec := buildRecommendation(models.ScoredListing{
		Listing: models.Listing{
			ItemID:    "1",
			Title:     "Sony A7 III reserved",
			Price:     0,
			PriceType: "reserved",
		},
		Score:      9,
		FairPrice:  90000,
		Confidence: 0.8,
	}, models.ShoppingProfile{Name: "Sony"})

	if rec.Label != models.RecommendationSkip {
		t.Fatalf("expected skip, got %s", rec.Label)
	}
}

// TestSanitizeSearchQueryBGPricePhrases verifies that BG Cyrillic price
// qualifiers are stripped from search queries (XOL-39 M3-E).
// AC: "камери под 500 лв" → "камери" (budget qualifier removed).
func TestSanitizeSearchQueryBGPricePhrases(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// BG Cyrillic: "under 500 lev"
		{"камери под 500 лв", "камери"},
		// BG Cyrillic: "up to 300"
		{"лаптоп до 300", "лаптоп"},
		// BG Cyrillic: "maximum 1000 lev"
		{"телефон максимум 1000 лв", "телефон"},
		// BG with BGN currency
		{"слушалки под 200 bgn", "слушалки"},
		// EN (regression)
		{"sony camera under 500 eur", "sony camera"},
		// NL (regression)
		{"canon lens tot 400 eur", "canon lens"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := sanitizeSearchQuery(tc.input)
			if got != tc.want {
				t.Errorf("sanitizeSearchQuery(%q): expected %q, got %q", tc.input, tc.want, got)
			}
		})
	}
}

// TestExtractBudgetBG verifies that BG Cyrillic budget markers are extracted
// correctly from natural-language budget specifications (XOL-39 M3-E).
func TestExtractBudgetBG(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"под 500 лв", 500},
		{"до 300", 300},
		{"максимум 1000", 1000},
		{"бюджет 800", 800},
		// EN (regression)
		{"under 600", 600},
		{"max 750", 750},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := extractBudget(tc.input)
			if got != tc.want {
				t.Errorf("extractBudget(%q): expected %d, got %d", tc.input, tc.want, got)
			}
		})
	}
}

// TestPriceWordPatternBGN verifies that лв/BGN currency markers are caught by
// priceWordPattern (XOL-39 M3-E).
func TestPriceWordPatternBGN(t *testing.T) {
	cases := []struct {
		input string
		match bool
	}{
		{"500 лв", true},
		{"200 bgn", true},
		{"700 eur", true},   // regression
		{"300 euro", true},  // regression
		{"sony a6000", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := priceWordPattern.MatchString(tc.input)
			if got != tc.match {
				t.Errorf("priceWordPattern.MatchString(%q): expected %v, got %v", tc.input, tc.match, got)
			}
		})
	}
}
