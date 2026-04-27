package api

// W18-2 tests for POST /public/matches/analyze.
//
// Coverage map:
//   - anonymous success path                          → TestAnonymousAnalyzeSuccess
//   - per-IP rate limit (5 ok, 6th 429)               → TestAnonymousAnalyzeRateLimit
//   - URL cache short-circuit (no AI on second call)  → TestAnonymousAnalyzeCacheShortCircuit
//   - daily cost circuit-breaker → 503 + Retry-After  → TestAnonymousAnalyzeCircuitBreaker
//   - authenticated path remains auth-required        → TestAuthenticatedAnalyzeStillRequiresAuth
//
// Tests use SetAnonymousAnalyzeHooks to swap the network fetcher and the
// AI-backed scorer with deterministic stubs. The stubs count invocations so
// the cache-hit test can assert that the AI was not invoked on a replay.
//
// nowAnonymousAnalyze is overridden per-test to drive the rate-limit window
// and UTC-day rollover deterministically. Always restored via t.Cleanup.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

// newAnonymousAnalyzeServer constructs a server wired with a non-nil scorer
// (the anonymous handler 503s if scorer is nil) and stubbed fetch + score
// hooks. Returns the server, the fetch counter, and the score counter so
// individual tests can assert which path was exercised.
//
// The default stub scorer returns CostUSD == 0 (heuristic-style path) so
// existing breaker / rate-limit / cache tests are unchanged in behaviour
// after the W19-3 reconcile plumb. Tests that need a non-zero per-call cost
// install their own hook via SetAnonymousAnalyzeHooks after construction.
func newAnonymousAnalyzeServer(t *testing.T) (*Server, *int32, *int32) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "anon-analyze.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// scorer.New requires a config interface and a (possibly nil) reasoner.
	// We pass nil reasoner — the real Score function is never reached because
	// anonymousScoreOverride short-circuits it. We just need a non-nil
	// *scorer.Scorer so handleAnonymousAnalyze does not 503 at the
	// "scorer not configured" guard.
	sc := scorer.New(st, stubScorerCfg{}, nil)

	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, sc)

	var fetchCalls int32
	var scoreCalls int32

	srv.SetAnonymousAnalyzeHooks(
		func(_ context.Context, rawURL string) (models.Listing, error) {
			atomic.AddInt32(&fetchCalls, 1)
			return models.Listing{
				ItemID:        "olxbg_test_" + extractTestID(rawURL),
				CanonicalID:   "olxbg:test_" + extractTestID(rawURL),
				MarketplaceID: "olxbg",
				Title:         "Test Listing",
				Description:   "Stubbed listing for anonymous-analyze tests.",
				Price:         5000,
				PriceType:     "fixed",
				URL:           rawURL,
			}, nil
		},
		func(_ context.Context, listing models.Listing, _ models.SearchSpec) models.ScoredListing {
			atomic.AddInt32(&scoreCalls, 1)
			return models.ScoredListing{
				Listing:           listing,
				Score:             7.5,
				FairPrice:         6000,
				OfferPrice:        4500,
				Confidence:        0.6,
				Reason:            "stub reason",
				ReasoningSource:   "stub",
				RecommendedAction: "buy",
			}
		},
	)
	return srv, &fetchCalls, &scoreCalls
}

// setStubScorerCost rebinds the score hook to a deterministic stub that
// returns ScoredListing.CostUSD == cost on every call. Used by W19-3
// reconcile-semantics tests so the test can assert post-reconcile budget
// state. The fetch hook is left untouched.
func setStubScorerCost(t *testing.T, srv *Server, cost float64, scoreCalls *int32) {
	t.Helper()
	srv.SetAnonymousAnalyzeHooks(
		func(_ context.Context, rawURL string) (models.Listing, error) {
			return models.Listing{
				ItemID:        "olxbg_test_" + extractTestID(rawURL),
				CanonicalID:   "olxbg:test_" + extractTestID(rawURL),
				MarketplaceID: "olxbg",
				Title:         "Test Listing",
				Description:   "Stubbed listing for anonymous-analyze tests.",
				Price:         5000,
				PriceType:     "fixed",
				URL:           rawURL,
			}, nil
		},
		func(_ context.Context, listing models.Listing, _ models.SearchSpec) models.ScoredListing {
			if scoreCalls != nil {
				atomic.AddInt32(scoreCalls, 1)
			}
			return models.ScoredListing{
				Listing:           listing,
				Score:             7.5,
				FairPrice:         6000,
				OfferPrice:        4500,
				Confidence:        0.6,
				Reason:            "stub reason",
				ReasoningSource:   "ai",
				RecommendedAction: "buy",
				CostUSD:           cost,
			}
		},
	)
}

