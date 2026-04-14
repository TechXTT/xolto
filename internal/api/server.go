package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/assistant"
	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/generator"
	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/marketplace/listingfetcher"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
	"github.com/stripe/stripe-go/v81"
	portalsession "github.com/stripe/stripe-go/v81/billingportal/session"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/customer"
	"github.com/stripe/stripe-go/v81/webhook"
)

type SearchRunner interface {
	RunAllNow(ctx context.Context) error
	RunUserNow(ctx context.Context, userID string) error
}

type Server struct {
	cfg       config.ServerConfig
	db        store.Store
	assistant *assistant.Assistant
	broker    *SSEBroker
	runner    SearchRunner
	scorer    *scorer.Scorer
	fetcher   *listingfetcher.Fetcher
	mux       *http.ServeMux
}

type googleTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type googleUserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func NewServer(cfg config.ServerConfig, db store.Store, asst *assistant.Assistant, broker *SSEBroker, runner SearchRunner, sc *scorer.Scorer) *Server {
	if broker == nil {
		broker = NewSSEBroker()
	}
	mux := http.NewServeMux()
	s := &Server{
		cfg:       cfg,
		db:        db,
		assistant: asst,
		broker:    broker,
		runner:    runner,
		scorer:    sc,
		fetcher:   listingfetcher.New(),
		mux:       mux,
	}
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/auth/providers", s.handleAuthProviders)
	mux.HandleFunc("/auth/register", s.handleRegister)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/google/start", s.handleGoogleStart)
	mux.HandleFunc("/auth/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/users/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("/searches", s.requireAuth(s.handleSearches))
	mux.HandleFunc("/searches/run", s.requireAuth(s.handleRunAllSearches))
	mux.HandleFunc("/searches/generate", s.requireAuth(s.handleGenerateSearches))
	mux.HandleFunc("/searches/", s.requireAuth(s.handleSearchByID))
	mux.HandleFunc("/missions", s.requireAuth(s.handleMissions))
	mux.HandleFunc("/missions/", s.requireAuth(s.handleMissionByID))
	mux.HandleFunc("/listings/feed", s.requireAuth(s.handleFeed))
	mux.HandleFunc("/matches/feedback", s.requireAuth(s.handleMatchFeedback))
	mux.HandleFunc("/matches/analyze", s.requireAuth(s.handleAnalyzeListing))
	mux.HandleFunc("/shortlist", s.requireAuth(s.handleShortlist))
	mux.HandleFunc("/shortlist/", s.requireAuth(s.handleShortlistItem))
	mux.HandleFunc("/assistant/converse", s.requireAuth(s.handleConverse))
	mux.HandleFunc("/assistant/session", s.requireAuth(s.handleAssistantSession))
	mux.HandleFunc("/actions", s.requireAuth(s.handleActions))
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/billing/checkout", s.requireAuth(s.handleBillingCheckout))
	mux.HandleFunc("/billing/portal", s.requireAuth(s.handleBillingPortal))
	mux.HandleFunc("/billing/webhook", s.handleBillingWebhook)
	// Admin routes
	mux.HandleFunc("/admin/stats", s.requireAdmin(s.handleAdminStats))
	mux.HandleFunc("/admin/users", s.requireAdmin(s.handleAdminUsers))
	mux.HandleFunc("/admin/usage", s.requireAdmin(s.handleAdminUsageTimeline))
	return s
}

func (s *Server) Handler() http.Handler {
	return s.corsMiddleware(s.mux)
}

// corsMiddleware adds CORS headers for requests from the configured app origin.
// It handles preflight OPTIONS requests and allows credentials.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	allowedOrigin := strings.TrimRight(s.cfg.AppBaseURL, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (allowedOrigin == "*" || origin == allowedOrigin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServe() error {
	server := &http.Server{
		Addr:    s.cfg.Address,
		Handler: s.Handler(),
		BaseContext: func(net.Listener) context.Context {
			return context.Background()
		},
	}
	return server.ListenAndServe()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "xolto-server"})
}

func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email_password": true,
		"google":         s.googleEnabled(),
	})
}

