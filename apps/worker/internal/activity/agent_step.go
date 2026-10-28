// Package activity registers the Temporal activities for the Agent Runtime.
package activity

import (
	"context"
	"errors"

	"go.temporal.io/sdk/temporal"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
)

// AgentStepActivity runs exactly one ReAct iteration.
// It is idempotent: the caller (workflow) guarantees the same idempotency key
// is used on retries via workflow.SideEffect.
type AgentStepActivity struct {
	executor *agent.Executor
}

// NewAgentStepActivity creates the activity handler.
func NewAgentStepActivity(executor *agent.Executor) *AgentStepActivity {
	return &AgentStepActivity{executor: executor}
}

// Execute runs one agent step.
//
// A detected tool-call loop is not retryable: repeating the same call cannot
// produce a different result, so the activity fails immediately instead of
// consuming the retry budget.
func (a *AgentStepActivity) Execute(ctx context.Context, input agent.StepInput) (*agent.StepResult, error) {
	result, err := a.executor.ExecuteStep(ctx, input)
	if err != nil && errors.Is(err, agent.ErrToolLoopDetected) {
		return nil, temporal.NewNonRetryableApplicationError(err.Error(), "ToolLoopDetected", err)
	}
	return result, err
}