// extractTestID returns the URL's last path segment so each call's listing
// has a different ItemID. Keeps test fixtures cheap.
func extractTestID(rawURL string) string {
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 && idx < len(rawURL)-1 {
		return rawURL[idx+1:]
	}
	return rawURL
}

// stubScorerCfg satisfies the config interface scorer.New requires.
type stubScorerCfg struct{}

func (stubScorerCfg) GetMinScore() float64      { return 0 }
func (stubScorerCfg) GetMarketSampleSize() int  { return 10 }

// freezeNow pins nowAnonymousAnalyze to a fixed instant for the duration of
// the test. The cleanup restores the real clock so subsequent tests are
// unaffected.
func freezeNow(t *testing.T, at time.Time) func(time.Time) {
	t.Helper()
	current := at
	prev := nowAnonymousAnalyze
	nowAnonymousAnalyze = func() time.Time { return current }
	t.Cleanup(func() { nowAnonymousAnalyze = prev })
	return func(next time.Time) { current = next }
}

func anonymousAnalyzeRequest(t *testing.T, srv *Server, urlStr, ip string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"url":"` + urlStr + `"}`
	req := httptest.NewRequest(http.MethodPost, "/public/matches/analyze", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ip != "" {
		req.RemoteAddr = ip + ":54321"
	}
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

func TestAnonymousAnalyzeSuccess(t *testing.T) {
	srv, fetchCalls, scoreCalls := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	res := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/test-listing-1", "203.0.113.10")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if _, ok := body["listing"]; !ok {
		t.Fatalf("response missing 'listing' key: %#v", body)
	}
	if got := body["reasoning_source"]; got != "stub" {
		t.Fatalf("expected reasoning_source=stub, got %v", got)
	}
	if atomic.LoadInt32(fetchCalls) != 1 {
		t.Fatalf("expected 1 fetch call, got %d", atomic.LoadInt32(fetchCalls))
	}
	if atomic.LoadInt32(scoreCalls) != 1 {
		t.Fatalf("expected 1 score call, got %d", atomic.LoadInt32(scoreCalls))
	}
	if got := res.Header().Get("X-Anonymous-Analyze-Cache"); got != "MISS" {
		t.Fatalf("expected X-Anonymous-Analyze-Cache=MISS on first call, got %q", got)
	}
}

func TestAnonymousAnalyzeRateLimit(t *testing.T) {
	srv, _, _ := newAnonymousAnalyzeServer(t)
	advance := freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	// Five distinct URLs from the same IP — all should succeed, exhausting
	// the budget. Use distinct URLs so the cache short-circuit doesn't
	// confound the rate-limit signal: cache hits still consume budget but
	// we want the test to also verify each call reached the AI.
	for i := 0; i < anonymousAnalyzeRateLimit; i++ {
		res := anonymousAnalyzeRequest(t, srv,
			"https://www.olx.bg/ad/listing-"+strconv.Itoa(i),
			"198.51.100.7")
		if res.Code != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %d body=%s", i+1, res.Code, res.Body.String())
		}
	}

	// 6th call within the same window must 429.
	res := anonymousAnalyzeRequest(t, srv,
		"https://www.olx.bg/ad/listing-overflow",
		"198.51.100.7")
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 6th call, got %d body=%s", res.Code, res.Body.String())
	}
	retryAfter := res.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header on 429")
	}
	parsed, err := strconv.Atoi(retryAfter)
	if err != nil || parsed < 1 {
		t.Fatalf("expected positive integer Retry-After, got %q", retryAfter)
	}

	// A different IP must NOT be rate-limited even though the first IP is.
	otherIP := anonymousAnalyzeRequest(t, srv,
		"https://www.olx.bg/ad/listing-different-ip",
		"198.51.100.99")
	if otherIP.Code != http.StatusOK {
		t.Fatalf("different IP should be allowed, got %d", otherIP.Code)
	}

	// Advance past the window — the original IP should be allowed again.
	advance(time.Date(2026, 4, 25, 13, 30, 0, 0, time.UTC))
	res2 := anonymousAnalyzeRequest(t, srv,
		"https://www.olx.bg/ad/listing-after-window",
		"198.51.100.7")
	if res2.Code != http.StatusOK {
		t.Fatalf("after window expiry expected 200, got %d", res2.Code)
	}
}

func TestAnonymousAnalyzeCacheShortCircuit(t *testing.T) {
	srv, fetchCalls, scoreCalls := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	// First call → MISS, populates cache.
	url1 := "https://www.olx.bg/ad/cache-test"
	res1 := anonymousAnalyzeRequest(t, srv, url1, "192.0.2.50")
	if res1.Code != http.StatusOK {
		t.Fatalf("first call: expected 200, got %d", res1.Code)
	}
	if atomic.LoadInt32(fetchCalls) != 1 || atomic.LoadInt32(scoreCalls) != 1 {
		t.Fatalf("first call: expected fetch=1 score=1, got fetch=%d score=%d",
			atomic.LoadInt32(fetchCalls), atomic.LoadInt32(scoreCalls))
	}
	body1 := res1.Body.Bytes()

	// Second call (different IP, so rate-limit isn't the gate) → cache HIT.
	res2 := anonymousAnalyzeRequest(t, srv, url1, "192.0.2.51")
	if res2.Code != http.StatusOK {
		t.Fatalf("cached call: expected 200, got %d", res2.Code)
	}
	if got := res2.Header().Get("X-Anonymous-Analyze-Cache"); got != "HIT" {
		t.Fatalf("expected X-Anonymous-Analyze-Cache=HIT on replay, got %q", got)
	}
	// The fetch + score counters MUST NOT have advanced. This is the AC:
	// "second call within 6h returns cached payload, no AI invocation".
	if atomic.LoadInt32(fetchCalls) != 1 {
		t.Fatalf("cached call: expected fetch still=1, got %d", atomic.LoadInt32(fetchCalls))
	}
	if atomic.LoadInt32(scoreCalls) != 1 {
		t.Fatalf("cached call: expected score still=1 (AI not invoked), got %d", atomic.LoadInt32(scoreCalls))
	}
	// Body parity — replay returns the exact same payload.
	if string(body1) != res2.Body.String() {
		t.Fatalf("cached body differs from original.\nfirst:  %s\nsecond: %s", body1, res2.Body.String())
	}

	// Cache hits DO consume rate-limit budget. Same IP can use the cache
	// only as many times as their per-IP budget allows. Verify that by
	// hammering the same URL from one IP until 429.
	srv2, _, _ := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))
	for i := 0; i < anonymousAnalyzeRateLimit; i++ {
		r := anonymousAnalyzeRequest(t, srv2, "https://www.olx.bg/ad/replay", "192.0.2.99")
		if r.Code != http.StatusOK {
			t.Fatalf("budget pre-fill call %d: expected 200, got %d", i+1, r.Code)
		}
	}
	// Same URL from same IP — budget exhausted by the previous five hits
	// (some of which were cache hits), so we expect 429 not 200.
	r := anonymousAnalyzeRequest(t, srv2, "https://www.olx.bg/ad/replay", "192.0.2.99")
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("expected cache replays to consume rate-limit budget, got %d", r.Code)
	}
}

func TestAnonymousAnalyzeCircuitBreaker(t *testing.T) {
	srv, _, _ := newAnonymousAnalyzeServer(t)
	advance := freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	// Pre-fill the day's projected spend just under the ceiling. Pushing
	// the projection over $5 directly via the budget primitive avoids
	// having to make 500 real calls (and hit the rate limiter) to trip the
	// breaker. This exercises the breaker wiring in handleAnonymousAnalyze
	// rather than the budget arithmetic — that's covered separately by
	// TestAnonymousAnalyzeBudgetMath.
	srv.initAnonymousAnalyze()
	srv.anonymousAnalyzeBudget.addAndProject(anonymousAnalyzeCostCeilingUSD, nowAnonymousAnalyze())

	// Next call must trip the breaker (estimated cost pushes it over).
	res := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/over-budget", "203.0.113.77")
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when budget exhausted, got %d body=%s", res.Code, res.Body.String())
	}
	retry := res.Header().Get("Retry-After")
	if retry == "" {
		t.Fatal("expected Retry-After header on 503 breaker")
	}
	retrySec, err := strconv.Atoi(retry)
	if err != nil || retrySec < 1 {
		t.Fatalf("expected positive integer Retry-After, got %q", retry)
	}
	// Retry-After must reach until next UTC midnight (noon → 12h ≈ 43200s).
	if retrySec < 60 {
		t.Fatalf("expected Retry-After to be ~hours, got %ds", retrySec)
	}

	// After UTC midnight rollover the breaker resets.
	advance(time.Date(2026, 4, 26, 0, 5, 0, 0, time.UTC))
	res2 := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/new-day", "203.0.113.77")
	if res2.Code != http.StatusOK {
		t.Fatalf("expected 200 on new UTC day, got %d body=%s", res2.Code, res2.Body.String())
	}
}

// TestAnonymousAnalyzeBudgetMath exercises the budget primitive directly so
// regressions in day rollover or the reset-on-rollover path don't slip past
// the breaker integration test.
func TestAnonymousAnalyzeBudgetMath(t *testing.T) {
	b := &anonymousAnalyzeBudget{}
	t1 := time.Date(2026, 4, 25, 8, 0, 0, 0, time.UTC)
	if got := b.addAndProject(2.0, t1); got != 2.0 {
		t.Fatalf("expected projection=2.0 after first add, got %v", got)
	}
	if got := b.addAndProject(1.5, t1); got != 3.5 {
		t.Fatalf("expected projection=3.5 after second add, got %v", got)
	}
	// Same UTC day, later in the day → still accumulating.
	t2 := time.Date(2026, 4, 25, 23, 59, 0, 0, time.UTC)
	if got := b.addAndProject(0.5, t2); got != 4.0 {
		t.Fatalf("expected projection=4.0 within same UTC day, got %v", got)
	}
	// New UTC day → reset to 0 then add.
	t3 := time.Date(2026, 4, 26, 0, 0, 1, 0, time.UTC)
	if got := b.addAndProject(0.25, t3); got != 0.25 {
		t.Fatalf("expected projection=0.25 on new UTC day, got %v", got)
	}
	// Notification dedup: first call true, subsequent same-day calls false.
	if !b.markBreakerNotified(t3) {
		t.Fatal("first markBreakerNotified should return true")
	}
	if b.markBreakerNotified(t3) {
		t.Fatal("second markBreakerNotified on same day must return false")
	}
	// Day roll → notification flag resets.
	t4 := time.Date(2026, 4, 27, 0, 0, 1, 0, time.UTC)
	if !b.markBreakerNotified(t4) {
		t.Fatal("markBreakerNotified must reset on UTC-day rollover")
	}
	// Rollback never goes negative.
	b.rollback(100.0)
	if got := b.projectedSpend(t4); got != 0 {
		t.Fatalf("rollback should clamp spend at 0, got %v", got)
	}
}

func TestAnonymousAnalyzeCanonicalizesURL(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://www.olx.bg/ad/foo-IDabc.html", "www.olx.bg/ad/foo-idabc.html"},
		{"https://www.olx.bg/ad/foo-IDabc.html?utm_source=fb", "www.olx.bg/ad/foo-idabc.html"},
		{"https://www.olx.bg/ad/foo-IDabc.html/", "www.olx.bg/ad/foo-idabc.html"},
		{"HTTPS://WWW.OLX.BG:443/ad/foo", "www.olx.bg/ad/foo"},
	}
	for _, c := range cases {
		got := canonicalizeAnonymousAnalyzeURL(c.raw)
		if got != c.want {
			t.Errorf("canonicalize(%q): want %q, got %q", c.raw, c.want, got)
		}
	}
}

// TestAuthenticatedAnalyzeStillRequiresAuth is the byte-identical-behaviour
// guard for /matches/analyze. W18-2 broadens the ANONYMOUS surface; the
// authenticated path's auth requirement must remain in place.
func TestAuthenticatedAnalyzeStillRequiresAuth(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "auth-analyze-still-private.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	sc := scorer.New(st, stubScorerCfg{}, nil)
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, sc)

	// Unauth: must be 401.
	body := `{"url":"https://www.olx.bg/ad/whatever"}`
	req := httptest.NewRequest(http.MethodPost, "/matches/analyze", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 on unauthenticated /matches/analyze, got %d", res.Code)
	}

	// With a valid token: handler proceeds (and likely fails on real fetch
	// of the test URL, but it must NOT 401). We stop short of asserting the
	// downstream behaviour because that is unchanged from before W18-2 and
	// covered by the existing scorer tests; the only new contract is that
	// auth is still required, which the 401 above proves.
	userID, err := st.CreateUser("auth-analyze@example.com", "hash", "Auth Analyze")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "auth-analyze@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/matches/analyze", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+token)
	res2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res2, req2)
	if res2.Code == http.StatusUnauthorized {
		t.Fatalf("authenticated /matches/analyze must not 401, got %d body=%s", res2.Code, res2.Body.String())
	}
}

// approxEqual returns true when |a - b| < 1e-9. Float-comparison helper used
// by the W19-3 reconcile-semantics tests where the budget arithmetic adds
// and subtracts small dollar amounts and exact equality is too brittle.
func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// TestAnonymousAnalyzeReconcileEstimateTooHigh covers W19-3 reconcile
// semantics when the actual AI cost is *below* the conservative pre-spend
// estimate. The post-call budget should reflect only the actual cost — the
// difference (estimate - actual) must be rebated.
func TestAnonymousAnalyzeReconcileEstimateTooHigh(t *testing.T) {
	srv, _, scoreCalls := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	// Real cost = $0.002 (well below the $0.01 pre-spend estimate). After
	// reconcile, daily spend should equal $0.002 exactly.
	const actualCost = 0.002
	setStubScorerCost(t, srv, actualCost, scoreCalls)

	res := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/reconcile-low", "203.0.113.20")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	got := srv.anonymousAnalyzeBudget.projectedSpend(nowAnonymousAnalyze())
	if !approxEqual(got, actualCost) {
		t.Fatalf("expected post-reconcile spend = %.6f (actual cost), got %.6f",
			actualCost, got)
	}
}

// TestAnonymousAnalyzeReconcileEstimateTooLow covers W19-3 reconcile
// semantics when the actual AI cost *exceeds* the conservative pre-spend
// estimate (e.g. an unusually long output, a retry, or a future model that
// is more expensive per call). The post-call budget should reflect the
// actual cost; the delta (actual - estimate) must be charged on top.
func TestAnonymousAnalyzeReconcileEstimateTooLow(t *testing.T) {
	srv, _, scoreCalls := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	// Real cost = $0.05 (5x the $0.01 pre-spend estimate). After reconcile,
	// daily spend should equal $0.05 exactly.
	const actualCost = 0.05
	setStubScorerCost(t, srv, actualCost, scoreCalls)

	res := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/reconcile-high", "203.0.113.21")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	got := srv.anonymousAnalyzeBudget.projectedSpend(nowAnonymousAnalyze())
	if !approxEqual(got, actualCost) {
		t.Fatalf("expected post-reconcile spend = %.6f (actual cost), got %.6f",
			actualCost, got)
	}
}

// TestAnonymousAnalyzeBudgetUsesActualCost asserts the W19-3 AC: on a
// successful call the running daily budget reflects the *real* per-call
// cost from scored.CostUSD, not the static $0.01 heuristic. Stub scorer
// returns CostUSD=$0.003 (close to the gpt-5-mini list-price expectation
// for a 3000-token-in / 500-token-out call) — after one call, the running
// total should be $0.003, not $0.01.
func TestAnonymousAnalyzeBudgetUsesActualCost(t *testing.T) {
	srv, _, scoreCalls := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	const actualCost = 0.003
	setStubScorerCost(t, srv, actualCost, scoreCalls)

	res := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/real-cost", "203.0.113.22")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}

	got := srv.anonymousAnalyzeBudget.projectedSpend(nowAnonymousAnalyze())
	if !approxEqual(got, actualCost) {
		t.Fatalf("post-call spend should equal actual cost %.6f (not the $0.01 heuristic), got %.6f",
			actualCost, got)
	}
	if approxEqual(got, estimatedCostPerAnonymousCallUSD) {
		t.Fatalf("post-call spend equals the static heuristic $%.4f — reconcile is broken",
			estimatedCostPerAnonymousCallUSD)
	}
}

// TestAnonymousAnalyzeFetchFailureRollback asserts that when the fetch
// step fails (before the AI is invoked), the pre-spend projection is
// rolled back so the daily ledger is not over-charged. This is part of
// the W18-2 set of guards that W19-3 must continue to honour.
func TestAnonymousAnalyzeFetchFailureRollback(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "anon-fetch-fail.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sc := scorer.New(st, stubScorerCfg{}, nil)
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, sc)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	srv.SetAnonymousAnalyzeHooks(
		func(_ context.Context, _ string) (models.Listing, error) {
			return models.Listing{}, errFetchStub
		},
		func(_ context.Context, listing models.Listing, _ models.SearchSpec) models.ScoredListing {
			t.Fatal("scorer must not be invoked when fetch fails")
			return models.ScoredListing{}
		},
	)

	res := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/fetch-fail", "203.0.113.30")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on fetch failure, got %d body=%s", res.Code, res.Body.String())
	}

	srv.initAnonymousAnalyze()
	got := srv.anonymousAnalyzeBudget.projectedSpend(nowAnonymousAnalyze())
	if got != 0 {
		t.Fatalf("expected post-fetch-failure spend = 0 (full rollback), got %.6f", got)
	}
}

// errFetchStub is a sentinel returned by the fetch-failure rollback test.
// We declare it at package level so the closure does not capture a fresh
// allocation each call.
var errFetchStub = stubFetchError("simulated upstream fetch failure")

type stubFetchError string

func (e stubFetchError) Error() string { return string(e) }

// TestAnonymousAnalyzeBreakerCeilingMath asserts the W18-2 ceiling math
// continues to trip at exactly the same projection threshold after the
// W19-3 reconcile plumb. Pre-fill the budget to ($5 - $0.01), which is the
// largest spent that still admits a single $0.01 pre-spend projection
// without tripping. The first call's pre-spend projection lands at exactly
// $5.00 (allowed); the actual cost ($0.001) reconciles down to spent
// = $4.991. The second call projects $4.991 + $0.01 = $5.001 > $5.00 →
// trips.
func TestAnonymousAnalyzeBreakerCeilingMath(t *testing.T) {
	srv, _, scoreCalls := newAnonymousAnalyzeServer(t)
	freezeNow(t, time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC))

	const actualCost = 0.001
	setStubScorerCost(t, srv, actualCost, scoreCalls)

	srv.initAnonymousAnalyze()
	srv.anonymousAnalyzeBudget.addAndProject(
		anonymousAnalyzeCostCeilingUSD-estimatedCostPerAnonymousCallUSD,
		nowAnonymousAnalyze())

	// One successful call (uses one rate-limit slot, not all five).
	res1 := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/ceiling-1", "203.0.113.40")
	if res1.Code != http.StatusOK {
		t.Fatalf("first call: expected 200, got %d body=%s", res1.Code, res1.Body.String())
	}
	// After reconcile: ($5 - $0.01) + $0.001 = $4.991.
	got := srv.anonymousAnalyzeBudget.projectedSpend(nowAnonymousAnalyze())
	wantSpent := anonymousAnalyzeCostCeilingUSD -
		estimatedCostPerAnonymousCallUSD + actualCost
	if !approxEqual(got, wantSpent) {
		t.Fatalf("post-reconcile spend: expected %.6f, got %.6f", wantSpent, got)
	}

	// Second call: pre-spend projection $4.991 + $0.01 = $5.001 > $5.00 → trips.
	res2 := anonymousAnalyzeRequest(t, srv, "https://www.olx.bg/ad/ceiling-2", "203.0.113.41")
	if res2.Code != http.StatusServiceUnavailable {
		t.Fatalf("second call should trip breaker, got %d body=%s", res2.Code, res2.Body.String())
	}
}
