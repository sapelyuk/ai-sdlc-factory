package testutil

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestRepoRoot(t *testing.T) {
	original, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(filepath.Join(original, "..", "..", "..", "..")), repoRoot(t))
}

func TestStartPostgres(t *testing.T) {
	StartPostgres(t, "testutil_skip_test", nil)
}

type fakeContainer struct {
	dsn        string
	dsnErr     error
	terminated bool
}

func (c *fakeContainer) Terminate(context.Context, ...testcontainers.TerminateOption) error {
	c.terminated = true
	return nil
}

func (c *fakeContainer) ConnectionString(context.Context, ...string) (string, error) {
	return c.dsn, c.dsnErr
}

type fakePool struct {
	execErr error
	closed  bool
	queries []string
}

func (p *fakePool) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	p.queries = append(p.queries, query)
	return pgconn.CommandTag{}, p.execErr
}

func (p *fakePool) Close() {
	p.closed = true
}

func TestStartPostgresSetup(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		dsnErr  error
		poolErr error
		readErr error
		execErr error
		wantErr string
	}{
		{name: "container start failure", runErr: errors.New("docker error"), wantErr: "failed to start PostgreSQL container"},
		{name: "connection string failure", dsnErr: errors.New("dsn error"), wantErr: "dsn error"},
		{name: "pool failure", poolErr: errors.New("pool error"), wantErr: "pool error"},
		{name: "migration read failure", readErr: errors.New("read error"), wantErr: "read migration 0001.sql"},
		{name: "migration execution failure", execErr: errors.New("migration error"), wantErr: "apply migration 0001.sql"},
		{name: "success"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := &fakeContainer{dsn: "postgres://test", dsnErr: tt.dsnErr}
			pool := &fakePool{execErr: tt.execErr}
			runCalls := 0
			runtime := postgresRuntime{
				run: func(_ context.Context, _ string) (postgresContainer, error) {
					runCalls++
					return container, tt.runErr
				},
				newPool: func(_ context.Context, dsn string) (postgresPool, error) {
					assert.Equal(t, "postgres://test", dsn)
					return pool, tt.poolErr
				},
				readFile: func(path string) ([]byte, error) {
					assert.True(t, strings.HasSuffix(path, filepath.Join("migrations", "postgres", "0001.sql")))
					return []byte("CREATE TABLE example;"), tt.readErr
				},
				repoRoot: func(*testing.T) string { return "repo" },
			}

			// Run resource cleanup before checking the state it produced.
			t.Cleanup(func() {
				if tt.runErr == nil {
					assert.True(t, container.terminated)
				}
				if tt.dsnErr == nil && tt.poolErr == nil && tt.runErr == nil {
					assert.True(t, pool.closed)
				}
			})

			migrations := []string(nil)
			if tt.readErr != nil || tt.execErr != nil || tt.name == "success" {
				migrations = []string{"0001.sql"}
			}
			gotPool, err := startPostgresWithDockerCheck(
				t,
				"test",
				migrations,
				func(context.Context) ([]byte, error) { return nil, nil },
				runtime,
			)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Nil(t, gotPool)
			} else {
				require.NoError(t, err)
				assert.Same(t, pool, gotPool)
				assert.Equal(t, []string{"CREATE TABLE example;"}, pool.queries)
			}
			if tt.runErr != nil {
				assert.Equal(t, 1, runCalls)
			}
		})
	}
}

func TestStartPostgresDockerCheck(t *testing.T) {
	t.Run("CI failure", func(t *testing.T) {
		t.Setenv("CI", "true")
		_, err := startPostgresWithDockerCheck(t, "test", nil,
			func(context.Context) ([]byte, error) { return []byte("daemon offline\n"), errors.New("unavailable") },
			postgresRuntime{},
		)
		require.ErrorContains(t, err, "Docker required for integration test: daemon offline")
	})

	t.Run("local skip", func(t *testing.T) {
		t.Setenv("CI", "false")
		startPostgresWithDockerCheck(t, "test", nil,
			func(context.Context) ([]byte, error) { return []byte("daemon offline\n"), errors.New("unavailable") },
			postgresRuntime{},
		)
	})
}
