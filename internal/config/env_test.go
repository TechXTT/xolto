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
