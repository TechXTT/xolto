package api

// TestSSESmoke is a CI regression gate for INC-20260417b / commit 7f21cc0.
//
// The bug: requestLoggingMiddleware wrapped the ResponseWriter in
// statusCapturingResponseWriter, which did not expose http.Flusher. The SSE
// handler's type-assertion w.(http.Flusher) returned ok=false and the handler
// responded with HTTP 500 "streaming unsupported".
//
// This test uses httptest.NewServer (a real net/http listener) rather than
// httptest.NewRecorder so that the full middleware stack sits between a real
// stdlib ResponseWriter (which IS a Flusher) and the handler. If the Flusher
// passthrough is ever removed from statusCapturingResponseWriter, this test
// goes red — exactly the same failure mode as the production incident.

import (
	"bufio"
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

func TestSSESmoke(t *testing.T) {
	// Spin up a real in-memory SQLite-backed server. No Postgres needed — the
	// SSE handler only validates the JWT and registers the client channel;
	// it does not touch the DB after auth.
	dbPath := filepath.Join(t.TempDir(), "sse-smoke-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("sse-smoke@example.com", "hash", "SSE Smoke")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "sse-smoke@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, nil, nil)

	// httptest.NewServer launches a real net/http listener. The response writer
	// the stdlib provides to handlers IS a http.Flusher. If the middleware
	// wrapper hides that interface, ServeHTTP returns 500 — the INC-20260417b
	// failure mode.
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Bounded total timeout: server boot + SSE connection + first frame must
	// complete within 15 seconds. Fail fast — never let a broken gate hang CI.
	const totalTimeout = 15 * time.Second
	client := &http.Client{Timeout: totalTimeout}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /events error = %v", err)
	}
	defer resp.Body.Close()

	// AC2: non-2xx status means SSE endpoint is broken (e.g. 500 from the
	// Flusher regression or 401 from auth failure).
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("GET /events returned status %d; want 2xx (SSE Flusher regression?)", resp.StatusCode)
	}

	// AC2: Content-Type must be text/event-stream (prefix match,
	// case-insensitive — accepts both "text/event-stream" and
	// "text/event-stream; charset=utf-8").
	ct := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("GET /events Content-Type = %q; want prefix \"text/event-stream\"", ct)
	}

	// AC2: read lines until we see at least one "data:" line. Tolerate any
	// number of comment lines (":heartbeat\n\n") before the first data frame.
	// The SSE handler immediately writes data: {"type":"connected"}\n\n and
	// flushes, so the first data line should arrive within milliseconds.
	scanner := bufio.NewScanner(resp.Body)
	deadline := time.Now().Add(10 * time.Second)
	foundData := false
	for time.Now().Before(deadline) && scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			foundData = true
			break
		}
	}
	if err := scanner.Err(); err != nil && !foundData {
		t.Fatalf("reading SSE stream error = %v", err)
	}
	if !foundData {
		t.Fatal("GET /events: no \"data:\" line received within 10s; SSE stream is broken")
	}
}
