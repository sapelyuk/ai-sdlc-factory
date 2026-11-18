package testutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
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

	pool, err := startPostgresWithDockerCheck(t, dbName, migrations, func(ctx context.Context) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", "info").CombinedOutput()
	}, postgresRuntime{
		run: func(ctx context.Context, name string) (postgresContainer, error) {
			return postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
				postgrescontainer.WithDatabase(name),
				postgrescontainer.WithUsername(name),
				postgrescontainer.WithPassword(name),
				postgrescontainer.BasicWaitStrategies(),
			)
		},
		newPool: func(ctx context.Context, dsn string) (postgresPool, error) {
			return pgxpool.New(ctx, dsn)
		},
		readFile: os.ReadFile,
		repoRoot: repoRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	pgxPool, ok := pool.(*pgxpool.Pool)
	if !ok {
		t.Fatalf("unexpected PostgreSQL pool type %T", pool)
	}
	return pgxPool
}

type postgresContainer interface {
	Terminate(context.Context, ...testcontainers.TerminateOption) error
	ConnectionString(context.Context, ...string) (string, error)
}

type postgresPool interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Close()
}

type postgresRuntime struct {
	run      func(context.Context, string) (postgresContainer, error)
	newPool  func(context.Context, string) (postgresPool, error)
	readFile func(string) ([]byte, error)
	repoRoot func(*testing.T) string
}

func startPostgresWithDockerCheck(
	t *testing.T,
	dbName string,
	migrations []string,
	dockerInfo func(context.Context) ([]byte, error),
	runtime postgresRuntime,
) (postgresPool, error) {
	t.Helper()

	if output, err := dockerInfo(t.Context()); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			return nil, fmt.Errorf("docker required for integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	return startPostgres(t, dbName, migrations, runtime)
}

func startPostgres(t *testing.T, dbName string, migrations []string, runtime postgresRuntime) (postgresPool, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := runtime.run(ctx, dbName)
	if err != nil {
		return nil, fmt.Errorf("failed to start PostgreSQL container: %w", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}
	pool, err := runtime.newPool(ctx, dsn)
	if err != nil {
		return nil, err
	}
	t.Cleanup(pool.Close)

	migrationsDir := runtime.repoRoot(t)
	for _, name := range migrations {
		contents, err := runtime.readFile(filepath.Join(migrationsDir, "migrations", "postgres", name))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			return nil, fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return pool, nil
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
