package config

import (
	"testing"
	"time"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "DATABASE_URL", "OPA_URL", "POLICY_DIR", "SCHEMA_DIR",
		"VAULT_ADDR", "VAULT_TOKEN", "VAULT_SECRET_MOUNT", "VAULT_CACHE_TTL",
		"POLICY_TIMEOUT", "MCP_UPSTREAMS",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8030 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if cfg.OPAURL != "http://localhost:8181" {
		t.Fatalf("opa url = %q", cfg.OPAURL)
	}
	if cfg.VaultCacheTTL != time.Minute {
		t.Fatalf("cache ttl = %v", cfg.VaultCacheTTL)
	}
	if cfg.PolicyTimeout != 5*time.Second {
		t.Fatalf("policy timeout = %v", cfg.PolicyTimeout)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("OPA_URL", "embedded")
	t.Setenv("POLICY_DIR", "/policies")
	t.Setenv("VAULT_CACHE_TTL", "30s")
	t.Setenv("POLICY_TIMEOUT", "2s")
	t.Setenv("MCP_UPSTREAMS", "*=http://sandbox:9000/mcp")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 9090 {
		t.Fatalf("port = %d", cfg.Port)
	}
	if cfg.OPAURL != "" {
		t.Fatalf("opa url = %q, want empty", cfg.OPAURL)
	}
	if cfg.PolicyDir != "/policies" {
		t.Fatalf("policy dir = %q", cfg.PolicyDir)
	}
	if cfg.VaultCacheTTL != 30*time.Second {
		t.Fatalf("cache ttl = %v", cfg.VaultCacheTTL)
	}
	if cfg.PolicyTimeout != 2*time.Second {
		t.Fatalf("policy timeout = %v", cfg.PolicyTimeout)
	}
	if cfg.MCPUpstreams != "*=http://sandbox:9000/mcp" {
		t.Fatalf("mcp upstreams = %q", cfg.MCPUpstreams)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{"PORT", "not-a-number"},
		{"PORT", "0"},
		{"PORT", "70000"},
		{"POLICY_TIMEOUT", "soon"},
		{"POLICY_TIMEOUT", "0s"},
		{"VAULT_CACHE_TTL", "-1s"},
	}
	for _, tc := range tests {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
