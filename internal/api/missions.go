package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/generator"
	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
)

func (s *Server) registerMissionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/searches", s.requireAuth(s.handleSearches))
	mux.HandleFunc("/searches/run", s.requireAuth(s.handleRunAllSearches))
	mux.HandleFunc("/searches/generate", s.requireAuth(s.handleGenerateSearches))
	mux.HandleFunc("/searches/", s.requireAuth(s.handleSearchByID))
	mux.HandleFunc("/missions", s.requireAuth(s.handleMissions))
	mux.HandleFunc("/missions/", s.requireAuth(s.handleMissionByID))
}

func (s *Server) handleSearches(w http.ResponseWriter, r *http.Request, user *models.User) {
	switch r.Method {
	case http.MethodGet:
		specs, err := s.db.GetSearchConfigs(user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"searches": specs})
	case http.MethodPost:
		var spec models.SearchSpec
		if err := Decode(r, &spec); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec.UserID = user.ID
		if spec.MarketplaceID == "" {
			defaults := marketplace.CountryDefaultMarketplaces(user.CountryCode)
			if len(defaults) > 0 {
				spec.MarketplaceID = defaults[0]
			} else {
				spec.MarketplaceID = "marktplaats"
			}
		}
		spec.MarketplaceID = marketplace.NormalizeMarketplaceID(spec.MarketplaceID)
		if spec.OfferPercentage == 0 {
			spec.OfferPercentage = 70
		}
		if spec.CheckInterval == 0 {
			spec.CheckInterval = 5 * time.Minute
		}
		spec.Enabled = true
		if spec.ProfileID > 0 {
			mission, err := s.db.GetMission(spec.ProfileID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if mission == nil || mission.UserID != user.ID {
				writeError(w, http.StatusBadRequest, "invalid mission for search")
				return
			}
		}
		limits := billing.LimitsFor(user.Tier)
		minInterval := time.Duration(limits.MinCheckIntervalMins) * time.Minute
		if spec.CheckInterval < minInterval {
			spec.CheckInterval = minInterval
		}
		if limits.MaxMarketplaces > 0 && spec.MarketplaceID != "" && !s.marketplaceAllowedForTier(spec.MarketplaceID, limits) {
			writeError(w, http.StatusPaymentRequired, "marketplace not available for current tier")
			return
		}
		count, err := s.db.CountSearchConfigs(user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if limits.MaxSearches > 0 && count >= limits.MaxSearches {
			writeError(w, http.StatusPaymentRequired, "search limit reached for current tier")
			return
		}
		id, err := s.db.CreateSearchConfig(spec)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		spec.ID = id
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "search_created", "search": spec}))
		writeJSON(w, http.StatusCreated, spec)
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleGenerateSearches(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	limits := billing.LimitsFor(user.Tier)
	if !limits.AIEnabled {
		writeError(w, http.StatusPaymentRequired, "ai search generation is not available on the current tier")
		return
	}
	var req struct {
		Topic string `json:"topic" validate:"required,min=2,max=200"`
	}
	if err := Decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	aiCfg := config.AIConfig{
		Enabled:     s.cfg.AIAPIKey != "",
		BaseURL:     s.cfg.AIBaseURL,
		APIKey:      s.cfg.AIAPIKey,
		Model:       s.cfg.AIModel,
		Temperature: 0.2,
	}
	gen := generator.New(aiCfg)
	gen.SetUsageCallback(s.makeUsageCallback(user.ID, 0))
	searches, err := gen.GenerateSearches(r.Context(), req.Topic)
	if err != nil && len(searches) == 0 {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"searches": searches,
		"warning":  errorString(err),
	})
}

func (s *Server) handleRunAllSearches(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.runner == nil {
		writeError(w, http.StatusServiceUnavailable, "background runner unavailable")
		return
	}
	if err := s.runner.RunUserNow(r.Context(), user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "active searches triggered"})
}

