package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDownSQLDropTableIfExists verifies that destructive down migrations use
// IF EXISTS for tables and indexes.
func TestDownSQLDropTableIfExists(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	dropTableRegex := regexp.MustCompile(`(?i)DROP\s+TABLE\s+IF\s+EXISTS`)
	dropIndexRegex := regexp.MustCompile(`(?i)DROP\s+INDEX\s+IF\s+EXISTS`)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 5 || name[len(name)-4:] != ".sql" {
			continue
		}
		base := name[:len(name)-4]
		if len(base) < 5 || base[len(base)-5:] != ".down" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		require.NoError(t, err, "failed to read %s", name)

		content := string(data)

		// Check that all DROP TABLE statements use IF EXISTS
		allDropTable := regexp.MustCompile(`(?i)DROP\s+TABLE`).FindAllStringIndex(content, -1)
		if len(allDropTable) > 0 {
			tableMatches := dropTableRegex.FindAllStringIndex(content, -1)
			assert.GreaterOrEqual(t, len(tableMatches), 1,
				"%s should use DROP TABLE IF EXISTS", name)
		} else {
			assert.True(t, regexp.MustCompile(`(?i)ALTER\s+TABLE|DROP\s+INDEX`).MatchString(content),
				"%s should contain a reversible schema change", name)
		}
		for _, match := range allDropTable {
			start := match[0]
			end := start + 25
			if end > len(content) {
				end = len(content)
			}
			following := strings.ToUpper(content[start:end])
			assert.True(t, strings.Contains(following, "IF EXISTS"),
				"All DROP TABLE in %s must use IF EXISTS, found at offset %d", name, start)
		}

		// Check for DROP INDEX IF EXISTS (if any DROP INDEX exists)
		allDropIndex := regexp.MustCompile(`(?i)DROP\s+INDEX`).FindAllStringIndex(content, -1)
		if len(allDropIndex) > 0 {
			indexMatches := dropIndexRegex.FindAllStringIndex(content, -1)
			assert.GreaterOrEqual(t, len(indexMatches), 1,
				"%s should contain DROP INDEX IF EXISTS", name)
		}
	}
}

// TestNoDangerousCommands verifies that no migration contains dangerous commands.
func TestNoDangerousCommands(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	// Dangerous patterns that must never appear.
	dangerousPatterns := []struct {
		name    string
		re      *regexp.Regexp
		message string
	}{
		{
			name:    "DROP DATABASE",
			re:      regexp.MustCompile(`(?i)DROP\s+DATABASE`),
			message: "DROP DATABASE is not allowed in migrations",
		},
		{
			name:    "TRUNCATE",
			re:      regexp.MustCompile(`(?i)TRUNCATE\s+`),
			message: "TRUNCATE is dangerous in migrations (use DROP TABLE instead)",
		},
		{
			name:    "ALTER SYSTEM",
			re:      regexp.MustCompile(`(?i)ALTER\s+SYSTEM`),
			message: "ALTER SYSTEM is not allowed in migrations",
		},
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 4 || name[len(name)-4:] != ".sql" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		require.NoError(t, err, "failed to read %s", name)
		content := string(data)

		for _, dp := range dangerousPatterns {
			assert.False(t, dp.re.MatchString(content),
				"%s should not contain: %s", name, dp.message)
		}

		// DROP SCHEMA is only rejected when NOT using IF EXISTS.
		dropSchemaRe := regexp.MustCompile(`(?i)DROP\s+SCHEMA`)
		for _, match := range dropSchemaRe.FindAllStringIndex(content, -1) {
			start := match[0]
			end := start + 30
			if end > len(content) {
				end = len(content)
			}
			following := strings.ToUpper(content[start:end])
			assert.True(t, strings.Contains(following, "IF EXISTS"),
				"%s: DROP SCHEMA must use IF EXISTS", name)
		}
	}
}

// TestUpSQLHasBEGINCOMMIT verifies that all .up.sql files wrap changes in BEGIN/COMMIT.
func TestUpSQLHasBEGINCOMMIT(t *testing.T) {
	assertBeginCommit(t, ".up")
}

