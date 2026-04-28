package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/TechXTT/xolto/internal/aibudget"
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

// handleHealth answers GET /healthz. Always 200 when the process is alive
// (the deploy-gate signal Railway consumes); the JSON payload reports the
// wiring of load-bearing subsystems so a regression that left a startup-time
// component half-wired is observable from a one-line curl rather than waiting
// for a synthetic-test cycle. Added in W19-24 after the 2026-04-27
// silent-migration incident where ai_budget_overrides went missing in
// production for hours without any deploy-gate signal.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"ok":         true,
		"service":    "xolto-server",
		"ai_budget":  s.aiBudgetWiringStatus(r),
	}
	writeJSON(w, http.StatusOK, resp)
}

// aiBudgetWiringStatus reports whether the W19-23 cap surface is wired end
// to end at request time:
//   - tracker_present: aibudget.Global() returned a non-nil tracker
//     (the global singleton was installed by main).
//   - audit_table_ready: a SELECT against ai_budget_overrides succeeds with
//     no error (the migration / inline CREATE landed; no relation-not-exists).
//   - cap_usd / rolling_24h_spend_usd / percentage: a quick snapshot read so
//     the operator can spot drift without opening the admin dashboard.
//
// All fields are best-effort — a probe failure surfaces as the boolean field
// being false, never as a 5xx, so /healthz remains the deploy-liveness gate.
func (s *Server) aiBudgetWiringStatus(r *http.Request) map[string]any {
	out := map[string]any{
		"tracker_present":   false,
		"audit_table_ready": false,
	}
	tracker := aibudget.Global()
	if tracker != nil {
		out["tracker_present"] = true
		snap := tracker.Snapshot()
		out["cap_usd"] = snap.CapUSD
		out["rolling_24h_spend_usd"] = snap.Rolling24hSpendUSD
		out["percentage"] = snap.Percentage
	}
	if s.db != nil {
		if err := s.db.AIBudgetTableReady(r.Context()); err == nil {
			out["audit_table_ready"] = true
		} else {
			out["audit_table_error"] = err.Error()
		}
	}
	return out
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
