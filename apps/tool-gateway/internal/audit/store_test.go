package audit

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// setupPostgresPool starts a PostgreSQL container via testcontainers and
// returns a pool connected to it. Skips the test when Docker is unavailable.
func setupPostgresPool(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()

	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for audit store tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping audit store test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("audit_test"),
		postgrescontainer.WithUsername("audit_test"),
		postgrescontainer.WithPassword("audit_test"),
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
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("PostgreSQL ping failed: %v", err)
	}

	// Apply migration 0011 (tool_call_audit_log table).
	migrationPath := "../../../../migrations/postgres/0011_tool_gateway_audit.up.sql"
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Skipf("read migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		pool.Close()
		t.Skipf("apply migration: %v", err)
	}

	// Apply migration 0012 (stub result support).
	stubMigrationPath := "../../../../migrations/postgres/0012_audit_stub_result.up.sql"
	stubMigration, err := os.ReadFile(stubMigrationPath)
	if err != nil {
		t.Skipf("read stub migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(stubMigration)); err != nil {
		pool.Close()
		t.Skipf("apply stub migration: %v", err)
	}

	teardown := func() {
		pool.Close()
	}
	return pool, teardown
}

func TestPostgresStoreAppendSuccess(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	ws := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agent := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	rec, err := logger.Record(context.Background(), Entry{
		WorkspaceID: ws, AgentID: agent,
		ToolName: "git.push", RiskLevel: "R1", Result: ResultSuccess,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rec.ID)
	require.Equal(t, ResultSuccess, rec.Result)
}

func TestPostgresStoreAppendStubResult(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	rec, err := logger.Record(context.Background(), Entry{
		WorkspaceID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ToolName:    "fs.read",
		RiskLevel:   "R0",
		Result:      ResultStub,
	})
	require.NoError(t, err)
	require.Equal(t, ResultStub, rec.Result)
}

func TestPostgresStoreAppendDeniedResult(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	rec, err := logger.Record(context.Background(), Entry{
		WorkspaceID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ToolName:    "deploy.prod",
		RiskLevel:   "R3",
		Result:      ResultDenied,
	})
	require.NoError(t, err)
	require.Equal(t, ResultDenied, rec.Result)
}

func TestPostgresStoreAppendErrorResult(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	rec, err := logger.Record(context.Background(), Entry{
		WorkspaceID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ToolName:     "git.push",
		RiskLevel:    "R1",
		Result:       ResultError,
		ErrorMessage: "connection timeout",
	})
	require.NoError(t, err)
	require.Equal(t, ResultError, rec.Result)
	require.Equal(t, "connection timeout", rec.ErrorMessage)
}

func TestPostgresStoreAppendHashChain(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	ws := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agent := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	first, err := logger.Record(context.Background(), Entry{
		WorkspaceID: ws, AgentID: agent,
		ToolName: "git.push", RiskLevel: "R1", Result: ResultSuccess,
	})
	require.NoError(t, err)

	second, err := logger.Record(context.Background(), Entry{
		WorkspaceID: ws, AgentID: agent,
		ToolName: "fs.read", RiskLevel: "R0", Result: ResultSuccess,
	})
	require.NoError(t, err)

	require.NotEqual(t, first.HashChain, second.HashChain)
	require.Equal(t, Hash(first.HashChain, second), second.HashChain)
}

func TestPostgresStoreAppendInvalidResult(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	_, err := logger.Record(context.Background(), Entry{
		WorkspaceID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ToolName:    "git.push",
		RiskLevel:   "R1",
		Result:      "invalid",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid result")
}

func TestPostgresStoreAppendInvalidRiskLevel(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)

	_, err := logger.Record(context.Background(), Entry{
		WorkspaceID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ToolName:    "git.push",
		RiskLevel:   "R9",
		Result:      ResultSuccess,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid risk level")
}

// TestPostgresStoreAppendDirect tests PostgresStore.Append directly, bypassing Logger.
func TestPostgresStoreAppendDirect(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	ws := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	rec, err := store.Append(context.Background(), ws, func(prevHash string) (Record, error) {
		id, err := uuid.NewV7()
		if err != nil {
			return Record{}, err
		}
		return Record{
			ID:           id,
			WorkspaceID:  ws,
			AgentID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			ToolName:     "git.push",
			RiskLevel:    "R1",
			Result:       ResultSuccess,
			ErrorMessage: "test error",
			CreatedAt:    time.Now().UTC().Truncate(time.Microsecond),
		}, nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, rec.ID)
	require.Equal(t, ResultSuccess, rec.Result)
	require.Equal(t, "test error", rec.ErrorMessage)
}

// TestPostgresStoreQueryDirect tests PostgresStore.Query directly.
func TestPostgresStoreQueryDirect(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	logger := NewLogger(store)
	ws := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	agent := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	// Insert a record first.
	_, err := logger.Record(context.Background(), Entry{
		WorkspaceID: ws, AgentID: agent,
		ToolName: "git.push", RiskLevel: "R1", Result: ResultSuccess,
	})
	require.NoError(t, err)

	// Query the record.
	records, err := store.Query(context.Background(), ws)
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, "git.push", records[0].ToolName)
	require.Equal(t, ResultSuccess, records[0].Result)
}

// TestPostgresStoreQueryEmpty returns empty slice for unknown workspace.
func TestPostgresStoreQueryEmpty(t *testing.T) {
	pool, teardown := setupPostgresPool(t)
	defer teardown()

	store := NewPostgresStore(pool)
	ws := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")

	records, err := store.Query(context.Background(), ws)
	require.NoError(t, err)
	require.Empty(t, records)
}
