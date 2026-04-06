package config

import (
	"fmt"
	"os"
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
}

func LoadServerConfigFromEnv() (ServerConfig, error) {
	cfg := ServerConfig{
		Address:             getenvDefault("SERVER_ADDR", ":8080"),
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
	}
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "marktbot-server.db"
	}
	if cfg.JWTSecret == "" {
		return cfg, fmt.Errorf("JWT_SECRET is required")
	}
	return cfg, nil
}

func getenvDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
