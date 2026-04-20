package store

// Unit tests for VAL-1a calibration aggregation helpers.
//
// Tests cover:
//   - confidenceBucket: 0.1-band bucketing logic
//   - fairPriceDeltaBucket: score-unit bucket boundaries
//   - GetCalibrationSummary via SQLite (verdict counts, histogram, delta)
//   - WriteScoringEvent round-trip via SQLite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Unit tests for bucketing helpers
// ---------------------------------------------------------------------------

func TestConfidenceBucket(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.0, "0.0"},
		{0.09, "0.0"},
		{0.10, "0.1"},
		{0.35, "0.3"},
		{0.70, "0.7"},
		{0.75, "0.7"},
		{0.79, "0.7"},
		{0.80, "0.8"},
		{0.99, "0.9"},
		{1.0, "0.9"},  // clamped into 0.9 bucket
		{-0.1, "0.0"}, // clamped
		{1.5, "0.9"},  // clamped
	}
	for _, tc := range cases {
		got := confidenceBucket(tc.in)
		if got != tc.want {
			t.Errorf("confidenceBucket(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFairPriceDeltaBucket(t *testing.T) {
	cases := []struct {
		comp float64
		want string
	}{
		{3.0, "very_underpriced"},
		{2.0, "very_underpriced"},
		{1.9, "underpriced"},
		{1.0, "underpriced"},
		{0.9, "fair"},
		{0.0, "fair"},
		{-0.9, "fair"},
		{-1.0, "overpriced"},
		{-1.5, "overpriced"},
		{-1.999, "overpriced"},
		{-2.0, "very_overpriced"},
		{-3.0, "very_overpriced"},
	}
	for _, tc := range cases {
		got := fairPriceDeltaBucket(tc.comp)
		if got != tc.want {
			t.Errorf("fairPriceDeltaBucket(%v) = %q, want %q", tc.comp, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// SQLite integration tests for WriteScoringEvent + GetCalibrationSummary
// ---------------------------------------------------------------------------

func openTestSQLite(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	st, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestWriteScoringEventRoundTrip(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	contribs := map[string]float64{
		"comparables":        6.5,
		"confidence":         0.4,
		"negotiable":         0.0,
		"recency":            0.0,
		"condition":          0.0,
		"category_condition": 0.0,
	}
	missionID := int64(42)
	ev := ScoringEvent{
		ListingID:     "test-item-1",
		Marketplace:   "olxbg",
		MissionID:     &missionID,
		Score:         7.3,
		Verdict:       "negotiate",
		Confidence:    0.72,
		Contributions: contribs,
		ScorerVersion: ScorerVersionV1,
	}

	if err := st.WriteScoringEvent(ctx, ev); err != nil {
		t.Fatalf("WriteScoringEvent() error = %v", err)
	}

	// Confirm row count via summary.
	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window:      24 * time.Hour,
		Marketplace: "olxbg",
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}
	if summary.TotalEvents != 1 {
		t.Errorf("TotalEvents = %d, want 1", summary.TotalEvents)
	}
	if summary.VerdictCounts["negotiate"] != 1 {
		t.Errorf("VerdictCounts[negotiate] = %d, want 1", summary.VerdictCounts["negotiate"])
	}
}

func TestCalibrationVerdictCounts(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	type fix struct {
		listingID string
		verdict   string
		score     float64
		conf      float64
	}
	fixtures := []fix{
		{"cal-buy-1", "buy", 9.0, 0.85},
		{"cal-buy-2", "buy", 8.5, 0.80},
		{"cal-neg-1", "negotiate", 7.0, 0.65},
		{"cal-ask-1", "ask_seller", 5.5, 0.35},
		{"cal-skip-1", "skip", 2.0, 0.20},
		{"cal-skip-2", "skip", 1.5, 0.10},
	}

	contribs := map[string]float64{"comparables": 5.0, "confidence": 0.0}
	for _, f := range fixtures {
		ev := ScoringEvent{
			ListingID:     f.listingID,
			Marketplace:   "olxbg",
			Score:         f.score,
			Verdict:       f.verdict,
			Confidence:    f.conf,
			Contributions: contribs,
			ScorerVersion: ScorerVersionV1,
		}
		if err := st.WriteScoringEvent(ctx, ev); err != nil {
			t.Fatalf("WriteScoringEvent(%s) error = %v", f.listingID, err)
		}
	}

	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}

	if summary.TotalEvents != len(fixtures) {
		t.Errorf("TotalEvents = %d, want %d", summary.TotalEvents, len(fixtures))
	}
	if summary.VerdictCounts["buy"] != 2 {
		t.Errorf("VerdictCounts[buy] = %d, want 2", summary.VerdictCounts["buy"])
	}
	if summary.VerdictCounts["negotiate"] != 1 {
		t.Errorf("VerdictCounts[negotiate] = %d, want 1", summary.VerdictCounts["negotiate"])
	}
	if summary.VerdictCounts["skip"] != 2 {
		t.Errorf("VerdictCounts[skip] = %d, want 2", summary.VerdictCounts["skip"])
	}
}

func TestCalibrationConfidenceHistogram(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	// Two "buy" events: one high-confidence (0.85 → bucket "0.8"),
	// one medium-confidence (0.65 → bucket "0.6").
	contribs := map[string]float64{"comparables": 5.0}
	events := []ScoringEvent{
		{ListingID: "hist-1", Marketplace: "olxbg", Score: 9.0, Verdict: "buy", Confidence: 0.85, Contributions: contribs, ScorerVersion: ScorerVersionV1},
		{ListingID: "hist-2", Marketplace: "olxbg", Score: 8.0, Verdict: "buy", Confidence: 0.65, Contributions: contribs, ScorerVersion: ScorerVersionV1},
	}
	for _, ev := range events {
		if err := st.WriteScoringEvent(ctx, ev); err != nil {
			t.Fatalf("WriteScoringEvent() error = %v", err)
		}
	}

	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}

	buyHist := summary.ConfidenceHistogram["buy"]
	if buyHist == nil {
		t.Fatal("ConfidenceHistogram[buy] is nil")
	}
	if buyHist["0.8"] != 1 {
		t.Errorf("ConfidenceHistogram[buy][0.8] = %d, want 1", buyHist["0.8"])
	}
	if buyHist["0.6"] != 1 {
		t.Errorf("ConfidenceHistogram[buy][0.6] = %d, want 1", buyHist["0.6"])
	}
}

func TestCalibrationFairPriceDelta(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	// Two "buy" events: underpriced (comparables=1.5) and very_underpriced (comparables=2.5).
	events := []ScoringEvent{
		{
			ListingID:     "delta-1",
			Marketplace:   "olxbg",
			Score:         9.0,
			Verdict:       "buy",
			Confidence:    0.80,
			Contributions: map[string]float64{"comparables": 1.5},
			ScorerVersion: ScorerVersionV1,
		},
		{
			ListingID:     "delta-2",
			Marketplace:   "olxbg",
			Score:         9.5,
			Verdict:       "buy",
			Confidence:    0.85,
			Contributions: map[string]float64{"comparables": 2.5},
			ScorerVersion: ScorerVersionV1,
		},
	}
	for _, ev := range events {
		if err := st.WriteScoringEvent(ctx, ev); err != nil {
			t.Fatalf("WriteScoringEvent() error = %v", err)
		}
	}

	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}

	buyDelta := summary.FairPriceDelta["buy"]
	if buyDelta == nil {
		t.Fatal("FairPriceDelta[buy] is nil")
	}
	if buyDelta["underpriced"] != 1 {
		t.Errorf("FairPriceDelta[buy][underpriced] = %d, want 1", buyDelta["underpriced"])
	}
	if buyDelta["very_underpriced"] != 1 {
		t.Errorf("FairPriceDelta[buy][very_underpriced] = %d, want 1", buyDelta["very_underpriced"])
	}
}

func TestCalibrationWindowFilter(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	// Insert one event now and rely on window filtering.
	// (We can't easily insert past-timestamp rows via store API,
	// so this test verifies that a 1d window captures a just-written row.)
	ev := ScoringEvent{
		ListingID:     "window-1",
		Marketplace:   "olxbg",
		Score:         7.0,
		Verdict:       "negotiate",
		Confidence:    0.70,
		Contributions: map[string]float64{"comparables": 0.5},
		ScorerVersion: ScorerVersionV1,
	}
	if err := st.WriteScoringEvent(ctx, ev); err != nil {
		t.Fatalf("WriteScoringEvent() error = %v", err)
	}

	// 1-day window should include the event.
	s1, err := st.GetCalibrationSummary(ctx, CalibrationQuery{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GetCalibrationSummary(1d) error = %v", err)
	}
	if s1.TotalEvents < 1 {
		t.Errorf("1d window: TotalEvents = %d, want >= 1", s1.TotalEvents)
	}
	if s1.WindowDays != 1 {
		t.Errorf("WindowDays = %d, want 1", s1.WindowDays)
	}
}

func TestCalibrationMarketplaceFilter(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	events := []ScoringEvent{
		{ListingID: "mp-1", Marketplace: "olxbg", Score: 7.0, Verdict: "buy", Confidence: 0.8, Contributions: map[string]float64{"comparables": 1.5}, ScorerVersion: ScorerVersionV1},
		{ListingID: "mp-2", Marketplace: "marktplaats", Score: 6.0, Verdict: "negotiate", Confidence: 0.6, Contributions: map[string]float64{"comparables": 0.5}, ScorerVersion: ScorerVersionV1},
	}
	for _, ev := range events {
		if err := st.WriteScoringEvent(ctx, ev); err != nil {
			t.Fatalf("WriteScoringEvent() error = %v", err)
		}
	}

	// Filter to olxbg only — should return 1 event.
	s, err := st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window:      24 * time.Hour,
		Marketplace: "olxbg",
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary(olxbg) error = %v", err)
	}
	if s.TotalEvents != 1 {
		t.Errorf("olxbg filter: TotalEvents = %d, want 1", s.TotalEvents)
	}
	if s.VerdictCounts["buy"] != 1 {
		t.Errorf("olxbg filter: VerdictCounts[buy] = %d, want 1", s.VerdictCounts["buy"])
	}

	// All-marketplace (no filter) — should return 2 events.
	all, err := st.GetCalibrationSummary(ctx, CalibrationQuery{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GetCalibrationSummary(all) error = %v", err)
	}
	if all.TotalEvents != 2 {
		t.Errorf("all-marketplace: TotalEvents = %d, want 2", all.TotalEvents)
	}
}

func TestCalibrationNullMissionID(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	// Event with no mission_id should write and query cleanly.
	ev := ScoringEvent{
		ListingID:     "nomission-1",
		Marketplace:   "olxbg",
		MissionID:     nil,
		Score:         5.0,
		Verdict:       "ask_seller",
		Confidence:    0.35,
		Contributions: map[string]float64{"comparables": 0.0},
		ScorerVersion: ScorerVersionV1,
	}
	if err := st.WriteScoringEvent(ctx, ev); err != nil {
		t.Fatalf("WriteScoringEvent(nil missionID) error = %v", err)
	}

	s, err := st.GetCalibrationSummary(ctx, CalibrationQuery{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}
	if s.TotalEvents != 1 {
		t.Errorf("TotalEvents = %d, want 1", s.TotalEvents)
	}
}

func TestCalibrationOutcomeAttribution(t *testing.T) {
	st := openTestSQLite(t)
	ctx := context.Background()

	// Insert a scoring event. Since we have no outreach_threads row for this
	// listing, the outcome should count as "unknown".
	ev := ScoringEvent{
		ListingID:     "outcome-1",
		Marketplace:   "olxbg",
		Score:         8.0,
		Verdict:       "buy",
		Confidence:    0.80,
		Contributions: map[string]float64{"comparables": 2.0},
		ScorerVersion: ScorerVersionV1,
	}
	if err := st.WriteScoringEvent(ctx, ev); err != nil {
		t.Fatalf("WriteScoringEvent() error = %v", err)
	}

	s, err := st.GetCalibrationSummary(ctx, CalibrationQuery{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}
	if s.OutcomeAttribution["unknown"] != 1 {
		t.Errorf("OutcomeAttribution[unknown] = %d, want 1", s.OutcomeAttribution["unknown"])
	}
}

// ---------------------------------------------------------------------------
// Postgres integration test
// ---------------------------------------------------------------------------

// TestPostgresCalibrationSummary writes N scoring_events rows via
// PostgresStore and asserts the aggregated shape returned by
// GetCalibrationSummary. Gated on TEST_POSTGRES_DSN.
func TestPostgresCalibrationSummary(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set — skipping Postgres integration tests")
	}

	ctx := context.Background()
	st := openTestPostgres(t)
	defer st.Close()

	// Insert a spread of scoring events.
	type fix struct {
		listingID string
		verdict   string
		score     float64
		conf      float64
		comp      float64
	}
	fixtures := []fix{
		{"pg-cal-buy-1", "buy", 9.0, 0.85, 2.5},
		{"pg-cal-buy-2", "buy", 8.5, 0.80, 1.5},
		{"pg-cal-neg-1", "negotiate", 7.0, 0.65, 0.5},
		{"pg-cal-ask-1", "ask_seller", 5.5, 0.35, 0.0},
		{"pg-cal-skip-1", "skip", 2.0, 0.20, -2.5},
	}

	for _, f := range fixtures {
		ev := ScoringEvent{
			ListingID:     f.listingID,
			Marketplace:   "olxbg",
			Score:         f.score,
			Verdict:       f.verdict,
			Confidence:    f.conf,
			Contributions: map[string]float64{"comparables": f.comp},
			ScorerVersion: ScorerVersionV1,
		}
		if err := st.WriteScoringEvent(ctx, ev); err != nil {
			t.Fatalf("WriteScoringEvent(%s) error = %v", f.listingID, err)
		}
	}

	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window:      24 * time.Hour,
		Marketplace: "olxbg",
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}

	// Must have at least the 5 rows we just inserted (other tests may have
	// added rows in the same DB within the 1d window).
	if summary.TotalEvents < 5 {
		t.Errorf("TotalEvents = %d, want >= 5", summary.TotalEvents)
	}

	// Verdict map must be populated.
	if summary.VerdictCounts["buy"] < 2 {
		t.Errorf("VerdictCounts[buy] = %d, want >= 2", summary.VerdictCounts["buy"])
	}
	if summary.VerdictCounts["skip"] < 1 {
		t.Errorf("VerdictCounts[skip] = %d, want >= 1", summary.VerdictCounts["skip"])
	}

	// Confidence histogram must be populated for "buy" verdict.
	if summary.ConfidenceHistogram["buy"] == nil {
		t.Error("ConfidenceHistogram[buy] is nil")
	}

	// FairPriceDelta must be populated.
	if summary.FairPriceDelta["buy"] == nil {
		t.Error("FairPriceDelta[buy] is nil")
	}

	// Outcome: no outreach rows were inserted, so everything should be "unknown".
	if summary.OutcomeAttribution["unknown"] < 5 {
		t.Errorf("OutcomeAttribution[unknown] = %d, want >= 5", summary.OutcomeAttribution["unknown"])
	}

	// WindowDays should be 1.
	if summary.WindowDays != 1 {
		t.Errorf("WindowDays = %d, want 1", summary.WindowDays)
	}

	t.Logf("Postgres calibration summary: total=%d verdicts=%v", summary.TotalEvents, summary.VerdictCounts)
}

// TestPostgresCalibrationEventShape ensures WriteScoringEvent persists all
// fields and the summary returns the correct shape.
func TestPostgresCalibrationEventShape(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set — skipping Postgres integration tests")
	}

	ctx := context.Background()
	st := openTestPostgres(t)
	defer st.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	missionID := int64(999)
	ev := ScoringEvent{
		ListingID: "pg-shape-" + suffix,
		Marketplace: "olxbg",
		MissionID: &missionID,
		Score:         8.5,
		Verdict:       "buy",
		Confidence:    0.82,
		Contributions: map[string]float64{
			"comparables":        6.0,
			"confidence":         0.4,
			"negotiable":         1.0,
			"recency":            0.5,
			"condition":          0.5,
			"category_condition": 0.0,
		},
		ScorerVersion: ScorerVersionV1,
	}
	if err := st.WriteScoringEvent(ctx, ev); err != nil {
		t.Fatalf("WriteScoringEvent() error = %v", err)
	}

	summary, err := st.GetCalibrationSummary(ctx, CalibrationQuery{
		Window:      1 * time.Hour,
		Marketplace: "olxbg",
	})
	if err != nil {
		t.Fatalf("GetCalibrationSummary() error = %v", err)
	}

	// Must include at least the row we just inserted.
	if summary.TotalEvents < 1 {
		t.Errorf("TotalEvents = %d, want >= 1", summary.TotalEvents)
	}

	// The confidence 0.82 → bucket "0.8"
	if summary.ConfidenceHistogram["buy"]["0.8"] < 1 {
		t.Errorf("ConfidenceHistogram[buy][0.8] = %d, want >= 1", summary.ConfidenceHistogram["buy"]["0.8"])
	}

	// The comparables 6.0 → "very_underpriced"
	if summary.FairPriceDelta["buy"]["very_underpriced"] < 1 {
		t.Errorf("FairPriceDelta[buy][very_underpriced] = %d, want >= 1", summary.FairPriceDelta["buy"]["very_underpriced"])
	}
}