func (s *Server) handleGoogleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if !s.googleEnabled() {
		writeError(w, http.StatusNotFound, "google auth is not configured")
		return
	}
	state, err := auth.IssueToken(s.cfg.JWTSecret, auth.Claims{
		UserID:    "google",
		Email:     s.safeReturnTo(r.URL.Query().Get("return_to")),
		TokenType: "oauth_state",
	}, 10*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	params := url.Values{}
	params.Set("client_id", s.cfg.GoogleClientID)
	params.Set("redirect_uri", s.cfg.GoogleRedirectURL)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("prompt", "select_account")
	params.Set("state", state)
	http.Redirect(w, r, "https://accounts.google.com/o/oauth2/v2/auth?"+params.Encode(), http.StatusTemporaryRedirect)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if !s.googleEnabled() {
		s.redirectAuthError(w, r, "google auth is not configured")
		return
	}
	claims, err := auth.ParseToken(s.cfg.JWTSecret, strings.TrimSpace(r.URL.Query().Get("state")))
	if err != nil || claims.TokenType != "oauth_state" {
		s.redirectAuthError(w, r, "invalid login state")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		s.redirectAuthError(w, r, "missing authorization code")
		return
	}
	token, err := s.exchangeGoogleCode(r.Context(), code)
	if err != nil {
		s.redirectAuthError(w, r, err.Error())
		return
	}
	info, err := s.fetchGoogleUserInfo(r.Context(), token.AccessToken)
	if err != nil {
		s.redirectAuthError(w, r, err.Error())
		return
	}
	if !info.EmailVerified {
		s.redirectAuthError(w, r, "google account email must be verified")
		return
	}

	identityUser, err := s.db.GetUserByAuthIdentity("google", info.Sub)
	if err != nil {
		s.redirectAuthError(w, r, "failed to load google identity")
		return
	}
	emailUser, err := s.db.GetUserByEmail(info.Email)
	if err != nil {
		s.redirectAuthError(w, r, "failed to load user account")
		return
	}
	if identityUser != nil && emailUser != nil && identityUser.ID != emailUser.ID {
		s.redirectAuthError(w, r, "google account is already linked to another user")
		return
	}

	user := identityUser
	if user == nil {
		user = emailUser
	}
	if user == nil {
		userID, err := s.db.CreateUser(info.Email, "!oauth-google!", info.Name)
		if err != nil {
			s.redirectAuthError(w, r, "failed to create account")
			return
		}
		user, err = s.db.GetUserByID(userID)
		if err != nil || user == nil {
			s.redirectAuthError(w, r, "failed to load new account")
			return
		}
	}
	if err := s.db.UpsertUserAuthIdentity(models.AuthIdentity{
		UserID:          user.ID,
		Provider:        "google",
		ProviderSubject: info.Sub,
		Email:           info.Email,
		EmailVerified:   true,
	}); err != nil {
		s.redirectAuthError(w, r, "failed to link google account")
		return
	}

	s.syncAdminFlag(user)
	accessToken, refreshToken, err := s.issueSessionTokens(*user)
	if err != nil {
		s.redirectAuthError(w, r, err.Error())
		return
	}
	s.setSessionCookies(w, accessToken, refreshToken)
	http.Redirect(w, r, strings.TrimRight(s.cfg.AppBaseURL, "/")+s.safeReturnTo(claims.Email), http.StatusTemporaryRedirect)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Email) == "" || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "email and password (min 8 chars) are required")
		return
	}
	existing, err := s.db.GetUserByEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "user already exists")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	userID, err := s.db.CreateUser(req.Email, hash, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	user, err := s.db.GetUserByID(userID)
	if err != nil || user == nil {
		writeError(w, http.StatusInternalServerError, "failed to load created user")
		return
	}
	s.syncAdminFlag(user)
	accessToken, refreshToken, err := s.issueSessionTokens(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookies(w, accessToken, refreshToken)
	userPayload, err := s.userPayload(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user":          userPayload,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	user, err := s.db.GetUserByEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if user == nil || !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.syncAdminFlag(user)
	accessToken, refreshToken, err := s.issueSessionTokens(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookies(w, accessToken, refreshToken)
	userPayload, err := s.userPayload(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user":          userPayload,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	refreshToken := strings.TrimSpace(r.Header.Get("X-Refresh-Token"))
	if refreshToken == "" {
		if cookie, err := r.Cookie("xolto_refresh"); err == nil {
			refreshToken = strings.TrimSpace(cookie.Value)
		}
	}
	if refreshToken == "" {
		writeError(w, http.StatusUnauthorized, "missing refresh token")
		return
	}
	claims, err := auth.ParseToken(s.cfg.JWTSecret, refreshToken)
	if err != nil || claims.TokenType != "refresh" {
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	user, err := s.db.GetUserByID(claims.UserID)
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	accessToken, refreshToken, err := s.issueSessionTokens(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookies(w, accessToken, refreshToken)
	userPayload, err := s.userPayload(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"user":          userPayload,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	s.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user *models.User) {
	switch r.Method {
	case http.MethodGet:
		payload, err := s.userPayload(*user)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, payload)
	case http.MethodPut:
		var req struct {
			Name               string `json:"name"`
			CountryCode        string `json:"country_code"`
			Region             string `json:"region"`
			City               string `json:"city"`
			PostalCode         string `json:"postal_code"`
			PreferredRadiusKm  int    `json:"preferred_radius_km"`
			CrossBorderEnabled bool   `json:"cross_border_enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		next := *user
		if strings.TrimSpace(req.Name) != "" {
			next.Name = strings.TrimSpace(req.Name)
		}
		if strings.TrimSpace(req.CountryCode) != "" {
			next.CountryCode = strings.ToUpper(strings.TrimSpace(req.CountryCode))
		}
		if strings.TrimSpace(req.Region) != "" || user.Region != "" {
			next.Region = strings.TrimSpace(req.Region)
		}
		if strings.TrimSpace(req.City) != "" || user.City != "" {
			next.City = strings.TrimSpace(req.City)
		}
		if strings.TrimSpace(req.PostalCode) != "" || user.PostalCode != "" {
			next.PostalCode = strings.TrimSpace(req.PostalCode)
		}
		if req.PreferredRadiusKm > 0 {
			next.PreferredRadiusKm = req.PreferredRadiusKm
		}
		if next.PreferredRadiusKm <= 0 {
			next.PreferredRadiusKm = 100
		}
		next.CrossBorderEnabled = req.CrossBorderEnabled
		if strings.TrimSpace(next.CountryCode) == "" {
			writeError(w, http.StatusBadRequest, "country_code is required")
			return
		}
		if err := s.db.UpdateUserProfile(next); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated, err := s.db.GetUserByID(user.ID)
		if err != nil || updated == nil {
			writeError(w, http.StatusInternalServerError, "failed to load updated user")
			return
		}
		payload, err := s.userPayload(*updated)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, payload)
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPut)
	}
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
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
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
		Topic string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
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
	gen.SetUsageCallback(s.makeUsageCallback(user.ID))
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
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
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
		if err := json.NewDecoder(r.Body).Decode(&mission); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
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
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
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
		if err := json.NewDecoder(r.Body).Decode(&mission); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body")
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
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
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
		ItemID string `json:"item_id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
		URL       string `json:"url"`
		MissionID int64  `json:"mission_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.cfg.StripeSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe is not configured")
		return
	}
	var req struct {
		PriceID string `json:"price_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	priceID := strings.TrimSpace(req.PriceID)
	if priceID == "" {
		writeError(w, http.StatusBadRequest, "price_id is required")
		return
	}
	tier, ok := s.subscriptionTier(priceID)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown price_id")
		return
	}
	stripe.Key = s.cfg.StripeSecret

	customerID := strings.TrimSpace(user.StripeCustomer)
	if customerID == "" {
		cust, err := customer.New(&stripe.CustomerParams{
			Email: stripe.String(user.Email),
			Name:  stripe.String(user.Name),
			Metadata: map[string]string{
				"user_id": user.ID,
				"tier":    tier,
			},
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		customerID = cust.ID
		if err := s.db.UpdateStripeCustomer(user.ID, customerID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	successURL := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings?checkout=success"
	cancelURL := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings?checkout=cancelled"
	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		Customer:   stripe.String(customerID),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		Metadata: map[string]string{
			"user_id": user.ID,
			"tier":    tier,
		},
	}
	sess, err := session.New(params)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL, "id": sess.ID})
}

func (s *Server) handleBillingPortal(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if s.cfg.StripeSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe is not configured")
		return
	}
	stripe.Key = s.cfg.StripeSecret
	customerID := strings.TrimSpace(user.StripeCustomer)
	if customerID == "" {
		writeError(w, http.StatusBadRequest, "no billing account found")
		return
	}
	returnURL := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings"
	sess, err := portalsession.New(&stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL})
}

func (s *Server) handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var event stripe.Event
	if s.cfg.StripeWebhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe webhook not configured")
		return
	}
	signature := r.Header.Get("Stripe-Signature")
	event, err = webhook.ConstructEventWithOptions(body, signature, s.cfg.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stripe webhook payload")
		return
	}
	if event.ID != "" {
		if err := s.db.RecordStripeEvent(event.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	switch event.Type {
	case "checkout.session.completed":
		var checkoutSession stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &checkoutSession); err == nil {
			customerID := ""
			if checkoutSession.Customer != nil {
				customerID = checkoutSession.Customer.ID
			}
			tier, ok := s.subscriptionTierFromMetadata(checkoutSession.Metadata)
			if ok && customerID != "" {
				_ = s.db.UpdateUserTierByStripeCustomer(customerID, tier)
			}
		}
	case "customer.subscription.created", "customer.subscription.updated":
		var sub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err == nil {
			priceID := ""
			if len(sub.Items.Data) > 0 && sub.Items.Data[0].Price != nil {
				priceID = sub.Items.Data[0].Price.ID
			}
			tier, ok := s.subscriptionTier(priceID)
			customerID := ""
			if sub.Customer != nil {
				customerID = sub.Customer.ID
			}
			if ok && customerID != "" {
				_ = s.db.UpdateUserTierByStripeCustomer(customerID, tier)
			}
		}
	case "customer.subscription.deleted":
		var sub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err == nil && sub.Customer != nil && sub.Customer.ID != "" {
			_ = s.db.UpdateUserTierByStripeCustomer(sub.Customer.ID, "free")
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Admin endpoints ---

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
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats, "search_stats": searchStats, "days": days})
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
			IsAdmin:      u.IsAdmin,
			CreatedAt:    u.CreatedAt.Format("2006-01-02T15:04:05Z"),
			MissionCount: u.MissionCount,
			SearchCount:  u.SearchCount,
			AICallCount:  u.AICallCount,
			AITokens:     u.AITokens,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": safe})
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
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "days": days})
}

func (s *Server) requireAuth(next func(http.ResponseWriter, *http.Request, *models.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.currentUser(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r, user)
	}
}

func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, *models.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, user *models.User) {
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		next(w, r, user)
	})
}

