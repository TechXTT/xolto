package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/assistant"
	"github.com/TechXTT/marktbot/internal/auth"
	"github.com/TechXTT/marktbot/internal/billing"
	"github.com/TechXTT/marktbot/internal/config"
	"github.com/TechXTT/marktbot/internal/generator"
	"github.com/TechXTT/marktbot/internal/models"
	"github.com/TechXTT/marktbot/internal/store"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/webhook"
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
	mux       *http.ServeMux
}

func NewServer(cfg config.ServerConfig, db store.Store, asst *assistant.Assistant, broker *SSEBroker, runner SearchRunner) *Server {
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
		mux:       mux,
	}
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/auth/register", s.handleRegister)
	mux.HandleFunc("/auth/login", s.handleLogin)
	mux.HandleFunc("/auth/refresh", s.handleRefresh)
	mux.HandleFunc("/auth/logout", s.handleLogout)
	mux.HandleFunc("/users/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("/searches", s.requireAuth(s.handleSearches))
	mux.HandleFunc("/searches/generate", s.requireAuth(s.handleGenerateSearches))
	mux.HandleFunc("/searches/", s.requireAuth(s.handleSearchByID))
	mux.HandleFunc("/listings/feed", s.requireAuth(s.handleFeed))
	mux.HandleFunc("/shortlist", s.requireAuth(s.handleShortlist))
	mux.HandleFunc("/shortlist/", s.requireAuth(s.handleShortlistItem))
	mux.HandleFunc("/assistant/converse", s.requireAuth(s.handleConverse))
	mux.HandleFunc("/assistant/session", s.requireAuth(s.handleAssistantSession))
	mux.HandleFunc("/actions", s.requireAuth(s.handleActions))
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/billing/checkout", s.requireAuth(s.handleBillingCheckout))
	mux.HandleFunc("/billing/webhook", s.handleBillingWebhook)
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "marktbot-server"})
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
	accessToken, refreshToken, err := s.issueSessionTokens(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookies(w, accessToken, refreshToken)
	writeJSON(w, http.StatusCreated, map[string]any{
		"access_token": accessToken,
		"user":         sanitizeUser(*user),
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
	accessToken, refreshToken, err := s.issueSessionTokens(*user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookies(w, accessToken, refreshToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"user":         sanitizeUser(*user),
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	refreshCookie, err := r.Cookie("marktbot_refresh")
	if err != nil || strings.TrimSpace(refreshCookie.Value) == "" {
		writeError(w, http.StatusUnauthorized, "missing refresh token")
		return
	}
	claims, err := auth.ParseToken(s.cfg.JWTSecret, strings.TrimSpace(refreshCookie.Value))
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
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
		"user":         sanitizeUser(*user),
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
	writeJSON(w, http.StatusOK, sanitizeUser(*user))
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
			spec.MarketplaceID = "marktplaats"
		}
		if spec.OfferPercentage == 0 {
			spec.OfferPercentage = 70
		}
		if spec.CheckInterval == 0 {
			spec.CheckInterval = 5 * time.Minute
		}
		spec.Enabled = true
		limits := billing.LimitsFor(user.Tier)
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
		if spec.CheckInterval == 0 {
			spec.CheckInterval = 5 * time.Minute
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
	listings, err := s.db.ListRecentListings(user.ID, 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listings": listings, "user_id": user.ID})
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
	itemID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/shortlist/"), "/")
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
		existing, err := s.db.GetShortlist(user.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, item := range existing {
			if item.ItemID == itemID {
				item.Status = "removed"
				if err := s.db.SaveShortlistEntry(item); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				break
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
	if s.cfg.StripeWebhookSecret != "" {
		signature := r.Header.Get("Stripe-Signature")
		event, err = webhook.ConstructEvent(body, signature, s.cfg.StripeWebhookSecret)
	} else {
		err = json.Unmarshal(body, &event)
	}
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
			customerID := checkoutSession.Customer.ID
			if customerID == "" {
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

func (s *Server) currentUser(r *http.Request) (*models.User, error) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("marktbot_session"); err == nil {
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
		Tier:      user.Tier,
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
		Name:     "marktbot_session",
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
		Name:     "marktbot_refresh",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{"marktbot_session", "marktbot_refresh"} {
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

func sanitizeUser(user models.User) map[string]any {
	return map[string]any{
		"id":    user.ID,
		"email": user.Email,
		"name":  user.Name,
		"tier":  user.Tier,
	}
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
	if marketplaceID == "" || limits.MaxMarketplaces == 0 {
		return true
	}
	switch limits.MaxMarketplaces {
	case 1:
		return marketplaceID == "marktplaats"
	default:
		return true
	}
}

func (s *Server) subscriptionTier(priceID string) (string, bool) {
	switch strings.TrimSpace(priceID) {
	case "":
		return "", false
	case strings.TrimSpace(s.cfg.StripeProPriceID):
		return "pro", true
	case strings.TrimSpace(s.cfg.StripeTeamPriceID):
		return "team", true
	default:
		return "", false
	}
}

func (s *Server) subscriptionTierFromMetadata(metadata map[string]string) (string, bool) {
	tier := strings.TrimSpace(metadata["tier"])
	switch tier {
	case "pro", "team":
		return tier, true
	default:
		return "", false
	}
}
