// Package routing provides integration tests for the LLM Gateway router.
// They run against a real PostgreSQL 18 (pgvector image) started with
// testcontainers-go, so RLS, the status CHECK constraints and the NUMERIC
// cost columns behave exactly as in production.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/catalog"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/providers"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/repository"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// ── Fake provider ─────────────────────────────────────────────

// fakeProvider implements providers.Provider with an injectable handler,
// so a test can script exactly what a provider answers per model.
type fakeProvider struct {
	name    string
	handler func(modelID string, req domain.ChatRequest) (domain.ChatResponse, error)
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
	return f.handler(modelID, req)
}

// buildProviders indexes the fakes by provider name.
func buildProviders(fakes ...*fakeProvider) map[string]providers.Provider {
	m := make(map[string]providers.Provider, len(fakes))
	for _, f := range fakes {
		m[f.name] = f
	}
	return m
}

// succeededProvider always returns the same canned response.
func succeededProvider(name, content string, tokensIn, tokensOut int) *fakeProvider {
	return &fakeProvider{
		name: name,
		handler: func(modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
			return domain.ChatResponse{Content: content, TokensIn: tokensIn, TokensOut: tokensOut}, nil
		},
	}
}

// failingProvider always returns the error built for the attempted model.
func failingProvider(name string, errFor func(modelID string) error) *fakeProvider {
	return &fakeProvider{
		name: name,
		handler: func(modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
			return domain.ChatResponse{}, errFor(modelID)
		},
	}
}

// ── Fixtures ──────────────────────────────────────────────────

// llmEnv bundles the container-backed pools of one integration test.
type llmEnv struct {
	t           *testing.T
	ctx         context.Context
	pool        *pgxpool.Pool // container superuser: migration + seeding
	appPool     *pgxpool.Pool // SET ROLE: RLS enforced
	repo        *repository.Repo
	cat         *catalog.Catalog
	workspaceID uuid.UUID
	authorID    uuid.UUID
}

// newLLMEnv starts PostgreSQL 18, applies migration 0010 and opens a pool
// that switches to a NOBYPASSRLS role, so llm_logical_calls RLS is enforced
// the same way the deployed gateway runs.
func newLLMEnv(t *testing.T, name string) *llmEnv {
	t.Helper()

	if output, err := exec.CommandContext(timeoutContext(t, 10*time.Second), "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker is unavailable; skipping PostgreSQL integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	// pgvector image ships PostgreSQL 18, the version used in deploy/compose.
	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase(name),
		postgrescontainer.WithUsername(name),
		postgrescontainer.WithPassword(name),
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
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("connect to PostgreSQL container: %v", err)
	}

	applyLLMMigration(t, ctx, pool)

	// The container user owns the schema and would bypass RLS, so the
	// gateway connects as a dedicated role that RLS applies to.
	role := name + "_app"
	if _, err := pool.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN NOBYPASSRLS"); err != nil {
		t.Fatalf("create non-bypass role: %v", err)
	}
	const tables = "llm_model_profiles, model_health, llm_logical_calls, llm_attempts"
	if _, err := pool.Exec(ctx, "GRANT SELECT, INSERT, UPDATE, DELETE ON "+tables+" TO "+role); err != nil {
		t.Fatalf("grant role permissions: %v", err)
	}

	workspaceID, authorID := uuid.New(), uuid.New()

	poolCfg, err := pgxpool.ParseConfig(connectionString)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	// SET ROLE makes RLS effective. The tenant GUC is set per transaction by
	// the repository; the session-level default keeps it always defined, as
	// current_setting(..., true) would otherwise return NULL on a fresh
	// connection and silently hide every row.
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, "SELECT set_config('app.current_workspace_id', $1, false)", workspaceID.String())
		return err
	}
	appPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("create app database pool: %v", err)
	}
	t.Cleanup(appPool.Close)

	return &llmEnv{
		t:           t,
		ctx:         ctx,
		pool:        pool,
		appPool:     appPool,
		repo:        repository.New(appPool),
		cat:         catalog.New(appPool),
		workspaceID: workspaceID,
		authorID:    authorID,
	}
}

// tenant returns a context carrying the environment workspace.
func (e *llmEnv) tenant() context.Context {
	return identity.WithTenant(e.ctx, e.workspaceID, e.authorID)
}

// router wires a Router over the environment catalog and repository.
func (e *llmEnv) router(fakes ...*fakeProvider) *Router {
	return New(e.cat, e.repo, buildProviders(fakes...))
}

