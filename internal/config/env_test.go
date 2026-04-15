package config

import (
	"reflect"
	"testing"
)

func TestLoadServerConfigPrefersAllowedOrigins(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
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
