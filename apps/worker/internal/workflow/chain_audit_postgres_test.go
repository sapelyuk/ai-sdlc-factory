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
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	workeractivity "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/workflow"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// chainMigrationFiles builds the global audit_log table (see the SDK test for
// the numbering rationale).
var chainMigrationFiles = []string{
	"0001_init_squads_and_agents.up.sql",
	"0002_init_tasks_and_sdlc.up.sql",
	"0007_init_audit_and_cost.up.sql",
	"0014_global_audit_log.up.sql",
}

// setupChainAuditPostgres starts a PostgreSQL container with the audit schema
// and returns a real audit logger backed by it. Tests are skipped when Docker
// is unavailable outside CI.
func setupChainAuditPostgres(t *testing.T) *sdkaudit.Logger {
	t.Helper()

	if output, err := exec.CommandContext(t.Context(), "docker", "info").CombinedOutput(); err != nil {
		if ci := os.Getenv("CI"); ci != "" && ci != "false" {
			t.Fatalf("Docker is required for chain audit integration tests in CI: %s", strings.TrimSpace(string(output)))
		}
		t.Skipf("Docker is unavailable; skipping chain audit integration test: %s", strings.TrimSpace(string(output)))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)

	container, err := postgrescontainer.Run(ctx, "pgvector/pgvector:pg18",
		postgrescontainer.WithDatabase("chain_audit_test"),
		postgrescontainer.WithUsername("chain_audit_test"),
		postgrescontainer.WithPassword("chain_audit_test"),
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
	t.Cleanup(pool.Close)

	migrationsDir := filepath.Join("..", "..", "..", "..", "migrations", "postgres")
	for _, name := range chainMigrationFiles {
		contents, err := os.ReadFile(filepath.Join(migrationsDir, filepath.Base(name)))
		if err != nil {
			t.Skipf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(contents)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}

	logger := sdkaudit.NewLogger(pool)
	return logger
}

// newChainEnv builds a test workflow environment that persists audit events
// through the real audit activity. The child TaskRunWorkflow is mocked, so
// the assertions focus on the events the chain itself produces.
func newChainEnv(t *testing.T, logger sdkaudit.Auditor, results ...*workflow.TaskRunResult) *testsuite.TestWorkflowEnvironment {
	t.Helper()

	auditActivity := workeractivity.NewAuditActivity(logger)
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(auditActivity.Execute, temporalactivity.RegisterOptions{
		Name: workflow.AuditRecordActivityName,
	})
	for _, res := range results {
		result := res
		env.OnWorkflow(workflow.TaskRunWorkflow, mock.Anything, mock.Anything).
			Return(result, nil).Once()
	}
	return env
}

// routedRecords filters the persisted records for handoff_routed events.
func routedRecords(records []sdkaudit.Record) []sdkaudit.Record {
	var out []sdkaudit.Record
	for _, rec := range records {
		if rec.Action == sdkaudit.ActionHandoffRouted {
			out = append(out, rec)
		}
	}
	return out
}

// requireUniqueKeys asserts the idempotency keys of the chain are distinct:
// a replayed routing decision must not collapse into one record.
func requireUniqueKeys(t *testing.T, records []sdkaudit.Record) {
	t.Helper()
	keys := map[string]bool{}
	for _, rec := range records {
		require.NotEmpty(t, rec.IdempotencyKey)
		require.False(t, keys[rec.IdempotencyKey], "duplicated idempotency key %s", rec.IdempotencyKey)
		keys[rec.IdempotencyKey] = true
	}
}

// TestChainWorkflow_AuditEventsPersisted verifies that the routing decision
// of a one-hop chain survives in the tamper-evident log as a verifiable
// handoff_routed record (issue #21).
func TestChainWorkflow_AuditEventsPersisted(t *testing.T) {
	logger := setupChainAuditPostgres(t)

	env := newChainEnv(t, logger, &workflow.TaskRunResult{
		Handoff: chainAuditPkg("Design reviewed", "agent-pm"),
	})

	ws := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	env.ExecuteWorkflow(workflow.ChainWorkflow, workflow.ChainInput{
		WorkspaceID:     ws,
		Team:            chainAuditTeam(handoff.TopologyOrchestrated),
		TaskDescription: "build the feature",
	})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)

	records, err := logger.Records(context.Background(), ws)
	require.NoError(t, err)
	require.NoError(t, sdkaudit.VerifyChain(records))
	requireUniqueKeys(t, records)

	routed := routedRecords(records)
	require.Len(t, routed, 1)
	details := routed[0].DetailsMap()
	require.Equal(t, true, details["terminal"])
	require.Equal(t, "agent-pm", details["from_agent_id"])
	require.Equal(t, "Design reviewed", details["goal"])
	// The routed object is the produced package.
	require.Equal(t, "agent-pm", routed[0].ResourceID)
	require.Equal(t, sdkaudit.SystemActor("worker"), routed[0].ActorID)
}

// TestChainWorkflow_AuditRecordsReturnToSender verifies that a rejected
// package produces the routing event that returns it to its author, all
// inside one verifiable audit chain (issue #21, SYSTEM_DESIGN.md §8.4).
func TestChainWorkflow_AuditRecordsReturnToSender(t *testing.T) {
	logger := setupChainAuditPostgres(t)

	env := newChainEnv(t, logger,
		&workflow.TaskRunResult{Handoff: chainAuditPkg("Implemented", "agent-dev")},
		&workflow.TaskRunResult{Rejected: true, Missing: []string{"acceptance_criteria"}},
		&workflow.TaskRunResult{Handoff: chainAuditPkg("Implemented v2", "agent-dev")},
		&workflow.TaskRunResult{Handoff: chainAuditPkg("QA passed", "agent-architect")},
	)

	ws := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	env.ExecuteWorkflow(workflow.ChainWorkflow, workflow.ChainInput{
		WorkspaceID:     ws,
		Team:            chainAuditTeam(handoff.TopologyPipeline),
		TaskDescription: "build the feature",
		InitialAgentID:  "agent-dev",
	})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)

	records, err := logger.Records(context.Background(), ws)
	require.NoError(t, err)
	require.NoError(t, sdkaudit.VerifyChain(records))
	requireUniqueKeys(t, records)

	routed := routedRecords(records)
	require.Len(t, routed, 4)

	// dev -> architect: the review matrix routes to the first allowed
	// reviewer.
	first := routed[0].DetailsMap()
	require.Equal(t, "agent-dev", first["from_agent_id"])
	require.Equal(t, "agent-architect", first["to_agent_id"])

	// architect -> dev: the return to sender carries the receiver
	// diagnostics.
	second := routed[1].DetailsMap()
	require.Equal(t, true, second["returned_to_sender"])
	require.Equal(t, "agent-architect", second["from_agent_id"])
	require.Equal(t, "agent-dev", second["to_agent_id"])
	require.Equal(t, []interface{}{"acceptance_criteria"}, second["missing"])

	// dev -> architect again, then the terminal decision.
	third := routed[2].DetailsMap()
	require.Equal(t, "agent-dev", third["from_agent_id"])
	require.Equal(t, "agent-architect", third["to_agent_id"])
	require.Equal(t, true, routed[3].DetailsMap()["terminal"])

	// Every routing decision is a system decision of the worker.
	for _, rec := range routed {
		require.Equal(t, sdkaudit.SystemActor("worker"), rec.ActorID)
	}
}

// chainAuditTeam builds a resolved team for the chain audit tests.
func chainAuditTeam(topology string) handoff.TeamConfig {
	return handoff.TeamConfig{
		ID:       "team-audit",
		Topology: topology,
		Lead:     &handoff.TeamMember{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
		Members: []handoff.TeamMember{
			{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
			{AgentID: "agent-architect", RoleName: "software-architect", Profile: "arch-strong"},
			{AgentID: "agent-dev", RoleName: "software-engineer", Profile: "code-fast"},
			{AgentID: "agent-qa", RoleName: "qa-engineer", Profile: "code-fast"},
		},
		ReviewMatrix: handoff.ReviewMatrix{
			RequiredReviewers: 1,
			AllowedRoles:      []string{"software-architect", "qa-engineer"},
		},
	}
}

// chainAuditPkg builds a complete package stamped with the given source agent.
func chainAuditPkg(goal, source string) *handoff.HandoffPackage {
	return &handoff.HandoffPackage{
		Goal:               goal,
		ContextRefs:        []string{"sem://project/1"},
		AcceptanceCriteria: []string{"tests pass"},
		Metadata:           &handoff.HandoffMetadata{SourceAgentID: source},
	}
}
