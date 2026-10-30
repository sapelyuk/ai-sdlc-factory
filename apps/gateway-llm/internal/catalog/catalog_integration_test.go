// Integration coverage for the model catalog: it runs against a real
// PostgreSQL 18 started with testcontainers-go, so the enabled flag, the
// priority ordering and the credential filter behave as in production.
// The test skips when Docker is unavailable outside CI.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// findMigrationsDir walks up from the test working directory until the
// repository migration directory is found.
func findMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "migrations", "postgres", "0010_llm_gateway.up.sql")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Join(dir, "migrations", "postgres")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}

// newCatalogEnv starts PostgreSQL, applies migration 0010 (which creates
// llm_model_profiles and seeds anthropic/openai/ollama rows) and, when asked,
// migration 0015 (google/deepseek/groq seeds, enabled = false).
func newCatalogEnv(t *testing.T, applyProvidersSeed bool) *pgxpool.Pool {
	t.Helper()

	if output, err := exec.CommandContext(envContext(t, 10*time.Second), "docker", "info").CombinedOutput(); err != nil {
		// In CI a missing Docker is a pipeline failure, not a skip.
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for integration tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping catalog integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx := envContext(t, 3*time.Minute)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("catalog_test"),
		postgrescontainer.WithUsername("catalog_test"),
		postgrescontainer.WithPassword("catalog_test"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL 18 container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate PostgreSQL container: %v", err)
		}
	})

	connectionString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get container connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	migrationsDir := findMigrationsDir(t)
	for _, name := range []string{"0010_llm_gateway.up.sql", "0015_seed_llm_providers.up.sql"} {
		if name == "0015_seed_llm_providers.up.sql" && !applyProvidersSeed {
			continue
		}
		sql, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	return pool
}

// envContext returns a canceled-on-cleanup context with the given timeout.
func envContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// setEnabled flips the enabled flag for one provider.
func setEnabled(t *testing.T, pool *pgxpool.Pool, provider string, enabled bool) {
	t.Helper()
	ctx := envContext(t, 30*time.Second)
	tag, err := pool.Exec(ctx, `UPDATE llm_model_profiles SET enabled = $1 WHERE provider = $2`, enabled, provider)
	if err != nil {
		t.Fatalf("enable %s: %v", provider, err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatalf("no llm_model_profiles rows for provider %q", provider)
	}
}

// TestLoadSkipsProvidersWithoutCredentials is the core guarantee of issue #112:
// a seeded row never enters the catalog when its provider has no credentials,
// so the router cannot try to call it.
func TestLoadSkipsProvidersWithoutCredentials(t *testing.T) {
	pool := newCatalogEnv(t, true)
	ctx := envContext(t, time.Minute)

	// 0015 seeds the new providers as disabled; activate them so the only
	// remaining filter is the credential check.
	for _, provider := range []string{"google", "deepseek", "groq"} {
		setEnabled(t, pool, provider, true)
	}

	// Only Anthropic is configured on this deployment.
	cfg := &config.Config{AnthropicAPIKey: "sk-ant"}
	cat := New(pool, cfg)
	if err := cat.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entries, err := cat.For(ctx, "reasoning-strong")
	if err != nil {
		t.Fatalf("For(reasoning-strong): %v", err)
	}
	for _, e := range entries {
		if e.Provider != "anthropic" {
			t.Errorf("profile reasoning-strong contains provider %q, want only anthropic", e.Provider)
		}
	}

	// Ollama has no key: with an empty base URL its rows are skipped too,
	// which is what keeps the local-private profile out of the catalog.
	if _, err := cat.For(ctx, "local-private"); !errors.Is(err, domain.ErrUnknownProfile) {
		t.Errorf("For(local-private) = %v, want ErrUnknownProfile when AI_OLLAMA_BASE_URL is empty", err)
	}
}

// TestLoadOrderingRespectsExistingPriorities pins that activating every
// provider keeps the 0010 rows first: anthropic (0), openai (1), then the new
// providers (>= 2). Existing routing behavior must not change (issue #112).
func TestLoadOrderingRespectsExistingPriorities(t *testing.T) {
	pool := newCatalogEnv(t, true)
	ctx := envContext(t, time.Minute)
	for _, provider := range []string{"google", "deepseek", "groq"} {
		setEnabled(t, pool, provider, true)
	}

	cfg := &config.Config{
		AnthropicAPIKey: "sk-ant",
		OpenAIAPIKey:    "sk-oai",
		GoogleAPIKey:    "sk-google",
		DeepSeekAPIKey:  "sk-ds",
		GroqAPIKey:      "sk-groq",
		OllamaBaseURL:   "http://ollama:11434",
	}
	cat := New(pool, cfg)
	if err := cat.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}

	entries, err := cat.For(ctx, "reasoning-strong")
	if err != nil {
		t.Fatalf("For(reasoning-strong): %v", err)
	}
	want := []struct{ provider, model string }{
		{"anthropic", "claude-opus-4-20250101"},
		{"openai", "o1-2024-12-17"},
		{"google", "gemini-2.5-pro"},
		{"deepseek", "deepseek-v4-pro"},
	}
	if len(entries) != len(want) {
		t.Fatalf("reasoning-strong entries = %d, want %d: %+v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i].Provider != w.provider || entries[i].ModelID != w.model {
			t.Errorf("position %d = %s/%s, want %s/%s", i,
				entries[i].Provider, entries[i].ModelID, w.provider, w.model)
		}
	}

	// The 0015 rows must never carry a priority below an existing 0010 row.
	for _, e := range entries {
		if e.Priority < 2 && e.Provider != "anthropic" && e.Provider != "openai" {
			t.Errorf("%s/%s priority = %d, want >= 2 so existing routing is unchanged",
				e.Provider, e.ModelID, e.Priority)
		}
	}
}

