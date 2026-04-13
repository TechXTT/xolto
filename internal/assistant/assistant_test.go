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
