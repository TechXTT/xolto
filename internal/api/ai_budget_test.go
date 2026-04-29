package api

// W19-23 Phase 1: tests for the AI-budget admin endpoints, the global-cap
// precedence over the local anon-analyze sub-cap, the assistant 503 path,
// and the VAL-1 calibration filter.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

// withGlobalTracker installs a fresh tracker for the test and restores the
// previous one on cleanup. Returns the tracker so the test can manipulate
// its state directly (e.g. seed an entry to fill the cap).
func withGlobalTracker(t *testing.T) *aibudget.Tracker {
	t.Helper()
	orig := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(orig) })
	tr := aibudget.New()
	aibudget.SetGlobal(tr)
	return tr
}

// fillBudget exhausts the tracker by spending exactly the cap so the
// next Allow returns false.
func fillBudget(t *testing.T, tr *aibudget.Tracker) {
	t.Helper()
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("seed Allow at exactly cap should succeed")
	}
}

// ---------------------------------------------------------------------------
// Owner-override endpoint
// ---------------------------------------------------------------------------

func TestAIBudgetOverrideOwnerOK(t *testing.T) {
	tr := withGlobalTracker(t)
	st, srv, operatorID, _ := newCalibrationTestServer(t)
	_ = st

	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	body := strings.NewReader(`{"new_cap_usd":5.0,"reason":"scaling test"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-budget/override", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", res.Code, res.Body.String())
	}
	if got := tr.CapUSD(); got != 5.0 {
		t.Fatalf("CapUSD after override = %v, want 5.0", got)
	}
}

func TestAIBudgetOverrideRejectsRegularUser(t *testing.T) {
	withGlobalTracker(t)
	_, srv, _, regularID := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, regularID, "user@example.com")
	body := strings.NewReader(`{"new_cap_usd":5.0,"reason":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-budget/override", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.Code)
	}
}

func TestAIBudgetOverrideRejectsZero(t *testing.T) {
	withGlobalTracker(t)
	_, srv, operatorID, _ := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	body := strings.NewReader(`{"new_cap_usd":0,"reason":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-budget/override", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for zero cap", res.Code)
	}
}

func TestAIBudgetOverrideRejectsAboveHardCeiling(t *testing.T) {
	withGlobalTracker(t)
	_, srv, operatorID, _ := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	body := strings.NewReader(`{"new_cap_usd":150,"reason":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-budget/override", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for over-ceiling cap", res.Code)
	}
}

func TestAIBudgetOverrideAuditLogged(t *testing.T) {
	withGlobalTracker(t)
	st, srv, operatorID, _ := newCalibrationTestServer(t)

	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	body := strings.NewReader(`{"new_cap_usd":7.0,"reason":"audit-log-test"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-budget/override", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("override failed: %s", res.Body.String())
	}

	// Read the audit log row directly.
	rows, err := st.ListRecentAIBudgetOverrides(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListRecentAIBudgetOverrides err: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least 1 audit row, got 0")
	}
	if rows[0].NewCapUSD != 7.0 {
		t.Fatalf("audit row cap = %v, want 7.0", rows[0].NewCapUSD)
	}
	if rows[0].Reason != "audit-log-test" {
		t.Fatalf("audit row reason = %q, want audit-log-test", rows[0].Reason)
	}
	if rows[0].SetByUserID != operatorID {
		t.Fatalf("audit row set_by = %q, want %q", rows[0].SetByUserID, operatorID)
	}
}

// ---------------------------------------------------------------------------
// Snapshot endpoint
// ---------------------------------------------------------------------------

func TestAIBudgetSnapshotShape(t *testing.T) {
	tr := withGlobalTracker(t)
	tr.Allow(context.Background(), "scorer", 0.05)

	_, srv, operatorID, _ := newCalibrationTestServer(t)
	tok := issueTokenForUser(t, operatorID, "operator@example.com")
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-budget/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() err: %v", err)
	}
	requiredKeys := []string{
		"rolling_24h_spend_usd", "cap_usd", "percentage",
		"oldest_entry_at", "warning_tiers_fired", "recent_overrides",
	}
	for _, k := range requiredKeys {
		if _, has := body[k]; !has {
			t.Errorf("snapshot missing key %q", k)
		}
	}
	if cap, ok := body["cap_usd"].(float64); !ok || cap != aibudget.DefaultCapUSD {
		t.Fatalf("cap_usd = %v, want %v", body["cap_usd"], aibudget.DefaultCapUSD)
	}
}

func TestAIBudgetSnapshotReflectsOverride(t *testing.T) {
	withGlobalTracker(t)
	_, srv, operatorID, _ := newCalibrationTestServer(t)
	tok := issueTokenForUser(t, operatorID, "operator@example.com")

	// Apply an override.
	overrideBody := strings.NewReader(`{"new_cap_usd":4.5,"reason":"snapshot-test"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/ai-budget/override", overrideBody)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("override failed: %s", res.Body.String())
	}

	// Snapshot should reflect the new cap.
	req = httptest.NewRequest(http.MethodGet, "/admin/ai-budget/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res = httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("snapshot failed: %s", res.Body.String())
	}
	var snapBody map[string]any
	_ = json.NewDecoder(res.Body).Decode(&snapBody)
	if cap, _ := snapBody["cap_usd"].(float64); cap != 4.5 {
		t.Fatalf("cap_usd after override = %v, want 4.5", cap)
	}
	if overrides, ok := snapBody["recent_overrides"].([]any); !ok || len(overrides) == 0 {
		t.Fatalf("recent_overrides should be non-empty after override")
	}
}

// ---------------------------------------------------------------------------
// Anonymous-analyze global-cap precedence
// ---------------------------------------------------------------------------

func TestAnonymousAnalyzeReturns503OnGlobalCapFire(t *testing.T) {
	tr := withGlobalTracker(t)
	fillBudget(t, tr)

	dbPath := filepath.Join(t.TempDir(), "anon-global-cap.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Wire a real scorer (so the nil-check passes) plus stub hooks so
	// the actual fetch + score paths never run.
	sc := scorer.New(st, stubScorerCfg{}, nil)
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, sc)
	srv.SetAnonymousAnalyzeHooks(
		func(ctx context.Context, raw string) (models.Listing, error) {
			return models.Listing{ItemID: "olxbg-1", Title: "Test", Price: 50000, MarketplaceID: "olxbg"}, nil
		},
		func(ctx context.Context, l models.Listing, s models.SearchSpec) models.ScoredListing {
			return models.ScoredListing{Listing: l, Score: 7.0, RecommendedAction: "ask_seller"}
		},
	)

	reqBody := strings.NewReader(`{"url":"https://www.olx.bg/d/listing/test"}`)
	req := httptest.NewRequest(http.MethodPost, "/public/matches/analyze", reqBody)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", res.Code, res.Body.String())
	}
	// Retry-After should be set (>= 1).
	if ra := res.Header().Get("Retry-After"); ra == "" {
		t.Fatalf("expected Retry-After header on 503 response")
	}
	// The error body should mention "global" so ops can tell which cap fired.
	if !strings.Contains(res.Body.String(), "global") {
		t.Errorf("503 body should mention global cap, got: %s", res.Body.String())
	}
}

// (helper removed — we use scorer.New with stub hooks instead).

// ---------------------------------------------------------------------------
// W19-28: paginated GET /admin/ai-budget/overrides
// ---------------------------------------------------------------------------

func TestAIBudgetOverridesListReturnsEmptyWhenNoRows(t *testing.T) {
	withGlobalTracker(t)
	_, srv, operatorID, _ := newCalibrationTestServer(t)
	tok := issueTokenForUser(t, operatorID, "operator@example.com")

	req := httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() err: %v", err)
	}
	overrides, ok := body["overrides"].([]any)
	if !ok {
		t.Fatalf("overrides field missing or wrong type: %v", body)
	}
	if len(overrides) != 0 {
		t.Fatalf("expected 0 overrides, got %d", len(overrides))
	}
	if nc, _ := body["next_cursor"].(float64); nc != 0 {
		t.Fatalf("next_cursor = %v, want 0", body["next_cursor"])
	}
}

