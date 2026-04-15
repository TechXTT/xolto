package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/models"
)

func (s *Server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/auth/providers", s.handleAuthProviders)
	mux.HandleFunc("/auth/register", s.handleRegister)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/google/start", s.handleGoogleStart)
	mux.HandleFunc("/auth/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/users/me", s.requireAuth(s.handleMe))
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
		Email    string `json:"email" validate:"required,email"`
		Password string `json:"password" validate:"required,min=8"`
		Name     string `json:"name" validate:"omitempty,max=120"`
	}
	if err := Decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		Email    string `json:"email" validate:"required,email"`
		Password string `json:"password" validate:"required,min=1"`
	}
	if err := Decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
	refreshToken := ""
	if cookie, err := r.Cookie("xolto_refresh"); err == nil {
		refreshToken = strings.TrimSpace(cookie.Value)
	}
	if refreshToken == "" {
		// Backward-compatible fallback during cookie auth rollout.
		refreshToken = strings.TrimSpace(r.Header.Get("X-Refresh-Token"))
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
			Name               string `json:"name" validate:"omitempty,max=120"`
			CountryCode        string `json:"country_code" validate:"omitempty,len=2"`
			Region             string `json:"region" validate:"omitempty,max=80"`
			City               string `json:"city" validate:"omitempty,max=80"`
			PostalCode         string `json:"postal_code" validate:"omitempty,max=32"`
			PreferredRadiusKm  int    `json:"preferred_radius_km" validate:"omitempty,min=1,max=2000"`
			CrossBorderEnabled bool   `json:"cross_border_enabled"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
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