// seedProfiles replaces the global catalog with the given entries. The
// migration ships seed rows for code-strong, so they are cleared first to
// keep the routing order deterministic.
func (e *llmEnv) seedProfiles(profiles ...domain.ModelProfile) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM llm_model_profiles`); err != nil {
		e.t.Fatalf("clear llm_model_profiles: %v", err)
	}
	for _, p := range profiles {
		caps := []byte(p.Capabilities)
		if len(caps) == 0 {
			caps = []byte(`{}`)
		}
		if _, err := e.pool.Exec(e.ctx, `
			INSERT INTO llm_model_profiles
				(profile_name, provider, model_id, priority, capabilities,
				 cost_per_mtok_in, cost_per_mtok_out, enabled)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			p.ProfileName, p.Provider, p.ModelID, p.Priority, caps,
			p.CostPerMtokIn, p.CostPerMtokOut, p.Enabled); err != nil {
			e.t.Fatalf("seed catalog entry %s/%s: %v", p.Provider, p.ModelID, err)
		}
	}
}

// model returns a catalog entry with default capabilities.
func model(profile, provider, modelID string, priority int) domain.ModelProfile {
	return domain.ModelProfile{
		ProfileName:  profile,
		Provider:     provider,
		ModelID:      modelID,
		Priority:     priority,
		Capabilities: json.RawMessage(`{"context_window":200000,"supports_tools":true}`),
		Enabled:      true,
	}
}

// priced is model with per-million-token pricing attached.
func priced(profile, provider, modelID string, priority int, costIn, costOut float64) domain.ModelProfile {
	p := model(profile, provider, modelID, priority)
	p.CostPerMtokIn = &costIn
	p.CostPerMtokOut = &costOut
	return p
}

func (e *llmEnv) health(provider, modelID string) *domain.ModelHealth {
	e.t.Helper()
	h, err := e.repo.HealthFor(e.ctx, []repository.ModelKey{{Provider: provider, ModelID: modelID}})
	if err != nil {
		e.t.Fatalf("load model_health: %v", err)
	}
	return h[repository.ModelKey{Provider: provider, ModelID: modelID}]
}

func (e *llmEnv) attempts(logicalCallID uuid.UUID) []domain.Attempt {
	e.t.Helper()
	out, err := e.repo.ListAttempts(e.ctx, logicalCallID)
	if err != nil {
		e.t.Fatalf("list attempts: %v", err)
	}
	return out
}

// ── Helpers ───────────────────────────────────────────────────

func timeoutContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// findRepoRoot walks up from the working directory until the migration
// directory of the repository is found.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "postgres", "0010_llm_gateway.up.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}

func applyLLMMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join(findRepoRoot(t), "migrations", "postgres", "0010_llm_gateway.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Conn().PgConn().Exec(ctx, string(sql)).ReadAll(); err != nil {
		t.Fatalf("apply llm gateway migration: %v", err)
	}
}

// costOf dereferences an attempt cost.
func costOf(t *testing.T, label string, v *float64) float64 {
	t.Helper()
	if v == nil {
		t.Fatalf("%s cost is NULL, want a value", label)
	}
	return *v
}

func nearlyEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ── Tests ─────────────────────────────────────────────────────

