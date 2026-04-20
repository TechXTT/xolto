package store

// Postgres integration test for per-model comparables pool (XOL-105).
//
// These tests execute real SQL against a live Postgres instance and are gated
// on TEST_POSTGRES_DSN. They skip silently when the variable is unset.
//
// To run:
//
//	TEST_POSTGRES_DSN="postgres://xolto:xoltotest@localhost:54320/xolto_test?sslmode=disable" \
//	  go test ./internal/store/ -run TestModelKeyMarketAverage -v -timeout 60s

import (
	"testing"
)

// TestModelKeyMarketAverage verifies the full model_key pool logic:
//  1. Rows inserted with model_key="sony:a6000" are returned when querying by
//     that model_key, even when the query string differs.
//  2. Rows with a different model_key (sony:a7r) do NOT contaminate the
//     sony:a6000 pool.
//  3. An empty model_key falls back to the raw query pool.
//  4. A brand-only key ("sony:") with fewer than minSamples rows falls back to
//     the query pool.
func TestModelKeyMarketAverage(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	const mpID = "olxbg"
	const minSamples = 6

	// Step 1: insert 6 price_history rows for sony:a6000 at various prices.
	// Average of these 6 = (20000+21000+22000+23000+24000+25000)/6 = 22500.
	a6000Prices := []int{20000, 21000, 22000, 23000, 24000, 25000}
	for _, p := range a6000Prices {
		if err := st.RecordPrice("sony a6000", "sony:a6000", 0, mpID, p); err != nil {
			t.Fatalf("RecordPrice(sony:a6000) error = %v", err)
		}
	}

	// Step 2: call GetMarketAverage with query "a6000 sony camera" but
	// model_key="sony:a6000" — should return the correct average via model_key path.
	avg, ok, err := st.GetMarketAverage("a6000 sony camera", "sony:a6000", 0, mpID, minSamples)
	if err != nil {
		t.Fatalf("GetMarketAverage(sony:a6000) error = %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for sony:a6000 pool with 6 samples, got false")
	}
	if avg != 22500 {
		t.Errorf("expected avg=22500 for sony:a6000 pool, got %d", avg)
	}

	// Step 3: insert 3 price_history rows for sony:a7r at much higher prices.
	// These must NOT contaminate the sony:a6000 pool.
	a7rPrices := []int{150000, 160000, 170000}
	for _, p := range a7rPrices {
		if err := st.RecordPrice("sony a7r", "sony:a7r", 0, mpID, p); err != nil {
			t.Fatalf("RecordPrice(sony:a7r) error = %v", err)
		}
	}

	// Step 4: re-query sony:a6000 — a7r prices must not appear.
	avg2, ok2, err2 := st.GetMarketAverage("sony a6000", "sony:a6000", 0, mpID, minSamples)
	if err2 != nil {
		t.Fatalf("GetMarketAverage(sony:a6000, after a7r insert) error = %v", err2)
	}
	if !ok2 {
		t.Fatal("expected ok=true for sony:a6000 pool after a7r insert, got false")
	}
	if avg2 != 22500 {
		t.Errorf("a7r prices contaminated sony:a6000 pool: expected avg=22500, got %d", avg2)
	}

	// Step 5: call GetMarketAverage with empty model_key — should fall back to
	// query pool. The query "sony a6000" was used for 6 rows above, so the
	// query pool should return that average (22500).
	avg3, ok3, err3 := st.GetMarketAverage("sony a6000", "", 0, mpID, minSamples)
	if err3 != nil {
		t.Fatalf("GetMarketAverage(empty model_key) error = %v", err3)
	}
	if !ok3 {
		t.Fatal("expected ok=true for query fallback with 6 rows, got false")
	}
	if avg3 != 22500 {
		t.Errorf("query fallback: expected avg=22500, got %d", avg3)
	}

	// Step 6: call GetMarketAverage with brand-only key "sony:" — only 0 rows
	// match that exact key, so the model_key pool is insufficient (<minSamples).
	// Should fall back to the query pool for "sony camera" which also has 0 rows,
	// so ok=false overall.
	avg4, ok4, err4 := st.GetMarketAverage("sony camera", "sony:", 0, mpID, minSamples)
	if err4 != nil {
		t.Fatalf("GetMarketAverage(sony:, fallback) error = %v", err4)
	}
	if ok4 {
		t.Errorf("expected ok=false for brand-only key with insufficient samples, got ok=true (avg=%d)", avg4)
	}
}
