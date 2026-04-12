package config

import (
	"fmt"
	"os"
	"strings"
)

type ServerConfig struct {
	Address             string
	DatabaseURL         string
	JWTSecret           string
	StripeSecret        string
	StripeWebhookSecret string
	StripeProPriceID    string
	StripeTeamPriceID   string
	AppBaseURL          string
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
}

func LoadServerConfigFromEnv() (ServerConfig, error) {
	cfg := ServerConfig{
		Address:             getenvDefault("SERVER_ADDR", ":8000"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		StripeSecret:        os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripeProPriceID:    os.Getenv("STRIPE_PRO_PRICE_ID"),
		StripeTeamPriceID:   os.Getenv("STRIPE_TEAM_PRICE_ID"),
		AppBaseURL:          getenvDefault("APP_BASE_URL", "http://localhost:3000"),
		AIAPIKey:            os.Getenv("AI_API_KEY"),
		AIBaseURL:           getenvDefault("AI_BASE_URL", "https://api.openai.com/v1"),
		AIModel:             getenvDefault("AI_MODEL", "gpt-4o-mini"),
		SMTPHost:            os.Getenv("SMTP_HOST"),
		SMTPPort:            getenvDefault("SMTP_PORT", "587"),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPass:            os.Getenv("SMTP_PASS"),
		SMTPFrom:            getenvDefault("SMTP_FROM", "alerts@marktbot.app"),
		AlertScore:          parseFloatDefault(os.Getenv("ALERT_SCORE"), 8.0),
		AdminEmails:         parseAdminEmails(os.Getenv("ADMIN_EMAILS")),
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "marktbot-server.db"
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
	if s == "" {
		return nil
	}
	var out []string
	for _, e := range strings.Split(s, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e != "" {
			out = append(out, e)
		}
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
