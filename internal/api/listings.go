package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/models"
)

func (s *Server) registerListingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/listings/feed", s.requireAuth(s.handleFeed))
	mux.HandleFunc("/matches/feedback", s.requireAuth(s.handleMatchFeedback))
	mux.HandleFunc("/matches/analyze", s.requireAuth(s.handleAnalyzeListing))
	mux.HandleFunc("/shortlist", s.requireAuth(s.handleShortlist))
	mux.HandleFunc("/shortlist/", s.requireAuth(s.handleShortlistItem))
	mux.HandleFunc("/assistant/converse", s.requireAuth(s.handleConverse))
	mux.HandleFunc("/assistant/session", s.requireAuth(s.handleAssistantSession))
	mux.HandleFunc("/actions", s.requireAuth(s.handleActions))
	mux.HandleFunc("/events", s.handleEvents)
}

func (s *Server) handleConverse(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	limits := billing.LimitsFor(user.Tier)
	if !limits.AIEnabled {
		writeError(w, http.StatusPaymentRequired, "assistant reasoning is not available on the current tier")
		return
	}
	if strings.TrimSpace(user.CountryCode) == "" {
		writeError(w, http.StatusBadRequest, "complete location setup before creating missions")
		return
	}
	var req struct {
		Message string `json:"message" validate:"required,min=1,max=3000"`
	}
	if err := Decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reply, err := s.assistant.Converse(r.Context(), user.ID, req.Message)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *Server) handleAssistantSession(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	session, err := s.db.GetAssistantSession(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	missionID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("mission_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			missionID = parsed
		}
	}
	listings, err := s.db.ListRecentListings(user.ID, 50, missionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": listings, "user_id": user.ID})
}

// handleMatchFeedback records user feedback (approve/dismiss/clear) on a
// listing. Approved listings become high-weight comparables for the mission's
// future scoring; dismissed listings are filtered out of reads and never
// resurface in matches or comparables.
func (s *Server) handleMatchFeedback(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var body struct {
		ItemID string `json:"item_id" validate:"required"`
		Action string `json:"action" validate:"omitempty,oneof=approve approved dismiss dismissed clear"`
	}
	if err := Decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	itemID := strings.TrimSpace(body.ItemID)
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "item_id is required")
		return
	}
	var feedback string
	switch strings.ToLower(strings.TrimSpace(body.Action)) {
	case "approve", "approved":
		feedback = "approved"
	case "dismiss", "dismissed":
		feedback = "dismissed"
	case "clear", "":
		feedback = ""
	default:
		writeError(w, http.StatusBadRequest, "action must be approve, dismiss, or clear")
		return
	}
	if err := s.db.SetListingFeedback(user.ID, itemID, feedback); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "feedback": feedback})
}

