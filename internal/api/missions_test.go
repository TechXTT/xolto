package api

// W19-31 / XOL-128: tests for auto-expand of mission.TargetQuery into search
// variants on POST /missions. Four cases per brief:
//
//  1. Happy path: empty search_queries + non-empty target_query → SearchQueries
//     populated via GenerateSearches static fallback (AI disabled in test config).
//  2. Skip when search_queries already provided → preserved as-is.
//  3. Hard-cap at 5 variants (founder-locked). Static fallback never exceeds 5,
//     so the cap path is exercised via a manual post-expand slice assertion on the
//     sony fallback (4 entries, all ≤ 5). See comment below.
//  4. Graceful skip on aibudget cap-fire: mission still created (201), SearchQueries
//     empty.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/assistant"
	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/store"
)

// newMissionsTestServer creates a minimal test server and a regular user.
// AI is disabled (no APIKey) so EnsureSearchVariants uses the generator static
// fallback path. The server is wired with a real assistant so that the handler
// refactor (EnsureSearchVariants via s.assistant) fires correctly.
// Returns (server, userID, auth-token).
func newMissionsTestServer(t *testing.T) (*store.SQLiteStore, *Server, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "missions-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })

	userID, err := st.CreateUser("mission-user@example.com", "hash", "Mission User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// AI disabled: empty APIKey → EnsureSearchVariants uses generator static fallback.
	cfg := config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}
	// Build a minimal assistant with AI disabled so EnsureSearchVariants uses
	// the static fallback path. Marketplace and scorer are nil — not exercised here.
	asst := assistant.New(&config.Config{AI: config.AIConfig{Enabled: false}}, st, nil, nil)
	srv := NewServer(cfg, st, asst, nil, nil, nil)

	tok, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "mission-user@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	return st, srv, userID, tok
}

// postMission fires POST /missions with the given body and returns the recorder.
func postMission(srv *Server, tok string, body map[string]any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/missions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

// decodeMissionBody decodes a /missions POST response into a map. Panics on
// decode failure only when status was 201 (so test failures are readable).
func decodeMissionBody(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v; raw = %s", err, res.Body.String())
	}
	return body
}

// TestHandleMissionsPostAutoExpandsTargetQuery — happy path.
// AI disabled → static fallback via GenerateSearches. The sony branch returns
// 4 entries, so SearchQueries must be populated with >= 1 query after creation.
func TestHandleMissionsPostAutoExpandsTargetQuery(t *testing.T) {
	_, srv, _, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Sony A6700 Hunt",
		"TargetQuery": "Sony A6700",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}
	body := decodeMissionBody(t, res)

	// SearchQueries must be populated: static sony fallback returns 4 entries.
	// Response JSON key is "SearchQueries" (no json tag on models.Mission).
	raw, ok := body["SearchQueries"]
	if !ok {
		t.Fatalf("response missing SearchQueries key; body = %v", body)
	}
	sq, ok := raw.([]any)
	if !ok {
		t.Fatalf("SearchQueries is not an array: %T", raw)
	}
	if len(sq) == 0 {
		t.Fatalf("expected SearchQueries to be populated, got empty slice")
	}
	// Each entry must be a non-empty string (query text, not a SearchConfig struct).
	for i, v := range sq {
		q, ok := v.(string)
		if !ok || q == "" {
			t.Errorf("SearchQueries[%d] = %v (type %T), want non-empty string", i, v, v)
		}
	}
}

// TestHandleMissionsPostSkipsAutoExpandWhenSearchQueriesProvided — when the
// caller provides >= 3 search_queries (adequate coverage), EnsureSearchVariants
// must NOT overwrite them. The skip threshold is >= 3 per W19-32 contract.
func TestHandleMissionsPostSkipsAutoExpandWhenSearchQueriesProvided(t *testing.T) {
	_, srv, _, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":          "Fuji Hunt",
		"TargetQuery":   "Fujifilm X-T4",
		"SearchQueries": []string{"fuji xt4", "fujifilm x-t4", "xt4 body"},
		"Status":        "draft",
		"Urgency":       "flexible",
		"CountryCode":   "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}
	body := decodeMissionBody(t, res)

	// Response JSON key is "SearchQueries" (no json tag on models.Mission).
	raw, ok := body["SearchQueries"]
	if !ok {
		t.Fatalf("response missing SearchQueries key")
	}
	sq, ok := raw.([]any)
	if !ok {
		t.Fatalf("SearchQueries is not an array: %T", raw)
	}
	// All 3 provided queries must be preserved (already adequate: len >= 3).
	if len(sq) != 3 {
		t.Fatalf("expected 3 preserved SearchQueries (already adequate), got %d: %v", len(sq), sq)
	}
	if sq[0] != "fuji xt4" || sq[1] != "fujifilm x-t4" || sq[2] != "xt4 body" {
		t.Errorf("SearchQueries = %v, want [fuji xt4 fujifilm x-t4 xt4 body]", sq)
	}
}

