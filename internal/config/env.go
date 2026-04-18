package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ServerConfig struct {
	Address             string
	DatabaseURL         string
	DBMaxOpenConns      int
	DBMaxIdleConns      int
	DBConnMaxLifetime   time.Duration
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
	AIPromptVersion     int
	SMTPHost            string
	SMTPPort            string
	SMTPUser            string
	SMTPPass            string
	SMTPFrom            string
	AlertScore          float64
	AdminEmails         []string
	AdminIPAllowlist    []string
	TrustProxy          bool
	HTTPTimeouts        HTTPTimeouts
	// Support platform (XOL-53 SUP-2).
	PlainAPIKey         string
	PlainWebhookSecret  string
	AppEnv              string
	// SMS escalation (XOL-56 SUP-5).
	TwilioAccountSID  string
	TwilioAuthToken   string
	TwilioFromNumber  string
	FounderSMSNumber  string
	// Support classifier (XOL-59 SUP-8, MCP retired SUP-10).
	// LinearAPIKey is required in production.
	// AIModelClassifier is the per-call-site model override for the classifier
	// worker; it falls through to AIModel if unset.
	// The classifier now routes all Plain calls through the GraphQL client
	// using PLAIN_API_KEY (same credential as SUP-2).
	LinearAPIKey             string
	AIModelClassifier        string
	SupportClassifierWorkers int
	// Per-call-site AI model overrides (XOL-60 SUP-9).
	// Each falls through to AIModel when unset so no Railway provisioning
	// is required for the PR to be safe to merge.
	AIModelScorer          string // AI_MODEL_SCORER
	AIModelGenerator       string // AI_MODEL_GENERATOR
	AIModelAssistantBrief  string // AI_MODEL_ASSISTANT_BRIEF
	AIModelAssistantDraft  string // AI_MODEL_ASSISTANT_DRAFT
	AIModelAssistantChat   string // AI_MODEL_ASSISTANT_CHAT
	// Must-have semantic evaluator (XOL-22).
	// AIModelMustHave falls through AI_MODEL_MUSTHAVE → AI_MODEL → "gpt-5-nano".
	// AIMaxMustHaveCallsPerMissionPerHour caps per-mission LLM calls per hour;
	// default 200. Both are optional — no Railway provisioning required.
	AIModelMustHave                    string // AI_MODEL_MUSTHAVE
	AIMaxMustHaveCallsPerMissionPerHour int    // AI_MAX_MUSTHAVE_CALLS_PER_MISSION_PER_HOUR
}