// TestFallbackOn404_NoCooldown covers ADR-021: a 404 is a configuration
// defect, not an availability event. The router must fall back to the next
// model in the profile AND must not start a cooldown for the failed pair.
func TestFallbackOn404_NoCooldown(t *testing.T) {
	env := newLLMEnv(t, "llm_router_404")
	env.seedProfiles(
		model("code-strong", "anthropic", "model-a", 0),
		model("code-strong", "openai", "model-b", 1),
	)
	if err := env.cat.Load(env.ctx); err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	router := env.router(
		failingProvider("anthropic", func(modelID string) error {
			return &providers.HTTPError{StatusCode: 404, Provider: "anthropic", ModelID: modelID}
		}),
		succeededProvider("openai", "fallback answer", 120, 60),
	)

	runID := uuid.New()
	res, err := router.Chat(env.tenant(), "code-strong", domain.ChatRequest{
		Messages: []domain.ChatMessage{{Role: "user", Content: "write a test"}},
	}, CallMeta{RunID: &runID})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if res.Content != "fallback answer" {
		t.Errorf("content = %q, want %q", res.Content, "fallback answer")
	}
	if res.LogicalCall.ModelIDUsed != "model-b" {
		t.Errorf("model_used = %q, want model-b", res.LogicalCall.ModelIDUsed)
	}
	if res.LogicalCall.ProviderUsed != "openai" {
		t.Errorf("provider_used = %q, want openai", res.LogicalCall.ProviderUsed)
	}
	if res.LogicalCall.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", res.LogicalCall.Status)
	}
	if res.LogicalCall.AttemptCount != 2 {
		t.Errorf("attempt_count = %d, want 2", res.LogicalCall.AttemptCount)
	}

	// 404 must not burn the failure counter nor open a cooldown window.
	h := env.health("anthropic", "model-a")
	if h == nil {
		t.Fatal("expected a model_health row for anthropic/model-a")
	}
	if h.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d, want 0", h.ConsecutiveFailures)
	}
	if h.CooldownUntil != nil {
		t.Errorf("cooldown_until = %v, want NULL", *h.CooldownUntil)
	}
	if h.LastErrorType == nil || *h.LastErrorType != "failed_404" {
		t.Errorf("last_error_type = %v, want failed_404", h.LastErrorType)
	}

	// Both attempts must be persisted in the spend layer.
	attempts := env.attempts(res.LogicalCall.ID)
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].Status != "failed_404" || attempts[0].Provider != "anthropic" || attempts[0].ModelID != "model-a" {
		t.Errorf("attempt 1 = %+v, want failed_404 anthropic/model-a", attempts[0])
	}
	if attempts[1].Status != "succeeded" || attempts[1].Provider != "openai" || attempts[1].ModelID != "model-b" {
		t.Errorf("attempt 2 = %+v, want succeeded openai/model-b", attempts[1])
	}
}