func TestAIBudgetOverridesListPaginatesViaCursor(t *testing.T) {
	withGlobalTracker(t)
	st, srv, operatorID, _ := newCalibrationTestServer(t)
	ctx := context.Background()
	tok := issueTokenForUser(t, operatorID, "operator@example.com")

	// Insert 7 overrides.
	for i := 1; i <= 7; i++ {
		_, err := st.RecordAIBudgetOverride(ctx, store.AIBudgetOverride{
			NewCapUSD:   float64(i),
			Reason:      "page-test",
			SetByUserID: operatorID,
		})
		if err != nil {
			t.Fatalf("RecordAIBudgetOverride iter %d: %v", i, err)
		}
	}

	// Page 1: limit=3, cursor=0.
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides?limit=3", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("page1 status = %d; body = %s", res.Code, res.Body.String())
	}
	var p1 map[string]any
	_ = json.NewDecoder(res.Body).Decode(&p1)
	p1Rows := p1["overrides"].([]any)
	if len(p1Rows) != 3 {
		t.Fatalf("page1: expected 3 rows, got %d", len(p1Rows))
	}
	nc1 := int64(p1["next_cursor"].(float64))
	if nc1 == 0 {
		t.Fatalf("page1: next_cursor should be non-zero")
	}
	// Rows must be id DESC (first row has highest id).
	firstID := int64(p1Rows[0].(map[string]any)["id"].(float64))
	lastID := int64(p1Rows[2].(map[string]any)["id"].(float64))
	if firstID <= lastID {
		t.Fatalf("page1: rows not in id DESC order: firstID=%d lastID=%d", firstID, lastID)
	}
	if nc1 != lastID {
		t.Fatalf("page1: next_cursor=%d want last row id=%d", nc1, lastID)
	}

	// Page 2: limit=3, cursor=nc1.
	req = httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides?limit=3&cursor="+strconv.FormatInt(nc1, 10), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res = httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("page2 status = %d; body = %s", res.Code, res.Body.String())
	}
	var p2 map[string]any
	_ = json.NewDecoder(res.Body).Decode(&p2)
	p2Rows := p2["overrides"].([]any)
	if len(p2Rows) != 3 {
		t.Fatalf("page2: expected 3 rows, got %d", len(p2Rows))
	}
	nc2 := int64(p2["next_cursor"].(float64))
	if nc2 == 0 {
		t.Fatalf("page2: next_cursor should be non-zero")
	}

	// Page 3: limit=3, cursor=nc2 — should have 1 row and next_cursor=0.
	req = httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides?limit=3&cursor="+strconv.FormatInt(nc2, 10), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res = httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("page3 status = %d; body = %s", res.Code, res.Body.String())
	}
	var p3 map[string]any
	_ = json.NewDecoder(res.Body).Decode(&p3)
	p3Rows := p3["overrides"].([]any)
	if len(p3Rows) != 1 {
		t.Fatalf("page3: expected 1 row, got %d", len(p3Rows))
	}
	if nc3 := int64(p3["next_cursor"].(float64)); nc3 != 0 {
		t.Fatalf("page3: next_cursor = %d, want 0 (end of history)", nc3)
	}
}

