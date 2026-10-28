package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	agentctx "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/context"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// StepResult is the outcome of a single AgentStep activity.
type StepResult struct {
	// Messages is the updated conversation after this step.
	Messages []client.ChatMessage `json:"messages"`
	// Usage is the cost consumed in this step.
	Usage client.LLMUsage `json:"usage"`
	// Handoff is set when the agent is ready to hand off (tool_calls empty).
	Handoff *handoff.HandoffPackage `json:"handoff,omitempty"`
	// RecentCalls carries tool-call hashes to the next step for loop detection.
	RecentCalls RecentCallHashes `json:"recent_calls,omitempty"`
	// BudgetExceeded indicates the agent must stop immediately.
	BudgetExceeded bool `json:"budget_exceeded,omitempty"`
	// BudgetExceededReason carries the error message when BudgetExceeded is true.
	BudgetExceededReason string `json:"budget_exceeded_reason,omitempty"`
}

// StepInput is the input to a single AgentStep.
type StepInput struct {
	TaskRunID string `json:"task_run_id"`
	// RunID, TaskID and AgentID are forwarded to the LLM Gateway for
	// attribution (llm_logical_calls.run_id/task_id/agent_id).
	RunID          string               `json:"run_id,omitempty"`
	TaskID         string               `json:"task_id,omitempty"`
	AgentID        string               `json:"agent_id,omitempty"`
	StepIndex      int                  `json:"step_index"`
	IdempotencyKey string               `json:"idempotency_key"`
	Messages       []client.ChatMessage `json:"messages"`
	Profile        string               `json:"profile"`
	ProjectID      string               `json:"project_id,omitempty"`
	TaskDesc       string               `json:"task_description"`
	Tools          []client.ToolDef     `json:"tools,omitempty"`
	BudgetSnapshot BudgetSnapshot       `json:"budget_snapshot"`
	MaxCostUSD     float64              `json:"max_cost_usd"`
	MaxSteps       int                  `json:"max_steps"`
	RecentCalls    RecentCallHashes     `json:"recent_calls,omitempty"`
}

// LLM is the subset of the LLM Gateway client used by the ReAct loop.
// The interface keeps the loop testable with in-process fakes.
type LLM interface {
	Chat(ctx context.Context, req *client.ChatRequest) (*client.ChatResponse, error)
}

// Tool is the subset of the Tool Gateway client used by the ReAct loop.
type Tool interface {
	Call(
		ctx context.Context,
		name string,
		args json.RawMessage,
		taskRunID string,
		idempotencyKey string,
	) (*client.ToolResult, error)
}

// Executor runs one ReAct iteration.
// It is wired by the activity layer with real HTTP clients.
type Executor struct {
	llm              LLM
	tool             Tool
	ctxBuilder       *agentctx.Builder
	handoffBuilder   *handoff.Builder
	handoffValidator *handoff.Validator
}

// NewExecutor creates a new agent executor.
func NewExecutor(
	llm LLM,
	tool Tool,
	ctxBuilder *agentctx.Builder,
	hb *handoff.Builder,
	hv *handoff.Validator,
) *Executor {
	return &Executor{
		llm:              llm,
		tool:             tool,
		ctxBuilder:       ctxBuilder,
		handoffBuilder:   hb,
		handoffValidator: hv,
	}
}

// ExecuteStep runs one ReAct iteration.
// At stepIndex==0 it builds the initial context and injects it as a system message.
func (e *Executor) ExecuteStep(ctx context.Context, input StepInput) (*StepResult, error) {
	msgs := input.Messages

	// On the first step, build context and prepend system message.
	if input.StepIndex == 0 && len(msgs) == 0 {
		built, err := e.ctxBuilder.Build(ctx, input.ProjectID, input.TaskDesc)
		if err != nil {
			return nil, fmt.Errorf("agent: context build: %w", err)
		}
		sysMsg := client.ChatMessage{Role: "system", Content: built.Content}
		userMsg := client.ChatMessage{Role: "user", Content: input.TaskDesc}
		msgs = []client.ChatMessage{sysMsg, userMsg}
	}

	// Call LLM with attribution and the per-step idempotency key.
	llmReq := &client.ChatRequest{
		Profile:        input.Profile,
		Messages:       msgs,
		Tools:          input.Tools,
		IdempotencyKey: input.IdempotencyKey,
	}
	if input.RunID != "" {
		llmReq.RunID = &input.RunID
	}
	if input.TaskID != "" {
		llmReq.TaskID = &input.TaskID
	}
	if input.AgentID != "" {
		llmReq.AgentID = &input.AgentID
	}
	llmResp, err := e.llm.Chat(ctx, llmReq)
	if err != nil {
		return nil, fmt.Errorf("agent: LLM call: %w", err)
	}

	// Check budget.
	guard := RestoreBudgetGuard(input.MaxCostUSD, input.MaxSteps, input.BudgetSnapshot)
	if err := guard.Consume(llmResp.Usage); err != nil {
		return &StepResult{
			Messages:             msgs,
			Usage:                llmResp.Usage,
			RecentCalls:          input.RecentCalls,
			BudgetExceeded:       true,
			BudgetExceededReason: err.Error(),
		}, nil
	}

	// If no tool calls, try to build handoff.
	if len(llmResp.ToolCalls) == 0 {
		pkg, err := e.handoffBuilder.Build(llmResp.Content)
		if err != nil {
			// LLM returned a non-JSON response or incomplete handoff.
			// Append assistant message and continue; the agent must regenerate.
			msgs = append(msgs, client.ChatMessage{Role: "assistant", Content: llmResp.Content})
			return &StepResult{
				Messages:    msgs,
				Usage:       llmResp.Usage,
				RecentCalls: input.RecentCalls,
			}, nil
		}
		// Validate the handoff package.
		if err := e.handoffValidator.Validate(pkg); err != nil {
			// Invalid handoff: return error so the agent regenerates.
			msgs = append(msgs, client.ChatMessage{Role: "assistant", Content: llmResp.Content})
			return &StepResult{
				Messages:    msgs,
				Usage:       llmResp.Usage,
				RecentCalls: input.RecentCalls,
			}, nil
		}
		return &StepResult{
			Messages:    msgs,
			Usage:       llmResp.Usage,
			RecentCalls: input.RecentCalls,
			Handoff:     pkg,
		}, nil
	}

	// Tool calls present: detect loops, execute each and append results.
	msgs = append(msgs, client.ChatMessage{Role: "assistant", Content: llmResp.Content})
	recent := input.RecentCalls
	for _, tc := range llmResp.ToolCalls {
		updated, err := recordToolCall(recent, tc.Name, tc.Arguments)
		if err != nil {
			return nil, err
		}
		recent = updated

		result, err := e.tool.Call(ctx, tc.Name, tc.Arguments, input.TaskRunID, input.IdempotencyKey)
		if err != nil {
			// Append error as tool result so LLM can recover.
			msgs = append(msgs, client.ChatMessage{
				Role:    "tool",
				Content: fmt.Sprintf("error calling %s: %v", tc.Name, err),
			})
			continue
		}
		outBytes, _ := json.Marshal(result)
		msgs = append(msgs, client.ChatMessage{
			Role:    "tool",
			Content: string(outBytes),
		})
	}

	return &StepResult{
		Messages:    msgs,
		Usage:       llmResp.Usage,
		RecentCalls: recent,
	}, nil
}
