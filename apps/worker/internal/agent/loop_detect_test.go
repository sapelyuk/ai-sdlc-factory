package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// stubLLM returns a fixed response and records requests.
type stubLLM struct {
	response *client.ChatResponse
	requests []*client.ChatRequest
}

func (s *stubLLM) Chat(_ context.Context, req *client.ChatRequest) (*client.ChatResponse, error) {
	s.requests = append(s.requests, req)
	return s.response, nil
}

// countingTool counts invocations and records the forwarded metadata.
type countingTool struct {
	calls      int
	keys       []string
	taskRunIDs []string
}

func (c *countingTool) Call(
	_ context.Context,
	_ string,
	_ json.RawMessage,
	taskRunID string,
	idempotencyKey string,
) (*client.ToolResult, error) {
	c.calls++
	c.keys = append(c.keys, idempotencyKey)
	c.taskRunIDs = append(c.taskRunIDs, taskRunID)
	return &client.ToolResult{Content: []client.ToolContent{{Type: "text", Text: "ok"}}}, nil
}

func newLoopTestExecutor(t *testing.T, llm LLM, tool Tool) *Executor {
	t.Helper()
	validator, err := handoff.NewValidator()
	require.NoError(t, err)
	return NewExecutor(llm, tool, nil, handoff.NewBuilder(), validator)
}

func TestRecentCallHashes_DetectsRepeatedCall(t *testing.T) {
	var recent RecentCallHashes
	args := json.RawMessage(`{"command":"ls"}`)

	recent, loop := recent.Add("sandbox.exec", args)
	require.False(t, loop)
	require.Len(t, recent, 1)

	recent, loop = recent.Add("sandbox.exec", args)
	require.True(t, loop, "identical call must be detected as a loop")
	require.Len(t, recent, 1, "the window must not grow on a detected loop")
}

func TestRecentCallHashes_NormalizesJSONWhitespace(t *testing.T) {
	var recent RecentCallHashes
	recent, loop := recent.Add("sandbox.exec", json.RawMessage(`{"command":"ls"}`))
	require.False(t, loop)

	_, loop = recent.Add("sandbox.exec", json.RawMessage(`{ "command" : "ls" }`))
	require.True(t, loop, "whitespace differences must not defeat loop detection")
}

func TestRecentCallHashes_KeepsLastThreeCalls(t *testing.T) {
	var recent RecentCallHashes
	first := json.RawMessage(`{"n":1}`)
	recent, _ = recent.Add("tool", first)
	for _, n := range []string{`{"n":2}`, `{"n":3}`, `{"n":4}`} {
		recent, _ = recent.Add("tool", json.RawMessage(n))
	}
	require.Len(t, recent, maxRecentCalls)

	// The oldest call has been evicted, so it is no longer a loop.
	_, loop := recent.Add("tool", first)
	require.False(t, loop)
}

func TestExecutor_ExecuteStep_DetectsRepeatedToolCallInOneResponse(t *testing.T) {
	args := json.RawMessage(`{"command":"ls"}`)
	llm := &stubLLM{response: &client.ChatResponse{
		Content: "calling tool twice",
		ToolCalls: []client.ToolCall{
			{ID: "tc-1", Name: "sandbox.exec", Arguments: args},
			{ID: "tc-2", Name: "sandbox.exec", Arguments: args},
		},
		Usage: client.LLMUsage{TotalCostUSD: 0.01},
	}}
	tool := &countingTool{}
	exec := newLoopTestExecutor(t, llm, tool)

	_, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:      "run-1",
		StepIndex:      1,
		IdempotencyKey: "key-1",
		Messages:       []client.ChatMessage{{Role: "user", Content: "do it"}},
		MaxCostUSD:     3.0,
		MaxSteps:       60,
	})

	require.ErrorIs(t, err, ErrToolLoopDetected)
	require.Equal(t, 1, tool.calls, "the repeated call must not reach the tool")
}

func TestExecutor_ExecuteStep_DetectsLoopAcrossSteps(t *testing.T) {
	args := json.RawMessage(`{"command":"ls"}`)
	var previous RecentCallHashes
	previous, _ = previous.Add("sandbox.exec", args)

	llm := &stubLLM{response: &client.ChatResponse{
		ToolCalls: []client.ToolCall{{ID: "tc-1", Name: "sandbox.exec", Arguments: args}},
		Usage:     client.LLMUsage{TotalCostUSD: 0.01},
	}}
	tool := &countingTool{}
	exec := newLoopTestExecutor(t, llm, tool)

	_, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:      "run-1",
		StepIndex:      2,
		IdempotencyKey: "key-2",
		Messages:       []client.ChatMessage{{Role: "user", Content: "do it"}},
		RecentCalls:    previous,
		MaxCostUSD:     3.0,
		MaxSteps:       60,
	})

	require.ErrorIs(t, err, ErrToolLoopDetected)
	require.Zero(t, tool.calls)
}

func TestExecutor_ExecuteStep_ForwardsIdempotencyAndAttribution(t *testing.T) {
	llm := &stubLLM{response: &client.ChatResponse{
		ToolCalls: []client.ToolCall{{
			ID:        "tc-1",
			Name:      "sandbox.exec",
			Arguments: json.RawMessage(`{"command":"ls"}`),
		}},
		Usage: client.LLMUsage{TotalCostUSD: 0.01},
	}}
	tool := &countingTool{}
	exec := newLoopTestExecutor(t, llm, tool)

	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:      "run-1",
		RunID:          "run-root-1",
		TaskID:         "task-1",
		AgentID:        "agent-1",
		StepIndex:      1,
		IdempotencyKey: "key-1",
		Messages:       []client.ChatMessage{{Role: "user", Content: "do it"}},
		MaxCostUSD:     3.0,
		MaxSteps:       60,
	})
	require.NoError(t, err)
	require.Len(t, result.RecentCalls, 1)

	require.Len(t, llm.requests, 1)
	require.Equal(t, "key-1", llm.requests[0].IdempotencyKey)
	require.NotNil(t, llm.requests[0].RunID)
	require.Equal(t, "run-root-1", *llm.requests[0].RunID)
	require.NotNil(t, llm.requests[0].TaskID)
	require.Equal(t, "task-1", *llm.requests[0].TaskID)
	require.NotNil(t, llm.requests[0].AgentID)
	require.Equal(t, "agent-1", *llm.requests[0].AgentID)

	require.Equal(t, []string{"key-1"}, tool.keys)
	require.Equal(t, []string{"run-1"}, tool.taskRunIDs)
}
