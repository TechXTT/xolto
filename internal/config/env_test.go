package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadServerConfigPrefersAllowedOrigins(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_BASE_URL", "http://localhost:3000")
	t.Setenv("ADMIN_BASE_URL", "http://localhost:3002")
	t.Setenv("ALLOWED_ORIGINS", "https://dash.xolto.app,https://admin.xolto.app")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://legacy.xolto.app")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadServerConfigFromEnv() error = %v", err)
	}

	want := []string{"https://dash.xolto.app", "https://admin.xolto.app"}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, want) {
		t.Fatalf("expected CORSAllowedOrigins=%v, got %v", want, cfg.CORSAllowedOrigins)
	}
}

func TestLoadServerConfigFallsBackToLegacyOrigins(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_BASE_URL", "http://localhost:3000")
	t.Setenv("ADMIN_BASE_URL", "http://localhost:3002")
	t.Setenv("ALLOWED_ORIGINS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://legacy-app.xolto.app,https://legacy-admin.xolto.app")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadServerConfigFromEnv() error = %v", err)
	}

	want := []string{"https://legacy-app.xolto.app", "https://legacy-admin.xolto.app"}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, want) {
		t.Fatalf("expected CORSAllowedOrigins=%v, got %v", want, cfg.CORSAllowedOrigins)
	}
}

func TestLoadServerConfigUsesAppAndAdminDefaultsForOrigins(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_BASE_URL", "https://dash.xolto.app/")
	t.Setenv("ADMIN_BASE_URL", "https://admin.xolto.app/")
	t.Setenv("ALLOWED_ORIGINS", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadServerConfigFromEnv() error = %v", err)
	}

	want := []string{"https://dash.xolto.app", "https://admin.xolto.app"}
	if !reflect.DeepEqual(cfg.CORSAllowedOrigins, want) {
		t.Fatalf("expected CORSAllowedOrigins=%v, got %v", want, cfg.CORSAllowedOrigins)
	}
}

// ---------------------------------------------------------------------------
// AC-5: PLAIN_API_KEY env-gate (XOL-53 SUP-2)
// ---------------------------------------------------------------------------

// TestPlainAPIKeyRequiredInProduction verifies that LoadServerConfigFromEnv
// refuses to load when PLAIN_API_KEY is unset and APP_ENV is "production".
func TestPlainAPIKeyRequiredInProduction(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "production")
	t.Setenv("PLAIN_API_KEY", "")

	_, err := LoadServerConfigFromEnv()
	if err == nil {
		t.Fatal("expected error when PLAIN_API_KEY is unset in production, got nil")
	}
	if !strings.Contains(err.Error(), "PLAIN_API_KEY") {
		t.Fatalf("expected error message to mention PLAIN_API_KEY, got %q", err.Error())
	}
}

// TestPlainAPIKeyRequiredWhenAppEnvUnset verifies the fail-safe: an unset
// APP_ENV should also require PLAIN_API_KEY (treats unset as production).
func TestPlainAPIKeyRequiredWhenAppEnvUnset(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "")
	t.Setenv("PLAIN_API_KEY", "")

	_, err := LoadServerConfigFromEnv()
	if err == nil {
		t.Fatal("expected error when PLAIN_API_KEY is unset and APP_ENV is empty (prod-safe default), got nil")
	}
	if !strings.Contains(err.Error(), "PLAIN_API_KEY") {
		t.Fatalf("expected error message to mention PLAIN_API_KEY, got %q", err.Error())
	}
}

