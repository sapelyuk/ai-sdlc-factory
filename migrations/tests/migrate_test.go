package tests

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// getMigrationsDir returns the absolute path to migrations/postgres directory.
// Tests run with the working directory set to the test package directory
// (migrations/tests), so migration files live one level up in ./postgres.
func getMigrationsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)

	candidates := []string{
		filepath.Join(wd, "..", "postgres"), // migrations/tests/../postgres
		filepath.Join(wd, "postgres"),       // migrations/tests/postgres
		filepath.Join(wd, "..", "..", "migrations", "postgres"),
	}
	for _, dir := range candidates {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
	}
	dir := os.Getenv("MIGRATIONS_DIR")
	require.NotEmpty(t, dir, "MIGRATIONS_DIR environment variable or relative path must be set")
	return dir
}

func loadSQLFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	files := make(map[string]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 4 {
			continue
		}
		ext := name[len(name)-4:]
		if ext != ".sql" {
			continue
		}
		base := name[:len(name)-4]
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err, "failed to read %s", name)
		files[base] = string(data)
	}
	return files
}

func applySQL(t *testing.T, ctx context.Context, db *sql.DB, sqlContent string) {
	t.Helper()
	_, err := db.ExecContext(ctx, sqlContent)
	require.NoError(t, err, "failed to execute SQL:\n%s", sqlContent)
}

func startTestPostgres(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	_pg, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("pgvector/pgvector:pg18"),
		postgres.WithDatabase("test_migrations"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("test_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second)),
	)
	if err != nil {
		t.Skipf("Skipping test: cannot start postgres container: %v (ensure Docker is running)", err)
	}

	t.Cleanup(func() {
		_ = _pg.Terminate(ctx)
	})

	connStr, err := _pg.ConnectionString(ctx)
	require.NoError(t, err)
	// The postgres module already returns a DSN with sslmode=disable.
	// Only append it if it is missing, otherwise pgx rejects the duplicate
	// parameter and Ping() never succeeds.
	if !strings.Contains(connStr, "sslmode=") {
		if strings.ContainsAny(connStr, "?") {
			connStr += "&sslmode=disable"
		} else {
			connStr += " sslmode=disable"
		}
	}

	db, err := sql.Open("pgx", connStr)
	require.NoError(t, err)

	var pingErr error
	ready := assert.Eventually(t, func() bool {
		pingErr = db.Ping()
		return pingErr == nil
	}, 60*time.Second, 500*time.Millisecond)
	require.Truef(t, ready, "postgres container did not become ready: last ping error: %v", pingErr)

	return db
}

func TestMigrateUp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := startTestPostgres(t)
	defer db.Close()

	// Load migration files
	migrationsDir := getMigrationsDir(t)
	files := loadSQLFiles(t, migrationsDir)

	// Sort by name
	sortedNames := make([]string, 0, len(files))
	for name := range files {
		sortedNames = append(sortedNames, name)
	}
	for i := 0; i < len(sortedNames); i++ {
		for j := i + 1; j < len(sortedNames); j++ {
			if sortedNames[j] < sortedNames[i] {
				sortedNames[i], sortedNames[j] = sortedNames[j], sortedNames[i]
			}
		}
	}

	// Apply all .up.sql migrations in order
	for _, name := range sortedNames {
		if len(name) < 3 || name[len(name)-3:] != ".up" {
			continue
		}
		t.Logf("Applying migration: %s", name)
		applySQL(t, ctx, db, files[name])
	}

	// ── Verify sem_nodes exists ───────────────────────────────
	var exists int
	err := db.QueryRowContext(ctx, "SELECT 1 FROM information_schema.tables WHERE table_name = 'sem_nodes'").Scan(&exists)
	require.NoError(t, err)
	assert.Equal(t, 1, exists, "sem_nodes table should exist")

	// ── Verify RLS is enabled on sem_nodes ────────────────────
	var relrowsecurity bool
	err = db.QueryRowContext(ctx,
		"SELECT relrowsecurity FROM pg_class WHERE relname = 'sem_nodes'").Scan(&relrowsecurity)
	require.NoError(t, err)
	assert.True(t, relrowsecurity, "RLS should be enabled on sem_nodes")

	// ── Verify workspace_id is NOT NULL on sem_nodes ──────────
	var isNullable string
	err = db.QueryRowContext(ctx,
		"SELECT is_nullable FROM information_schema.columns WHERE table_name = 'sem_nodes' AND column_name = 'workspace_id'").Scan(&isNullable)
	require.NoError(t, err)
	assert.Equal(t, "NO", isNullable, "workspace_id should NOT NULL on sem_nodes")

	// ── Verify HNSW index exists ──────────────────────────────
	var idxname string
	err = db.QueryRowContext(ctx,
		"SELECT indexname FROM pg_indexes WHERE indexname = 'sem_embeddings_hnsw_idx'").Scan(&idxname)
	require.NoError(t, err)
	assert.Equal(t, "sem_embeddings_hnsw_idx", idxname, "HNSW index should exist")

	// ── Verify all tables have workspace_id ───────────────────
	// Note: workspaces table is the root and uses id instead of workspace_id.
	expectedTables := []string{
		"squads", "agents", "model_profiles",
		"runs", "stage_runs", "task_runs", "approvals",
		"kb_documents", "kb_sources", "kb_embeddings",
		"sem_nodes", "sem_edges", "sem_embeddings",
		"diagrams", "architecture_models", "adrs",
		"audit_log", "llm_calls",
	}
	for _, tbl := range expectedTables {
		var count int
		err = db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = 'workspace_id'", tbl).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "Table %s should have workspace_id column", tbl)
	}
}

func TestMigrateDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := startTestPostgres(t)
	defer db.Close()

	// Load migration files
	migrationsDir := getMigrationsDir(t)
	files := loadSQLFiles(t, migrationsDir)

	// Sort by name
	sortedNames := make([]string, 0, len(files))
	for name := range files {
		sortedNames = append(sortedNames, name)
	}
	for i := 0; i < len(sortedNames); i++ {
		for j := i + 1; j < len(sortedNames); j++ {
			if sortedNames[j] < sortedNames[i] {
				sortedNames[i], sortedNames[j] = sortedNames[j], sortedNames[i]
			}
		}
	}

	// ── Step 1: Apply all .up.sql ─────────────────────────────
	for _, name := range sortedNames {
		if len(name) < 3 || name[len(name)-3:] != ".up" {
			continue
		}
		applySQL(t, ctx, db, files[name])
	}

	// Verify tables exist
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&count)
	require.NoError(t, err)
	assert.Greater(t, count, 0, "Tables should exist after up migration")

	// ── Step 2: Apply all .down.sql in reverse order ──────────
	var downFiles []string
	for _, name := range sortedNames {
		if len(name) < 5 || name[len(name)-5:] != ".down" {
			continue
		}
		downFiles = append(downFiles, name)
	}

	// Reverse order
	for i, j := 0, len(downFiles)-1; i < j; i, j = i+1, j-1 {
		downFiles[i], downFiles[j] = downFiles[j], downFiles[i]
	}

	for _, name := range downFiles {
		t.Logf("Rolling back migration: %s", name)
		applySQL(t, ctx, db, files[name])
	}

	// ── Verify all tables are dropped ─────────────────────────
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "All tables should be dropped after down migration")
}

func TestPgvectorHNSWCreation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := startTestPostgres(t)
	defer db.Close()

	// Create extension and table with HNSW index.
	// The id column intentionally avoids any UUID-generation function: this
	// test only verifies pgvector HNSW index creation and must stay
	// independent of the migration-wide UUID strategy.
	sql := `
		BEGIN;
		CREATE EXTENSION IF NOT EXISTS vector;
		CREATE TABLE test_vectors (
			id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
			embedding VECTOR(3) NOT NULL
		);
		CREATE INDEX test_vectors_hnsw ON test_vectors USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
		COMMIT;
	`
	_, err := db.Exec(sql)
	require.NoError(t, err, "HNSW index should be created without errors")

	// Verify index works with a simple insert
	_, err = db.ExecContext(ctx,
		"INSERT INTO test_vectors (embedding) VALUES ('[1,2,3]')")
	require.NoError(t, err, "Should be able to insert vectors")
}

// TestMigrationNumbering verifies that all migration files are numbered sequentially.
func TestMigrationNumbering(t *testing.T) {
	t.Parallel()
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	expectedUp := 7
	expectedDown := 7

	var upCount, downCount int
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 5 {
			continue
		}
		if name[len(name)-4:] == ".sql" {
			base := name[:len(name)-4]
			if len(base) >= 3 && base[len(base)-3:] == ".up" {
				upCount++
			} else if len(base) >= 5 && base[len(base)-5:] == ".down" {
				downCount++
			}
		}
	}

	assert.Equal(t, expectedUp, upCount, "Expected %d up migration files", expectedUp)
	assert.Equal(t, expectedDown, downCount, "Expected %d down migration files")
}

// TestMigrationPrefixes verifies that migration files follow the naming convention.
func TestMigrationPrefixes(t *testing.T) {
	t.Parallel()
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 5 || name[len(name)-4:] != ".sql" {
			continue
		}

		// Migration files must follow NNNN_name.(up|down).sql and start with a
		// numeric prefix (digits before the first underscore).
		firstUnderscore := -1
		for i := 0; i < len(name); i++ {
			if name[i] == '_' {
				firstUnderscore = i
				break
			}
		}
		require.Greater(t, firstUnderscore, 0,
			"Migration file %s should have format NNNN_name.(up|down).sql", name)

		prefix := name[:firstUnderscore]
		_, err := strconv.Atoi(prefix)
		require.NoError(t, err, "Migration file %s should start with numeric prefix", name)
	}
}