func LoadServerConfigFromEnv() (ServerConfig, error) {
	cfg := ServerConfig{
		Address:             getenvDefault("SERVER_ADDR", ":8000"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		DBMaxOpenConns:      parseIntDefault(os.Getenv("DB_MAX_OPEN_CONNS"), 25),
		DBMaxIdleConns:      parseIntDefault(os.Getenv("DB_MAX_IDLE_CONNS"), 5),
		DBConnMaxLifetime:   parseDurationDefault(os.Getenv("DB_CONN_MAX_LIFETIME"), 30*time.Minute),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		GoogleClientID:      os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:   os.Getenv("GOOGLE_REDIRECT_URL"),
		StripeSecret:        os.Getenv("STRIPE_SECRET_KEY"),
		StripeWebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		StripeProPriceID:    os.Getenv("STRIPE_PRO_PRICE_ID"),
		StripePowerPriceID:  os.Getenv("STRIPE_POWER_PRICE_ID"),
		AppBaseURL:          getenvDefault("APP_BASE_URL", "http://localhost:3000"),
		AdminBaseURL:        getenvDefault("ADMIN_BASE_URL", "http://localhost:3002"),
		AIAPIKey:            os.Getenv("AI_API_KEY"),
		AIBaseURL:           getenvDefault("AI_BASE_URL", "https://api.openai.com/v1"),
		AIModel:             getenvDefault("AI_MODEL", "gpt-4o-mini"),
		AIPromptVersion:     parseIntDefault(os.Getenv("AI_PROMPT_VERSION"), 1),
		SMTPHost:            os.Getenv("SMTP_HOST"),
		SMTPPort:            getenvDefault("SMTP_PORT", "587"),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPass:            os.Getenv("SMTP_PASS"),
		SMTPFrom:            getenvDefault("SMTP_FROM", "alerts@xolto.app"),
		AlertScore:          parseFloatDefault(os.Getenv("ALERT_SCORE"), 8.0),
		AdminEmails:         parseAdminEmails(os.Getenv("ADMIN_EMAILS")),
		AdminIPAllowlist:    parseCSV(os.Getenv("ADMIN_IP_ALLOWLIST")),
		TrustProxy:          parseBoolDefault(os.Getenv("TRUST_PROXY"), false),
		HTTPTimeouts: HTTPTimeouts{
			ReadTimeout:       parseDurationDefault(os.Getenv("SERVER_READ_TIMEOUT"), defaultServerReadTimeout),
			WriteTimeout:      parseDurationDefault(os.Getenv("SERVER_WRITE_TIMEOUT"), defaultServerWriteTimeout),
			IdleTimeout:       parseDurationDefault(os.Getenv("SERVER_IDLE_TIMEOUT"), defaultServerIdleTimeout),
			ReadHeaderTimeout: parseDurationDefault(os.Getenv("SERVER_READ_HEADER_TIMEOUT"), defaultServerReadHeaderTimeout),
		},
		// Support platform (XOL-53 SUP-2).
		PlainAPIKey:        os.Getenv("PLAIN_API_KEY"),
		PlainWebhookSecret: os.Getenv("PLAIN_WEBHOOK_SECRET"),
		AppEnv:             os.Getenv("APP_ENV"),
		// SMS escalation (XOL-56 SUP-5).
		TwilioAccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
		TwilioAuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
		TwilioFromNumber: os.Getenv("TWILIO_FROM_NUMBER"),
		FounderSMSNumber: os.Getenv("FOUNDER_SMS_NUMBER"),
		// Support classifier (XOL-59 SUP-8, MCP retired SUP-10): uses the shared
		// OpenAI-compatible AI_API_KEY + AI_BASE_URL path. AI_MODEL_CLASSIFIER is
		// the per-call-site model override; falls through to AIModel if unset.
		// Plain calls route through PLAIN_API_KEY (GraphQL) — PLAIN_MCP_TOKEN removed.
		LinearAPIKey:             os.Getenv("LINEAR_API_KEY"),
		AIModelClassifier:        getenvDefault("AI_MODEL_CLASSIFIER", "gpt-5-nano"),
		SupportClassifierWorkers: parseIntDefault(os.Getenv("SUPPORT_CLASSIFIER_WORKERS"), 2),
	}
	// Per-call-site AI model overrides (XOL-60 SUP-9). All default to
	// AIModel so Railway provisioning can happen post-merge independently.
	cfg.AIModelScorer = getenvDefault("AI_MODEL_SCORER", cfg.AIModel)
	cfg.AIModelGenerator = getenvDefault("AI_MODEL_GENERATOR", cfg.AIModel)
	cfg.AIModelAssistantBrief = getenvDefault("AI_MODEL_ASSISTANT_BRIEF", cfg.AIModel)
	cfg.AIModelAssistantDraft = getenvDefault("AI_MODEL_ASSISTANT_DRAFT", cfg.AIModel)
	cfg.AIModelAssistantChat = getenvDefault("AI_MODEL_ASSISTANT_CHAT", cfg.AIModel)
	// Must-have semantic evaluator (XOL-22). Defaults to AI_MODEL then "gpt-5-nano";
	// no Railway provisioning required for safe merge.
	cfg.AIModelMustHave = getenvDefault("AI_MODEL_MUSTHAVE", getenvDefault("AI_MODEL", "gpt-5-nano"))
	cfg.AIMaxMustHaveCallsPerMissionPerHour = parseIntDefault(os.Getenv("AI_MAX_MUSTHAVE_CALLS_PER_MISSION_PER_HOUR"), 200)
	if cfg.DBMaxOpenConns <= 0 {
		cfg.DBMaxOpenConns = 25
	}
	if cfg.DBMaxIdleConns < 0 {
		cfg.DBMaxIdleConns = 5
	}
	if cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
		cfg.DBMaxIdleConns = cfg.DBMaxOpenConns
	}
	if cfg.DBConnMaxLifetime <= 0 {
		cfg.DBConnMaxLifetime = 30 * time.Minute
	}
	cfg.CORSAllowedOrigins = parseAllowedOrigins(cfg)
	if cfg.DatabaseURL == "" {
		cfg.DatabaseURL = "xolto-server.db"
	}
	if cfg.JWTSecret == "" {
		return cfg, fmt.Errorf("JWT_SECRET is required")
	}
	// Fail-safe env gate: PLAIN_API_KEY is required unless APP_ENV is explicitly
	// set to a non-production value (dev, test, staging, etc.).
	// Unset APP_ENV defaults to prod-safe behaviour.
	if cfg.PlainAPIKey == "" && isProductionEnv(cfg.AppEnv) {
		return cfg, fmt.Errorf("PLAIN_API_KEY is required in production")
	}
	// Fail-safe env gate: Twilio SMS vars are required in production (XOL-56 SUP-5).
	// Unset APP_ENV is treated as production (fail-safe default).
	if isProductionEnv(cfg.AppEnv) {
		if cfg.TwilioAccountSID == "" {
			return cfg, fmt.Errorf("TWILIO_ACCOUNT_SID is required in production")
		}
		if cfg.TwilioAuthToken == "" {
			return cfg, fmt.Errorf("TWILIO_AUTH_TOKEN is required in production")
		}
		if cfg.TwilioFromNumber == "" {
			return cfg, fmt.Errorf("TWILIO_FROM_NUMBER is required in production")
		}
		if cfg.FounderSMSNumber == "" {
			return cfg, fmt.Errorf("FOUNDER_SMS_NUMBER is required in production")
		}
	}
	// Fail-safe env gate: classifier infra vars are required in production
	// (XOL-59 SUP-8, MCP retired SUP-10). Unset APP_ENV is treated as production.
	// Plain calls now use PLAIN_API_KEY (already validated above); only LINEAR_API_KEY
	// needs its own gate here.
	if isProductionEnv(cfg.AppEnv) {
		if cfg.LinearAPIKey == "" {
			return cfg, fmt.Errorf("LINEAR_API_KEY is required in production")
		}
	}
	// SUPPORT_CLASSIFIER_WORKERS must be a positive integer when set.
	if cfg.SupportClassifierWorkers <= 0 {
		cfg.SupportClassifierWorkers = 2
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

// isProductionEnv returns true when the APP_ENV value should enforce all
// production-required env vars. The only values that opt out are explicit
// non-production names. Unset (empty string) is treated as production to
// ensure a fail-safe default.
func isProductionEnv(appEnv string) bool {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "dev", "development", "test", "testing", "staging", "local":
		return false
	default:
		// "production", "", or any unrecognised value → prod-safe.
		return true
	}
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

func parseAllowedOrigins(cfg ServerConfig) []string {
	allowedOriginsRaw := os.Getenv("ALLOWED_ORIGINS")
	if strings.TrimSpace(allowedOriginsRaw) == "" {
		// Backward-compatible fallback for one release.
		allowedOriginsRaw = os.Getenv("CORS_ALLOWED_ORIGINS")
	}
	return parseOrigins(allowedOriginsRaw, cfg.AppBaseURL, cfg.AdminBaseURL)
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

func parseBoolDefault(s string, def bool) bool {
	value := strings.TrimSpace(strings.ToLower(s))
	switch value {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

func parseIntDefault(s string, def int) int {
	value := strings.TrimSpace(s)
	if value == "" {
		return def
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return def
	}
	return parsed
}