func TestAIBudgetOverridesListEnforcesLimitCap(t *testing.T) {
	withGlobalTracker(t)
	_, srv, operatorID, _ := newCalibrationTestServer(t)
	tok := issueTokenForUser(t, operatorID, "operator@example.com")

	// limit=999 should be silently clamped to 100; no error.
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides?limit=999", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() err: %v", err)
	}
	if _, ok := body["overrides"]; !ok {
		t.Fatalf("overrides key missing in response")
	}
}

func TestAIBudgetOverridesListRequiresAuth(t *testing.T) {
	withGlobalTracker(t)
	_, srv, _, regularID := newCalibrationTestServer(t)

	// Unauthenticated request should be rejected.
	req := httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", res.Code)
	}

	// Regular user (non-operator) should be forbidden.
	tok := issueTokenForUser(t, regularID, "user@example.com")
	req = httptest.NewRequest(http.MethodGet, "/admin/ai-budget/overrides", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res = httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("regular user: status = %d, want 403", res.Code)
	}
}

// ---------------------------------------------------------------------------
// Global cap precedence over local sub-cap when global is more restrictive
// ---------------------------------------------------------------------------

// TestGlobalCapFiresBeforeLocalAnonCap confirms that even though the
// local $5/day anon cap has plenty of room, the global $3/24h fires
// first and the user sees a 503 with the global outcome tag.
func TestGlobalCapFiresBeforeLocalAnonCap(t *testing.T) {
	tr := withGlobalTracker(t)
	// Spend exactly the global cap → next Allow refused but local anon
	// cap (which has its own $5/day breaker, completely empty) would
	// allow if checked in isolation.
	fillBudget(t, tr)

	dbPath := filepath.Join(t.TempDir(), "anon-precedence.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sc := scorer.New(st, stubScorerCfg{}, nil)
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, sc)
	srv.SetAnonymousAnalyzeHooks(
		func(ctx context.Context, raw string) (models.Listing, error) {
			return models.Listing{ItemID: "olxbg-2", Title: "Test", Price: 50000, MarketplaceID: "olxbg"}, nil
		},
		func(ctx context.Context, l models.Listing, s models.SearchSpec) models.ScoredListing {
			return models.ScoredListing{Listing: l, Score: 7.0, RecommendedAction: "ask_seller"}
		},
	)

	reqBody := strings.NewReader(`{"url":"https://www.olx.bg/d/listing/precedence"}`)
	req := httptest.NewRequest(http.MethodPost, "/public/matches/analyze", reqBody)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.Code)
	}
	// The response body should reference the global cap, NOT the local
	// anon-analyze cap, because global fired first.
	if !strings.Contains(res.Body.String(), "global") {
		t.Errorf("expected global cap message; got %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), "anonymous-analyze") {
		t.Errorf("expected NOT to reference anonymous-analyze cap (which fired second), got %s", res.Body.String())
	}
}

