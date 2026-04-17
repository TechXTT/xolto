package api

import (
	"net/http"
	"os"
	"strings"
)

func (s *Server) registerHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealth)
	// Debug panic route — fail-safe gated: only registered when
	// SENTRY_ENVIRONMENT is explicitly a non-production value (e.g.
	// "development", "staging", "local"). Unset or "production" → not
	// registered. Used for manual Sentry live-verification.
	if isDebugEnvExplicit() {
		mux.HandleFunc("/debug/panic", s.handleDebugPanic)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "xolto-server"})
}

// handleDebugPanic deliberately panics when the X-Sentry-Test-Panic: 1 header
// is present. It is registered only when SENTRY_ENVIRONMENT != "production".
func (s *Server) handleDebugPanic(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Sentry-Test-Panic") != "1" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":    false,
			"error": "set X-Sentry-Test-Panic: 1 to trigger a test panic",
		})
		return
	}
	panic("sentry test panic triggered via /debug/panic")
}

// isDebugEnvExplicit returns true only when SENTRY_ENVIRONMENT is explicitly
// set to a known non-production value. Unset, empty, or any unrecognized value
// (including "production") → false. This is a fail-safe default: a missing
// env var in production must NOT expose debug routes.
func isDebugEnvExplicit() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SENTRY_ENVIRONMENT"))) {
	case "development", "staging", "local", "test":
		return true
	default:
		return false
	}
}