func (s *Server) currentUser(r *http.Request) (*models.User, error) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("xolto_session"); err == nil {
			token = strings.TrimSpace(cookie.Value)
		}
	}
	if token == "" {
		return nil, errors.New("missing token")
	}
	claims, err := auth.ParseToken(s.cfg.JWTSecret, token)
	if err != nil {
		return nil, err
	}
	if claims.TokenType != "" && claims.TokenType != "access" {
		return nil, errors.New("invalid token type")
	}
	user, err := s.db.GetUserByID(claims.UserID)
	if err != nil || user == nil {
		return nil, errors.New("user not found")
	}
	return user, nil
}

func (s *Server) issueToken(user models.User, tokenType string, ttl time.Duration) (string, error) {
	return auth.IssueToken(s.cfg.JWTSecret, auth.Claims{
		UserID:    user.ID,
		Email:     user.Email,
		Tier:      billing.NormalizeTier(user.Tier),
		TokenType: tokenType,
	}, ttl)
}

func (s *Server) issueSessionTokens(user models.User) (string, string, error) {
	accessToken, err := s.issueToken(user, "access", 24*time.Hour)
	if err != nil {
		return "", "", err
	}
	refreshToken, err := s.issueToken(user, "refresh", 30*24*time.Hour)
	if err != nil {
		return "", "", err
	}
	return accessToken, refreshToken, nil
}