// ---------------------------------------------------------------------------
// VAL-1 calibration filter: ai_path = heuristic_fallback excluded by default
// ---------------------------------------------------------------------------

func TestCalibrationSummaryExcludesHeuristicFallbackByDefault(t *testing.T) {
	st, srv, operatorID, _ := newCalibrationTestServer(t)
	ctx := context.Background()

	// Two scoring events, same window:
	//   - One with ai_path = "ai" (real LLM call).
	//   - One with ai_path = "heuristic_fallback" (cap-fire degradation).
	// Default summary should count only the "ai" row.
	if err := st.WriteScoringEvent(ctx, store.ScoringEvent{
		ListingID: "ai-row-1", Marketplace: "olxbg", Score: 7.0, Verdict: "buy",
		Confidence: 0.8, Contributions: map[string]float64{"comparables": 5}, ScorerVersion: store.ScorerVersionV1,
		AIPath: "ai",
	}); err != nil {
		t.Fatalf("WriteScoringEvent(ai) err: %v", err)
	}
	if err := st.WriteScoringEvent(ctx, store.ScoringEvent{
		ListingID: "fallback-row-1", Marketplace: "olxbg", Score: 4.7, Verdict: "ask_seller",
		Confidence: 0.35, Contributions: map[string]float64{"comparables": 5}, ScorerVersion: store.ScorerVersionV1,
		AIPath: "heuristic_fallback",
	}); err != nil {
		t.Fatalf("WriteScoringEvent(fallback) err: %v", err)
	}

	tok := issueTokenForUser(t, operatorID, "operator@example.com")

	// Default: heuristic_fallback excluded → 1 event.
	req := httptest.NewRequest(http.MethodGet, "/internal/calibration/summary?window=1d&marketplace=olxbg", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("default summary failed: %s", res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	summary := body["summary"].(map[string]any)
	if total := summary["total_events"].(float64); total != 1 {
		t.Errorf("default total_events = %v, want 1 (heuristic_fallback excluded)", total)
	}

	// include_heuristic_fallback=true → both rows.
	req = httptest.NewRequest(http.MethodGet, "/internal/calibration/summary?window=1d&marketplace=olxbg&include_heuristic_fallback=true", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res = httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("include summary failed: %s", res.Body.String())
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	summary = body["summary"].(map[string]any)
	if total := summary["total_events"].(float64); total != 2 {
		t.Errorf("include_heuristic_fallback=true total_events = %v, want 2", total)
	}
}
