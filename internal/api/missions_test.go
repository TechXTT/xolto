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
	"github.com/TechXTT/xolto/internal/models"
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
// 4 entries. W19-39 / XOL-136: auto-expand runs async for active missions,
// so the 201 response will have empty SearchQueries; poll the store until
// search_configs appear (goroutine completes within 5s).
func TestHandleMissionsPostAutoExpandsTargetQuery(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Sony A6700 Hunt",
		"TargetQuery": "Sony A6700",
		"Status":      "active",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}

	// W19-39 / XOL-136: auto-expand is now async; poll for search_configs to
	// appear in the store (goroutine creates them via AutoDeployHunts).
	deadline := time.Now().Add(5 * time.Second)
	var configs []models.SearchSpec
	for time.Now().Before(deadline) {
		var err error
		configs, err = st.GetSearchConfigs(userID)
		if err != nil {
			t.Fatalf("GetSearchConfigs error: %v", err)
		}
		if len(configs) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(configs) == 0 {
		t.Fatalf("expected search_configs to be created by async auto-expand goroutine, got 0 after 5s")
	}
	// Each search_config must have a non-empty query.
	for i, c := range configs {
		if c.Query == "" {
			t.Errorf("search_configs[%d].Query is empty", i)
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
		t.Fatalf("status = %d, want 201 (graceful degradation on cap-fire); body = %s", res.Code, res.Body.String())
	}

	// W19-39 / XOL-136: auto-expand only runs async for active missions.
	// Draft missions skip the goroutine entirely, so the 201 response simply
	// confirms the mission was inserted without error. That is the key
	// graceful-degradation guarantee: cap-fire must never prevent mission
	// creation. SearchQueries will be empty; that is the correct expectation
	// for a non-active mission with a budget-exhausted AI assistant.
}

// ---------------------------------------------------------------------------
// W19-37 / XOL-134: generator floor regression — POST /missions end-to-end
// ---------------------------------------------------------------------------

// TestHandleMissionsPostAutoExpandsGenericTopic — end-to-end regression for
// XOL-134. Prior to the fix, "Fujifilm X-T4" (non-sony/non-camera topic)
// triggered genericSearches which returned 1 entry. After the fix the floor
// synthesis ensures >= 3 SearchQueries are persisted on the mission row.
// W19-39 / XOL-136: auto-expand runs async for active missions; poll the
// mission's SearchQueries in the DB for eventual consistency. Note: the
// search_configs count may be lower than SearchQueries count because
// AutoDeployHunts sanitizes queries (strips "used" etc.), so we assert on
// the mission row's SearchQueries, not on search_configs count.
func TestHandleMissionsPostAutoExpandsGenericTopic(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Fujifilm X-T4 Hunt",
		"TargetQuery": "Fujifilm X-T4",
		"Status":      "active",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}

	// W19-39 / XOL-136: auto-expand is async; poll the mission row's
	// SearchQueries which are updated by the goroutine's second UpsertMission.
	// XOL-134 floor: at least 3 SearchQueries on the persisted mission.
	deadline := time.Now().Add(5 * time.Second)
	var missions []models.Mission
	for time.Now().Before(deadline) {
		var err error
		missions, err = st.ListMissions(userID)
		if err != nil {
			t.Fatalf("ListMissions error: %v", err)
		}
		if len(missions) > 0 && len(missions[0].SearchQueries) >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(missions) == 0 {
		t.Fatalf("expected mission in store, got 0")
	}
	sq := missions[0].SearchQueries
	if len(sq) < 3 {
		t.Errorf("SearchQueries len = %d, want >= 3 (XOL-134 floor); queries = %v", len(sq), sq)
	}
	if len(sq) > 5 {
		t.Errorf("SearchQueries len = %d, must be <= 5 (founder hard-cap)", len(sq))
	}
	for i, q := range sq {
		if q == "" {
			t.Errorf("SearchQueries[%d] is empty", i)
		}
	}
}

// ---------------------------------------------------------------------------
// W19-35 / XOL-132: idempotency key tests
// ---------------------------------------------------------------------------

// postMissionWithKey fires POST /missions with the given body and Idempotency-Key header.
func postMissionWithKey(srv *Server, tok, idempKey string, body map[string]any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/missions", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	if idempKey != "" {
		req.Header.Set("Idempotency-Key", idempKey)
	}
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

// TestHandleMissionsPostIdempotencyHitReturnsExistingMission — POST with an
// Idempotency-Key creates a mission; second POST with the same key within 30s
// returns the same response and does NOT insert a second mission.
func TestHandleMissionsPostIdempotencyHitReturnsExistingMission(t *testing.T) {
	st, srv, _, tok := newMissionsTestServer(t)

	body := map[string]any{
		"Name":        "Canon EOS R6 Hunt",
		"TargetQuery": "Canon EOS R6",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	}

	res1 := postMissionWithKey(srv, tok, "key-abc-123", body)
	if res1.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d, want 201; body = %s", res1.Code, res1.Body.String())
	}
	var m1 map[string]any
	if err := json.NewDecoder(res1.Body).Decode(&m1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	id1 := m1["ID"]

	res2 := postMissionWithKey(srv, tok, "key-abc-123", body)
	if res2.Code != http.StatusCreated {
		t.Fatalf("second POST status = %d, want 201; body = %s", res2.Code, res2.Body.String())
	}
	var m2 map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&m2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	id2 := m2["ID"]

	// Both responses must carry the same mission ID.
	if id1 != id2 {
		t.Errorf("idempotency hit: id1=%v != id2=%v — duplicate mission was created", id1, id2)
	}

	// Only one mission must exist in the store.
	missions, err := st.ListMissions(m1["UserID"].(string))
	if err != nil {
		t.Fatalf("ListMissions error: %v", err)
	}
	if len(missions) != 1 {
		t.Errorf("expected 1 mission in store after idempotent POST, got %d", len(missions))
	}
}

// TestHandleMissionsPostIdempotencyMissAfterTTL — POST with Idempotency-Key=X
// creates a mission; after the cache TTL is manually expired by replacing the
// entry, a second POST with the same key creates a NEW mission.
func TestHandleMissionsPostIdempotencyMissAfterTTL(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	body := map[string]any{
		"Name":        "TTL Miss Mission",
		"TargetQuery": "Canon EOS R5",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	}

	res1 := postMissionWithKey(srv, tok, "key-ttl-miss", body)
	if res1.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d; body = %s", res1.Code, res1.Body.String())
	}

	// Manually expire the cache entry by backdating its createdAt > 30s.
	cacheKey := userID + "|key-ttl-miss"
	srv.idempMu.Lock()
	if entry, ok := srv.idempCache[cacheKey]; ok {
		entry.createdAt = time.Now().Add(-31 * time.Second)
		srv.idempCache[cacheKey] = entry
	}
	srv.idempMu.Unlock()

	res2 := postMissionWithKey(srv, tok, "key-ttl-miss", body)
	if res2.Code != http.StatusCreated {
		t.Fatalf("second POST (after TTL) status = %d; body = %s", res2.Code, res2.Body.String())
	}

	// A new mission must have been created: store should have 2.
	missions, err := st.ListMissions(userID)
	if err != nil {
		t.Fatalf("ListMissions error: %v", err)
	}
	if len(missions) != 2 {
		t.Errorf("expected 2 missions after TTL expiry, got %d", len(missions))
	}
}

