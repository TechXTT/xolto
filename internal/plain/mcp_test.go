package plain_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TechXTT/xolto/internal/plain"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newMCPServer(t *testing.T, statusCode int, result any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		payload := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  result,
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func newMCPErrorServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		payload := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

// ---------------------------------------------------------------------------
// GetThread
// ---------------------------------------------------------------------------

func TestPlainMCPClient_GetThread_OK(t *testing.T) {
	result := map[string]any{
		"thread": map[string]any{
			"id":    "th_abc",
			"title": "I can't log in",
			"customer": map[string]any{
				"email":    "user@example.com",
				"fullName": "Test User",
			},
		},
		"content": []map[string]any{
			{"type": "text", "text": "Hello, I can't log in to my account."},
		},
	}
	srv := newMCPServer(t, http.StatusOK, result)
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	info, err := c.GetThread(context.Background(), "th_abc")
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if info.ThreadID != "th_abc" {
		t.Errorf("expected ThreadID=th_abc, got %q", info.ThreadID)
	}
	if info.CustomerEmail != "user@example.com" {
		t.Errorf("expected CustomerEmail=user@example.com, got %q", info.CustomerEmail)
	}
	if info.Subject != "I can't log in" {
		t.Errorf("expected Subject=%q, got %q", "I can't log in", info.Subject)
	}
	if !strings.Contains(info.Body, "can't log in") {
		t.Errorf("expected Body to contain login text, got %q", info.Body)
	}
}

func TestPlainMCPClient_GetThread_FallsBackOnBadJSON(t *testing.T) {
	// If the result is not the expected shape, GetThread should not error —
	// it returns a minimal ThreadInfo with at least the ThreadID preserved.
	srv := newMCPServer(t, http.StatusOK, "not-an-object")
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	info, err := c.GetThread(context.Background(), "th_fallback")
	if err != nil {
		t.Fatalf("expected no error on bad JSON result, got %v", err)
	}
	if info.ThreadID != "th_fallback" {
		t.Errorf("expected ThreadID=th_fallback (fallback), got %q", info.ThreadID)
	}
}

// ---------------------------------------------------------------------------
// AddLabels
// ---------------------------------------------------------------------------

func TestPlainMCPClient_AddLabels_OK(t *testing.T) {
	srv := newMCPServer(t, http.StatusOK, map[string]any{"ok": true})
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	if err := c.AddLabels(context.Background(), "th_abc", []string{"lt_01", "lt_02"}); err != nil {
		t.Fatalf("AddLabels() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// AddNote
// ---------------------------------------------------------------------------

func TestPlainMCPClient_AddNote_OK(t *testing.T) {
	srv := newMCPServer(t, http.StatusOK, map[string]any{"ok": true})
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	if err := c.AddNote(context.Background(), "th_abc", "Draft reply text."); err != nil {
		t.Fatalf("AddNote() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// SetPriority
// ---------------------------------------------------------------------------

func TestPlainMCPClient_SetPriority_OK(t *testing.T) {
	srv := newMCPServer(t, http.StatusOK, map[string]any{"ok": true})
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	if err := c.SetPriority(context.Background(), "th_abc", "urgent"); err != nil {
		t.Fatalf("SetPriority() error = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Error paths
// ---------------------------------------------------------------------------

func TestPlainMCPClient_RPC_Error(t *testing.T) {
	srv := newMCPErrorServer(t)
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	err := c.AddNote(context.Background(), "th_abc", "hello")
	if err == nil {
		t.Fatal("expected error from MCP error response, got nil")
	}
	if !strings.Contains(err.Error(), "method not found") {
		t.Errorf("expected error to contain 'method not found', got %q", err.Error())
	}
}

func TestPlainMCPClient_HTTP_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("unauthorized"))
	}))
	defer srv.Close()

	c := plain.NewPlainMCPClient("bad-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	_, err := c.GetThread(context.Background(), "th_abc")
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

// TestPreflight_2xx verifies that a 200 response marks the preflight as
// successful, with Configured=true and StatusCode=200.
func TestPreflight_2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Minimal valid JSON-RPC 2.0 response.
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer srv.Close()

	c := plain.NewPlainMCPClient("test-token-ok")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	pf := c.Preflight(context.Background())

	if !pf.Configured {
		t.Error("expected Configured=true for non-empty token")
	}
	if pf.StatusCode != http.StatusOK {
		t.Errorf("expected StatusCode=200, got %d", pf.StatusCode)
	}
	if pf.Err != nil {
		t.Errorf("expected no error, got %v", pf.Err)
	}
	if pf.Endpoint == "" {
		t.Error("expected Endpoint to be populated")
	}
}

// TestPreflight_401 verifies that a 401 response is captured with the correct
// status code and a non-empty body snippet.
func TestPreflight_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_token","message":"Token is not valid"}`))
	}))
	defer srv.Close()

	c := plain.NewPlainMCPClient("clearly-not-a-real-token")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	pf := c.Preflight(context.Background())

	if !pf.Configured {
		t.Error("expected Configured=true for non-empty token")
	}
	if pf.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusCode=401, got %d", pf.StatusCode)
	}
	if pf.Err == nil {
		t.Error("expected an error for 401 response")
	}
	if pf.BodySnippet == "" {
		t.Error("expected BodySnippet to be populated on 401")
	}
	if !strings.Contains(pf.BodySnippet, "invalid_token") {
		t.Errorf("expected BodySnippet to contain response body, got %q", pf.BodySnippet)
	}
}

// TestPreflight_EmptyToken verifies that an empty token skips the HTTP call
// entirely and returns Configured=false.
func TestPreflight_EmptyToken(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := plain.NewPlainMCPClient("") // empty token
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	pf := c.Preflight(context.Background())

	if pf.Configured {
		t.Error("expected Configured=false for empty token")
	}
	if called {
		t.Error("expected no HTTP call when token is empty")
	}
	if pf.StatusCode != 0 {
		t.Errorf("expected StatusCode=0 when no call made, got %d", pf.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// MCPCallError — errors.As accessor
// ---------------------------------------------------------------------------

// TestMCPCallError_ErrorsAs verifies that a 401 response from GetThread
// produces an error that exposes status=401 and a body snippet via errors.As.
func TestMCPCallError_ErrorsAs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("token rejected by server"))
	}))
	defer srv.Close()

	c := plain.NewPlainMCPClient("bad-token-for-errors-as-test")
	c.Endpoint = srv.URL
	c.HTTPClient = srv.Client()

	_, err := c.GetThread(context.Background(), "th_test")
	if err == nil {
		t.Fatal("expected error from 401 response, got nil")
	}

	var mcpErr *plain.MCPCallError
	if !errors.As(err, &mcpErr) {
		t.Fatalf("expected *plain.MCPCallError via errors.As, got %T: %v", err, err)
	}
	if mcpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected StatusCode=401, got %d", mcpErr.StatusCode)
	}
	if !strings.Contains(mcpErr.BodySnippet, "token rejected") {
		t.Errorf("expected BodySnippet to contain body, got %q", mcpErr.BodySnippet)
	}
	if mcpErr.Endpoint == "" {
		t.Error("expected Endpoint to be populated in MCPCallError")
	}
	// errors.Is(err, ErrPlainMCPUnavailable) must still work via Unwrap.
	if !errors.Is(err, plain.ErrPlainMCPUnavailable) {
		t.Error("expected errors.Is(err, ErrPlainMCPUnavailable) to be true via Unwrap")
	}
}