func (s *Server) handleSearchByID(w http.ResponseWriter, r *http.Request, user *models.User) {
	targetPath := r.URL.Path
	if strings.HasSuffix(targetPath, "/run") {
		targetPath = strings.TrimSuffix(targetPath, "/run")
	}
	id, err := parseIDFromPath(targetPath, "/searches/")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid search id")
		return
	}
	switch r.Method {
	case http.MethodPut:
		var spec models.SearchSpec
		if err := Decode(r, &spec); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		spec.ID = id
		spec.UserID = user.ID
		spec.MarketplaceID = marketplace.NormalizeMarketplaceID(spec.MarketplaceID)
		if spec.CheckInterval == 0 {
			spec.CheckInterval = 5 * time.Minute
		}
		minInterval := time.Duration(billing.LimitsFor(user.Tier).MinCheckIntervalMins) * time.Minute
		if spec.CheckInterval < minInterval {
			spec.CheckInterval = minInterval
		}
		if spec.ProfileID > 0 {
			mission, err := s.db.GetMission(spec.ProfileID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if mission == nil || mission.UserID != user.ID {
				writeError(w, http.StatusBadRequest, "invalid mission for search")
				return
			}
		}
		if err := s.db.UpdateSearchConfig(spec); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "search_updated", "search": spec}))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := s.db.DeleteSearchConfig(id, user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "search_deleted", "search_id": id}))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodPost:
		if !strings.HasSuffix(r.URL.Path, "/run") {
			writeMethodNotAllowed(w, http.MethodPut, http.MethodDelete, http.MethodPost)
			return
		}
		if s.runner == nil {
			writeError(w, http.StatusServiceUnavailable, "background runner unavailable")
			return
		}
		if err := s.runner.RunUserNow(r.Context(), user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "search run triggered"})
	default:
		writeMethodNotAllowed(w, http.MethodPut, http.MethodDelete, http.MethodPost)
	}
}

func (s *Server) handleMissions(w http.ResponseWriter, r *http.Request, user *models.User) {
	switch r.Method {
	case http.MethodGet:
		missions, err := s.db.ListMissions(user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"missions": missions})
	case http.MethodPost:
		var mission models.Mission
		if err := Decode(r, &mission); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mission.UserID = user.ID
		mission.ID = 0
		normalized, err := s.normalizeMissionForWrite(user, mission, nil)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mission = normalized

		limits := billing.LimitsFor(user.Tier)
		if limits.MaxMissions > 0 {
			count, err := s.db.CountActiveMissions(user.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if count >= limits.MaxMissions {
				writeError(w, http.StatusPaymentRequired, "mission limit reached for current tier")
				return
			}
		}

		id, err := s.db.UpsertMission(mission)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		mission.ID = id
		if mission.Status == "active" && s.assistant != nil {
			_, _ = s.assistant.AutoDeployHunts(r.Context(), user.ID, mission)
		}
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "mission_created", "mission": mission}))
		writeJSON(w, http.StatusCreated, mission)
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleMissionByID(w http.ResponseWriter, r *http.Request, user *models.User) {
	rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/missions/"), "/")
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "invalid mission path")
		return
	}

	// PUT /missions/{id}/status
	if strings.HasSuffix(rawPath, "/status") {
		if r.Method != http.MethodPut {
			writeMethodNotAllowed(w, http.MethodPut)
			return
		}
		idPart := strings.TrimSuffix(rawPath, "/status")
		idPart = strings.Trim(idPart, "/")
		id, err := strconv.ParseInt(idPart, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid mission id")
			return
		}
		mission, err := s.db.GetMission(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if mission == nil || mission.UserID != user.ID {
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
		if err := s.db.UpdateMissionStatus(id, req.Status); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		updated, err := s.db.GetMission(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "mission_status_updated", "mission": updated}))
		writeJSON(w, http.StatusOK, updated)
		return
	}

	// GET /missions/{id}/matches
	if strings.HasSuffix(rawPath, "/matches") {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		idPart := strings.TrimSuffix(rawPath, "/matches")
		idPart = strings.Trim(idPart, "/")
		id, err := strconv.ParseInt(idPart, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid mission id")
			return
		}
		mission, err := s.db.GetMission(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if mission == nil || mission.UserID != user.ID {
			writeError(w, http.StatusNotFound, "mission not found")
			return
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
				limit = parsed
			}
		}
		listings, err := s.db.ListRecentListings(user.ID, limit, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"mission":  mission,
			"listings": listings,
		})
		return
	}

	// GET/PUT /missions/{id}
	id, err := strconv.ParseInt(rawPath, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid mission id")
		return
	}
	existing, err := s.db.GetMission(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil || existing.UserID != user.ID {
		writeError(w, http.StatusNotFound, "mission not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, existing)
	case http.MethodPut:
		var mission models.Mission
		if err := Decode(r, &mission); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mission.ID = id
		mission.UserID = user.ID
		normalized, err := s.normalizeMissionForWrite(user, mission, existing)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mission = normalized
		if _, err := s.db.UpsertMission(mission); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated, err := s.db.GetMission(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if updated != nil && updated.Status == "active" && s.assistant != nil {
			_, _ = s.assistant.AutoDeployHunts(r.Context(), user.ID, *updated)
		}
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "mission_updated", "mission": updated}))
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := s.db.DeleteMission(id, user.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.broker.Publish(user.ID, mustJSON(map[string]any{"type": "mission_deleted", "mission_id": id}))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPut, http.MethodDelete)
	}
}