// TestLoadOllamaUsesBaseURL verifies the rule the owner called out: Ollama is
// identified by its base URL, not an API key, so local models survive startup.
func TestLoadOllamaUsesBaseURL(t *testing.T) {
	pool := newCatalogEnv(t, false)
	ctx := envContext(t, time.Minute)

	cat := New(pool, &config.Config{OllamaBaseURL: "http://ollama:11434"})
	if err := cat.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	entries, err := cat.For(ctx, "local-private")
	if err != nil {
		t.Fatalf("For(local-private): %v", err)
	}
	if len(entries) != 1 || entries[0].Provider != "ollama" {
		t.Fatalf("local-private entries = %+v, want the single ollama row", entries)
	}
}

// TestLoadRejectsFloatingAlias keeps the ADR-021 guard active for rows added
// by any provider, including the new OpenAI-compatible ones.
func TestLoadRejectsFloatingAlias(t *testing.T) {
	pool := newCatalogEnv(t, true)
	ctx := envContext(t, time.Minute)

	if _, err := pool.Exec(ctx, `
		INSERT INTO llm_model_profiles
			(profile_name, provider, model_id, priority, capabilities, enabled)
		VALUES ('vision-capable', 'google', 'gemini-2.5-pro-latest', 0, '{"context_window":1}', true)`); err != nil {
		t.Fatalf("insert floating alias row: %v", err)
	}

	cat := New(pool, &config.Config{GoogleAPIKey: "sk-google"})
	err := cat.Load(ctx)
	if !errors.Is(err, domain.ErrFloatingAlias) {
		t.Fatalf("Load = %v, want ErrFloatingAlias", err)
	}
}

// TestCapabilitiesJSON covers the capabilities accessor used by callers that
// inspect context windows and tool support.
func TestCapabilitiesJSON(t *testing.T) {
	raw := json.RawMessage(`{"context_window":1048576,"supports_tools":true}`)
	got, err := Capabilities(domain.ModelProfile{Capabilities: raw})
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if got["supports_tools"] != true {
		t.Errorf("supports_tools = %v, want true", got["supports_tools"])
	}
	if got["context_window"].(float64) != 1048576 {
		t.Errorf("context_window = %v, want 1048576", got["context_window"])
	}

	empty, err := Capabilities(domain.ModelProfile{})
	if err != nil {
		t.Fatalf("Capabilities(empty): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty capabilities = %v, want an empty map", empty)
	}

	if _, err := Capabilities(domain.ModelProfile{Capabilities: json.RawMessage(`{oops`)}); err == nil {
		t.Error("Capabilities accepted malformed JSON")
	}
}