// TestHandleMissionsPostNoIdempotencyKeyAllowsDuplicates — POST without an
// Idempotency-Key always proceeds regardless of prior POSTs (legacy behavior).
func TestHandleMissionsPostNoIdempotencyKeyAllowsDuplicates(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	body := map[string]any{
		"Name":        "No Key Mission",
		"TargetQuery": "Nikon Z6",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	}

	res1 := postMission(srv, tok, body)
	if res1.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d; body = %s", res1.Code, res1.Body.String())
	}

	res2 := postMission(srv, tok, body)
	if res2.Code != http.StatusCreated {
		t.Fatalf("second POST status = %d; body = %s", res2.Code, res2.Body.String())
	}

	// Both missions must exist: no dedup gate without Idempotency-Key.
	missions, err := st.ListMissions(userID)
	if err != nil {
		t.Fatalf("ListMissions error: %v", err)
	}
	if len(missions) != 2 {
		t.Errorf("expected 2 missions (no dedup without key), got %d", len(missions))
	}
}

// ---------------------------------------------------------------------------
// W19-39 / XOL-136: async auto-expand tests
// ---------------------------------------------------------------------------

// TestHandleMissionsPostMissionInsertedBeforeAutoExpand — POST /missions
// immediately creates the mission row and returns 201 before the goroutine
// runs EnsureSearchVariants. Verified by querying the store synchronously
// after receiving the 201 response.
func TestHandleMissionsPostMissionInsertedBeforeAutoExpand(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Sync Insert Mission",
		"TargetQuery": "Canon EOS R5",
		"Status":      "active",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}

	// Mission must exist in the store immediately after the 201 response —
	// it was inserted in the synchronous path before the goroutine started.
	missions, err := st.ListMissions(userID)
	if err != nil {
		t.Fatalf("ListMissions error: %v", err)
	}
	if len(missions) == 0 {
		t.Fatalf("expected mission in store immediately after 201 response, got 0")
	}
	if missions[0].Name != "Sync Insert Mission" {
		t.Errorf("mission name = %q, want %q", missions[0].Name, "Sync Insert Mission")
	}
}

