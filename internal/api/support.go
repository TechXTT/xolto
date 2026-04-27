package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/billing"
	"github.com/TechXTT/xolto/internal/marketplace"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/google/uuid"
)

type requestIDContextKey struct{}

const requestIDHeader = "X-Request-ID"

type statusCapturingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusCapturingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusCapturingResponseWriter) Write(p []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// Flush delegates to the underlying ResponseWriter when it implements http.Flusher.
// Without this, the interface wrapper hides the concrete type's Flush method, causing
// SSE handlers that type-assert w.(http.Flusher) to receive ok=false and return 500.
func (w *statusCapturingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter, enabling http.ResponseController (Go 1.20+).
func (w *statusCapturingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := requestIDFromRequest(r)
		if requestID == "" {
			requestID = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		w.Header().Set(requestIDHeader, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requestLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusCapturingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, r)
		slog.Default().Info(
			"http request completed",
			"op", "http.request",
			"request_id", requestIDFromRequest(r),
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func (s *Server) adminIPAllowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdminPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if len(s.cfg.AdminIPAllowlist) == 0 {
			s.adminAllowlistWarnOnce.Do(func() {
				slog.Default().Warn("admin ip allowlist is empty; allowing all admin sources", "op", "admin.ip_allowlist.empty")
			})
			next.ServeHTTP(w, r)
			return
		}
		if !s.adminSourceAllowed(r) {
			slog.Default().Warn(
				"admin request denied by ip allowlist",
				"op", "admin.ip_allowlist.deny",
				"request_id", requestIDFromRequest(r),
				"path", r.URL.Path,
				"remote_addr", strings.TrimSpace(r.RemoteAddr),
			)
			writeError(w, http.StatusForbidden, "request source is not allowed for admin access")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isAdminPath(path string) bool {
	path = strings.TrimSpace(path)
	return path == "/admin" || strings.HasPrefix(path, "/admin/")
}

func requestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if value, ok := r.Context().Value(requestIDContextKey{}).(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	for _, header := range []string{requestIDHeader, "X-Request-Id", "CF-Ray"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return value
		}
	}
	return ""
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

func (s *Server) requireOperatorOrOwner(next func(http.ResponseWriter, *http.Request, *models.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, user *models.User) {
		if !models.HasOperatorAccess(*user) {
			writeError(w, http.StatusForbidden, "operator or owner access required")
			return
		}
		next(w, r, user)
	})
}

func (s *Server) requireOwner(next func(http.ResponseWriter, *http.Request, *models.User)) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request, user *models.User) {
		if !models.HasOwnerAccess(*user) {
			writeError(w, http.StatusForbidden, "owner access required")
			return
		}
		next(w, r, user)
	})
}

func (s *Server) requireAdmin(next func(http.ResponseWriter, *http.Request, *models.User)) http.HandlerFunc {
	return s.requireOperatorOrOwner(next)
}

func (s *Server) allowedCORSOrigin(origin string) (string, bool) {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if origin == "" {
		return "", false
	}
	for _, allowed := range s.cfg.CORSAllowedOrigins {
		candidate := strings.TrimRight(strings.TrimSpace(allowed), "/")
		if candidate == "" {
			continue
		}
		if candidate == "*" || candidate == origin {
			return origin, true
		}
		if strings.Contains(candidate, "*") {
			re, err := compileOriginPattern(candidate)
			if err != nil {
				// Malformed pattern — skip rather than crash. Next entry may still match.
				continue
			}
			if re.MatchString(origin) {
				return origin, true
			}
		}
	}
	return "", false
}

// compileOriginPattern converts a glob-style origin pattern into an anchored
// regular expression. All regex metacharacters are escaped except for '*',
// which is expanded to '.*'. The returned regexp matches the full origin
// string (anchored with ^...$), preventing substring-based bypasses such as
// "https://trusted.example.com.evil.com" matching a pattern for
// "https://trusted.example.com".
func compileOriginPattern(pattern string) (*regexp.Regexp, error) {
	var sb strings.Builder
	sb.Grow(len(pattern) + 4)
	sb.WriteString("^")
	for _, r := range pattern {
		if r == '*' {
			sb.WriteString(".*")
			continue
		}
		sb.WriteString(regexp.QuoteMeta(string(r)))
	}
	sb.WriteString("$")
	return regexp.Compile(sb.String())
}

func (s *Server) adminSourceAllowed(r *http.Request) bool {
	if len(s.cfg.AdminIPAllowlist) == 0 {
		return true
	}
	clientIP := requestIP(r, s.cfg.TrustProxy)
	if clientIP == nil {
		return false
	}
	for _, allowed := range s.cfg.AdminIPAllowlist {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if strings.Contains(allowed, "/") {
			_, cidr, err := net.ParseCIDR(allowed)
			if err == nil && cidr.Contains(clientIP) {
				return true
			}
			continue
		}
		ip := net.ParseIP(allowed)
		if ip != nil && ip.Equal(clientIP) {
			return true
		}
	}
	return false
}

func requestIP(r *http.Request, trustProxy bool) net.IP {
	parse := func(raw string) net.IP {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return nil
		}
		return net.ParseIP(raw)
	}
	if trustProxy {
		forwarded := r.Header.Get("X-Forwarded-For")
		if forwarded != "" {
			parts := strings.Split(forwarded, ",")
			for _, part := range parts {
				if ip := parse(part); ip != nil {
					return ip
				}
			}
		}
		if ip := parse(r.Header.Get("X-Real-IP")); ip != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return parse(host)
	}
	return parse(r.RemoteAddr)
}

func (s *Server) currentUser(r *http.Request) (*models.User, error) {
	token := bearerToken(r)
	if token == "" {
		if cookie, err := r.Cookie("xolto_access"); err == nil {
			token = strings.TrimSpace(cookie.Value)
		}
	}
	if token == "" {
		// Legacy cookie fallback during rollout.
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
	domain := s.cookieDomain()
	http.SetCookie(w, &http.Cookie{
		Name:     "xolto_access",
		Value:    token,
		Path:     "/",
		Domain:   domain,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((24 * time.Hour).Seconds()),
	})
}

func (s *Server) setRefreshCookie(w http.ResponseWriter, token string) {
	domain := s.cookieDomain()
	http.SetCookie(w, &http.Cookie{
		Name:     "xolto_refresh",
		Value:    token,
		Path:     "/auth/refresh",
		Domain:   domain,
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	s.expireCookie(w, "xolto_access", "/")
	s.expireCookie(w, "xolto_refresh", "/auth/refresh")
	// Legacy cleanup for old path/name combinations.
	s.expireCookie(w, "xolto_session", "/")
	s.expireCookie(w, "xolto_refresh", "/")
}

func (s *Server) expireCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Domain:   s.cookieDomain(),
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

func (s *Server) secureCookies() bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s.cfg.AppBaseURL)), "https://")
}

