package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type ServerConfig struct {
	Address             string
	DatabaseURL         string
	JWTSecret           string
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleRedirectURL   string
	StripeSecret        string
	StripeWebhookSecret string
	StripeProPriceID    string
	StripePowerPriceID  string
	AppBaseURL          string
	AdminBaseURL        string
	CORSAllowedOrigins  []string
	AIAPIKey            string
	AIBaseURL           string
	AIModel             string
	SMTPHost            string
	SMTPPort            string
	SMTPUser            string
	SMTPPass            string
	SMTPFrom            string
	AlertScore          float64
	AdminEmails         []string
	AdminIPAllowlist    []string
	HTTPTimeouts        HTTPTimeouts
}

func LoadServerConfigFromEnv() (ServerConfig, error) {
	cfg := ServerConfig{
		Address:             getenvDefault("SERVER_ADDR", ":8000"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		GoogleClientID:      os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:   os.Getenv("GOOGLE_REDIRECT_URL"),
		StripeSecret:        os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripeProPriceID:    os.Getenv("STRIPE_PRO_PRICE_ID"),
		StripePowerPriceID:  os.Getenv("STRIPE_POWER_PRICE_ID"),
		AppBaseURL:          getenvDefault("APP_BASE_URL", "http://localhost:3000"),
		AdminBaseURL:        getenvDefault("ADMIN_BASE_URL", "http://localhost:3010"),
		AIAPIKey:            os.Getenv("AI_API_KEY"),
		AIBaseURL:           getenvDefault("AI_BASE_URL", "https://api.openai.com/v1"),
		AIModel:             getenvDefault("AI_MODEL", "gpt-4o-mini"),
		SMTPHost:            os.Getenv("SMTP_HOST"),
		SMTPPort:            getenvDefault("SMTP_PORT", "587"),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPass:            os.Getenv("SMTP_PASS"),
		SMTPFrom:            getenvDefault("SMTP_FROM", "alerts@xolto.app"),
		AlertScore:          parseFloatDefault(os.Getenv("ALERT_SCORE"), 8.0),
		AdminEmails:         parseAdminEmails(os.Getenv("ADMIN_EMAILS")),
		AdminIPAllowlist:    parseCSV(os.Getenv("ADMIN_IP_ALLOWLIST")),
		HTTPTimeouts: HTTPTimeouts{
			ReadTimeout:       parseDurationDefault(os.Getenv("SERVER_READ_TIMEOUT"), defaultServerReadTimeout),
			WriteTimeout:      parseDurationDefault(os.Getenv("SERVER_WRITE_TIMEOUT"), defaultServerWriteTimeout),
			IdleTimeout:       parseDurationDefault(os.Getenv("SERVER_IDLE_TIMEOUT"), defaultServerIdleTimeout),
			ReadHeaderTimeout: parseDurationDefault(os.Getenv("SERVER_READ_HEADER_TIMEOUT"), defaultServerReadHeaderTimeout),
		},
	}
	cfg.CORSAllowedOrigins = parseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"), cfg.AppBaseURL, cfg.AdminBaseURL)
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "xolto-server.db"
	}
	if cfg.JWTSecret == "" {
		return cfg, fmt.Errorf("JWT_SECRET is required")
	}
	return cfg, nil
}

func (c ServerConfig) IsAdminEmail(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, admin := range c.AdminEmails {
		if admin == email {
			return true
		}
	}
	return false
}

func getenvDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func parseAdminEmails(s string) []string {
	values := parseCSV(s)
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, e := range values {
		out = append(out, strings.ToLower(strings.TrimSpace(e)))
	}
	return out
}

func parseOrigins(raw string, defaults ...string) []string {
	values := parseCSV(raw)
	if len(values) == 0 {
		values = defaults
	}
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, value := range parts {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func parseFloatDefault(s string, def float64) float64 {
	if s == "" {
		return def
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return def
	}
	return f
}

func parseDurationDefault(s string, def time.Duration) time.Duration {
	value := strings.TrimSpace(s)
	if value == "" {
		return def
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return def
	}
	return parsed
}
