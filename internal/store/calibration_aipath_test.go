package store

// W19-23 VAL-1: regression coverage for the ai_path column on
// scoring_events. The migration is additive and back-compat — empty
// AIPath defaults to "ai" so legacy rows aggregate normally.

import (
	"context"
	"testing"
	"time"
)

// TestWriteScoringEventTagsAIPath verifies both ai_path values round-trip
// through the SQLite store correctly, and that empty AIPath defaults to "ai".
func TestWriteScoringEventTagsAIPath(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	type fix struct {
		listingID string
		aiPath    string
		wantPath  string
	}
	cases := []fix{
		{"ai-row", "ai", "ai"},
		{"fallback-row", "heuristic_fallback", "heuristic_fallback"},
		{"legacy-row", "", "ai"}, // empty defaults to "ai"
	}
	for _, c := range cases {
		ev := ScoringEvent{
			ListingID:     c.listingID,
			Marketplace:   "olxbg",
			Score:         5.0,
			Verdict:       "ask_seller",
			Confidence:    0.5,
			Contributions: map[string]float64{"comparables": 5},
			ScorerVersion: ScorerVersionV1,
			AIPath:        c.aiPath,
		}
		if err := st.WriteScoringEvent(ctx, ev); err != nil {
			t.Fatalf("WriteScoringEvent(%s) err: %v", c.listingID, err)
		}
	}

	// Default summary: only ai_path = "ai" rows (ai-row + legacy-row).
	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window:      24 * time.Hour,
		Marketplace: "olxbg",
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary err: %v", err)
	}
	if summary.TotalEvents != 2 {
		t.Errorf("default summary TotalEvents = %d, want 2 (heuristic_fallback excluded)", summary.TotalEvents)
	}

	// IncludeHeuristicFallback: all 3 rows.
	summary, err = st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window:                   24 * time.Hour,
		Marketplace:              "olxbg",
		IncludeHeuristicFallback: true,
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary(include) err: %v", err)
	}
	if summary.TotalEvents != 3 {
		t.Errorf("include summary TotalEvents = %d, want 3", summary.TotalEvents)
	}
}
