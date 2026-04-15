package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/models"
)

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/stats", s.requireOperatorOrOwner(s.handleAdminStats))
	mux.HandleFunc("/admin/users", s.requireOperatorOrOwner(s.handleAdminUsers))
	mux.HandleFunc("/admin/users/", s.requireOperatorOrOwner(s.handleAdminUserMutation))
	mux.HandleFunc("/admin/missions/", s.requireOperatorOrOwner(s.handleAdminMissionMutation))
	mux.HandleFunc("/admin/searches/", s.requireOperatorOrOwner(s.handleAdminSearchMutation))
	mux.HandleFunc("/admin/usage", s.requireOperatorOrOwner(s.handleAdminUsageTimeline))
	mux.HandleFunc("/admin/search-runs", s.requireOperatorOrOwner(s.handleAdminSearchRuns))
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	daysStr := r.URL.Query().Get("days")
	days := 30
	if v, err := strconv.Atoi(daysStr); err == nil && v > 0 && v <= 365 {
		days = v
	}
	stats, err := s.db.GetAIUsageStats(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	searchStats, err := s.db.GetSearchOpsStats(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Estimate cost: gpt-4o-mini input $0.15/M, output $0.60/M tokens.
	stats.EstimatedCostUSD = float64(stats.TotalPrompt)*0.15/1_000_000 + float64(stats.TotalCompletion)*0.60/1_000_000
	writeAdminOK(w, http.StatusOK, map[string]any{
		"stats":        stats,
		"search_stats": searchStats,
		"days":         days,
	})
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	users, err := s.db.ListAllUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Sanitize — don't send password hashes to the frontend.
	type safeUser struct {
		ID           string `json:"id"`
		Email        string `json:"email"`
		Name         string `json:"name"`
		Tier         string `json:"tier"`
		Role         string `json:"role"`
		IsAdmin      bool   `json:"is_admin"`
		CreatedAt    string `json:"created_at"`
		MissionCount int    `json:"mission_count"`
		SearchCount  int    `json:"search_count"`
		AICallCount  int    `json:"ai_call_count"`
		AITokens     int    `json:"ai_tokens"`
	}
	safe := make([]safeUser, len(users))
	for i, u := range users {
		safe[i] = safeUser{
			ID:           u.ID,
			Email:        u.Email,
			Name:         u.Name,
			Tier:         billing.NormalizeTier(u.Tier),
			Role:         models.EffectiveUserRole(u.User),
			IsAdmin:      models.HasOperatorAccess(u.User),
			CreatedAt:    u.CreatedAt.Format("2006-01-02T15:04:05Z"),
			MissionCount: u.MissionCount,
			SearchCount:  u.SearchCount,
			AICallCount:  u.AICallCount,
			AITokens:     u.AITokens,
		}
	}
	writeAdminOK(w, http.StatusOK, map[string]any{"users": safe})
}

func (s *Server) handleAdminUsageTimeline(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	daysStr := r.URL.Query().Get("days")
	days := 7
	if v, err := strconv.Atoi(daysStr); err == nil && v > 0 && v <= 90 {
		days = v
	}
	entries, err := s.db.GetAIUsageTimeline(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"entries": entries,
		"days":    days,
	})
}

func (s *Server) handleAdminSearchRuns(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	days := 7
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	filter := models.AdminSearchRunFilter{
		Days:          days,
		Status:        strings.TrimSpace(r.URL.Query().Get("status")),
		MarketplaceID: strings.TrimSpace(r.URL.Query().Get("marketplace")),
		CountryCode:   strings.TrimSpace(r.URL.Query().Get("country")),
		UserID:        strings.TrimSpace(r.URL.Query().Get("user")),
		Limit:         limit,
	}
	entries, err := s.db.ListSearchRuns(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"entries": entries,
		"days":    days,
		"limit":   limit,
		"filters": map[string]any{
			"status":      filter.Status,
			"marketplace": filter.MarketplaceID,
			"country":     strings.ToUpper(filter.CountryCode),
			"user":        filter.UserID,
		},
	})
}

