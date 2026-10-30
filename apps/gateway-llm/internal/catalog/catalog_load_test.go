package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
)

// fakeSource builds a catalog whose fetch serves a fixed row set, so the rules
// of Load (pinning, credential filter, ordering, cache swap) are unit-testable
// without PostgreSQL.
func fakeSource(cfg *config.Config, rows []domain.ModelProfile, err error) *Catalog {
	c := &Catalog{cfg: cfg}
	c.fetch = func(context.Context) ([]domain.ModelProfile, error) {
		if err != nil {
			return nil, err
		}
		out := make([]domain.ModelProfile, len(rows))
		copy(out, rows)
		return out, nil
	}
	return c
}

// captureLogs redirects the default logger into a buffer for the duration of
// the test and restores it afterwards.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

func mustLoad(t *testing.T, c *Catalog) {
	t.Helper()
	if err := c.Load(context.Background()); err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
}

func loadedProviders(t *testing.T, c *Catalog, profile string) []string {
	t.Helper()
	entries, err := c.For(context.Background(), profile)
	if err != nil {
		t.Fatalf("For(%q): %v", profile, err)
	}
	got := make([]string, 0, len(entries))
	for _, e := range entries {
		got = append(got, e.Provider+"/"+e.ModelID)
	}
	return got
}

// TestLoadSkipsUnconfiguredProviders is the end-to-end unit guarantee of
// issue #112: rows of a provider without credentials never reach the router.
func TestLoadSkipsUnconfiguredProviders(t *testing.T) {
	cfg := fullConfig()
	cfg.DeepSeekAPIKey = ""

	rows := []domain.ModelProfile{
		{ProfileName: "code-strong", Provider: "deepseek", ModelID: "deepseek-v4-pro", Priority: 1},
		{ProfileName: "code-strong", Provider: "deepseek", ModelID: "deepseek-flash", Priority: 2},
		{ProfileName: "code-strong", Provider: "anthropic", ModelID: "claude-opus-4-1", Priority: 3},
	}
	logs := captureLogs(t)

	c := fakeSource(cfg, rows, nil)
	mustLoad(t, c)

	got := loadedProviders(t, c, "code-strong")
	want := []string{"anthropic/claude-opus-4-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("loaded entries = %v, want %v", got, want)
	}

	// One log line per skipped provider, not one per skipped row.
	if n := strings.Count(logs.String(), "skipping provider"); n != 1 {
		t.Errorf("log lines about skipped providers = %d, want 1 (deduplicated per provider): %q", n, logs.String())
	}
	if !strings.Contains(logs.String(), "provider=deepseek") {
		t.Errorf("log does not name the skipped provider: %q", logs.String())
	}
}

// TestLoadKeepsAllConfiguredProviders guards against the filter being too
// eager: with full credentials every provider row is loaded.
func TestLoadKeepsAllConfiguredProviders(t *testing.T) {
	rows := []domain.ModelProfile{
		{ProfileName: "p", Provider: "anthropic", ModelID: "claude-opus-4-1"},
		{ProfileName: "p", Provider: "openai", ModelID: "gpt-5"},
		{ProfileName: "p", Provider: "google", ModelID: "gemini-2.5-pro"},
		{ProfileName: "p", Provider: "deepseek", ModelID: "deepseek-v4-pro"},
		{ProfileName: "p", Provider: "groq", ModelID: "openai/gpt-oss-120b"},
		{ProfileName: "p", Provider: "ollama", ModelID: "qwen3-coder:48b"},
	}
	c := fakeSource(fullConfig(), rows, nil)
	mustLoad(t, c)

	if got := loadedProviders(t, c, "p"); len(got) != len(rows) {
		t.Errorf("loaded %d entries, want %d: %v", len(got), len(rows), got)
	}
}

