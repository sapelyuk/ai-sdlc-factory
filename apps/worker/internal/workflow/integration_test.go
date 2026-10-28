package workflow_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	temporalactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	workeractivity "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	agentctx "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/context"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/workflow"
)

// scriptedLLM returns queued responses in order and records every request.
// It implements agent.LLM, so the ReAct loop runs unchanged.
type scriptedLLM struct {
	mu        sync.Mutex
	responses []*client.ChatResponse
	requests  []*client.ChatRequest
}

func (m *scriptedLLM) Chat(_ context.Context, req *client.ChatRequest) (*client.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	idx := len(m.requests) - 1
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	return m.responses[idx], nil
}

func (m *scriptedLLM) Requests() []*client.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*client.ChatRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// recordedToolCall is one invocation observed by recordingTool.
type recordedToolCall struct {
	name           string
	args           json.RawMessage
	taskRunID      string
	idempotencyKey string
}

// recordingTool implements agent.Tool and records the forwarded metadata.
type recordingTool struct {
	mu    sync.Mutex
	calls []recordedToolCall
}

func (t *recordingTool) Call(
	_ context.Context,
	name string,
	args json.RawMessage,
	taskRunID string,
	idempotencyKey string,
) (*client.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calls = append(t.calls, recordedToolCall{
		name:           name,
		args:           args,
		taskRunID:      taskRunID,
		idempotencyKey: idempotencyKey,
	})
	return &client.ToolResult{
		Content: []client.ToolContent{{Type: "text", Text: "ok"}},
	}, nil
}

func (t *recordingTool) Calls() []recordedToolCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]recordedToolCall, len(t.calls))
	copy(out, t.calls)
	return out
}

// newEmptyService starts an httptest server that answers with an empty JSON array.
func newEmptyService(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const integrationHandoffJSON = `{
	"goal": "Implement feature",
	"context_refs": ["https://example.com/spec"],
	"acceptance_criteria": ["tests pass"],
	"expected_output_schema": {"type": "object"}
}`

// TestTaskRunWorkflow_Integration runs TaskRunWorkflow on the real Temporal
// test server with the real AgentStep activity. Only the LLM and Tool clients
// are replaced, so the workflow, activity wiring, ReAct loop, handoff
// validation and idempotency propagation are all exercised end to end.
func TestTaskRunWorkflow_Integration(t *testing.T) {
	semSrv := newEmptyService(t)
	kbSrv := newEmptyService(t)

	llm := &scriptedLLM{responses: []*client.ChatResponse{
		{
			LogicalCallID: "call-1",
			Status:        "succeeded",
			Content:       "I will run the tests",
			ToolCalls: []client.ToolCall{{
				ID:        "tc-1",
				Name:      "sandbox.exec",
				Arguments: json.RawMessage(`{"command":"go test ./..."}`),
			}},
			Usage: client.LLMUsage{TotalCostUSD: 0.01},
		},
		{
			LogicalCallID: "call-2",
			Status:        "succeeded",
			Content:       integrationHandoffJSON,
			Usage:         client.LLMUsage{TotalCostUSD: 0.01},
		},
	}}
	tool := &recordingTool{}

	validator, err := handoff.NewValidator()
	require.NoError(t, err)

	executor := agent.NewExecutor(
		llm,
		tool,
		agentctx.NewBuilder(
			client.NewSemanticClient(semSrv.URL),
			client.NewKnowledgeClient(kbSrv.URL),
		),
		handoff.NewBuilder(),
		validator,
	)
	stepActivity := workeractivity.NewAgentStepActivity(executor)

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(workflow.TaskRunWorkflow)
	env.RegisterActivityWithOptions(stepActivity.Execute, temporalactivity.RegisterOptions{
		Name: workflow.AgentStepActivityName,
	})

	input := workflow.TaskRunInput{
		TaskRunID:         "0190f0e0-0000-7000-8000-000000000001",
		RunID:             "0190f0e0-0000-7000-8000-000000000002",
		TaskID:            "0190f0e0-0000-7000-8000-000000000003",
		AgentID:           "0190f0e0-0000-7000-8000-000000000004",
		Profile:           "code-fast",
		TaskDescription:   "implement the feature",
		DefaultMaxCostUSD: 3.0,
		DefaultMaxSteps:   60,
	}

	env.ExecuteWorkflow(workflow.TaskRunWorkflow, input)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result *handoff.HandoffPackage
	require.NoError(t, env.GetWorkflowResult(&result))
	require.NotNil(t, result)
	require.Equal(t, "Implement feature", result.Goal)
	require.Equal(t, []string{"tests pass"}, result.AcceptanceCriteria)

	// The ReAct loop ran twice: tool call, then handoff.
	requests := llm.Requests()
	require.Len(t, requests, 2)

	// Idempotency keys are unique per step and present on every LLM call.
	require.NotEmpty(t, requests[0].IdempotencyKey)
	require.NotEmpty(t, requests[1].IdempotencyKey)
	require.NotEqual(t, requests[0].IdempotencyKey, requests[1].IdempotencyKey)

	// Attribution is forwarded to the LLM Gateway.
	require.NotNil(t, requests[0].RunID)
	require.Equal(t, input.RunID, *requests[0].RunID)
	require.NotNil(t, requests[0].TaskID)
	require.Equal(t, input.TaskID, *requests[0].TaskID)
	require.NotNil(t, requests[0].AgentID)
	require.Equal(t, input.AgentID, *requests[0].AgentID)

	// The tool call carries the same key as the step that produced it.
	calls := tool.Calls()
	require.Len(t, calls, 1)
	require.Equal(t, "sandbox.exec", calls[0].name)
	require.Equal(t, input.TaskRunID, calls[0].taskRunID)
	require.Equal(t, requests[0].IdempotencyKey, calls[0].idempotencyKey)
}