func (s *Server) setSessionCookies(w http.ResponseWriter, accessToken, refreshToken string) {
	s.setAccessCookie(w, accessToken)
	s.setRefreshCookie(w, refreshToken)
}

func (s *Server) setAccessCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "xolto_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
}

func (s *Server) setRefreshCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "xolto_refresh",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{"xolto_session", "xolto_refresh"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			Secure:   s.secureCookies(),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

func (s *Server) secureCookies() bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.cfg.AppBaseURL)), "https://")
}

func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func parseIDFromPath(path, prefix string) (int64, error) {
	raw := strings.TrimPrefix(path, prefix)
	raw = strings.Trim(raw, "/")
	return strconv.ParseInt(raw, 10, 64)
}

// makeUsageCallback returns a UsageCallback that records AI usage for the given user.
func (s *Server) makeUsageCallback(userID string) func(string, string, int, int, int, bool, string) {
	return func(callType, model string, prompt, completion, latencyMs int, success bool, errMsg string) {
		_ = s.db.RecordAIUsage(models.AIUsageEntry{
			UserID:           userID,
			CallType:         callType,
			Model:            model,
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			LatencyMs:        latencyMs,
			Success:          success,
			ErrorMsg:         errMsg,
		})
	}
}

// syncAdminFlag promotes or demotes a user based on the ADMIN_EMAILS env var.
// Called on login/register so the flag stays in sync without manual SQL.
func (s *Server) syncAdminFlag(user *models.User) {
	shouldBeAdmin := s.cfg.IsAdminEmail(user.Email)
	if user.IsAdmin != shouldBeAdmin {
		_ = s.db.SetUserAdmin(user.ID, shouldBeAdmin)
		user.IsAdmin = shouldBeAdmin
	}
}

