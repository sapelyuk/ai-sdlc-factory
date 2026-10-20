package config

import "testing"

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