// TestPlainAPIKeyOptionalInDev verifies that PLAIN_API_KEY is not required
// when APP_ENV is set to a recognised non-production value.
func TestPlainAPIKeyOptionalInDev(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("PLAIN_API_KEY", "")

	_, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("expected no error when PLAIN_API_KEY is unset in dev, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Twilio env-gate tests (XOL-56 SUP-5)
// ---------------------------------------------------------------------------

// setTwilioEnvVars is a helper that sets all four Twilio env vars.
func setTwilioEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("TWILIO_ACCOUNT_SID", "ACtest")
	t.Setenv("TWILIO_AUTH_TOKEN", "authtoken")
	t.Setenv("TWILIO_FROM_NUMBER", "+15550001111")
	t.Setenv("FOUNDER_SMS_NUMBER", "+15550002222")
}

// setClassifierEnvVars is a helper that sets all classifier env vars
// required in production (SUP-10: PLAIN_MCP_TOKEN retired; Plain calls now
// route through PLAIN_API_KEY via GraphQL).
func setClassifierEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("LINEAR_API_KEY", "lin_key")
}

// TestTwilioVarsRequiredInProduction verifies all four vars are required in prod.
func TestTwilioVarsRequiredInProduction(t *testing.T) {
	for _, tc := range []struct {
		name     string
		skip     string // env var to leave unset
		wantMsg  string
	}{
		{"missing_sid", "TWILIO_ACCOUNT_SID", "TWILIO_ACCOUNT_SID"},
		{"missing_token", "TWILIO_AUTH_TOKEN", "TWILIO_AUTH_TOKEN"},
		{"missing_from", "TWILIO_FROM_NUMBER", "TWILIO_FROM_NUMBER"},
		{"missing_founder", "FOUNDER_SMS_NUMBER", "FOUNDER_SMS_NUMBER"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", "test-secret")
			t.Setenv("APP_ENV", "production")
			t.Setenv("PLAIN_API_KEY", "plain-key")
			setTwilioEnvVars(t)
			setClassifierEnvVars(t)
			t.Setenv(tc.skip, "")

			_, err := LoadServerConfigFromEnv()
			if err == nil {
				t.Fatalf("expected error when %s is unset in production, got nil", tc.skip)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("expected error to mention %q, got %q", tc.wantMsg, err.Error())
			}
		})
	}
}

// TestTwilioVarsRequiredWhenAppEnvUnset verifies fail-safe: unset APP_ENV
// triggers the Twilio gate.
func TestTwilioVarsRequiredWhenAppEnvUnset(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "")
	t.Setenv("PLAIN_API_KEY", "plain-key")
	// Leave Twilio vars unset.

	_, err := LoadServerConfigFromEnv()
	if err == nil {
		t.Fatal("expected error when Twilio vars are unset and APP_ENV is empty (prod-safe default), got nil")
	}
	if !strings.Contains(err.Error(), "TWILIO_ACCOUNT_SID") {
		t.Fatalf("expected error to mention TWILIO_ACCOUNT_SID, got %q", err.Error())
	}
}

// TestTwilioVarsOptionalInDev verifies Twilio vars are not required in dev.
func TestTwilioVarsOptionalInDev(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("PLAIN_API_KEY", "")
	// Twilio vars deliberately unset.

	_, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("expected no error when Twilio vars are unset in dev, got %v", err)
	}
}

// TestTwilioVarsAllPresentInProduction verifies server starts cleanly when all
// four Twilio vars are present in production.
func TestTwilioVarsAllPresentInProduction(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "production")
	t.Setenv("PLAIN_API_KEY", "plain-key")
	setTwilioEnvVars(t)
	setClassifierEnvVars(t)

	_, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("expected no error when all Twilio vars are set in production, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Classifier env-gate tests (XOL-55 SUP-4)
// ---------------------------------------------------------------------------

// TestClassifierVarsRequiredInProduction verifies that each required classifier
// infra var causes a startup failure when absent in production (XOL-59 SUP-8).
// PLAIN_MCP_TOKEN is retired (SUP-10); the classifier uses PLAIN_API_KEY (GraphQL).
func TestClassifierVarsRequiredInProduction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		skip    string
		wantMsg string
	}{
		{"missing_linear_api_key", "LINEAR_API_KEY", "LINEAR_API_KEY"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("JWT_SECRET", "test-secret")
			t.Setenv("APP_ENV", "production")
			t.Setenv("PLAIN_API_KEY", "plain-key")
			setTwilioEnvVars(t)
			setClassifierEnvVars(t)
			t.Setenv(tc.skip, "") // unset the var under test

			_, err := LoadServerConfigFromEnv()
			if err == nil {
				t.Fatalf("expected error when %s is unset in production, got nil", tc.skip)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("expected error to mention %q, got %q", tc.wantMsg, err.Error())
			}
		})
	}
}

