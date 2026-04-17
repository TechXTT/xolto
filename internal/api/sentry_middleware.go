package api

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/TechXTT/xolto/internal/observability"
	"github.com/getsentry/sentry-go"
)

// sentryMiddleware is the outermost middleware in the handler chain.
// It performs two jobs:
//
//  1. Panic recovery — catches any panic in an inner handler, captures the
//     event to Sentry with request context, and returns HTTP 500 to the client.
//
//  2. 5xx capture — after the inner handler returns normally, checks the
//     response status via the existing statusCapturingResponseWriter and sends
//     a Sentry event for any 5xx that was not already reported by a panic.
//
// Interface-preservation note: the middleware must NOT introduce another
// statusCapturingResponseWriter because requestLoggingMiddleware (which is
// inner) already wraps the writer. sentryMiddleware sits OUTSIDE the full
// chain, so it receives the raw http.ResponseWriter from the net/http server.
// It wraps that with its own sentryCaptureWriter (which itself preserves
// http.Flusher and Unwrap) and then lets the rest of the chain run.
func (s *Server) sentryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !observability.SentryEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		sw := &sentryCaptureWriter{ResponseWriter: w}
		panicCaptured := false

		defer func() {
			if rec := recover(); rec != nil {
				panicCaptured = true
				panicMsg := fmt.Sprintf("%v", rec)
				slog.Default().Error("panic recovered",
					"op", "sentry.middleware.panic",
					"panic", panicMsg,
					"method", r.Method,
					"path", r.URL.Path,
				)
				captureHTTPEvent(r, http.StatusInternalServerError, panicMsg)
				if !sw.written {
					http.Error(w, "internal server error", http.StatusInternalServerError)
				}
			}
		}()

		next.ServeHTTP(sw, r)

		if !panicCaptured && sw.statusCode >= 500 {
			captureHTTPEvent(r, sw.statusCode, "")
		}
	})
}

// captureHTTPEvent sends a Sentry event tagged with HTTP context.
// It pulls request_id from the standard header set by requestIDMiddleware.
func captureHTTPEvent(r *http.Request, status int, panicValue string) {
	hub := sentry.CurrentHub().Clone()
	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("method", r.Method)
		scope.SetTag("path", r.URL.Path)
		scope.SetTag("status", fmt.Sprintf("%d", status))

		requestID := requestIDFromRequest(r)
		if requestID != "" {
			scope.SetTag("request_id", requestID)
		}

		scope.SetRequest(r)
	})

	if panicValue != "" {
		hub.CaptureMessage(fmt.Sprintf("panic: %s", panicValue))
	} else {
		hub.CaptureMessage(fmt.Sprintf("HTTP %d: %s %s", status, r.Method, r.URL.Path))
	}
}

// sentryCaptureWriter wraps http.ResponseWriter and records the status code
// written by inner handlers. It preserves http.Flusher and Unwrap() so that
// SSE handlers continue to work through this outer wrapper.
type sentryCaptureWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *sentryCaptureWriter) WriteHeader(code int) {
	w.statusCode = code
	w.written = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *sentryCaptureWriter) Write(p []byte) (int, error) {
	if !w.written {
		w.statusCode = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(p)
}

// Flush delegates to the underlying ResponseWriter when it implements http.Flusher.
// This preserves SSE streaming through the outermost middleware layer.
func (w *sentryCaptureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for http.ResponseController (Go 1.20+).
func (w *sentryCaptureWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