// TestDownSQLHasBEGINCOMMIT verifies that all .down.sql files wrap changes in BEGIN/COMMIT.
func TestDownSQLHasBEGINCOMMIT(t *testing.T) {
	assertBeginCommit(t, ".down")
}

func assertBeginCommit(t *testing.T, suffix string) {
	t.Helper()
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	beginRegex := regexp.MustCompile(`(?im)^\s*BEGIN\s*;`)
	commitRegex := regexp.MustCompile(`(?im)^\s*COMMIT\s*;`)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 4 || name[len(name)-4:] != ".sql" {
			continue
		}
		base := name[:len(name)-4]
		if len(base) < len(suffix) || base[len(base)-len(suffix):] != suffix {
			continue
		}

		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		require.NoError(t, err, "failed to read %s", name)
		content := string(data)

		assert.True(t, beginRegex.MatchString(content),
			"%s should start with BEGIN;", name)
		assert.True(t, commitRegex.MatchString(content),
			"%s should end with COMMIT;", name)
	}
}

// TestAllTablesHaveWorkspaceID verifies that migrations creating tables
// include workspace_id columns and RLS policies.
func TestAllTablesHaveWorkspaceID(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	workspaceIDRegex := regexp.MustCompile(`(?i)workspace_id\s+UUID\s+NOT\s+NULL`)
	rlsRegex := regexp.MustCompile(`(?i)ENABLE\s+ROW\s+LEVEL\s+SECURITY`)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 4 || name[len(name)-4:] != ".sql" {
			continue
		}
		base := name[:len(name)-4]
		if len(base) < 3 || base[len(base)-3:] != ".up" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		require.NoError(t, err, "failed to read %s", name)

		content := string(data)
		if !regexp.MustCompile(`(?i)CREATE\s+TABLE`).MatchString(content) {
			continue
		}

		// Check for workspace_id NOT NULL
		workspaceMatches := workspaceIDRegex.FindAllStringIndex(content, -1)
		assert.GreaterOrEqual(t, len(workspaceMatches), 1,
			"%s should define workspace_id UUID NOT NULL", name)

		// Check for RLS
		rlsMatches := rlsRegex.FindAllStringIndex(content, -1)
		assert.GreaterOrEqual(t, len(rlsMatches), 1,
			"%s should enable Row Level Security", name)
	}
}

// TestNoUUIDFallbacks verifies that migrations use native uuidv7() and not
// gen_random_uuid()/uuid_generate_v4()/pg_uuidv7 (hard rule, PG 17).
func TestNoUUIDFallbacks(t *testing.T) {
	migrationsDir := getMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	forbidden := []struct {
		name string
		re   *regexp.Regexp
	}{
		{"gen_random_uuid", regexp.MustCompile(`(?i)gen_random_uuid\s*\(`)},
		{"uuid_generate_v4", regexp.MustCompile(`(?i)uuid_generate_v4\s*\(`)},
		{"pg_uuidv7", regexp.MustCompile(`(?i)pg_uuidv7`)},
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) < 4 || name[len(name)-4:] != ".sql" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(migrationsDir, name))
		require.NoError(t, err, "failed to read %s", name)
		content := string(data)

		for _, f := range forbidden {
			assert.False(t, f.re.MatchString(content),
				"%s must not use %s (use native uuidv7())", name, f.name)
		}
	}
}

// TestNoCypherFiles verifies that migrations/graph contains only SQL, never
// Cypher/Neo4j files (ADR-003).
func TestNoCypherFiles(t *testing.T) {
	graphDir := filepath.Join(filepath.Dir(getMigrationsDir(t)), "graph")
	if _, err := os.Stat(graphDir); err != nil {
		t.Skipf("graph dir not found: %v", err)
	}
	entries, err := os.ReadDir(graphDir)
	require.NoError(t, err)

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		assert.False(t, strings.HasSuffix(strings.ToLower(e.Name()), ".cypher"),
			"graph dir must not contain Cypher files (ADR-003): %s", e.Name())
	}
}
