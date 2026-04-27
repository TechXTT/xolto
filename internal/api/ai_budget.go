package api

// W19-23 Phase 1: owner-role admin endpoints for the global AI-spend cap.
//
// Routes:
//   POST /admin/ai-budget/override   — change the in-memory cap, audit-log
//                                      the change, return the new value.
//   GET  /admin/ai-budget/snapshot   — read the current rolling-24h spend,
//                                      cap, percentage, and recent overrides
//                                      for the admin tile to render.
//
// Auth: requireOperatorOrOwner (matches /internal/calibration/summary).
//
// Decision Log 2026-04-27 binds the cap defaults and hard ceiling. The
// endpoint validates new_cap_usd in (0, 100] — exceeding 100 requires a
// founder code change to constants in internal/aibudget/budget.go. The
// override does NOT survive process restart (cap is in-memory); the audit
// row in ai_budget_overrides records who changed it and why.

import (
	"net/http"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
)

func (s *Server) registerAIBudgetRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/ai-budget/override", s.requireOperatorOrOwner(s.handleAIBudgetOverride))
	mux.HandleFunc("/admin/ai-budget/snapshot", s.requireOperatorOrOwner(s.handleAIBudgetSnapshot))
}

// handleAIBudgetOverride accepts a POST { new_cap_usd, reason } and
// updates the in-memory tracker plus the ai_budget_overrides audit log.
func (s *Server) handleAIBudgetOverride(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var body struct {
		NewCapUSD float64 `json:"new_cap_usd" validate:"required"`
		Reason    string  `json:"reason"`
	}
	if err := Decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Validate range. 0 < new_cap_usd <= HardCeilingUSD.
	if body.NewCapUSD <= 0 {
		writeError(w, http.StatusBadRequest, "new_cap_usd must be > 0")
		return
	}
	if body.NewCapUSD > aibudget.HardCeilingUSD {
		writeError(w, http.StatusBadRequest, "new_cap_usd exceeds the 100x hard ceiling; founder code change required")
		return
	}
	reason := strings.TrimSpace(body.Reason)

	tracker := aibudget.Global()
	if tracker == nil {
		writeError(w, http.StatusServiceUnavailable, "ai budget tracker is not configured")
		return
	}
	if !tracker.SetCapUSD(body.NewCapUSD) {
		writeError(w, http.StatusBadRequest, "invalid new_cap_usd")
		return
	}

	// Audit-log the change. Failure here does NOT roll back the cap update
	// (the cap is in-memory and the operator already saw the update take
	// effect); we surface the persistence error to the caller so they
	// know to retry the audit insert if it failed.
	auditErr := error(nil)
	if s.db != nil {
		_, auditErr = s.db.RecordAIBudgetOverride(r.Context(), store.AIBudgetOverride{
			NewCapUSD:   body.NewCapUSD,
			Reason:      reason,
			SetByUserID: user.ID,
		})
	}

	resp := map[string]any{
		"ok":      true,
		"cap_usd": body.NewCapUSD,
		"set_at":  time.Now().UTC().Format(time.RFC3339),
	}
	if auditErr != nil {
		resp["audit_error"] = auditErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleAIBudgetSnapshot returns the current budget state plus the last
// few audit-log rows for the admin tile.
func (s *Server) handleAIBudgetSnapshot(w http.ResponseWriter, r *http.Request, _ *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	tracker := aibudget.Global()
	if tracker == nil {
		writeError(w, http.StatusServiceUnavailable, "ai budget tracker is not configured")
		return
	}
	snap := tracker.Snapshot()

	// Recent overrides — best-effort; on store error we still return the
	// snapshot so the admin tile renders the live cap value.
	var recent []store.AIBudgetOverride
	if s.db != nil {
		recent, _ = s.db.ListRecentAIBudgetOverrides(r.Context(), 5)
	}

	overrides := make([]map[string]any, 0, len(recent))
	for _, o := range recent {
		overrides = append(overrides, map[string]any{
			"set_at":         o.SetAt.UTC().Format(time.RFC3339),
			"new_cap_usd":    o.NewCapUSD,
			"reason":         o.Reason,
			"set_by_user_id": o.SetByUserID,
		})
	}

	// Render warning_tiers_fired with stable string keys and ISO times.
	tiers := map[string]any{}
	for k, v := range snap.WarningTiersFired {
		if v == nil {
			tiers[k] = nil
		} else {
			tiers[k] = v.UTC().Format(time.RFC3339)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"rolling_24h_spend_usd": snap.Rolling24hSpendUSD,
		"cap_usd":               snap.CapUSD,
		"percentage":            snap.Percentage,
		"oldest_entry_at":       snap.OldestEntryAt.UTC().Format(time.RFC3339),
		"warning_tiers_fired":   tiers,
		"recent_overrides":      overrides,
	})
}
