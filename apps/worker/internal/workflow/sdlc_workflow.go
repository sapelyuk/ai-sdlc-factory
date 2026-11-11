package workflow

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	"go.temporal.io/sdk/workflow"
)

// SDLCInput is the input for the minimal SDLC workflow.
type SDLCInput struct {
	TaskRunID string `json:"task_run_id"`
	// WorkspaceID is the tenant of the run. It is forwarded to the child
	// workflow, where it becomes mandatory for the audit log (issue #14).
	WorkspaceID uuid.UUID `json:"workspace_id"`
	RunID       string    `json:"run_id,omitempty"`
	TaskID      string    `json:"task_id,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	Profile     string    `json:"profile"`
	TaskDesc    string    `json:"task_description"`
	ProjectID   string    `json:"project_id,omitempty"`
	MaxCostUSD  float64   `json:"max_cost_usd,omitempty"`
	MaxSteps    int       `json:"max_steps,omitempty"`
	// Defaults are injected from worker config.
	DefaultMaxCostUSD float64 `json:"default_max_cost_usd"`
	DefaultMaxSteps   int     `json:"default_max_steps"`
	// Gate is the quality gate declared by the transition that this run
	// produces the input for (issue #126). When nil, the run produces
	// its handoff directly (current behavior, R0/R1).
	// When set, the production path goes through ChainWorkflow which
	// evaluates the gate before accepting the output (SDLC_STATE_MACHINE.md §4).
	Gate *GateSpec `json:"gate,omitempty"`
	// Team is the resolved team for the gate path. When Gate != nil,
	// this must be a valid single-agent team wrapping the current agent.
	// Required by ChainInput.Team (issue #21).
	Team *handoff.TeamConfig `json:"team,omitempty"`
	// RequestedBy is the human owner who initiated the run (for audit and
	// approval queue). Required when Gate is set.
	RequestedBy uuid.UUID `json:"requested_by,omitempty"`
}

// SDLCWorkflow is a minimal SDLC workflow: a single TaskRunWorkflow child call.
// Stages, transitions, and gates are out of scope (tracked as #40).
func SDLCWorkflow(ctx workflow.Context, input SDLCInput) (*handoff.HandoffPackage, error) {
	// Create child workflow options.
	cwo := workflow.ChildWorkflowOptions{
		WorkflowID: "task-run-" + input.TaskRunID,
	}
	ctx = workflow.WithChildOptions(ctx, cwo)

	taskInput := TaskRunInput{
		TaskRunID:         input.TaskRunID,
		WorkspaceID:       input.WorkspaceID,
		RunID:             input.RunID,
		TaskID:            input.TaskID,
		AgentID:           input.AgentID,
		Profile:           input.Profile,
		ProjectID:         input.ProjectID,
		TaskDescription:   input.TaskDesc,
		MaxCostUSD:        input.MaxCostUSD,
		MaxSteps:          input.MaxSteps,
		DefaultMaxCostUSD: input.DefaultMaxCostUSD,
		DefaultMaxSteps:   input.DefaultMaxSteps,
	}

	var result *TaskRunResult
	if err := workflow.ExecuteChildWorkflow(ctx, TaskRunWorkflow, taskInput).Get(ctx, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("workflow: task run returned no result")
	}
	if result.Rejected {
		return nil, fmt.Errorf("workflow: task run rejected its input: missing %v", result.Missing)
	}
	return result.Handoff, nil
}