func (s *Server) cookieDomain() string {
	if !s.secureCookies() {
		return ""
	}
	parsed, err := url.Parse(strings.TrimSpace(s.cfg.AppBaseURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "xolto.app" || strings.HasSuffix(host, ".xolto.app") {
		return ".xolto.app"
	}
	return ""
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

// makeUsageCallback returns a UsageCallback that records AI usage for the given user/mission context.
func (s *Server) makeUsageCallback(userID string, missionID int64) func(string, string, int, int, int, bool, string) {
	return func(callType, model string, prompt, completion, latencyMs int, success bool, errMsg string) {
		_ = s.db.RecordAIUsage(models.AIUsageEntry{
			UserID:           userID,
			MissionID:        missionID,
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
	role := models.NormalizeUserRole(user.Role)
	if role != "" {
		shouldBeAdmin := models.IsTeamRole(role)
		if user.IsAdmin != shouldBeAdmin {
			_ = s.db.SetUserAdmin(user.ID, shouldBeAdmin)
			user.IsAdmin = shouldBeAdmin
		}
		return
	}
	shouldBeAdmin := s.cfg.IsAdminEmail(user.Email)
	if user.IsAdmin != shouldBeAdmin {
		_ = s.db.SetUserAdmin(user.ID, shouldBeAdmin)
		user.IsAdmin = shouldBeAdmin
	}
	if shouldBeAdmin && models.NormalizeUserRole(user.Role) == "" {
		_ = s.db.UpdateUserRole(user.ID, string(models.UserRoleAdmin))
		user.Role = string(models.UserRoleAdmin)
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
		"role":                 models.EffectiveUserRole(user),
	}
	if models.HasOperatorAccess(user) {
		m["is_admin"] = true
	}
	return m, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAdminOK(w http.ResponseWriter, status int, payload map[string]any) {
	resp := map[string]any{
		"ok":    true,
		"error": "",
		"data":  payload,
	}
	for key, value := range payload {
		resp[key] = value
	}
	writeJSON(w, status, resp)
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
	// Internal slugs are stable: "pro" = mid tier (display "Buyer"),
	// "power" = top tier (display "Pro"). See internal/billing/limits.go.
	switch strings.TrimSpace(priceID) {
	case "":
		return "", false
	case strings.TrimSpace(s.cfg.StripeBuyerPriceID):
		return "pro", true
	case strings.TrimSpace(s.cfg.StripeProPriceID):
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
