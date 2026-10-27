// Package config loads Tool Gateway configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// Port is the HTTP listen port (default 8030).
	Port int

	// DatabaseURL is the PostgreSQL DSN used for the audit log.
	DatabaseURL string

	// OPAURL is the base URL of the OPA server. When empty, the policy
	// engine evaluates the Rego files from PolicyDir in-process.
	OPAURL string

	// PolicyDir contains the Rego policy files for the in-process engine.
	PolicyDir string

	// SchemaDir contains the MCP tool schemas.
	SchemaDir string

	// VaultAddr is the Vault/OpenBao address used for credential brokering.
	VaultAddr string

	// VaultToken authenticates the Tool Gateway to Vault. When empty,
	// credential brokering is disabled and tools that need a secret fail.
	VaultToken string

	// VaultSecretMount is the KV mount used by secret requirements.
	VaultSecretMount string

	// VaultCacheTTL is how long fetched secrets are cached in memory.
	VaultCacheTTL time.Duration

	// PolicyTimeout bounds every OPA request.
	PolicyTimeout time.Duration

	// MCPUpstreams routes tools to upstream MCP servers. The raw value is a
	// comma-separated list of tool=url pairs; "*" is the fallback route.
	// When empty, the gateway runs with the stub executor.
	MCPUpstreams string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	port, err := envInt("PORT", 8030)
	if err != nil {
		return nil, err
	}
	timeout, err := envDuration("POLICY_TIMEOUT", 5*time.Second)
	if err != nil {
		return nil, err
	}
	cacheTTL, err := envDuration("VAULT_CACHE_TTL", time.Minute)
	if err != nil {
		return nil, err
	}

	opaURL := env("OPA_URL", "http://localhost:8181")
	if opaURL == "embedded" || opaURL == "in-process" {
		// Explicit opt-in for the in-process Rego engine (tests, local runs
		// without an OPA container).
		opaURL = ""
	}

	return &Config{
		Port:             port,
		DatabaseURL:      env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable"),
		OPAURL:           opaURL,
		PolicyDir:        env("POLICY_DIR", "packages/policy"),
		SchemaDir:        env("SCHEMA_DIR", "packages/contracts/mcp-schemas"),
		VaultAddr:        env("VAULT_ADDR", "http://localhost:8200"),
		VaultToken:       os.Getenv("VAULT_TOKEN"),
		VaultSecretMount: env("VAULT_SECRET_MOUNT", "secret"),
		VaultCacheTTL:    cacheTTL,
		PolicyTimeout:    timeout,
		MCPUpstreams:     os.Getenv("MCP_UPSTREAMS"),
	}, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if v < 1 || v > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", key)
	}
	return v, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return v, nil
}
