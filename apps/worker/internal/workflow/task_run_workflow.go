// Package workflow registers the Temporal workflows for the Agent Runtime.
package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// TaskRunInput is the input for TaskRunWorkflow.
type TaskRunInput struct {
	// TaskRunID uniquely identifies this task run instance.
	TaskRunID string `json:"task_run_id"`
	// RunID is the SDLC run that owns this task run (LLM attribution).
	RunID string `json:"run_id,omitempty"`
	// TaskID is the task being executed (LLM attribution).
	TaskID string `json:"task_id,omitempty"`
	// AgentID is the agent executing the task (LLM attribution).
	AgentID string `json:"agent_id,omitempty"`
	// Profile is the LLM capability profile to use.
	Profile string `json:"profile"`
	// ProjectID is the semantic-layer project scope (optional).
	ProjectID string `json:"project_id,omitempty"`
	// TaskDescription is the natural-language description of the task.
	TaskDescription string `json:"task_description"`
	// Tools is the list of MCP tools available to the agent.
	Tools []client.ToolDef `json:"tools,omitempty"`
	// MaxCostUSD overrides the default cost budget (0 = use default).
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
	// MaxSteps overrides the default steps budget (0 = use default).
	MaxSteps int `json:"max_steps,omitempty"`
	// DefaultMaxCostUSD is injected from the worker config.
	DefaultMaxCostUSD float64 `json:"default_max_cost_usd"`
	// DefaultMaxSteps is injected from the worker config.
	DefaultMaxSteps int `json:"default_max_steps"`
}

// TaskRunWorkflow runs a full ReAct agent loop for one task.
// It loops over AgentStep activities until a valid handoff is produced
// or the budget is exhausted.
func TaskRunWorkflow(ctx workflow.Context, input TaskRunInput) (*handoff.HandoffPackage, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Resolve effective limits.
	maxCost := input.DefaultMaxCostUSD
	if input.MaxCostUSD > 0 {
		maxCost = input.MaxCostUSD
	}
	maxSteps := input.DefaultMaxSteps
	if input.MaxSteps > 0 {
		maxSteps = input.MaxSteps
	}

	// Idempotency keys are generated per step via SideEffect. Temporal records
	// the result in the workflow history, so an activity retry reuses the same
	// key while every step gets a distinct one.
	var messages []client.ChatMessage
	var recentCalls agent.RecentCallHashes
	budgetSnap := agent.BudgetSnapshot{}

	for stepIndex := 0; stepIndex < maxSteps; stepIndex++ {
		var idempotencyKey string
		if err := workflow.SideEffect(ctx, func(ctx workflow.Context) any {
			return uuid.NewString()
		}).Get(&idempotencyKey); err != nil {
			return nil, fmt.Errorf("workflow: side effect: %w", err)
		}

		activityInput := agent.StepInput{
			TaskRunID:      input.TaskRunID,
			RunID:          input.RunID,
			TaskID:         input.TaskID,
			AgentID:        input.AgentID,
			StepIndex:      stepIndex,
			IdempotencyKey: idempotencyKey,
			Messages:       messages,
			Profile:        input.Profile,
			ProjectID:      input.ProjectID,
			TaskDesc:       input.TaskDescription,
			Tools:          input.Tools,
			BudgetSnapshot: budgetSnap,
			MaxCostUSD:     maxCost,
			MaxSteps:       maxSteps,
			RecentCalls:    recentCalls,
		}

		var result agent.StepResult
		if err := workflow.ExecuteActivity(ctx, AgentStepActivityName, activityInput).Get(ctx, &result); err != nil {
			return nil, fmt.Errorf("workflow: agent step %d: %w", stepIndex, err)
		}

		messages = result.Messages
		recentCalls = result.RecentCalls
		budgetSnap.Steps++
		budgetSnap.TotalCost += result.Usage.TotalCostUSD

		// Hard stop on budget exceeded.
		if result.BudgetExceeded {
			return nil, fmt.Errorf("workflow: budget exceeded at step %d: %s", stepIndex, result.BudgetExceededReason)
		}

		// Handoff ready.
		if result.Handoff != nil {
			return result.Handoff, nil
		}
	}

	return nil, fmt.Errorf("workflow: max steps %d reached without handoff", maxSteps)
}

// AgentStepActivityName is the registered name of the AgentStep activity.
const AgentStepActivityName = "AgentStep"
