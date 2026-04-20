package api

// VAL-1a: internal calibration summary endpoint.
//
// Route: GET /internal/calibration/summary
// Auth:  operator or owner access required (HasOperatorAccess check)
//
// Query parameters:
//   window      string  lookback window; accepted values: 1d, 7d, 14d, 30d, 90d
//               default: 7d
//   marketplace string  filter by marketplace id (e.g. "olxbg"); default: all
//   category    string  reserved for future use; accepted but not yet filtered
//
// Response (200 OK):
//   {
//     "ok": true,
//     "summary": { ... CalibrationSummary fields ... }
//   }
//
// Errors:
//   401  — not authenticated
//   403  — authenticated but not operator/owner
//   405  — wrong HTTP method
//   500  — DB error

import (
	"net/http"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
)

func (s *Server) registerInternalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/internal/calibration/summary", s.requireOperatorOrOwner(s.handleCalibrationSummary))
}

func (s *Server) handleCalibrationSummary(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	window := parseCalibrationWindow(r.URL.Query().Get("window"))
	marketplace := strings.TrimSpace(r.URL.Query().Get("marketplace"))
	// "all" and "" are both treated as no-marketplace filter.
	if marketplace == "all" {
		marketplace = ""
	}
	// category reserved for future use — accepted, not yet stored on scoring_events.
	// Parsing it here keeps the API contract stable.
	// category := strings.TrimSpace(r.URL.Query().Get("category"))

	summary, err := s.db.GetCalibrationSummary(r.Context(), store.CalibrationQuery{
		Window:      window,
		Marketplace: marketplace,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"summary": summary,
	})
}

// parseCalibrationWindow maps a window query-param string to a time.Duration.
// Accepted values: "1d", "7d", "14d", "30d", "90d".
// Unrecognised or empty values default to 7 days.
func parseCalibrationWindow(s string) time.Duration {
	switch strings.TrimSpace(s) {
	case "1d":
		return 24 * time.Hour
	case "7d", "":
		return 7 * 24 * time.Hour
	case "14d":
		return 14 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	case "90d":
		return 90 * 24 * time.Hour
	default:
		return 7 * 24 * time.Hour
	}
}
