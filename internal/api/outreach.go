package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
)

// validDraftShapes is the exhaustive allowlist for the draft_shape field.
var validDraftShapes = map[string]bool{
	"buy":        true,
	"negotiate":  true,
	"ask_seller": true,
	"generic":    true,
}

// validDraftLangs is the exhaustive allowlist for the draft_lang field.
var validDraftLangs = map[string]bool{
	"bg": true,
	"nl": true,
	"en": true,
}

// validOutreachMarketplaces is the exhaustive allowlist for marketplace_id on
// the outreach endpoints.
var validOutreachMarketplaces = map[string]bool{
	"marktplaats": true,
	"olxbg":       true,
	"vinted_nl":   true,
	"vinted_dk":   true,
}

func (s *Server) registerOutreachRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/outreach/sent", s.requireAuth(s.handleOutreachSent))
	mux.HandleFunc("/outreach/replied", s.requireAuth(s.handleOutreachReplied))
	// /outreach/threads must be registered before /outreach/threads/ so that
	// the more-specific sub-path (per-listing lookup) takes priority.
	mux.HandleFunc("/outreach/threads", s.requireAuth(s.handleOutreachThreads))
	mux.HandleFunc("/outreach/threads/", s.requireAuth(s.handleOutreachThreadByListing))
}

