package tests

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
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

	var workspacePathExists int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'task_runs' AND column_name = 'workspace_path'").Scan(&workspacePathExists)
	require.NoError(t, err)
	assert.Equal(t, 1, workspacePathExists, "task_runs.workspace_path should exist after migrations")

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

	expectedUp := 14
	expectedDown := 14

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

// TestMigration0014GuardsExistingRows verifies that migration 0014 refuses to run
// when audit_log already holds rows. The table is created empty by migration
// 0007, and 0014 adds a NOT NULL hash column: PostgreSQL cannot satisfy that for
// pre-existing rows, and any backfilled hash would not belong to a verifiable
// chain, so the migration must fail fast instead of corrupting evidence.
func TestMigration0014GuardsExistingRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := startTestPostgres(t)
	defer db.Close()

	files := loadSQLFiles(t, getMigrationsDir(t))

	upNames := make([]string, 0, len(files))
	for name := range files {
		if len(name) >= 3 && name[len(name)-3:] == ".up" {
			upNames = append(upNames, name)
		}
	}
	sort.Strings(upNames)

	// Apply every migration except 0014, so audit_log exists in its 0007 shape.
	for _, name := range upNames {
		if strings.HasPrefix(name, "0014_") {
			continue
		}
		applySQL(t, ctx, db, files[name])
	}

	// A fresh schema leaves the log empty, and that is the state 0014 accepts.
	var rows int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM audit_log").Scan(&rows))
	require.Equal(t, 0, rows, "audit_log must start empty")

	// Seed a workspace, then add one legacy audit row (resource_type is part of
	// the pre-0014 shape).
	_, err := db.ExecContext(ctx,
		"INSERT INTO workspaces (name, owner_id) VALUES ('guard-test', 'human:guard-test')")
	require.NoError(t, err, "seed workspace")

	_, err = db.ExecContext(ctx,
		`INSERT INTO audit_log (workspace_id, actor_id, action, resource_type, resource_id)
		 SELECT id, 'system:test', 'tool_call', 'tool', NULL FROM workspaces WHERE name = 'guard-test'`)
	require.NoError(t, err, "seed audit row")

	// Re-running 0014 against the non-empty table must fail with the guard error.
	//
	// The migration opens an explicit BEGIN;, and PostgreSQL stops the batch at
	// the first error, so the session is left in an aborted transaction. A
	// dedicated connection makes the rollback deterministic.
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, files["0014_global_audit_log.up"])
	require.Error(t, err, "migration 0014 must reject a non-empty audit_log")
	assert.Contains(t, err.Error(), "requires empty audit_log",
		"the failure must name the guard requirement")

	_, err = conn.ExecContext(ctx, "ROLLBACK")
	require.NoError(t, err, "reset the transaction aborted by the guard")

	// The whole migration is one transaction: it must have rolled back, leaving
	// both the data and the old column shape untouched.
	var rowsAfter int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT count(*) FROM audit_log").Scan(&rowsAfter))
	assert.Equal(t, 1, rowsAfter, "the failed migration must not delete evidence")

	var hashColumn int
	require.NoError(t, conn.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'audit_log' AND column_name = 'hash'").Scan(&hashColumn))
	assert.Equal(t, 0, hashColumn, "the failed migration must not add the hash column")

	var resourceType int
	require.NoError(t, conn.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'audit_log' AND column_name = 'resource_type'").Scan(&resourceType))
	assert.Equal(t, 1, resourceType, "the failed migration must not drop the old columns")
}

// TestMigration0014AppliesOnEmptyTable pins the other side of the guard: the
// normal forward path (audit_log created empty by 0007) must still succeed, so
// the guard cannot be an unconditional abort.
func TestMigration0014AppliesOnEmptyTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := startTestPostgres(t)
	defer db.Close()

	files := loadSQLFiles(t, getMigrationsDir(t))

	upNames := make([]string, 0, len(files))
	for name := range files {
		if len(name) >= 3 && name[len(name)-3:] == ".up" {
			upNames = append(upNames, name)
		}
	}
	sort.Strings(upNames)

	for _, name := range upNames {
		applySQL(t, ctx, db, files[name])
	}

	var hashColumn int
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT count(*) FROM information_schema.columns WHERE table_name = 'audit_log' AND column_name = 'hash'").Scan(&hashColumn))
	assert.Equal(t, 1, hashColumn, "hash column should exist after a clean migration run")
}