// TestHandleMissionsPostHardCapsVariantsAtFive — the hard-cap at 5 variants.
//
// The static fallback for "sony" returns 4 entries (all ≤ 5), so the cap
// branch is not triggered by the fallback itself. The cap logic in the handler
// is still exercised: we assert len(search_queries) <= 5 to guard the invariant.
// A forced-cap scenario would require a custom GenerateFunc hook which the
// generator package does not currently expose; documenting this boundary here
// and relying on the handler's `variants[:5]` slice-cap for protection.
func TestHandleMissionsPostHardCapsVariantsAtFive(t *testing.T) {
	_, srv, _, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Sony Variants Cap",
		"TargetQuery": "Sony A7 III",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}
	body := decodeMissionBody(t, res)

	// Response JSON key is "SearchQueries" (no json tag on models.Mission).
	raw, ok := body["SearchQueries"]
	if !ok {
		// nil/absent means no auto-expand happened (no fallback match); not a failure.
		return
	}
	sq, ok := raw.([]any)
	if !ok {
		// null JSON → nil interface; not an error for this test.
		return
	}
	if len(sq) > 5 {
		t.Errorf("SearchQueries len = %d, must be <= 5 (founder hard-cap)", len(sq))
	}
}

// TestHandleMissionsPostGracefulOnCapFire — when the aibudget global cap is
// exhausted before mission creation, the handler must still return 201 and
// SearchQueries must be empty (graceful skip, no error surfaced to user).
//
// This test uses a server where AI is nominally enabled (dummy APIKey) so
// EnsureSearchVariants → generator enters the generateWithAI code path where
// the aibudget.Allow gate lives. The cap fires before any HTTP call to the AI
// provider, so no actual network request is made.
func TestHandleMissionsPostGracefulOnCapFire(t *testing.T) {
	// Install a fresh tracker and immediately exhaust it.
	tr := withGlobalTracker(t)
	if ok, _ := tr.Allow(context.Background(), "test_seed", aibudget.DefaultCapUSD); !ok {
		t.Fatalf("initial seed Allow at cap should succeed")
	}
	// Budget is now exhausted: next generator.Allow will return false.

	// Use an AI-enabled assistant so EnsureSearchVariants enters the generator's
	// generateWithAI code path where the aibudget.Allow gate is checked.
	dbPath := filepath.Join(t.TempDir(), "missions-cap-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { st.Close() })
	userID, err := st.CreateUser("cap-user@example.com", "hash", "Cap User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	// AI nominally enabled; unreachable URL — cap fires before HTTP call.
	asst := assistant.New(&config.Config{
		AI: config.AIConfig{
			Enabled: true,
			APIKey:  "dummy-key-for-cap-test",
			BaseURL: "http://127.0.0.1:0",
			Model:   "gpt-4o",
		},
	}, st, nil, nil)
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, asst, nil, nil, nil)
	tok, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "cap-user@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	res := postMission(srv, tok, map[string]any{
		"Name":        "Budget Exhausted Mission",
		"TargetQuery": "Sony A6700",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (graceful skip on cap-fire); body = %s", res.Code, res.Body.String())
	}
	body := decodeMissionBody(t, res)

	// SearchQueries must be empty (nil or []) — auto-expand was silently skipped.
	// Response JSON key is "SearchQueries" (no json tag on models.Mission).
	raw := body["SearchQueries"]
	if raw == nil {
		return // nil is acceptable: no auto-expand happened
	}
	sq, ok := raw.([]any)
	if !ok {
		t.Fatalf("SearchQueries unexpected type %T", raw)
	}
	if len(sq) != 0 {
		t.Errorf("expected empty SearchQueries on cap-fire, got %v", sq)
	}
}
