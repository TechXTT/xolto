package plain_test

import (
	"context"
	"encoding/json"
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
