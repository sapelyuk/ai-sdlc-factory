package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// StartPostgres starts a PostgreSQL container (pgvector/18), applies the named
// migrations and returns the connection pool.
//
// Docker availability is the only permitted skip reason, and never on CI: a
// container that cannot start in CI fails the build instead of silently dropping
// integration coverage to zero.
func StartPostgres(t *testing.T, dbName string, migrations []string) *pgxpool.Pool {
	t.Helper()

	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker required for integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase(dbName),
		postgrescontainer.WithUsername(dbName),
		postgrescontainer.WithPassword(dbName),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("failed to start PostgreSQL container: %v", err)
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

	migrationsDir := repoRoot(t)
	for _, name := range migrations {
		contents, err := os.ReadFile(filepath.Join(migrationsDir, "migrations", "postgres", name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
	return pool
}

// repoRoot walks up the directory tree to find the repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations", "postgres", "0001_init_squads_and_agents.up.sql")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repository root")
		}
		dir = parent
	}
}
