package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/observability"
	"github.com/getsentry/sentry-go"
)

// mockTransport is an in-memory Sentry transport that captures events without
// making any real network calls. It implements sentry.Transport.
type mockTransport struct {
	events []*sentry.Event
}

func (m *mockTransport) Flush(_ time.Duration) bool            { return true }
func (m *mockTransport) FlushWithContext(_ context.Context) bool { return true }
func (m *mockTransport) Configure(_ sentry.ClientOptions)      {}
func (m *mockTransport) SendEvent(event *sentry.Event)         { m.events = append(m.events, event) }
func (m *mockTransport) Close()                                {}

// initTestSentry configures the Sentry SDK with the mockTransport so no real
// HTTP calls are made. It enables SentryEnabled() and returns the transport for
// inspecting captured events, plus a cleanup function to restore state.
func initTestSentry(t *testing.T) (*mockTransport, func()) {
	t.Helper()
	transport := &mockTransport{}

	// sentry.Init binds a new client to the current (global) hub.
	err := sentry.Init(sentry.ClientOptions{
		// Use a syntactically valid but fake DSN — the mockTransport intercepts
		// all sends so nothing is transmitted over the network.
		Dsn:       "https://public@o0.ingest.sentry.io/0",
		Transport: transport,
	})
	if err != nil {
		t.Fatalf("sentry.Init() error = %v", err)
	}

	observability.SetEnabledForTest(true)

	cleanup := func() {
		observability.SetEnabledForTest(false)
		// Unbind the client so no further events are captured.
		sentry.CurrentHub().BindClient(nil)
	}
	return transport, cleanup
}

// TestSentryMiddlewarePanicCaptures verifies that a panicking handler results
// in HTTP 500 and a Sentry event being captured.
func TestSentryMiddlewarePanicCaptures(t *testing.T) {
	transport, cleanup := initTestSentry(t)
	defer cleanup()

	srv := &Server{}
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic value")
	})

	req := httptest.NewRequest(http.MethodGet, "/test/panic", nil)
	res := httptest.NewRecorder()
	srv.sentryMiddleware(panicHandler).ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 after panic, got %d", res.Code)
	}
	if len(transport.events) == 0 {
		t.Fatal("expected Sentry event to be captured after panic, got none")
	}
	msg := transport.events[0].Message
	if !strings.Contains(msg, "panic") {
		t.Fatalf("expected captured message to contain 'panic', got %q", msg)
	}
}

// TestSentryMiddleware5xxCaptures verifies that a handler returning 5xx causes
// a Sentry event without swallowing the response body or status.
func TestSentryMiddleware5xxCaptures(t *testing.T) {
	transport, cleanup := initTestSentry(t)
	defer cleanup()

	srv := &Server{}
	fiveHundredHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"temporarily unavailable"}`))
	})

	req := httptest.NewRequest(http.MethodGet, "/matches", nil)
	res := httptest.NewRecorder()
	srv.sentryMiddleware(fiveHundredHandler).ServeHTTP(res, req)

	// Status must reach the client.
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 to reach client, got %d", res.Code)
	}
	// Body must not be swallowed.
	body := res.Body.String()
	if !strings.Contains(body, "temporarily unavailable") {
		t.Fatalf("expected response body to contain 'temporarily unavailable', got %q", body)
	}
	// Sentry must have captured an event.
	if len(transport.events) == 0 {
		t.Fatal("expected Sentry event for 5xx response, got none")
	}
	msg := transport.events[0].Message
	if !strings.Contains(msg, "503") {
		t.Fatalf("expected captured message to reference 503, got %q", msg)
	}
}

// TestSentryMiddleware2xxNoCapture verifies that successful responses do not
// generate Sentry events.
func TestSentryMiddleware2xxNoCapture(t *testing.T) {
	transport, cleanup := initTestSentry(t)
	defer cleanup()

	srv := &Server{}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()
	srv.sentryMiddleware(okHandler).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}
	if len(transport.events) != 0 {
		t.Fatalf("expected no Sentry events for 2xx, got %d", len(transport.events))
	}
}

// TestSentryMiddlewareSSEFlusherPreserved is the regression guard for INC-2.
// It confirms that http.Flusher is still assertable on the ResponseWriter
// passed to an SSE-style handler when sentryMiddleware is the outermost layer.
func TestSentryMiddlewareSSEFlusherPreserved(t *testing.T) {
	_, cleanup := initTestSentry(t)
	defer cleanup()

	srv := &Server{}
	var flusherOK bool
	sseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, flusherOK = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	})

	// Use a flushableRecorder (defined in support_test.go) so the assertion
	// chain goes: sentryMiddleware -> sentryCaptureWriter -> flushableRecorder.
	inner := &flushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	srv.sentryMiddleware(sseHandler).ServeHTTP(inner, req)

	if !flusherOK {
		t.Fatal("http.Flusher not available inside handler wrapped by sentryMiddleware; SSE regression INC-2 would reoccur")
	}
}

// TestSentryMiddlewareNoopWhenDSNUnset verifies that without SENTRY_DSN the
// middleware is completely transparent: no panics, no event captures, and the
// response passes through unchanged.
func TestSentryMiddlewareNoopWhenDSNUnset(t *testing.T) {
	// Do NOT call initTestSentry — SentryEnabled() must return false.
	if observability.SentryEnabled() {
		t.Skip("Sentry is enabled in this environment; skipping no-op test")
	}

	srv := &Server{}
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()
	srv.sentryMiddleware(inner).ServeHTTP(res, req)

	if !called {
		t.Fatal("inner handler was not called when Sentry is disabled")
	}
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200 in no-op mode, got %d", res.Code)
	}
}
