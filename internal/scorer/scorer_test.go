package scorer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/reasoner"
	"github.com/TechXTT/marktbot/internal/store"
)

func TestScorePrefiltersObviouslyOverBudgetListing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "marktbot-scorer.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	rsn := reasoner.New(config.AIConfig{})
	sc := New(st, config.ScoringConfig{MinScore: 7, MarketSampleSize: 20}, rsn)

	scored := sc.Score(context.Background(), models.Listing{
		ItemID:    "m1",
		Title:     "Sony A7 III",
		Price:     200000,
		PriceType: "fixed",
	}, models.SearchSpec{
		UserID:          "u1",
		Query:           "sony a7 iii",
		MaxPrice:        100000,
		OfferPercentage: 70,
	})

	if scored.ReasoningSource != "prefilter" {
		t.Fatalf("expected prefilter reasoning source, got %q", scored.ReasoningSource)
	}
	if scored.Confidence <= 0 {
		t.Fatalf("expected heuristic confidence to be preserved, got %.2f", scored.Confidence)
	}
}