// handleAnalyzeListing accepts a marketplace URL, fetches the listing
// metadata, runs it through the scorer (which in turn invokes the AI
// reasoner) and returns the verdict. An optional mission_id anchors the
// analysis — when set, the scorer uses that mission's approved comparables
// and search context so the verdict reflects the user's actual buying goal.
func (s *Server) handleAnalyzeListing(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.scorer == nil {
		writeError(w, http.StatusServiceUnavailable, "scorer is not configured")
		return
	}

	var body struct {
		URL       string `json:"url" validate:"required,url"`
		MissionID int64  `json:"mission_id"`
	}
	if err := Decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rawURL := strings.TrimSpace(body.URL)
	if rawURL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	// Fetching can hit a slow third party — cap at 25s so we don't hold the
	// request connection open indefinitely on a misbehaving origin.
	fetchCtx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	listing, err := s.fetcher.Fetch(fetchCtx, rawURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to load listing: "+err.Error())
		return
	}

	// Build the SearchSpec the scorer expects. When the user supplies a
	// mission we anchor the analysis to that mission's goal (query, budget,
	// ProfileID so approved comparables flow through). Otherwise we fall back
	// to a minimal spec built from the listing title so the AI at least gets
	// coherent relevance context.
	spec := models.SearchSpec{
		UserID:          user.ID,
		MarketplaceID:   listing.MarketplaceID,
		Query:           listing.Title,
		OfferPercentage: 72,
	}
	if body.MissionID > 0 {
		mission, err := s.db.GetMission(body.MissionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if mission == nil || mission.UserID != user.ID {
			writeError(w, http.StatusNotFound, "mission not found")
			return
		}
		spec.ProfileID = mission.ID
		spec.Name = mission.Name
		spec.CountryCode = mission.CountryCode
		spec.City = mission.City
		spec.PostalCode = mission.PostalCode
		spec.RadiusKm = mission.TravelRadius
		spec.CategoryID = mission.CategoryID
		spec.Condition = mission.PreferredCondition
		if mission.BudgetMax > 0 {
			spec.MaxPrice = mission.BudgetMax * 100
		}
		if q := strings.TrimSpace(mission.TargetQuery); q != "" {
			spec.Query = q
		}
		listing.ProfileID = mission.ID
	}

	scored := s.scorer.Score(fetchCtx, listing, spec)

	// Fold the scorer's verdict into the Listing struct so the frontend can
	// render it through the same ListingCard it uses everywhere else. We also
	// expose the search advice + comparables as sibling fields for the
	// analyze panel's "why" section.
	enriched := scored.Listing
	enriched.Score = scored.Score
	enriched.FairPrice = scored.FairPrice
	enriched.OfferPrice = scored.OfferPrice
	enriched.Confidence = scored.Confidence
	enriched.Reason = scored.Reason
	enriched.RiskFlags = scored.RiskFlags

	writeJSON(w, http.StatusOK, map[string]any{
		"listing":          enriched,
		"reasoning_source": scored.ReasoningSource,
		"search_advice":    scored.SearchAdvice,
		"comparables":      scored.ComparableDeals,
		"market_average":   scored.MarketAverage,
	})
}

func (s *Server) handleShortlist(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	items, err := s.db.GetShortlist(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shortlist": items})
}

func (s *Server) handleShortlistItem(w http.ResponseWriter, r *http.Request, user *models.User) {
	rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/shortlist/"), "/")

	// Handle /shortlist/{itemID}/draft as a POST sub-resource.
	if strings.HasSuffix(rawPath, "/draft") && strings.Count(rawPath, "/") == 1 {
		itemID := strings.TrimSuffix(rawPath, "/draft")
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		draft, err := s.assistant.DraftSellerMessage(r.Context(), user.ID, itemID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, draft)
		return
	}

	itemID := rawPath
	if itemID == "" {
		writeError(w, http.StatusBadRequest, "missing shortlist item id")
		return
	}
	switch r.Method {
	case http.MethodPost:
		limits := billing.LimitsFor(user.Tier)
		if limits.MaxShortlistEntries > 0 {
			items, err := s.db.GetShortlist(user.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			exists := false
			for _, item := range items {
				if item.ItemID == itemID && item.Status != "removed" {
					exists = true
					break
				}
			}
			if !exists && len(items) >= limits.MaxShortlistEntries {
				writeError(w, http.StatusPaymentRequired, "shortlist limit reached for current tier")
				return
			}
		}
		entry, err := s.assistant.SaveToShortlist(r.Context(), user.ID, itemID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entry)
	case http.MethodDelete:
		item, err := s.db.GetShortlistEntry(user.ID, itemID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if item != nil {
			item.Status = "removed"
			if err := s.db.SaveShortlistEntry(*item); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeMethodNotAllowed(w, http.MethodPost, http.MethodDelete)
	}
}

func (s *Server) handleActions(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	actions, err := s.db.ListActionDrafts(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"actions": actions})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	s.broker.ServeHTTP(w, r, user.ID)
}
