package config

import (
	"fmt"
	"log"
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
	// Stripe price IDs after the W18 tier rename (2026-04-25):
	//   StripeBuyerPriceID = mid tier (display "Buyer", internal slug "pro").
	//   StripeProPriceID   = top tier (display "Pro",   internal slug "power").
	// Resolved from env via resolveTierStripePriceIDs which handles the legacy
	// STRIPE_PRO_PRICE_ID / STRIPE_POWER_PRICE_ID names with a migration-state
	// detection so the env-var meaning shift can't misroute checkouts.
	StripeBuyerPriceID string
	StripeProPriceID   string
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
	// DebugScorerAttribution enables the scoreContributions field on /matches
	// items for users with operator-or-above access. Internal-only for VAL-2
	// calibration. Default: false (prod-safe). Enable via SCORER_ATTRIBUTION_DEBUG=true.
	// Fail-safe posture: unset means OFF. Must be explicitly enabled.
	DebugScorerAttribution bool // SCORER_ATTRIBUTION_DEBUG
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
		// W18 (2026-04-25) tier rename: the env var STRIPE_PRO_PRICE_ID changed
		// meaning (was mid-tier, now top-tier). Use resolveTierStripePriceIDs to
		// disambiguate legacy vs migrated state safely; see helper for details.
		StripeBuyerPriceID:  "", // overwritten by resolveTierStripePriceIDs below
		StripeProPriceID:    "", // overwritten by resolveTierStripePriceIDs below
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
	// W18 tier rename Stripe price ID resolution.
	cfg.StripeBuyerPriceID, cfg.StripeProPriceID = resolveTierStripePriceIDs()
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
	// Fail-safe: attribution debug is OFF unless explicitly set to true.
	cfg.DebugScorerAttribution = parseBoolDefault(os.Getenv("SCORER_ATTRIBUTION_DEBUG"), false)
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

// resolveTierStripePriceIDs returns (buyerPriceID, proPriceID) for the W18
// (2026-04-25) tier rename. The env-var rename is semantically tricky because
// the env var STRIPE_PRO_PRICE_ID changed meaning:
//
//	old:  STRIPE_PRO_PRICE_ID   = mid-tier price.   STRIPE_POWER_PRICE_ID = top-tier price.
//	new:  STRIPE_BUYER_PRICE_ID = mid-tier ("Buyer"). STRIPE_PRO_PRICE_ID = top-tier ("Pro").
//
// Migration state is detected by the presence of STRIPE_BUYER_PRICE_ID:
//
//   - If STRIPE_BUYER_PRICE_ID is set, the operator has migrated env var names.
//     Read STRIPE_BUYER_PRICE_ID for mid tier and STRIPE_PRO_PRICE_ID for top tier
//     under the NEW semantics. STRIPE_POWER_PRICE_ID is ignored and (if set)
//     warned about as a stale legacy value.
//
//   - If STRIPE_BUYER_PRICE_ID is unset, fall back to LEGACY semantics:
//     STRIPE_PRO_PRICE_ID = mid tier (Buyer), STRIPE_POWER_PRICE_ID = top tier (Pro).
//     Log a [WARN] line directing the operator to rename Railway env vars.
//
// This avoids the "STRIPE_PRO_PRICE_ID was the mid tier; now the new code reads
// it as the top tier" misroute that a naive per-field fallback would cause.
func resolveTierStripePriceIDs() (buyerPriceID, proPriceID string) {
	buyerNew := strings.TrimSpace(os.Getenv("STRIPE_BUYER_PRICE_ID"))
	proLegacy := strings.TrimSpace(os.Getenv("STRIPE_PRO_PRICE_ID"))   // semantics depend on migration state
	powerLegacy := strings.TrimSpace(os.Getenv("STRIPE_POWER_PRICE_ID"))

	if buyerNew != "" {
		// Migrated state: use new env-var meanings.
		if powerLegacy != "" {
			log.Printf("[WARN] config: STRIPE_POWER_PRICE_ID is set but ignored after the W18 tier rename. Remove it from Railway; the top-tier price ID now lives in STRIPE_PRO_PRICE_ID.")
		}
		return buyerNew, proLegacy
	}
	// Legacy state: fall back, warn the operator to rename.
	if proLegacy != "" || powerLegacy != "" {
		log.Printf("[WARN] config: STRIPE_BUYER_PRICE_ID is unset; falling back to legacy STRIPE_PRO_PRICE_ID (mid tier) and STRIPE_POWER_PRICE_ID (top tier). Rename Railway env vars: STRIPE_PRO_PRICE_ID -> STRIPE_BUYER_PRICE_ID and STRIPE_POWER_PRICE_ID -> STRIPE_PRO_PRICE_ID. New semantic mapping: display \"Buyer\" = mid tier (internal slug \"pro\"); display \"Pro\" = top tier (internal slug \"power\").")
	}
	return proLegacy, powerLegacy
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
