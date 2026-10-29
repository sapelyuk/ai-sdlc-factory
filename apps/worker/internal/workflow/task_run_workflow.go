// Package workflow registers the Temporal workflows for the Agent Runtime.
package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// TaskRunInput is the input for TaskRunWorkflow.
type TaskRunInput struct {
	// TaskRunID uniquely identifies this task run instance.
	TaskRunID string `json:"task_run_id"`
	// WorkspaceID is the tenant that owns the run. It is mandatory: the audit
	// log is partitioned and RLS-filtered by workspace (issue #14), and every
	// event of the run must land in that workspace's chain.
	WorkspaceID uuid.UUID `json:"workspace_id"`
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

	// The audit event is required by the security contract, so a run without a
	// workspace cannot be attributed and must not start.
	if input.WorkspaceID == uuid.Nil {
		return nil, fmt.Errorf("workflow: workspace_id is required")
	}
	actor := audit.ActorForRun(input.AgentID)

	// Audit events are small writes; they get their own budget and must never
	// stall the run, so a failure is logged and the loop continues.
	auditCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	})

	runStartedKey, err := auditIdempotencyKey(ctx)
	if err != nil {
		return nil, err
	}
	recordAudit(auditCtx, audit.Event{
		WorkspaceID:    input.WorkspaceID,
		ActorID:        actor,
		Action:         sdkaudit.ActionRunStarted,
		IdempotencyKey: runStartedKey,
		ResourceID:     input.TaskRunID,
		Details: map[string]any{
			"task_run_id": input.TaskRunID,
			"run_id":      input.RunID,
			"task_id":     input.TaskID,
			"agent_id":    input.AgentID,
			"profile":     input.Profile,
		},
	})

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
			handoffKey, err := auditIdempotencyKey(ctx)
			if err != nil {
				return nil, err
			}
			recordAudit(auditCtx, audit.Event{
				WorkspaceID:    input.WorkspaceID,
				ActorID:        actor,
				Action:         sdkaudit.ActionHandoffCreated,
				IdempotencyKey: handoffKey,
				ResourceID:     input.TaskRunID,
				Details:        handoffDetails(input, result.Handoff),
			})
			completedKey, err := auditIdempotencyKey(ctx)
			if err != nil {
				return nil, err
			}
			recordAudit(auditCtx, audit.Event{
				WorkspaceID:    input.WorkspaceID,
				ActorID:        actor,
				Action:         sdkaudit.ActionTaskCompleted,
				IdempotencyKey: completedKey,
				ResourceID:     input.TaskRunID,
				Details: map[string]any{
					"task_run_id":    input.TaskRunID,
					"run_id":         input.RunID,
					"task_id":        input.TaskID,
					"agent_id":       input.AgentID,
					"profile":        input.Profile,
					"steps":          budgetSnap.Steps,
					"total_cost_usd": budgetSnap.TotalCost,
				},
			})
			return result.Handoff, nil
		}
	}

	return nil, fmt.Errorf("workflow: max steps %d reached without handoff", maxSteps)
}

// AgentStepActivityName is the registered name of the AgentStep activity.
const AgentStepActivityName = "AgentStep"

// AuditRecordActivityName is the registered name of the audit activity.
const AuditRecordActivityName = "AuditRecord"

func auditIdempotencyKey(ctx workflow.Context) (string, error) {
	var key string
	if err := workflow.SideEffect(ctx, func(workflow.Context) any {
		return uuid.NewString()
	}).Get(&key); err != nil {
		return "", fmt.Errorf("workflow: create audit idempotency key: %w", err)
	}
	return key, nil
}

// recordAudit appends one event through the audit activity.
//
// A failed append is logged and swallowed: an audit outage must not abort an
// agent run that is already consuming budget. The run stays attributable
// because the remaining events of the same chain are still written.
func recordAudit(ctx workflow.Context, ev audit.Event) {
	if err := workflow.ExecuteActivity(ctx, AuditRecordActivityName, ev).Get(ctx, nil); err != nil {
		workflow.GetLogger(ctx).Warn(
			"audit event was not recorded",
			"action", ev.Action,
			"resource_id", ev.ResourceID,
			"error", err,
		)
	}
}

// handoffDetails renders the handoff summary for the audit log. Only the goal
// and the item counts are stored: the full package is already persisted by the
// caller of the workflow, and the audit chain must stay compact.
func handoffDetails(input TaskRunInput, pkg *handoff.HandoffPackage) map[string]any {
	return map[string]any{
		"task_run_id":         input.TaskRunID,
		"run_id":              input.RunID,
		"task_id":             input.TaskID,
		"agent_id":            input.AgentID,
		"goal":                pkg.Goal,
		"context_refs":        len(pkg.ContextRefs),
		"acceptance_criteria": len(pkg.AcceptanceCriteria),
		"constraints":         len(pkg.Constraints),
		"decisions_made":      len(pkg.DecisionsMade),
		"open_questions":      len(pkg.OpenQuestions),
	}
}