func (s *Server) userPayload(user models.User) (map[string]any, error) {
	authMethods, err := s.db.ListUserAuthMethods(user.ID)
	if err != nil {
		return nil, err
	}
	m := map[string]any{
		"id":                   user.ID,
		"email":                user.Email,
		"name":                 user.Name,
		"tier":                 billing.NormalizeTier(user.Tier),
		"country_code":         strings.ToUpper(strings.TrimSpace(user.CountryCode)),
		"region":               user.Region,
		"city":                 user.City,
		"postal_code":          user.PostalCode,
		"preferred_radius_km":  user.PreferredRadiusKm,
		"cross_border_enabled": user.CrossBorderEnabled,
		"auth_methods":         authMethods,
	}
	if user.IsAdmin {
		m["is_admin"] = true
	}
	return m, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}

func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func mustJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) marketplaceAllowedForTier(marketplaceID string, limits billing.Limits) bool {
	_, ok := marketplace.DescriptorByID(marketplaceID)
	return ok
}

func (s *Server) subscriptionTier(priceID string) (string, bool) {
	switch strings.TrimSpace(priceID) {
	case "":
		return "", false
	case strings.TrimSpace(s.cfg.StripeProPriceID):
		return "pro", true
	case strings.TrimSpace(s.cfg.StripePowerPriceID):
		return "power", true
	default:
		return "", false
	}
}

func (s *Server) subscriptionTierFromMetadata(metadata map[string]string) (string, bool) {
	tier := billing.NormalizeTier(metadata["tier"])
	switch tier {
	case "pro", "power":
		return tier, true
	default:
		return "", false
	}
}

func (s *Server) googleEnabled() bool {
	return strings.TrimSpace(s.cfg.GoogleClientID) != "" &&
		strings.TrimSpace(s.cfg.GoogleClientSecret) != "" &&
		strings.TrimSpace(s.cfg.GoogleRedirectURL) != ""
}

func (s *Server) safeReturnTo(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/missions"
	}
	return value
}

func (s *Server) redirectAuthError(w http.ResponseWriter, r *http.Request, message string) {
	target := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/login?error=" + url.QueryEscape(message)
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
}

func (s *Server) exchangeGoogleCode(ctx context.Context, code string) (googleTokenResponse, error) {
	values := url.Values{}
	values.Set("code", code)
	values.Set("client_id", s.cfg.GoogleClientID)
	values.Set("client_secret", s.cfg.GoogleClientSecret)
	values.Set("redirect_uri", s.cfg.GoogleRedirectURL)
	values.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(values.Encode()))
	if err != nil {
		return googleTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return googleTokenResponse{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var token googleTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return googleTokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if token.ErrorDescription != "" {
			return googleTokenResponse{}, errors.New(token.ErrorDescription)
		}
		if token.Error != "" {
			return googleTokenResponse{}, errors.New(token.Error)
		}
		return googleTokenResponse{}, errors.New("google token exchange failed")
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return googleTokenResponse{}, errors.New("google token exchange returned no access token")
	}
	return token, nil
}

func (s *Server) fetchGoogleUserInfo(ctx context.Context, accessToken string) (googleUserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return googleUserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return googleUserInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return googleUserInfo{}, errors.New("google userinfo request failed")
	}
	var info googleUserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return googleUserInfo{}, err
	}
	if strings.TrimSpace(info.Sub) == "" || strings.TrimSpace(info.Email) == "" {
		return googleUserInfo{}, errors.New("google userinfo response was incomplete")
	}
	return info, nil
}