// outreachThreadResponse is the JSON representation of an OutreachThread
// returned by the four outreach endpoints.
type outreachThreadResponse struct {
	ID                    int64   `json:"id"`
	UserID                string  `json:"user_id"`
	ListingID             string  `json:"listing_id"`
	MarketplaceID         string  `json:"marketplace_id"`
	MissionID             *int64  `json:"mission_id,omitempty"`
	DraftText             string  `json:"draft_text"`
	DraftShape            string  `json:"draft_shape"`
	DraftLang             string  `json:"draft_lang"`
	SentAt                string  `json:"sent_at"`
	RepliedAt             *string `json:"replied_at,omitempty"`
	ReplyText             *string `json:"reply_text,omitempty"`
	State                 string  `json:"state"`
	LastStateTransitionAt string  `json:"last_state_transition_at"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
}

func outreachThreadToResponse(t store.OutreachThread) outreachThreadResponse {
	r := outreachThreadResponse{
		ID:                    t.ID,
		UserID:                t.UserID,
		ListingID:             t.ListingID,
		MarketplaceID:         t.MarketplaceID,
		MissionID:             t.MissionID,
		DraftText:             t.DraftText,
		DraftShape:            t.DraftShape,
		DraftLang:             t.DraftLang,
		SentAt:                t.SentAt.UTC().Format(time.RFC3339),
		ReplyText:             t.ReplyText,
		State:                 t.State,
		LastStateTransitionAt: t.LastStateTransitionAt.UTC().Format(time.RFC3339),
		CreatedAt:             t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:             t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.RepliedAt != nil {
		s := t.RepliedAt.UTC().Format(time.RFC3339)
		r.RepliedAt = &s
	}
	return r
}

// handleOutreachSent serves POST /outreach/sent.
func (s *Server) handleOutreachSent(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var body struct {
		ListingID     string `json:"listing_id"`
		MarketplaceID string `json:"marketplace_id"`
		MissionID     *int64 `json:"mission_id"`
		DraftText     string `json:"draft_text"`
		DraftShape    string `json:"draft_shape"`
		DraftLang     string `json:"draft_lang"`
	}
	if err := Decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if strings.TrimSpace(body.ListingID) == "" {
		writeError(w, http.StatusBadRequest, "listing_id is required")
		return
	}
	if strings.TrimSpace(body.MarketplaceID) == "" {
		writeError(w, http.StatusBadRequest, "marketplace_id is required")
		return
	}
	if strings.TrimSpace(body.DraftText) == "" {
		writeError(w, http.StatusBadRequest, "draft_text is required")
		return
	}
	if strings.TrimSpace(body.DraftShape) == "" {
		writeError(w, http.StatusBadRequest, "draft_shape is required")
		return
	}
	if strings.TrimSpace(body.DraftLang) == "" {
		writeError(w, http.StatusBadRequest, "draft_lang is required")
		return
	}
	if !validDraftShapes[body.DraftShape] {
		writeError(w, http.StatusBadRequest, "draft_shape must be one of: buy, negotiate, ask_seller, generic")
		return
	}
	if !validDraftLangs[body.DraftLang] {
		writeError(w, http.StatusBadRequest, "draft_lang must be one of: bg, nl, en")
		return
	}
	if !validOutreachMarketplaces[body.MarketplaceID] {
		writeError(w, http.StatusBadRequest, "marketplace_id must be one of: marktplaats, olxbg, vinted_nl, vinted_dk")
		return
	}

	t := store.OutreachThread{
		UserID:        user.ID,
		ListingID:     strings.TrimSpace(body.ListingID),
		MarketplaceID: body.MarketplaceID,
		DraftText:     body.DraftText,
		DraftShape:    body.DraftShape,
		DraftLang:     body.DraftLang,
	}
	if body.MissionID != nil && *body.MissionID > 0 {
		t.MissionID = body.MissionID
	}

	saved, err := s.db.UpsertThreadOnSent(r.Context(), t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, outreachThreadToResponse(saved))
}

// handleOutreachReplied serves POST /outreach/replied.
func (s *Server) handleOutreachReplied(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}

	var body struct {
		ListingID     string `json:"listing_id"`
		MarketplaceID string `json:"marketplace_id"`
		ReplyText     string `json:"reply_text"`
	}
	if err := Decode(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if strings.TrimSpace(body.ListingID) == "" {
		writeError(w, http.StatusBadRequest, "listing_id is required")
		return
	}
	if strings.TrimSpace(body.MarketplaceID) == "" {
		writeError(w, http.StatusBadRequest, "marketplace_id is required")
		return
	}
	if strings.TrimSpace(body.ReplyText) == "" {
		writeError(w, http.StatusBadRequest, "reply_text is required")
		return
	}

	// Check current thread state before attempting the transition.
	existing, err := s.db.GetThreadForListing(r.Context(), user.ID, body.ListingID, body.MarketplaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "outreach thread not found")
		return
	}
	if existing.State == "replied" {
		writeError(w, http.StatusConflict, "thread already replied")
		return
	}

	updated, err := s.db.MarkReplied(r.Context(), user.ID, body.ListingID, body.MarketplaceID, body.ReplyText)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, outreachThreadToResponse(updated))
}

// handleOutreachThreads serves GET /outreach/threads.
func (s *Server) handleOutreachThreads(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	var missionID *int64
	if raw := strings.TrimSpace(r.URL.Query().Get("mission_id")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "mission_id must be a non-negative integer")
			return
		}
		if parsed > 0 {
			missionID = &parsed
		}
	}

	threads, err := s.db.ListThreadsByUser(r.Context(), user.ID, missionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if threads == nil {
		threads = []store.OutreachThread{}
	}

	resp := make([]outreachThreadResponse, len(threads))
	for i, t := range threads {
		resp[i] = outreachThreadToResponse(t)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"threads": resp,
		"total":   len(resp),
	})
}

// handleOutreachThreadByListing serves GET /outreach/threads/{listing_id}.
// The marketplace_id query parameter is required.
func (s *Server) handleOutreachThreadByListing(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}

	listingID := strings.TrimPrefix(r.URL.Path, "/outreach/threads/")
	listingID = strings.Trim(listingID, "/")
	if listingID == "" {
		writeError(w, http.StatusBadRequest, "listing_id is required in path")
		return
	}

	marketplaceID := strings.TrimSpace(r.URL.Query().Get("marketplace_id"))
	if marketplaceID == "" {
		writeError(w, http.StatusBadRequest, "marketplace_id query parameter is required")
		return
	}

	thread, err := s.db.GetThreadForListing(r.Context(), user.ID, listingID, marketplaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if thread == nil {
		writeError(w, http.StatusNotFound, "outreach thread not found")
		return
	}
	writeJSON(w, http.StatusOK, outreachThreadToResponse(*thread))
}
