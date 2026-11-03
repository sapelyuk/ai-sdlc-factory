package gates

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
	"github.com/stretchr/testify/require"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestPostgresStoreCRUD exercises the PostgresStore with a real PostgreSQL
// container (issue #20). Skipped when Docker is unavailable.
func TestPostgresStoreCRUD(t *testing.T) {
	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker required for PostgresStore integration test: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("gates_test"),
		postgrescontainer.WithUsername("gates_test"),
		postgrescontainer.WithPassword("gates_test"),
		postgrescontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Apply migrations.
	migrations := []string{
		"0001_init_squads_and_agents.up.sql",
		"0002_init_tasks_and_sdlc.up.sql",
		"0016_sdlc_gates.up.sql",
	}
	for _, name := range migrations {
		contents, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", "postgres", name))
		require.NoError(t, err, "read migration %s", name)
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	store := NewPostgresStore(pool)
	ws := uuid.New()
	author := uuid.New()
	approver := uuid.New()

	// Create workspace (needed for FK + RLS).
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'gates', $2)`,
		ws, author.String())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	// Create approval request.
	req := BuildApprovalRequest(ws, GateProdApproved, author, 2*time.Hour, map[string]any{
		"from": "deploy", "to": "release",
	})
	trID := uuid.New()
	req.TaskRunID = &trID
	runID := uuid.New()
	req.RunID = &runID
	req.RiskLevel = "R3"
	req.WorkflowID = "wf-gates-1"

	require.NoError(t, store.Create(ctx, req))

	// Get.
	got, err := store.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, GateProdApproved, got.GateName)
	require.Equal(t, ws, got.WorkspaceID)
	require.Equal(t, "R3", got.RiskLevel)
	require.Equal(t, "wf-gates-1", got.WorkflowID)
	require.NotNil(t, got.DeadlineAt)

	// List pending.
	pending := ApprovalPending
	list, err := store.List(ctx, ws, &pending)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, req.ID, list[0].ID)

	// Approve.
	require.NoError(t, store.Decide(ctx, req.ID, true, approver, "looks good"))
	got, _ = store.Get(ctx, req.ID)
	require.Equal(t, ApprovalApproved, got.Status)
	require.NotNil(t, got.DecidedAt)

	// List approved.
	approved := ApprovalApproved
	list, err = store.List(ctx, ws, &approved)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// Create a second request, reject it.
	req2 := BuildApprovalRequest(ws, GateRequirementsApproved, author, time.Hour, nil)
	require.NoError(t, store.Create(ctx, req2))
	require.NoError(t, store.Decide(ctx, req2.ID, false, approver, "not yet"))
	got2, _ := store.Get(ctx, req2.ID)
	require.Equal(t, ApprovalRejected, got2.Status)

	// List pending should show only req2's predecessor was approved, req2 is rejected...
	// Actually req2 was created as pending then immediately rejected, so it's not in pending.
	list, err = store.List(ctx, ws, &pending)
	require.NoError(t, err)
	require.Len(t, list, 0)

	// Expire a new request.
	req3 := BuildApprovalRequest(ws, GateArchitectureApproved, author, 0, nil)
	require.NoError(t, store.Create(ctx, req3))
	require.NoError(t, store.Expire(ctx, req3.ID))
	got3, _ := store.Get(ctx, req3.ID)
	require.Equal(t, ApprovalExpired, got3.Status)

	// Idempotent expire.
	require.NoError(t, store.Expire(ctx, req3.ID)) // no-op
	got3, _ = store.Get(ctx, req3.ID)
	require.Equal(t, ApprovalExpired, got3.Status)

	// Idempotent decide.
	require.Error(t, store.Decide(ctx, req.ID, false, approver, "too late"))  // already approved
	require.Error(t, store.Decide(ctx, req3.ID, false, approver, "too late")) // already expired
}

func TestPostgresStoreWorkspaceIsolation(t *testing.T) {
	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		t.Skipf("Docker unavailable; skipping: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("gates_iso"),
		postgrescontainer.WithUsername("gates_iso"),
		postgrescontainer.WithPassword("gates_iso"),
		postgrescontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	migrations := []string{
		"0001_init_squads_and_agents.up.sql",
		"0002_init_tasks_and_sdlc.up.sql",
		"0016_sdlc_gates.up.sql",
	}
	for _, name := range migrations {
		contents, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", "postgres", name))
		require.NoError(t, err, "read migration %s", name)
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	store := NewPostgresStore(pool)
	ws1, ws2 := uuid.New(), uuid.New()
	author := uuid.New()

	// Create both workspaces.
	for _, ws := range []uuid.UUID{ws1, ws2} {
		tx, _ := pool.Begin(ctx)
		_, _ = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
		_, _ = tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'iso', $2)`,
			ws, author.String())
		_ = tx.Commit(ctx)
	}

	// Create a request in ws1.
	req := BuildApprovalRequest(ws1, GateProdApproved, author, time.Hour, nil)
	require.NoError(t, store.Create(ctx, req))

	// Store.Get reads via workspace_of which bypasses RLS (it uses the pool directly).
	// Then it runs inTenant with the resolved workspace. So it should work.
	got, err := store.Get(ctx, req.ID)
	require.NoError(t, err)
	require.Equal(t, ws1, got.WorkspaceID)

	// List for ws1 finds it.
	list, err := store.List(ctx, ws1, nil)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// List for ws2 finds nothing (RLS).
	list, err = store.List(ctx, ws2, nil)
	require.NoError(t, err)
	require.Len(t, list, 0)
}

func TestPostgresStoreNilPool(t *testing.T) {
	s := NewPostgresStore(nil)
	err := s.Create(context.Background(), ApprovalRequest{})
	require.Error(t, err)

	_, err = s.Get(context.Background(), uuid.New())
	require.Error(t, err)

	_, err = s.List(context.Background(), uuid.New(), nil)
	require.Error(t, err)

	require.Error(t, s.Decide(context.Background(), uuid.New(), true, uuid.New(), ""))
	require.Error(t, s.Expire(context.Background(), uuid.New()))
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
