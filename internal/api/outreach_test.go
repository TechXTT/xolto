package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/store"
)

// newOutreachTestServer creates an in-memory SQLite-backed server with one
// pre-authenticated user and returns the server + token.
func newOutreachTestServer(t *testing.T) (*store.SQLiteStore, *Server, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "outreach-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	userID, err := st.CreateUser("outreach@example.com", "hash", "Outreach User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "outreach@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, nil)
	return st, srv, userID, token
}

// postJSON sends a POST request with a JSON body to the handler.
func postJSON(srv *Server, token, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

// getJSON sends a GET request to the handler.
func getJSON(srv *Server, token, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	return res
}

// ---------------------------------------------------------------------------
// POST /outreach/sent — validation
// ---------------------------------------------------------------------------

func TestOutreachSentRequiresListingID(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := postJSON(srv, token, "/outreach/sent", map[string]any{
		"marketplace_id": "olxbg",
		"draft_text":     "Hello",
		"draft_shape":    "buy",
		"draft_lang":     "bg",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing listing_id, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestOutreachSentInvalidDraftShape(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id":     "listing-1",
		"marketplace_id": "olxbg",
		"draft_text":     "Hello",
		"draft_shape":    "invalid_shape",
		"draft_lang":     "bg",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid draft_shape, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "draft_shape") {
		t.Fatalf("expected error about draft_shape, got %q", errMsg)
	}
}

func TestOutreachSentInvalidDraftLang(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id":     "listing-1",
		"marketplace_id": "olxbg",
		"draft_text":     "Hello",
		"draft_shape":    "buy",
		"draft_lang":     "fr",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid draft_lang, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "draft_lang") {
		t.Fatalf("expected error about draft_lang, got %q", errMsg)
	}
}

func TestOutreachSentInvalidMarketplace(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id":     "listing-1",
		"marketplace_id": "amazon",
		"draft_text":     "Hello",
		"draft_shape":    "buy",
		"draft_lang":     "en",
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid marketplace_id, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "marketplace_id") {
		t.Fatalf("expected error about marketplace_id, got %q", errMsg)
	}
}

func TestOutreachSentSuccess(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id":     "listing-olx-1",
		"marketplace_id": "olxbg",
		"draft_text":     "Здравейте!",
		"draft_shape":    "negotiate",
		"draft_lang":     "bg",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["state"] != "awaiting_reply" {
		t.Fatalf("expected state=awaiting_reply, got %v", body["state"])
	}
}

// ---------------------------------------------------------------------------
// POST /outreach/replied — validation and state machine
// ---------------------------------------------------------------------------

func TestOutreachRepliedNotFound(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := postJSON(srv, token, "/outreach/replied", map[string]any{
		"listing_id":     "does-not-exist",
		"marketplace_id": "olxbg",
		"reply_text":     "Thanks!",
	})
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on missing thread, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestOutreachRepliedAlreadyReplied409(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	// Create thread and mark replied.
	postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id": "listing-409", "marketplace_id": "olxbg",
		"draft_text": "text", "draft_shape": "buy", "draft_lang": "bg",
	})
	postJSON(srv, token, "/outreach/replied", map[string]any{
		"listing_id": "listing-409", "marketplace_id": "olxbg", "reply_text": "ok",
	})

	// Second reply attempt must return 409.
	res := postJSON(srv, token, "/outreach/replied", map[string]any{
		"listing_id": "listing-409", "marketplace_id": "olxbg", "reply_text": "ok again",
	})
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409 on double-reply, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	errMsg, _ := body["error"].(string)
	if !strings.Contains(errMsg, "already replied") {
		t.Fatalf("expected 'already replied' error, got %q", errMsg)
	}
}

func TestOutreachRepliedOnStaleThread(t *testing.T) {
	st, srv, userID, token := newOutreachTestServer(t)
	defer st.Close()

	// Create thread.
	postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id": "listing-stale", "marketplace_id": "olxbg",
		"draft_text": "text", "draft_shape": "buy", "draft_lang": "bg",
	})

	// Manually force to stale in the DB.
	ctx := t.Context()
	_, err := st.TransitionStaleThreads(ctx, 0) // cutoff=0 makes all awaiting_reply stale
	if err != nil {
		t.Fatalf("TransitionStaleThreads() error = %v", err)
	}
	// Verify it is now stale.
	thr, _ := st.GetThreadForListing(ctx, userID, "listing-stale", "olxbg")
	if thr == nil || thr.State != "stale" {
		t.Fatalf("expected thread to be stale, got %v", thr)
	}

	// Replying on a stale thread must succeed with 200.
	res := postJSON(srv, token, "/outreach/replied", map[string]any{
		"listing_id": "listing-stale", "marketplace_id": "olxbg", "reply_text": "late reply",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 on stale→replied, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["state"] != "replied" {
		t.Fatalf("expected state=replied after late reply, got %v", body["state"])
	}
}

// ---------------------------------------------------------------------------
// GET /outreach/threads
// ---------------------------------------------------------------------------

func TestOutreachThreadsList(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	// Insert two threads.
	for _, id := range []string{"thr-1", "thr-2"} {
		postJSON(srv, token, "/outreach/sent", map[string]any{
			"listing_id": id, "marketplace_id": "olxbg",
			"draft_text": "text", "draft_shape": "buy", "draft_lang": "bg",
		})
	}

	res := getJSON(srv, token, "/outreach/threads")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	threads, _ := body["threads"].([]any)
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	total, _ := body["total"].(float64)
	if int(total) != 2 {
		t.Fatalf("expected total=2, got %v", total)
	}
}

// ---------------------------------------------------------------------------
// GET /outreach/threads/{listing_id}
// ---------------------------------------------------------------------------

func TestOutreachThreadByListingFound(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	postJSON(srv, token, "/outreach/sent", map[string]any{
		"listing_id": "olxbg-777", "marketplace_id": "olxbg",
		"draft_text": "text", "draft_shape": "buy", "draft_lang": "bg",
	})

	res := getJSON(srv, token, "/outreach/threads/olxbg-777?marketplace_id=olxbg")
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", res.Code, res.Body.String())
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["listing_id"] != "olxbg-777" {
		t.Fatalf("expected listing_id=olxbg-777, got %v", body["listing_id"])
	}
}

func TestOutreachThreadByListingNotFound(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := getJSON(srv, token, "/outreach/threads/nonexistent?marketplace_id=olxbg")
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", res.Code, res.Body.String())
	}
}

func TestOutreachThreadByListingMissingMarketplace(t *testing.T) {
	st, srv, _, token := newOutreachTestServer(t)
	defer st.Close()

	res := getJSON(srv, token, "/outreach/threads/some-listing")
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on missing marketplace_id, got %d body=%s", res.Code, res.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Auth required
// ---------------------------------------------------------------------------

func TestOutreachEndpointsRequireAuth(t *testing.T) {
	st, srv, _, _ := newOutreachTestServer(t)
	defer st.Close()

	paths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/outreach/sent"},
		{http.MethodPost, "/outreach/replied"},
		{http.MethodGet, "/outreach/threads"},
		{http.MethodGet, "/outreach/threads/x?marketplace_id=olxbg"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, nil)
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for %s %s (no token), got %d", p.method, p.path, res.Code)
		}
	}
}