func (s *Server) normalizeMissionForWrite(user *models.User, mission models.Mission, existing *models.Mission) (models.Mission, error) {
	if strings.TrimSpace(mission.Name) == "" {
		if existing != nil && strings.TrimSpace(existing.Name) != "" {
			mission.Name = existing.Name
		} else {
			mission.Name = strings.TrimSpace(mission.TargetQuery)
		}
	}
	if mission.BudgetStretch == 0 && mission.BudgetMax > 0 {
		mission.BudgetStretch = mission.BudgetMax
	}
	if strings.TrimSpace(mission.Status) == "" {
		if existing != nil {
			mission.Status = existing.Status
		} else {
			mission.Status = "active"
		}
	}
	if strings.TrimSpace(mission.Urgency) == "" {
		if existing != nil && strings.TrimSpace(existing.Urgency) != "" {
			mission.Urgency = existing.Urgency
		} else {
			mission.Urgency = "flexible"
		}
	}
	if strings.TrimSpace(mission.Category) == "" {
		if existing != nil && strings.TrimSpace(existing.Category) != "" {
			mission.Category = existing.Category
		} else {
			mission.Category = "other"
		}
	}

	if strings.TrimSpace(mission.CountryCode) == "" {
		switch {
		case existing != nil && strings.TrimSpace(existing.CountryCode) != "":
			mission.CountryCode = existing.CountryCode
		case user != nil:
			mission.CountryCode = user.CountryCode
		}
	}
	mission.CountryCode = strings.ToUpper(strings.TrimSpace(mission.CountryCode))
	if mission.CountryCode == "" {
		return mission, errors.New("country_code is required")
	}
	if strings.TrimSpace(mission.Region) == "" && existing != nil {
		mission.Region = existing.Region
	}
	if strings.TrimSpace(mission.City) == "" && existing != nil {
		mission.City = existing.City
	}
	if strings.TrimSpace(mission.PostalCode) == "" {
		switch {
		case existing != nil && strings.TrimSpace(existing.PostalCode) != "":
			mission.PostalCode = existing.PostalCode
		case user != nil && strings.TrimSpace(user.PostalCode) != "":
			mission.PostalCode = user.PostalCode
		}
	}
	if mission.TravelRadius <= 0 {
		switch {
		case existing != nil && existing.TravelRadius > 0:
			mission.TravelRadius = existing.TravelRadius
		case user != nil && user.PreferredRadiusKm > 0:
			mission.TravelRadius = user.PreferredRadiusKm
		default:
			mission.TravelRadius = 100
		}
	}
	if mission.TravelRadius <= 0 {
		return mission, errors.New("travel_radius must be positive")
	}
	if mission.Distance == 0 {
		mission.Distance = mission.TravelRadius * 1000
	}
	if mission.ZipCode == "" {
		mission.ZipCode = mission.PostalCode
	}

	limits := billing.LimitsFor(user.Tier)
	scopeRequest := mission.MarketplaceScope
	if len(scopeRequest) == 0 && existing != nil && len(existing.MarketplaceScope) > 0 && !locationFieldsChanged(mission) {
		scopeRequest = existing.MarketplaceScope
	}
	mission.MarketplaceScope = marketplace.ValidateScope(mission.CountryCode, mission.CrossBorderEnabled, scopeRequest)
	if len(mission.MarketplaceScope) == 0 {
		return mission, errors.New("marketplace_scope is required")
	}
	if limits.MaxMarketplaces > 0 && len(mission.MarketplaceScope) > limits.MaxMarketplaces {
		return mission, errors.New("marketplace_scope exceeds plan limits")
	}
	mission.Active = mission.Status == "active"
	return mission, nil
}

func locationFieldsChanged(mission models.Mission) bool {
	return strings.TrimSpace(mission.CountryCode) != "" ||
		strings.TrimSpace(mission.Region) != "" ||
		strings.TrimSpace(mission.City) != "" ||
		strings.TrimSpace(mission.PostalCode) != "" ||
		mission.TravelRadius > 0 ||
		mission.CrossBorderEnabled ||
		len(mission.MarketplaceScope) > 0
}
