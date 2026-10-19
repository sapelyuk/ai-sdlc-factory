package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSeedCommandLoadsSLPCoreIdempotently(t *testing.T) {
	if output, err := exec.CommandContext(context.Background(), "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker is unavailable; skipping seed integration test: %s", strings.TrimSpace(string(output)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := postgrescontainer.Run(ctx, "postgres:18-alpine",
		postgrescontainer.WithDatabase("seed_test"),
		postgrescontainer.WithUsername("seed_test"),
		postgrescontainer.WithPassword("seed_test"),
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
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	applySeedTestMigration(t, ctx, pool)

	workspaceID, authorID := uuid.New(), uuid.New()
	t.Setenv("DATABASE_URL", connectionString)
	t.Setenv("SEED_WORKSPACE_ID", workspaceID.String())
	t.Setenv("SEED_AUTHOR_ID", authorID.String())
	t.Setenv("SLP_PATH", "")
	if err := run(ctx); err != nil {
		t.Fatalf("first seed run: %v", err)
	}
	if err := run(ctx); err != nil {
		t.Fatalf("repeat seed run: %v", err)
	}

	var roles, teams, processes int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE workspace_id = $1`, workspaceID).Scan(&roles); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM teams WHERE workspace_id = $1`, workspaceID).Scan(&teams); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM processes WHERE workspace_id = $1`, workspaceID).Scan(&processes); err != nil {
		t.Fatal(err)
	}
	if roles != 5 || teams != 1 || processes != 1 {
		t.Fatalf("seeded %d roles, %d teams, %d processes; want 5, 1, 1", roles, teams, processes)
	}
	cancelledContext, cancel := context.WithCancel(ctx)
	cancel()
	if err := ensureWorkspace(cancelledContext, pool, workspaceID, authorID); err == nil {
		t.Fatal("ensureWorkspace succeeded with a cancelled context")
	}
}

func applySeedTestMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	root := findSeedRepositoryRoot(t)
	migration, err := os.ReadFile(filepath.Join(root, "migrations", "postgres", "0008_init_registry.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE workspaces (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create workspace fixture: %v", err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Conn().PgConn().Exec(ctx, string(migration)).ReadAll(); err != nil {
		t.Fatalf("apply Registry migration: %v", err)
	}
}

func findSeedRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "postgres", "0008_init_registry.up.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