// TestLoadRejectsFloatingAliasAndKeepsSnapshot pins ADR-021 behaviour: a
// floating alias fails the whole load and the previous cache stays usable.
func TestLoadRejectsFloatingAliasAndKeepsSnapshot(t *testing.T) {
	c := fakeSource(fullConfig(),
		[]domain.ModelProfile{{ProfileName: "reasoning-strong", Provider: "openai", ModelID: "o3"}}, nil)
	mustLoad(t, c)

	c.fetch = func(context.Context) ([]domain.ModelProfile, error) {
		return []domain.ModelProfile{
			{ProfileName: "reasoning-strong", Provider: "openai", ModelID: "o3-latest"},
		}, nil
	}
	err := c.Load(context.Background())
	if !errors.Is(err, domain.ErrFloatingAlias) {
		t.Fatalf("Load with -latest row = %v, want ErrFloatingAlias", err)
	}
	if got := loadedProviders(t, c, "reasoning-strong"); strings.Join(got, ",") != "openai/o3" {
		t.Errorf("cache after a failed load = %v, want the previous snapshot openai/o3", got)
	}
}

// TestLoadSortsByPriority documents the router contract: entries come back in
// ascending priority regardless of the order the source returned them.
func TestLoadSortsByPriority(t *testing.T) {
	rows := []domain.ModelProfile{
		{ProfileName: "code-fast", Provider: "groq", ModelID: "openai/gpt-oss-20b", Priority: 5},
		{ProfileName: "code-fast", Provider: "openai", ModelID: "gpt-5-mini", Priority: 1},
		{ProfileName: "code-fast", Provider: "google", ModelID: "gemini-2.5-flash", Priority: 3},
	}
	c := fakeSource(fullConfig(), rows, nil)
	mustLoad(t, c)

	entries, err := c.For(context.Background(), "code-fast")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Priority > entries[i].Priority {
			t.Fatalf("entries not sorted by priority: %+v", entries)
		}
	}
	if entries[0].ModelID != "gpt-5-mini" {
		t.Errorf("first entry = %q, want the priority-1 model gpt-5-mini", entries[0].ModelID)
	}
}

// TestLoadPropagatesSourceError: a database failure surfaces and leaves the
// cached snapshot untouched.
func TestLoadPropagatesSourceError(t *testing.T) {
	c := fakeSource(fullConfig(),
		[]domain.ModelProfile{{ProfileName: "long-context", Provider: "google", ModelID: "gemini-2.5-pro"}}, nil)
	mustLoad(t, c)

	wantErr := errors.New("dial tcp: connection refused")
	c.fetch = func(context.Context) ([]domain.ModelProfile, error) { return nil, wantErr }
	if err := c.Load(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Load = %v, want the source error %v", err, wantErr)
	}
	if got := loadedProviders(t, c, "long-context"); len(got) != 1 {
		t.Errorf("cache after a failed load = %v, want the previous snapshot", got)
	}
}

// TestLoadEmptyResult: an empty catalog is not an error, but no profile
// resolves.
func TestLoadEmptyResult(t *testing.T) {
	c := fakeSource(fullConfig(), nil, nil)
	mustLoad(t, c)

	if _, err := c.For(context.Background(), "code-strong"); !errors.Is(err, domain.ErrUnknownProfile) {
		t.Errorf("For on an empty catalog = %v, want ErrUnknownProfile", err)
	}
}

// TestForRefreshesWhenStale: after refreshInterval the next For reloads.
func TestForRefreshesWhenStale(t *testing.T) {
	calls := 0
	c := &Catalog{cfg: fullConfig()}
	c.fetch = func(context.Context) ([]domain.ModelProfile, error) {
		calls++
		return []domain.ModelProfile{{ProfileName: "cheap-bulk", Provider: "groq", ModelID: "openai/gpt-oss-120b"}}, nil
	}
	mustLoad(t, c)
	if calls != 1 {
		t.Fatalf("fetch calls after Load = %d, want 1", calls)
	}

	if _, err := c.For(context.Background(), "cheap-bulk"); err != nil {
		t.Fatalf("For: %v", err)
	}
	if calls != 1 {
		t.Errorf("fetch calls on a fresh cache = %d, want 1 (no reload within refreshInterval)", calls)
	}

	c.mu.Lock()
	c.loadedAt = time.Now().Add(-refreshInterval - time.Minute)
	c.mu.Unlock()

	if _, err := c.For(context.Background(), "cheap-bulk"); err != nil {
		t.Fatalf("For after the cache went stale: %v", err)
	}
	if calls != 2 {
		t.Errorf("fetch calls on a stale cache = %d, want 2 (refresh triggered)", calls)
	}
}

