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

// matchesSortValues is the set of allowed values for the sort parameter.
// "newest" is the default (last_seen DESC, item_id ASC) and matches the
// Phase 1 ordering exactly, so Phase 2 dash clients get byte-for-byte the
// same result when they omit the sort param.
var matchesSortValues = map[string]bool{
	"newest":     true,
	"score":      true,
	"price_asc":  true,
	"price_desc": true,
}

// matchesMarketValues is the set of allowed market param values (dash vocabulary).
// "all" means no market filter. The dash uses "olx_bg"; backend stores "olxbg";
// "vinted" is a legacy alias for "vinted_nl" — both are normalised at parse time.
var matchesMarketValues = map[string]bool{
	"all":        true,
	"marktplaats": true,
	"vinted":     true, // legacy alias → normalised to "vinted_nl"
	"vinted_nl":  true,
	"vinted_dk":  true,
	"olx_bg":     true, // dash vocabulary → normalised to "olxbg"
	"olxbg":      true, // canonical backend ID; also accepted directly
}

// normalizeMarket maps the dash-facing market vocabulary to the backend's
// canonical stored marketplace_id values. Returns "" for "all".
func normalizeMarket(v string) string {
	switch v {
	case "all", "":
		return ""
	case "vinted":
		return "vinted_nl"
	case "olx_bg":
		return "olxbg"
	default:
		return v // marktplaats, vinted_nl, vinted_dk, olxbg — pass through
	}
}

// matchesConditionValues is the set of allowed condition param values.
// These mirror the canonical condition strings stored by all marketplace
// mappers (marktplaats, vinted, olxbg). "all" means no condition filter.
var matchesConditionValues = map[string]bool{
	"all":      true,
	"new":      true,
	"like_new": true,
	"good":     true,
	"fair":     true,
}

// normalizeCondition returns the stored condition value, or "" for "all".
func normalizeCondition(v string) string {
	if v == "all" || v == "" {
		return ""
	}
	return v
}

func (s *Server) registerListingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/listings/feed", s.requireAuth(s.handleFeed))
	// /matches must be registered before /matches/feedback and /matches/analyze
	// so that the more-specific sub-paths take priority in the ServeMux.
	mux.HandleFunc("/matches", s.requireAuth(s.handleMatches))
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

// handleMatches serves GET /matches with offset pagination and server-side
// filtering (Phase 3).
//
// Existing query parameters (unchanged from Phase 1):
//   - limit      int,   optional, default = 20, min = 1, max = 100
//   - offset     int,   optional, default = 0, min = 0
//   - mission_id int64, optional, default = 0 (all missions)
//
// New query parameters (Phase 3, all optional, all additive):
//   - sort       string, default "newest"
//                allowed: "score" | "price_asc" | "price_desc" | "newest"
//   - market     string, default "all"
//                allowed: "all" | "marktplaats" | "vinted" | "vinted_nl" |
//                         "vinted_dk" | "olx_bg" | "olxbg"
//                "vinted" is normalised to "vinted_nl".
//                "olx_bg" (dash vocabulary) is normalised to "olxbg" (stored).
//   - condition  string, default "all"
//                allowed: "all" | "new" | "like_new" | "good" | "fair"
//   - min_score  int,    default 0, range 0..10 (0 = no minimum)
//
// Semantic order of operations:
//  1. Apply mission_id filter.
//  2. Apply market / condition / min_score filters.
//  3. Compute total = COUNT after all filters (ignoring limit/offset).
//  4. Apply ORDER BY per sort (with item_id ASC tie-breaker).
//  5. Apply LIMIT + OFFSET for the page.
//
// Default behaviour (no new params supplied) is byte-for-byte identical to
// the Phase 1 response: sort=newest → last_seen DESC, item_id ASC. The
// Phase 2 dash build (commit 95aa25e) calls /matches without new params and
// must not experience any change.
//
// Response shape (unchanged):
//
//	{
//	  "items":  [...Listing],
//	  "limit":  <int>,
//	  "offset": <int>,
//	  "total":  <int>   <- filtered count, independent of limit/offset
//	}
//
// Errors: 400 Bad Request for invalid param values, with the standard
// {"ok": false, "error": "<message>"} envelope.
func (s *Server) handleMatches(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	const (
		defaultLimit = 20
		maxLimit     = 100
	)

	// --- Existing Phase 1 params (unchanged) ---

	limit := defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be an integer")
			return
		}
		if parsed < 1 || parsed > maxLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}

	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "offset must be an integer")
			return
		}
		if parsed < 0 {
			writeError(w, http.StatusBadRequest, "offset must be 0 or greater")
			return
		}
		offset = parsed
	}

	missionID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("mission_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "mission_id must be a non-negative integer")
			return
		}
		missionID = parsed
	}

	// --- New Phase 3 filter params ---

	var filter models.MatchesFilter

	// sort
	if raw := strings.TrimSpace(r.URL.Query().Get("sort")); raw != "" {
		if !matchesSortValues[raw] {
			writeError(w, http.StatusBadRequest, "sort must be one of: newest, score, price_asc, price_desc")
			return
		}
		filter.Sort = raw
	}
	// Default sort "" is treated as "newest" in the store layer.

	// market
	if raw := strings.TrimSpace(r.URL.Query().Get("market")); raw != "" {
		if !matchesMarketValues[raw] {
			writeError(w, http.StatusBadRequest, "market must be one of: all, marktplaats, vinted, vinted_nl, vinted_dk, olx_bg, olxbg")
			return
		}
		filter.Market = normalizeMarket(raw)
	}

	// condition
	if raw := strings.TrimSpace(r.URL.Query().Get("condition")); raw != "" {
		if !matchesConditionValues[raw] {
			writeError(w, http.StatusBadRequest, "condition must be one of: all, new, like_new, good, fair")
			return
		}
		filter.Condition = normalizeCondition(raw)
	}

	// min_score
	if raw := strings.TrimSpace(r.URL.Query().Get("min_score")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "min_score must be an integer")
			return
		}
		if parsed < 0 || parsed > 10 {
			writeError(w, http.StatusBadRequest, "min_score must be between 0 and 10")
			return
		}
		filter.MinScore = parsed
	}

	listings, total, err := s.db.ListRecentListingsPaginated(user.ID, limit, offset, missionID, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Return an empty slice rather than null so clients can always iterate.
	if listings == nil {
		listings = []models.Listing{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  listings,
		"limit":  limit,
		"offset": offset,
		"total":  total,
	})
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