// TestClassifierVarsOptionalInDev verifies that the classifier infra vars are
// not required when APP_ENV is a recognised non-production value.
// PLAIN_MCP_TOKEN is retired (SUP-10).
func TestClassifierVarsOptionalInDev(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("PLAIN_API_KEY", "")
	// Classifier infra vars deliberately unset.
	t.Setenv("LINEAR_API_KEY", "")

	_, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("expected no error when classifier vars are unset in dev, got %v", err)
	}
}

// TestSupportClassifierWorkersDefault verifies the default value of 2 when
// SUPPORT_CLASSIFIER_WORKERS is not set.
func TestSupportClassifierWorkersDefault(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("SUPPORT_CLASSIFIER_WORKERS", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SupportClassifierWorkers != 2 {
		t.Errorf("expected SupportClassifierWorkers=2 (default), got %d", cfg.SupportClassifierWorkers)
	}
}

// TestSupportClassifierWorkersInvalidInt verifies that an invalid integer
// for SUPPORT_CLASSIFIER_WORKERS falls back to the default (2) and does not
// cause a startup error.
func TestSupportClassifierWorkersInvalidInt(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("SUPPORT_CLASSIFIER_WORKERS", "not-a-number")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error on invalid SUPPORT_CLASSIFIER_WORKERS: %v", err)
	}
	// parseIntDefault returns the default when the value is not a valid integer.
	if cfg.SupportClassifierWorkers != 2 {
		t.Errorf("expected SupportClassifierWorkers=2 (fallback on invalid), got %d", cfg.SupportClassifierWorkers)
	}
}

// ---------------------------------------------------------------------------
// AIModelClassifier env tests (XOL-59 SUP-8)
// ---------------------------------------------------------------------------

// TestAIModelClassifierDefault verifies that AI_MODEL_CLASSIFIER defaults to
// "gpt-5-nano" when the env var is not set.
func TestAIModelClassifierDefault(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("AI_MODEL_CLASSIFIER", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelClassifier != "gpt-5-nano" {
		t.Errorf("expected AIModelClassifier=gpt-5-nano (default), got %q", cfg.AIModelClassifier)
	}
}

// TestAIModelClassifierOverride verifies that AI_MODEL_CLASSIFIER is
// respected when explicitly set, allowing per-call-site model selection.
func TestAIModelClassifierOverride(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
	t.Setenv("AI_MODEL_CLASSIFIER", "gpt-4o-mini")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelClassifier != "gpt-4o-mini" {
		t.Errorf("expected AIModelClassifier=gpt-4o-mini, got %q", cfg.AIModelClassifier)
	}
}

// ---------------------------------------------------------------------------
// Per-call-site AI_MODEL_* env tests (XOL-60 SUP-9)
// ---------------------------------------------------------------------------

// setBaseDevEnv sets the minimum env vars required for a dev-env config load.
func setBaseDevEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("APP_ENV", "development")
}

// TestAIModelScorerFallthrough verifies that AI_MODEL_SCORER falls through to
// AI_MODEL when unset (XOL-60 SUP-9 AC).
func TestAIModelScorerFallthrough(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_SCORER", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelScorer != "gpt-4o-mini" {
		t.Errorf("expected AIModelScorer=gpt-4o-mini (fallthrough to AI_MODEL), got %q", cfg.AIModelScorer)
	}
}

