package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// migrationFiles lists the migrations that build the global audit_log table.
// 0001 creates workspaces, 0002 creates tasks_and_sdlc (task_runs FK target),
// 0007 creates the generic audit_log, 0014 turns it into the hash-chained global log.
var migrationFiles = []string{
	"0001_init_squads_and_agents.up.sql",
	"0002_init_tasks_and_sdlc.up.sql",
	"0007_init_audit_and_cost.up.sql",
	"0014_global_audit_log.up.sql",
}

// setupPostgres starts a PostgreSQL container and returns two pools: the owner
// pool (bypasses RLS) and an appRole pool that is subject to the workspace
// policy, exactly like the deployed services.
func setupPostgres(t *testing.T) (owner *pgxpool.Pool, appRole string) {
	t.Helper()

	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for audit integration tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping audit integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("audit_sdk_test"),
		postgrescontainer.WithUsername("audit_sdk_test"),
		postgrescontainer.WithPassword("audit_sdk_test"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("failed to start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	migrationsDir := filepath.Join("..", "..", "..", "migrations", "postgres")
	for _, name := range migrationFiles {
		contents, err := os.ReadFile(filepath.Join(migrationsDir, filepath.Base(name)))
		if err != nil {
			t.Skipf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	// Services connect as a NOBYPASSRLS role so the RLS policy is exercised.
	const role = "audit_sdk_app"
	if _, err := pool.Exec(ctx, "CREATE ROLE "+role+" NOLOGIN NOBYPASSRLS"); err != nil {
		t.Fatalf("create app role: %v", err)
	}
	// CREATE/INSERT also need the sequence-free UUID default, so only data
	// privileges are granted.
	if _, err := pool.Exec(ctx, "GRANT SELECT, INSERT, UPDATE ON audit_log TO "+role); err != nil {
		t.Fatalf("grant table privileges: %v", err)
	}
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA public TO "+role); err != nil {
		t.Fatalf("grant usage: %v", err)
	}
	return pool, role
}

// newServicePool opens a pool that behaves like one deployed service: it
// assumes the NOBYPASSRLS role on every connection.
func newServicePool(t *testing.T, dsnOwner *pgxpool.Pool, role string) *pgxpool.Pool {
	t.Helper()

	raw := dsnOwner.Config().ConnString()
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE "+role)
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func sdkEvent(ws uuid.UUID, actor, action, resource string) Event {
	return Event{
		WorkspaceID: ws,
		ActorID:     actor,
		Action:      action,
		ResourceID:  resource,
		Details:     map[string]any{"source": actor},
	}
}

// TestPostgresChainLinksRecords verifies that records written through
// PostgreSQL form a verifiable chain and that prev_hash is NULL only for the
// first record.
func TestPostgresChainLinksRecords(t *testing.T) {
	owner, role := setupPostgres(t)
	pool := newServicePool(t, owner, role)
	logger := NewLogger(pool)

	ws := uuid.New()
	ctx := context.Background()

	first, err := logger.Append(ctx, sdkEvent(ws, SystemActor("tool-gateway"), ActionToolCall, "git.push"))
	if err != nil {
		t.Fatalf("Append first: %v", err)
	}
	if first.PrevHash != nil {
		t.Fatalf("first.prev_hash = %v, want NULL", first.PrevHash)
	}

	second, err := logger.Append(ctx, sdkEvent(ws, SystemActor("gateway-llm"), ActionLLMCall, "logical-call-1"))
	if err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if second.PrevHash == nil || *second.PrevHash != first.Hash {
		t.Fatalf("second.prev_hash = %v, want %s", second.PrevHash, first.Hash)
	}

	records, err := logger.Records(ctx, ws)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if err := VerifyChain(records); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestPostgresTamperDetection edits a stored row directly in SQL and asserts
// that verification fails, which is the acceptance criterion of issue #14.
func TestPostgresTamperDetection(t *testing.T) {
	owner, role := setupPostgres(t)
	pool := newServicePool(t, owner, role)
	logger := NewLogger(pool)

	ws := uuid.New()
	ctx := context.Background()
	for _, action := range []string{ActionRunStarted, ActionToolCall, ActionTaskCompleted} {
		if _, err := logger.Append(ctx, sdkEvent(ws, SystemActor("agent-runtime"), action, "run-1")); err != nil {
			t.Fatalf("Append %s: %v", action, err)
		}
	}
	if err := logger.Verify(ctx, ws); err != nil {
		t.Fatalf("chain must verify before tampering: %v", err)
	}

	// The owner role bypasses RLS, so the UPDATE reaches the row.
	if _, err := owner.Exec(ctx,
		`UPDATE audit_log SET details = '{"result":"approved"}'::jsonb
		 WHERE workspace_id = $1 AND action = $2`, ws, ActionToolCall); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	err := logger.Verify(ctx, ws)
	if err == nil {
		t.Fatal("expected verification to fail after tampering with details")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPostgresConcurrentServices is the issue #14 requirement that three
// services can append to one log at the same time without deadlocks and
// without forks in the chain.
func TestPostgresConcurrentServices(t *testing.T) {
	owner, role := setupPostgres(t)

	services := map[string]*pgxpool.Pool{
		"gateway-llm":    newServicePool(t, owner, role),
		"tool-gateway":   newServicePool(t, owner, role),
		"agent-runtime":  newServicePool(t, owner, role),
		"sandbox-manage": newServicePool(t, owner, role),
	}

	ws := uuid.New()
	const perService = 15

	var wg sync.WaitGroup
	var failures atomic.Int64
	for service, pool := range services {
		logger := NewLogger(pool)
		wg.Add(1)
		go func(service string, logger *Logger) {
			defer wg.Done()
			for i := 0; i < perService; i++ {
				ev := sdkEvent(ws, SystemActor(service), ActionToolCall, fmt.Sprintf("%s-call-%d", service, i))
				if err := logger.Log(context.Background(), ev); err != nil {
					// A serialization conflict is expected to be retried by the
					// caller, not to succeed silently, so record it.
					t.Errorf("%s: Log: %v", service, err)
					failures.Add(1)
					return
				}
			}
		}(service, logger)
	}
	wg.Wait()

	if failures.Load() > 0 {
		t.Fatalf("%d writers failed", failures.Load())
	}

	records, err := NewLogger(services["gateway-llm"]).Records(context.Background(), ws)
	if err != nil {
		t.Fatalf("Records: %v", err)
	}
	if len(records) != perService*len(services) {
		t.Fatalf("records = %d, want %d", len(records), perService*len(services))
	}
	if err := VerifyChain(records); err != nil {
		t.Fatalf("chain forked or deadlocked: %v", err)
	}

	// Every service must appear in the chain, proving the writes really came
	// from independent pools.
	seen := map[string]int{}
	for _, rec := range records {
		seen[rec.ActorID]++
	}
	if len(seen) != len(services) {
		t.Fatalf("expected %d distinct actors, got %d: %v", len(services), len(seen), seen)
	}
}

// TestPostgresRLSScopesTheChain asserts that a service cannot read or write
// another workspace's chain, and that reading the head of a foreign chain does
// not link two tenants together.
func TestPostgresRLSScopesTheChain(t *testing.T) {
	owner, role := setupPostgres(t)
	pool := newServicePool(t, owner, role)
	logger := NewLogger(pool)
	ctx := context.Background()

	tenantA := uuid.New()
	tenantB := uuid.New()

	if _, err := logger.Append(ctx, sdkEvent(tenantA, SystemActor("tool-gateway"), ActionToolCall, "a")); err != nil {
		t.Fatal(err)
	}
	// A service that only knows tenant B must start its own chain.
	firstB, err := logger.Append(ctx, sdkEvent(tenantB, SystemActor("gateway-llm"), ActionLLMCall, "b"))
	if err != nil {
		t.Fatal(err)
	}
	if firstB.PrevHash != nil {
		t.Fatal("workspace B must not chain onto workspace A")
	}

	records, err := logger.Records(ctx, tenantB)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("tenant B sees %d records, want 1 (tenant isolation leak)", len(records))
	}

	// RLS is exercised with raw SQL, because the SDK always sets the tenant
	// context to the event's workspace. A connection scoped to tenant A must
	// not see tenant B rows, and must not be able to insert one.
	raw := pool.Config().ConnString()
	badCfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	badCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
			return err
		}
		_, err := conn.Exec(ctx, "SELECT set_config('app.current_workspace_id', $1, false)", tenantA.String())
		return err
	}
	badPool, err := pgxpool.NewWithConfig(ctx, badCfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(badPool.Close)

	var visible int
	if err := badPool.QueryRow(ctx, `SELECT count(*) FROM audit_log`).Scan(&visible); err != nil {
		t.Fatalf("count as tenant A: %v", err)
	}
	if visible != 1 {
		t.Fatalf("tenant A sees %d rows, want 1 (only its own)", visible)
	}

	err = badPool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE workspace_id = $1`, tenantB).Scan(&visible)
	if err != nil {
		t.Fatalf("count tenant B rows as tenant A: %v", err)
	}
	if visible != 0 {
		t.Fatalf("tenant A sees %d tenant B rows, want 0", visible)
	}

	_, err = badPool.Exec(ctx, `
		INSERT INTO audit_log (id, workspace_id, actor_id, action, details, hash, created_at)
		VALUES (uuidv7(), $1, 'human:attacker', 'tool_call', '{}'::jsonb, 'forged', now())`, tenantB)
	if err == nil {
		t.Fatal("expected RLS WITH CHECK to reject a cross-tenant audit write")
	}
	if !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPostgresStoreRejectsNilPool covers the defensive check in the store.
func TestPostgresStoreRejectsNilPool(t *testing.T) {
	store := NewPostgresStore(nil)
	_, err := store.Append(context.Background(), uuid.New(), func(*string) (Record, error) {
		return Record{}, nil
	})
	if err == nil {
		t.Fatal("expected an error for a nil pool")
	}
	if _, err := store.Query(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected an error for a nil pool on query")
	}
}

// TestPostgresStorePropagatesBuildErrors asserts the transaction is rolled back
// when the record builder fails.
func TestPostgresStorePropagatesBuildErrors(t *testing.T) {
	owner, role := setupPostgres(t)
	pool := newServicePool(t, owner, role)
	store := NewPostgresStore(pool)

	sentinel := errors.New("builder failed")
	_, err := store.Append(context.Background(), uuid.New(), func(*string) (Record, error) {
		return Record{}, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Append error = %v, want %v", err, sentinel)
	}
}