// TestFallbackOn429_WithCooldown covers the availability branch of ADR-021:
// a 429 falls back AND puts (provider, model) into exponential cooldown.
func TestFallbackOn429_WithCooldown(t *testing.T) {
	env := newLLMEnv(t, "llm_router_429")
	env.seedProfiles(
		model("code-strong", "anthropic", "model-a", 0),
		model("code-strong", "openai", "model-b", 1),
	)
	if err := env.cat.Load(env.ctx); err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	router := env.router(
		failingProvider("anthropic", func(modelID string) error {
			return &providers.HTTPError{StatusCode: 429, Provider: "anthropic", ModelID: modelID}
		}),
		succeededProvider("openai", "rate limit bypassed", 10, 5),
	)

	res, err := router.Chat(env.tenant(), "code-strong", domain.ChatRequest{
		Messages: []domain.ChatMessage{{Role: "user", Content: "summarize"}},
	}, CallMeta{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.LogicalCall.ModelIDUsed != "model-b" {
		t.Errorf("model_used = %q, want model-b", res.LogicalCall.ModelIDUsed)
	}

	h := env.health("anthropic", "model-a")
	if h == nil {
		t.Fatal("expected a model_health row for anthropic/model-a")
	}
	if h.ConsecutiveFailures != 1 {
		t.Errorf("consecutive_failures = %d, want 1", h.ConsecutiveFailures)
	}
	if h.CooldownUntil == nil {
		t.Fatal("cooldown_until is NULL, want a cooldown window")
	}
	if !h.IsCoolingDown(time.Now().UTC()) {
		t.Errorf("cooldown_until = %v, want a timestamp in the future", *h.CooldownUntil)
	}
	if h.LastErrorType == nil || *h.LastErrorType != "failed_429" {
		t.Errorf("last_error_type = %v, want failed_429", h.LastErrorType)
	}
}

// TestDualCost covers the two-level cost accounting of ADR-021: an earlier
// attempt that was rate limited after producing tokens still contributes to
// total_cost_usd, while final_cost_usd reflects only the winning attempt.
func TestDualCost(t *testing.T) {
	env := newLLMEnv(t, "llm_router_cost")
	env.seedProfiles(
		priced("code-strong", "anthropic", "model-a", 0, 10, 20),
		priced("code-strong", "openai", "model-b", 1, 2, 4),
	)
	if err := env.cat.Load(env.ctx); err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	router := env.router(
		failingProvider("anthropic", func(modelID string) error {
			return &providers.UsageError{
				Err:       &providers.HTTPError{StatusCode: 429, Provider: "anthropic", ModelID: modelID},
				TokensIn:  1000,
				TokensOut: 500,
			}
		}),
		succeededProvider("openai", "done", 2000, 1000),
	)

	res, err := router.Chat(env.tenant(), "code-strong", domain.ChatRequest{
		Messages: []domain.ChatMessage{{Role: "user", Content: "refactor"}},
	}, CallMeta{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// 1000/1e6*10 + 500/1e6*20 = 0.02      (failed attempt)
	// 2000/1e6*2  + 1000/1e6*4  = 0.008    (successful attempt)
	// total = 0.028
	const wantFinal, wantTotal = 0.008, 0.028
	if res.LogicalCall.FinalCostUSD == nil {
		t.Fatal("final_cost_usd is NULL")
	}
	if !nearlyEqual(*res.LogicalCall.FinalCostUSD, wantFinal) {
		t.Errorf("final_cost_usd = %v, want %v", *res.LogicalCall.FinalCostUSD, wantFinal)
	}
	if !nearlyEqual(res.LogicalCall.TotalCostUSD, wantTotal) {
		t.Errorf("total_cost_usd = %v, want %v", res.LogicalCall.TotalCostUSD, wantTotal)
	}

	// Read the row back so the NUMERIC(10,4) columns are verified too.
	stored, err := env.repo.GetLogicalCall(env.tenant(), res.LogicalCall.ID)
	if err != nil {
		t.Fatalf("get logical call: %v", err)
	}
	if stored.FinalCostUSD == nil || !nearlyEqual(*stored.FinalCostUSD, wantFinal) {
		t.Errorf("stored final_cost_usd = %v, want %v", stored.FinalCostUSD, wantFinal)
	}
	if !nearlyEqual(stored.TotalCostUSD, wantTotal) {
		t.Errorf("stored total_cost_usd = %v, want %v", stored.TotalCostUSD, wantTotal)
	}
	if stored.TotalTokensIn != 3000 || stored.TotalTokensOut != 1500 {
		t.Errorf("stored totals = %d/%d, want 3000/1500", stored.TotalTokensIn, stored.TotalTokensOut)
	}

	attempts := env.attempts(res.LogicalCall.ID)
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].Status != "failed_429" {
		t.Errorf("attempt 1 status = %q, want failed_429", attempts[0].Status)
	}
	if got := costOf(t, "attempt 1", attempts[0].CostUSD); !nearlyEqual(got, 0.02) {
		t.Errorf("attempt 1 cost_usd = %v, want 0.02", got)
	}
	if attempts[1].Status != "succeeded" {
		t.Errorf("attempt 2 status = %q, want succeeded", attempts[1].Status)
	}
	if got := costOf(t, "attempt 2", attempts[1].CostUSD); !nearlyEqual(got, 0.008) {
		t.Errorf("attempt 2 cost_usd = %v, want 0.008", got)
	}
}

// TestRLS_Isolation checks NFR-3: a workspace cannot read the logical calls
// of another workspace, even when it knows the primary key.
func TestRLS_Isolation(t *testing.T) {
	env := newLLMEnv(t, "llm_router_rls")

	workspaceA, authorID := env.workspaceID, env.authorID
	workspaceB := uuid.New()

	call := &domain.LogicalCall{
		ProfileName: "code-strong",
		StartedAt:   time.Now().UTC(),
	}
	if err := env.repo.CreateLogicalCall(identity.WithTenant(env.ctx, workspaceA, authorID), call); err != nil {
		t.Fatalf("create logical call in workspace A: %v", err)
	}

	// Sanity check: the owning workspace can read it back.
	gotA, err := env.repo.GetLogicalCall(identity.WithTenant(env.ctx, workspaceA, authorID), call.ID)
	if err != nil {
		t.Fatalf("get logical call in workspace A: %v", err)
	}
	if gotA.ID != call.ID || gotA.WorkspaceID != workspaceA {
		t.Fatalf("workspace A read back %+v", gotA)
	}

	// Another workspace must not see it.
	_, err = env.repo.GetLogicalCall(identity.WithTenant(env.ctx, workspaceB, authorID), call.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get logical call from workspace B: got %v, want ErrNotFound", err)
	}
}

// TestCatalogRejectsLatest covers ADR-021: profiles must pin model versions.
// A floating "-latest" alias is a configuration defect and must abort the
// whole catalog load rather than be silently skipped.
func TestCatalogRejectsLatest(t *testing.T) {
	env := newLLMEnv(t, "llm_router_alias")
	env.seedProfiles(model("code-strong", "anthropic", "claude-latest-sonnet", 0))

	err := env.cat.Load(env.ctx)
	if !errors.Is(err, domain.ErrFloatingAlias) {
		t.Fatalf("catalog.Load = %v, want ErrFloatingAlias", err)
	}

	// A rejected load must leave the profile unusable.
	if _, err := env.cat.For(env.ctx, "code-strong"); err == nil {
		t.Error("catalog.For succeeded after a rejected load, want an error")
	}
}