// TestForPropagatesRefreshError: when the cache is stale and the reload
// fails, For reports the error instead of serving an out-of-date view.
func TestForPropagatesRefreshError(t *testing.T) {
	c := &Catalog{cfg: fullConfig()}
	fails := false
	c.fetch = func(context.Context) ([]domain.ModelProfile, error) {
		if fails {
			return nil, errors.New("database is down")
		}
		return []domain.ModelProfile{{ProfileName: "code-strong", Provider: "openai", ModelID: "gpt-5"}}, nil
	}
	mustLoad(t, c)

	fails = true
	c.mu.Lock()
	c.loadedAt = time.Now().Add(-refreshInterval - time.Minute)
	c.mu.Unlock()

	if _, err := c.For(context.Background(), "code-strong"); err == nil {
		t.Fatal("For = nil error, want the refresh error to surface")
	}
}

// TestForReturnsCopy protects the cache: callers must not be able to mutate the
// shared slice.
func TestForReturnsCopy(t *testing.T) {
	c := fakeSource(fullConfig(),
		[]domain.ModelProfile{{ProfileName: "code-strong", Provider: "anthropic", ModelID: "claude-opus-4-1"}}, nil)
	mustLoad(t, c)

	entries, err := c.For(context.Background(), "code-strong")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	entries[0].ModelID = "clobbered"

	again, err := c.For(context.Background(), "code-strong")
	if err != nil {
		t.Fatalf("For (second call): %v", err)
	}
	if again[0].ModelID != "claude-opus-4-1" {
		t.Errorf("cached model_id = %q, want claude-opus-4-1 (For must return a copy)", again[0].ModelID)
	}
}

// TestForUnknownProfile names the profile in the error for API responses.
func TestForUnknownProfile(t *testing.T) {
	c := fakeSource(fullConfig(),
		[]domain.ModelProfile{{ProfileName: "code-strong", Provider: "openai", ModelID: "gpt-5"}}, nil)
	mustLoad(t, c)

	_, err := c.For(context.Background(), "no-such-profile")
	if !errors.Is(err, domain.ErrUnknownProfile) {
		t.Fatalf("For = %v, want ErrUnknownProfile", err)
	}
	if !strings.Contains(err.Error(), "no-such-profile") {
		t.Errorf("error %q does not mention the requested profile", err)
	}
}

// TestCapabilities covers the three shapes the handler can receive.
func TestCapabilities(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		got, err := Capabilities(domain.ModelProfile{})
		if err != nil {
			t.Fatalf("Capabilities: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("Capabilities = %v, want an empty map", got)
		}
	})

	t.Run("valid", func(t *testing.T) {
		p := domain.ModelProfile{Capabilities: json.RawMessage(`{"structured_output":true,"max_input_tokens":1048576}`)}
		got, err := Capabilities(p)
		if err != nil {
			t.Fatalf("Capabilities: %v", err)
		}
		if got["structured_output"] != true {
			t.Errorf("structured_output = %v, want true", got["structured_output"])
		}
	})

	t.Run("invalid", func(t *testing.T) {
		p := domain.ModelProfile{Capabilities: json.RawMessage(`{oops`)}
		if _, err := Capabilities(p); err == nil {
			t.Fatal("Capabilities = nil error, want a parse error for malformed JSON")
		}
	})
}

// TestQueryProfilesUnreachableHost exercises the real SQL path: the pool is
// created lazily, so the first query fails with a connection error rather than
// returning rows. Loop and scan branches are covered by the Docker tests.
func TestQueryProfilesUnreachableHost(t *testing.T) {
	pool, err := pgxpool.New(context.Background(),
		"postgres://llm:llm@127.0.0.1:1/llm?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := New(pool, fullConfig()).queryProfiles(ctx); err == nil {
		t.Fatal("queryProfiles = nil error, want a connection error for an unreachable host")
	}
}
