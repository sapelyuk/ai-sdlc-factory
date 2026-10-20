package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PORT", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("JWT_DURATION", "")
	t.Setenv("DEPLOYMENT_PROFILE", "")
	t.Setenv("ALLOW_LEGACY_AUTH", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseURL == "" {
		t.Error("DatabaseURL is empty, want default DSN")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.JWTDuration != 24*time.Hour {
		t.Errorf("JWTDuration = %v, want 24h", cfg.JWTDuration)
	}
	if cfg.DeploymentProfile != "prod" {
		t.Errorf("DeploymentProfile = %q, want %q", cfg.DeploymentProfile, "prod")
	}
	if cfg.AllowLegacyAuth {
		t.Error("AllowLegacyAuth = true, want false by default")
	}
	if err := cfg.ValidateAPIAuth(); err != nil {
		t.Errorf("ValidateAPIAuth() error = %v, want nil", err)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for invalid PORT")
	}
}

func TestLoadRejectsInvalidJWTDuration(t *testing.T) {
	t.Setenv("JWT_DURATION", "forever")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for invalid JWT_DURATION")
	}
}

func TestValidateAPIAuthRequiresJWTSecret(t *testing.T) {
	t.Setenv("JWT_SECRET", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.ValidateAPIAuth(); err == nil {
		t.Fatal("ValidateAPIAuth() error = nil, want error when JWT_SECRET is missing")
	}
}

func TestLoadAllowsLegacyAuthOnlyInLocalDev(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("ALLOW_LEGACY_AUTH", "true")
	t.Setenv("DEPLOYMENT_PROFILE", "local-full")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error when legacy auth is enabled outside local-dev")
	}
}

func TestLoadAllowsLegacyAuthInLocalDev(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	t.Setenv("ALLOW_LEGACY_AUTH", "true")
	t.Setenv("DEPLOYMENT_PROFILE", "local-dev")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.AllowLegacyAuth {
		t.Fatal("AllowLegacyAuth = false, want true")
	}
}
