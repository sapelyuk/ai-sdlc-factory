// Package activity registers the Temporal activities for the Agent Runtime.
package activity

import (
	"context"

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
func (a *AgentStepActivity) Execute(ctx context.Context, input agent.StepInput) (*agent.StepResult, error) {
	return a.executor.ExecuteStep(ctx, input)
}