// TestAIModelScorerOverride verifies that AI_MODEL_SCORER is respected when set.
func TestAIModelScorerOverride(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_SCORER", "gpt-5-mini")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelScorer != "gpt-5-mini" {
		t.Errorf("expected AIModelScorer=gpt-5-mini, got %q", cfg.AIModelScorer)
	}
}

// TestAIModelGeneratorFallthrough verifies AI_MODEL_GENERATOR falls through.
func TestAIModelGeneratorFallthrough(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_GENERATOR", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelGenerator != "gpt-4o-mini" {
		t.Errorf("expected AIModelGenerator=gpt-4o-mini (fallthrough to AI_MODEL), got %q", cfg.AIModelGenerator)
	}
}

// TestAIModelGeneratorOverride verifies AI_MODEL_GENERATOR is respected when set.
func TestAIModelGeneratorOverride(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_GENERATOR", "gpt-5-nano")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelGenerator != "gpt-5-nano" {
		t.Errorf("expected AIModelGenerator=gpt-5-nano, got %q", cfg.AIModelGenerator)
	}
}

// TestAIModelAssistantBriefFallthrough verifies AI_MODEL_ASSISTANT_BRIEF falls through.
func TestAIModelAssistantBriefFallthrough(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_ASSISTANT_BRIEF", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelAssistantBrief != "gpt-4o-mini" {
		t.Errorf("expected AIModelAssistantBrief=gpt-4o-mini (fallthrough to AI_MODEL), got %q", cfg.AIModelAssistantBrief)
	}
}

// TestAIModelAssistantBriefOverride verifies AI_MODEL_ASSISTANT_BRIEF is respected.
func TestAIModelAssistantBriefOverride(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_ASSISTANT_BRIEF", "gpt-5-mini")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelAssistantBrief != "gpt-5-mini" {
		t.Errorf("expected AIModelAssistantBrief=gpt-5-mini, got %q", cfg.AIModelAssistantBrief)
	}
}

// TestAIModelAssistantDraftFallthrough verifies AI_MODEL_ASSISTANT_DRAFT falls through.
func TestAIModelAssistantDraftFallthrough(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_ASSISTANT_DRAFT", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelAssistantDraft != "gpt-4o-mini" {
		t.Errorf("expected AIModelAssistantDraft=gpt-4o-mini (fallthrough to AI_MODEL), got %q", cfg.AIModelAssistantDraft)
	}
}

// TestAIModelAssistantDraftOverride verifies AI_MODEL_ASSISTANT_DRAFT is respected.
func TestAIModelAssistantDraftOverride(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_ASSISTANT_DRAFT", "gpt-5-mini")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelAssistantDraft != "gpt-5-mini" {
		t.Errorf("expected AIModelAssistantDraft=gpt-5-mini, got %q", cfg.AIModelAssistantDraft)
	}
}

// TestAIModelAssistantChatFallthrough verifies AI_MODEL_ASSISTANT_CHAT falls through.
func TestAIModelAssistantChatFallthrough(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_ASSISTANT_CHAT", "")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelAssistantChat != "gpt-4o-mini" {
		t.Errorf("expected AIModelAssistantChat=gpt-4o-mini (fallthrough to AI_MODEL), got %q", cfg.AIModelAssistantChat)
	}
}

// TestAIModelAssistantChatOverride verifies AI_MODEL_ASSISTANT_CHAT is respected.
func TestAIModelAssistantChatOverride(t *testing.T) {
	setBaseDevEnv(t)
	t.Setenv("AI_MODEL", "gpt-4o-mini")
	t.Setenv("AI_MODEL_ASSISTANT_CHAT", "gpt-5-mini")

	cfg, err := LoadServerConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AIModelAssistantChat != "gpt-5-mini" {
		t.Errorf("expected AIModelAssistantChat=gpt-5-mini, got %q", cfg.AIModelAssistantChat)
	}
}