func (s *Server) handleAdminUserMutation(w http.ResponseWriter, r *http.Request, actor *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/users/"), "/")
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "invalid admin user path")
		return
	}

	switch {
	case strings.HasSuffix(rawPath, "/tier"):
		userID := strings.Trim(strings.TrimSuffix(rawPath, "/tier"), "/")
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		target, err := s.db.GetUserByID(userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if target == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		var req struct {
			Tier string `json:"tier" validate:"required,oneof=free pro power"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		tier := billing.NormalizeTier(req.Tier)
		switch tier {
		case "free", "pro", "power":
		default:
			writeError(w, http.StatusBadRequest, "unsupported tier")
			return
		}
		before := map[string]any{
			"tier": billing.NormalizeTier(target.Tier),
		}
		if err := s.db.UpdateUserTier(userID, tier); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
			ActorUserID: actor.ID,
			ActorRole:   models.EffectiveUserRole(*actor),
			RequestID:   requestIDFromRequest(r),
			Action:      "user_tier_updated",
			TargetType:  "user",
			TargetID:    userID,
			BeforeJSON:  mustJSON(before),
			AfterJSON:   mustJSON(map[string]any{"tier": tier}),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminOK(w, http.StatusOK, map[string]any{
			"user_id": userID,
			"tier":    tier,
		})
		return
	case strings.HasSuffix(rawPath, "/role"):
		if !models.HasOwnerAccess(*actor) {
			writeError(w, http.StatusForbidden, "owner access required")
			return
		}
		userID := strings.Trim(strings.TrimSuffix(rawPath, "/role"), "/")
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		target, err := s.db.GetUserByID(userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if target == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		var req struct {
			Role string `json:"role" validate:"required,oneof=user admin operator owner"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		role := models.NormalizeUserRole(req.Role)
		if role == "" {
			writeError(w, http.StatusBadRequest, "unsupported role")
			return
		}
		nextIsAdmin := models.IsTeamRole(role)
		before := map[string]any{
			"role":     models.EffectiveUserRole(*target),
			"is_admin": target.IsAdmin,
		}
		if err := s.db.UpdateUserRole(userID, role); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.db.SetUserAdmin(userID, nextIsAdmin); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
			ActorUserID: actor.ID,
			ActorRole:   models.EffectiveUserRole(*actor),
			RequestID:   requestIDFromRequest(r),
			Action:      "user_role_updated",
			TargetType:  "user",
			TargetID:    userID,
			BeforeJSON:  mustJSON(before),
			AfterJSON:   mustJSON(map[string]any{"role": role, "is_admin": nextIsAdmin}),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminOK(w, http.StatusOK, map[string]any{
			"user_id":  userID,
			"role":     role,
			"is_admin": nextIsAdmin,
		})
		return
	case strings.HasSuffix(rawPath, "/admin"):
		userID := strings.Trim(strings.TrimSuffix(rawPath, "/admin"), "/")
		if userID == "" {
			writeError(w, http.StatusBadRequest, "invalid user id")
			return
		}
		target, err := s.db.GetUserByID(userID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if target == nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		var req struct {
			IsAdmin *bool `json:"is_admin" validate:"required"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		before := map[string]any{
			"is_admin": target.IsAdmin,
			"role":     models.EffectiveUserRole(*target),
		}
		if err := s.db.SetUserAdmin(userID, *req.IsAdmin); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		nextRole := models.NormalizeUserRole(target.Role)
		if *req.IsAdmin && !models.IsTeamRole(nextRole) {
			nextRole = string(models.UserRoleAdmin)
			if err := s.db.UpdateUserRole(userID, nextRole); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if !*req.IsAdmin && nextRole == string(models.UserRoleAdmin) {
			nextRole = string(models.UserRoleUser)
			if err := s.db.UpdateUserRole(userID, nextRole); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
			ActorUserID: actor.ID,
			ActorRole:   models.EffectiveUserRole(*actor),
			RequestID:   requestIDFromRequest(r),
			Action:      "user_admin_updated",
			TargetType:  "user",
			TargetID:    userID,
			BeforeJSON:  mustJSON(before),
			AfterJSON:   mustJSON(map[string]any{"is_admin": *req.IsAdmin, "role": nextRole}),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminOK(w, http.StatusOK, map[string]any{
			"user_id":  userID,
			"is_admin": *req.IsAdmin,
			"role":     nextRole,
		})
		return
	default:
		writeError(w, http.StatusNotFound, "unknown admin user action")
		return
	}
}

func (s *Server) handleAdminMissionMutation(w http.ResponseWriter, r *http.Request, actor *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/missions/"), "/")
	if rawPath == "" || !strings.HasSuffix(rawPath, "/status") {
		writeError(w, http.StatusNotFound, "unknown admin mission action")
		return
	}
	idPart := strings.Trim(strings.TrimSuffix(rawPath, "/status"), "/")
	missionID, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mission id")
		return
	}

	mission, err := s.db.GetMission(missionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mission == nil {
		writeError(w, http.StatusNotFound, "mission not found")
		return
	}

	var req struct {
		Status string `json:"status" validate:"required,oneof=active paused completed"`
	}
	if err := Decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	before := map[string]any{
		"status": mission.Status,
		"active": mission.Active,
	}
	if err := s.db.UpdateMissionStatus(missionID, req.Status); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.db.GetMission(missionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated == nil {
		writeError(w, http.StatusNotFound, "mission not found")
		return
	}
	if err := s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
		ActorUserID: actor.ID,
		ActorRole:   models.EffectiveUserRole(*actor),
		RequestID:   requestIDFromRequest(r),
		Action:      "mission_status_updated",
		TargetType:  "mission",
		TargetID:    strconv.FormatInt(missionID, 10),
		BeforeJSON:  mustJSON(before),
		AfterJSON: mustJSON(map[string]any{
			"status": updated.Status,
			"active": updated.Active,
		}),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"mission": updated,
	})
}

func (s *Server) handleAdminSearchMutation(w http.ResponseWriter, r *http.Request, actor *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/searches/"), "/")
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "invalid admin search path")
		return
	}

	switch {
	case strings.HasSuffix(rawPath, "/enabled"):
		idPart := strings.Trim(strings.TrimSuffix(rawPath, "/enabled"), "/")
		searchID, err := strconv.ParseInt(idPart, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid search id")
			return
		}
		spec, err := s.db.GetSearchConfigByID(searchID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if spec == nil {
			writeError(w, http.StatusNotFound, "search not found")
			return
		}
		var req struct {
			Enabled *bool `json:"enabled" validate:"required"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		before := map[string]any{"enabled": spec.Enabled}
		if err := s.db.SetSearchEnabled(searchID, *req.Enabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated, err := s.db.GetSearchConfigByID(searchID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if updated == nil {
			writeError(w, http.StatusNotFound, "search not found")
			return
		}
		if err := s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
			ActorUserID: actor.ID,
			ActorRole:   models.EffectiveUserRole(*actor),
			RequestID:   requestIDFromRequest(r),
			Action:      "search_enabled_updated",
			TargetType:  "search",
			TargetID:    strconv.FormatInt(searchID, 10),
			BeforeJSON:  mustJSON(before),
			AfterJSON:   mustJSON(map[string]any{"enabled": *req.Enabled}),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminOK(w, http.StatusOK, map[string]any{
			"search_id": searchID,
			"enabled":   *req.Enabled,
			"search":    updated,
		})
		return
	case strings.HasSuffix(rawPath, "/run"):
		idPart := strings.Trim(strings.TrimSuffix(rawPath, "/run"), "/")
		searchID, err := strconv.ParseInt(idPart, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid search id")
			return
		}
		spec, err := s.db.GetSearchConfigByID(searchID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if spec == nil {
			writeError(w, http.StatusNotFound, "search not found")
			return
		}
		if !spec.Enabled {
			writeError(w, http.StatusBadRequest, "search is disabled")
			return
		}
		before := map[string]any{"next_run_at": spec.NextRunAt}
		now := time.Now().UTC()
		if err := s.db.SetSearchNextRunAt(searchID, now); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if s.runner != nil {
			if err := s.runner.RunUserNow(r.Context(), spec.UserID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		updated, err := s.db.GetSearchConfigByID(searchID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if updated == nil {
			writeError(w, http.StatusNotFound, "search not found")
			return
		}
		if err := s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
			ActorUserID: actor.ID,
			ActorRole:   models.EffectiveUserRole(*actor),
			RequestID:   requestIDFromRequest(r),
			Action:      "search_run_triggered",
			TargetType:  "search",
			TargetID:    strconv.FormatInt(searchID, 10),
			BeforeJSON:  mustJSON(before),
			AfterJSON: mustJSON(map[string]any{
				"next_run_at": updated.NextRunAt,
			}),
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeAdminOK(w, http.StatusOK, map[string]any{
			"search_id": searchID,
			"user_id":   spec.UserID,
			"message":   "search run triggered",
			"search":    updated,
		})
		return
	default:
		writeError(w, http.StatusNotFound, "unknown admin search action")
		return
	}
}
