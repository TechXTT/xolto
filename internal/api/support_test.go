package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// flushableRecorder embeds httptest.ResponseRecorder and explicitly implements
// http.Flusher so we can verify delegation through statusCapturingResponseWriter.
type flushableRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (r *flushableRecorder) Flush() {
	r.flushed = true
	r.ResponseRecorder.Flush()
}

// Compile-time assertion: *statusCapturingResponseWriter must satisfy http.Flusher.
var _ http.Flusher = (*statusCapturingResponseWriter)(nil)

func TestStatusCapturingResponseWriterImplementsFlusher(t *testing.T) {
	inner := &flushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &statusCapturingResponseWriter{ResponseWriter: inner}

	flusher, ok := any(w).(http.Flusher)
	if !ok {
		t.Fatal("statusCapturingResponseWriter does not implement http.Flusher")
	}
	flusher.Flush()
	if !inner.flushed {
		t.Fatal("Flush() did not delegate to the underlying ResponseWriter")
	}
}

func TestStatusCapturingResponseWriterFlushIsNoopWhenInnerNotFlusher(t *testing.T) {
	// Use a bare http.ResponseWriter that does NOT implement http.Flusher.
	// Calling Flush() must not panic.
	var inner http.ResponseWriter = httptest.NewRecorder()
	w := &statusCapturingResponseWriter{ResponseWriter: inner}
	w.Flush() // must not panic
}

func TestStatusCapturingResponseWriterUnwrap(t *testing.T) {
	inner := httptest.NewRecorder()
	w := &statusCapturingResponseWriter{ResponseWriter: inner}
	if w.Unwrap() != inner {
		t.Fatal("Unwrap() did not return the underlying ResponseWriter")
	}
}

// TestRequestLoggingMiddlewarePreservesFlusher exercises the full middleware chain
// (matching server.go Handler()) against a handler that asserts http.Flusher is
// available. Before the fix this would observe ok=false and write a 500.
func TestRequestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	srv := &Server{}

	var flusherOK bool
	sseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, flusherOK = w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
	})

	// Wrap with only requestLoggingMiddleware — the layer that introduces
	// statusCapturingResponseWriter and was hiding the Flusher interface.
	handler := srv.requestLoggingMiddleware(sseHandler)

	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if !flusherOK {
		t.Fatal("http.Flusher not available inside handler wrapped by requestLoggingMiddleware; SSE would return 500")
	}
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}
}
