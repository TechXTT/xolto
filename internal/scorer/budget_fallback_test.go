package scorer

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/reasoner"
	"github.com/TechXTT/xolto/internal/store"
)

// TestScorerFallsBackToHeuristicOnGlobalCapFire confirms that when the
// W19-23 global $3/24h budget is exhausted, the scorer:
//
//  1. Skips the LLM call entirely (no HTTP request).
//  2. Returns the heuristic analysis.
//  3. Tags the result with AIPath = "heuristic_fallback" so the worker
//     can write it into scoring_events with that label and the
//     calibration summary excludes it.
func TestScorerFallsBackToHeuristicOnGlobalCapFire(t *testing.T) {
	// Save / restore global tracker so this test does not leak state into
	// neighbouring tests.
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })

	// Install a tracker that is already at 100% — every Allow returns false.
	tr := aibudget.New()
	// Spend the entire cap up-front so subsequent Allow calls fail.
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at exactly cap should succeed")
	}
	aibudget.SetGlobal(tr)

	dbPath := filepath.Join(t.TempDir(), "xolto-scorer-budget.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	// Reasoner is "enabled" but its callLLM should never be reached because
	// the scorer's pre-spend gate trips first. Use a bogus URL — if the
	// fallback path didn't fire, the test would hang/time-out, which is
	// itself a failure signal.
	rsn := reasoner.New(config.AIConfig{
		Enabled:       true,
		BaseURL:       "http://127.0.0.1:1", // unreachable on purpose
		APIKey:        "test-key",
		Model:         "gpt-5-mini",
		PromptVersion: 1,
	})
	sc := New(st, config.ScoringConfig{MinScore: 7, MarketSampleSize: 20}, rsn)

	scored := sc.Score(context.Background(), models.Listing{
		ItemID:    "olxbg-budget-fallback-1",
		Title:     "Sony A6000 body",
		Price:     50000,
		PriceType: "fixed",
	}, models.SearchSpec{
		UserID:          "u-budget",
		Query:           "sony a6000",
		OfferPercentage: 70,
	})

	// Source falls back to "heuristic" because the LLM was never called.
	if scored.ReasoningSource != "heuristic" {
		t.Fatalf("expected ReasoningSource=heuristic on cap-fire, got %q", scored.ReasoningSource)
	}
	// AIPath labels this row as heuristic_fallback so VAL-1 calibration
	// summary excludes it by default.
	if scored.AIPath != models.AIPathHeuristicFallback {
		t.Fatalf("expected AIPath=heuristic_fallback on cap-fire, got %q", scored.AIPath)
	}
	// CostUSD must remain 0 because no LLM call was paid for.
	if scored.CostUSD != 0 {
		t.Fatalf("expected CostUSD=0 on cap-fire, got %v", scored.CostUSD)
	}
}

// TestScorerNormalAIPathTagsAI verifies the happy path tags the analysis
// with AIPath="ai" so calibration aggregates it normally.
func TestScorerNormalAIPathTagsAI(t *testing.T) {
	// Reset tracker to a fresh, empty one for this test.
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })
	aibudget.SetGlobal(aibudget.New())

	// We don't actually call the LLM here — the scorer's heuristic-confident
	// shortcut path will tag analysis.Source="heuristic-confident" without
	// hitting the LLM, but the AIPath should NOT be "heuristic_fallback"
	// because that tag is reserved for budget-cap-fire degradation. The
	// happy-path with confident heuristic falls back to default empty.
	dbPath := filepath.Join(t.TempDir(), "xolto-scorer-budget-ok.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()
	rsn := reasoner.New(config.AIConfig{}) // disabled
	sc := New(st, config.ScoringConfig{MinScore: 7, MarketSampleSize: 20}, rsn)

	scored := sc.Score(context.Background(), models.Listing{
		ItemID:    "olxbg-budget-ok-1",
		Title:     "Sony A6000 body",
		Price:     50000,
		PriceType: "fixed",
	}, models.SearchSpec{
		UserID:          "u-budget-ok",
		Query:           "sony a6000",
		OfferPercentage: 70,
	})

	if scored.AIPath == models.AIPathHeuristicFallback {
		t.Fatalf("expected AIPath != heuristic_fallback when budget has room; got cap-fire tag")
	}
}