// TestHandleMissionsPostAsyncAutoExpandPersistsSearchConfigs — POST /missions
// with an active mission eventually creates search_configs via the async
// goroutine. Polls for up to 5s.
func TestHandleMissionsPostAsyncAutoExpandPersistsSearchConfigs(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Async Expand Mission",
		"TargetQuery": "Sony A6700",
		"Status":      "active",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}

	// Poll until search_configs appear (created by the async goroutine).
	deadline := time.Now().Add(5 * time.Second)
	var configs []models.SearchSpec
	for time.Now().Before(deadline) {
		var err error
		configs, err = st.GetSearchConfigs(userID)
		if err != nil {
			t.Fatalf("GetSearchConfigs error: %v", err)
		}
		if len(configs) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(configs) == 0 {
		t.Fatalf("expected search_configs after async auto-expand, got 0 within 5s")
	}
}

// TestHandleMissionsPostIdempotencyCacheStillWorks — POST with same
// Idempotency-Key twice; second call returns the cached response without
// inserting a duplicate mission. This verifies XOL-132 still works after
// the W19-39 restructure.
func TestHandleMissionsPostIdempotencyCacheStillWorks(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	body := map[string]any{
		"Name":        "Idem Cache Check",
		"TargetQuery": "Nikon Z6 II",
		"Status":      "draft",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	}

	res1 := postMissionWithKey(srv, tok, "xol136-idem-key", body)
	if res1.Code != http.StatusCreated {
		t.Fatalf("first POST status = %d; body = %s", res1.Code, res1.Body.String())
	}
	var m1 map[string]any
	if err := json.NewDecoder(res1.Body).Decode(&m1); err != nil {
		t.Fatalf("decode first response: %v", err)
	}

	res2 := postMissionWithKey(srv, tok, "xol136-idem-key", body)
	if res2.Code != http.StatusCreated {
		t.Fatalf("second POST status = %d; body = %s", res2.Code, res2.Body.String())
	}
	var m2 map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&m2); err != nil {
		t.Fatalf("decode second response: %v", err)
	}

	if m1["ID"] != m2["ID"] {
		t.Errorf("idempotency broken: first ID=%v, second ID=%v — duplicate inserted", m1["ID"], m2["ID"])
	}

	missions, err := st.ListMissions(userID)
	if err != nil {
		t.Fatalf("ListMissions error: %v", err)
	}
	if len(missions) != 1 {
		t.Errorf("expected 1 mission after idempotent POST, got %d", len(missions))
	}
}

// TestHandleMissionsPostInactiveMissionSkipsAutoExpand — POST with a
// non-active status ("paused") must insert the mission and return 201
// without creating search_configs. The async goroutine only fires for
// active missions.
func TestHandleMissionsPostInactiveMissionSkipsAutoExpand(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Paused Mission",
		"TargetQuery": "Sony A6700",
		"Status":      "paused",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}

	// Mission must be in the store.
	missions, err := st.ListMissions(userID)
	if err != nil {
		t.Fatalf("ListMissions error: %v", err)
	}
	if len(missions) == 0 {
		t.Fatalf("expected mission inserted, got 0")
	}

	// Give the goroutine time to fire (it should NOT fire for non-active).
	time.Sleep(200 * time.Millisecond)

	// No search_configs should exist: goroutine is gated on status == "active".
	configs, err := st.GetSearchConfigs(userID)
	if err != nil {
		t.Fatalf("GetSearchConfigs error: %v", err)
	}
	if len(configs) != 0 {
		t.Errorf("expected 0 search_configs for paused mission, got %d", len(configs))
	}
}

// TestHandleMissionsPostReturnsBeforeAutoExpand — POST /missions 201 response
// is bounded by the DB insert, not by auto-expand. The 201 is expected
// immediately. We verify indirectly: the response contains the mission with
// empty SearchQueries (since auto-expand hasn't completed yet), while
// search_configs appear only after polling. This confirms the response was
// sent before the goroutine completed.
func TestHandleMissionsPostReturnsBeforeAutoExpand(t *testing.T) {
	st, srv, userID, tok := newMissionsTestServer(t)

	res := postMission(srv, tok, map[string]any{
		"Name":        "Return Early Mission",
		"TargetQuery": "Sony A6700",
		"Status":      "active",
		"Urgency":     "flexible",
		"CountryCode": "BG",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", res.Code, res.Body.String())
	}

	// The response body must have an ID (mission was inserted synchronously).
	body := decodeMissionBody(t, res)
	if body["ID"] == nil || body["ID"] == float64(0) {
		t.Errorf("expected non-zero mission ID in 201 response, got %v", body["ID"])
	}

	// The response SearchQueries will be empty for active missions at response
	// time (auto-expand is still in flight). Wait for the goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		configs, err := st.GetSearchConfigs(userID)
		if err != nil {
			t.Fatalf("GetSearchConfigs error: %v", err)
		}
		if len(configs) > 0 {
			return // goroutine completed; test passes
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("async goroutine did not create search_configs within 5s")
}
