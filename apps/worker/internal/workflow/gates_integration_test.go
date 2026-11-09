package workflow_test

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
	tdactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	tdworkflow "go.temporal.io/sdk/workflow"

	gateact "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/workflow"
)

// TestHumanApprovalFullCycleWithPostgres runs the complete human_approval
// cycle against a real PostgreSQL container (issue #20):
//
//	create request -> owner approves via the store -> workflow poll observes
//	the decision -> the gate passes -> the transition is allowed.
//
// It also covers the rejection and the expiry paths.
func TestHumanApprovalFullCycleWithPostgres(t *testing.T) {
	pool, ctx := setupGatesPostgres(t)

	store := gates.NewPostgresStore(pool)
	svc := gates.NewService(gates.WithApprovalStore(store))

	ws := uuid.New()
	author := uuid.New()
	approver := uuid.New()
	createWorkspace(t, ctx, pool, ws, author)

	cases := []struct {
		name         string
		decide       func(id uuid.UUID) error
		wantPassed   bool
		wantTimeout  bool
		wantRejected bool
	}{
		{
			name: "approved",
			decide: func(id uuid.UUID) error {
				return store.Decide(ctx, id, true, approver, "ok")
			},
			wantPassed: true,
		},
		{
			name: "rejected",
			decide: func(id uuid.UUID) error {
				return store.Decide(ctx, id, false, approver, "no")
			},
			wantRejected: true,
		},
		{
			name: "expired",
			decide: func(id uuid.UUID) error {
				return store.Expire(ctx, id)
			},
			wantTimeout: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The decision is applied by a delayed callback, exactly like a
			// human answering the queue while the workflow waits.
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()

			var approvalID string
			env.RegisterActivityWithOptions(
				func(c context.Context, in gateact.GateCheckInput) (gateact.GateCheckOutput, error) {
					return gateact.NewGateCheckActivity(svc).Execute(c, in)
				}, tdactivity.RegisterOptions{Name: workflow.GateCheckActivityName})
			env.RegisterActivityWithOptions(
				func(c context.Context, in gateact.ApprovalCreateInput) (gateact.ApprovalCreateOutput, error) {
					out, err := gateact.NewApprovalCreateActivity(store).Execute(c, in)
					approvalID = out.ApprovalID
					return out, err
				}, tdactivity.RegisterOptions{Name: workflow.ApprovalCreateActivityName})
			env.RegisterActivityWithOptions(
				func(c context.Context, in gateact.ApprovalPollInput) (gateact.ApprovalPollOutput, error) {
					return gateact.NewApprovalPollActivity(store).Execute(c, in)
				}, tdactivity.RegisterOptions{Name: workflow.ApprovalPollActivityName})
			env.RegisterActivityWithOptions(
				func(c context.Context, in gateact.ApprovalExpireInput) error {
					return gateact.NewApprovalExpireActivity(store).Execute(c, in)
				}, tdactivity.RegisterOptions{Name: workflow.ApprovalExpireActivityName})
			env.RegisterActivityWithOptions(
				func(c context.Context, in gateact.ApprovalDecideInput) error {
					return gateact.NewApprovalDecideActivity(store).Execute(c, in)
				}, tdactivity.RegisterOptions{Name: workflow.ApprovalDecideActivityName})

			env.RegisterWorkflow(gateCycleWorkflow)
			env.RegisterDelayedCallback(func() {
				require.NotEmpty(t, approvalID, "approval request was not created")
				require.NoError(t, tc.decide(uuid.MustParse(approvalID)))
			}, 5*time.Second)

			env.ExecuteWorkflow(gateCycleWorkflow, workflow.WaitGateInput{
				WorkspaceID: ws,
				Name:        gates.GateProdApproved,
				RequestedBy: author,
				RiskLevel:   "R3",
				WorkflowID:  "gate-cycle-" + tc.name,
				Timeout:     2 * time.Minute,
			})
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var out workflow.GateOutcome
			require.NoError(t, env.GetWorkflowResult(&out))
			require.Equal(t, tc.wantPassed, out.Passed, "reason=%s", out.Reason)
			require.Equal(t, tc.wantRejected, out.Rejected, "reason=%s", out.Reason)
			require.Equal(t, tc.wantTimeout, out.Timeout, "reason=%s", out.Reason)

			// The queue keeps the decision: an approved request is not pending
			// and an expired one is terminal.
			if approvalID != "" {
				got, err := store.Get(ctx, uuid.MustParse(approvalID))
				require.NoError(t, err)
				switch {
				case tc.wantPassed:
					require.Equal(t, gates.ApprovalApproved, got.Status)
					require.NotNil(t, got.ApprovedBy)
					require.Equal(t, approver, *got.ApprovedBy)
				case tc.wantRejected:
					require.Equal(t, gates.ApprovalRejected, got.Status)
					require.Equal(t, "no", got.Comment)
				case tc.wantTimeout:
					require.Equal(t, gates.ApprovalExpired, got.Status)
				}
				// The request is linked to the workflow that waits on it.
				require.Equal(t, "gate-cycle-"+tc.name, got.WorkflowID)
				require.Equal(t, "R3", got.RiskLevel)
			}
		})
	}
}

// gateCycleWorkflow is a minimal workflow that executes WaitGate once.
func gateCycleWorkflow(ctx tdworkflow.Context, in workflow.WaitGateInput) (workflow.GateOutcome, error) {
	return workflow.WaitGate(ctx, in)
}

// setupGatesPostgres starts PostgreSQL with the approvals schema and returns a
// pool plus a context bound to the test lifetime.
func setupGatesPostgres(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for gate integration tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping gate integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("gates_cycle"),
		postgrescontainer.WithUsername("gates_cycle"),
		postgrescontainer.WithPassword("gates_cycle"),
		postgrescontainer.BasicWaitStrategies(),
	)
	if err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("failed to start PostgreSQL container in CI: %v", err)
		}
		t.Skipf("failed to start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	migrations := []string{
		"0001_init_squads_and_agents.up.sql",
		"0002_init_tasks_and_sdlc.up.sql",
		"0016_sdlc_gates.up.sql",
		"0017_approvals_run_id.up.sql",
		"0018_approvals_run_id_set_null.up.sql",
	}
	for _, name := range migrations {
		contents, err := os.ReadFile(filepath.Join(gateRepoRoot(t), "migrations", "postgres", name))
		require.NoError(t, err, "read migration %s", name)
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
	return pool, ctx
}

// createWorkspace inserts a workspace row so the approvals FK and RLS can be
// exercised by the store.
func createWorkspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, ws, owner uuid.UUID) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, ws.String())
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO workspaces (id, name, owner_id) VALUES ($1, 'gates-cycle', $2)`,
		ws, owner.String())
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func gateRepoRoot(t *testing.T) string {
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
