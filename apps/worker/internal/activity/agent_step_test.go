package activity

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// fixedLLM always returns the same response and counts calls.
type fixedLLM struct {
	response *client.ChatResponse
	calls    int
}

func (f *fixedLLM) Chat(_ context.Context, _ *client.ChatRequest) (*client.ChatResponse, error) {
	f.calls++
	return f.response, nil
}

// idempotentTool emulates the Tool Gateway cache: a call with an already seen
// idempotency key returns the cached result without executing the tool again.
type idempotentTool struct {
	mu         sync.Mutex
	cache      map[string]*client.ToolResult
	executions int
	keys       []string
}

func newIdempotentTool() *idempotentTool {
	return &idempotentTool{cache: make(map[string]*client.ToolResult)}
}

func (t *idempotentTool) Call(
	_ context.Context,
	name string,
	args json.RawMessage,
	_ string,
	idempotencyKey string,
) (*client.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.keys = append(t.keys, idempotencyKey)
	cacheKey := idempotencyKey + "|" + name + "|" + string(args)
	if cached, ok := t.cache[cacheKey]; ok {
		return cached, nil
	}

	t.executions++
	result := &client.ToolResult{
		Content: []client.ToolContent{{Type: "text", Text: "ok"}},
	}
	t.cache[cacheKey] = result
	return result, nil
}

func newTestActivity(t *testing.T, llm agent.LLM, tool agent.Tool) *AgentStepActivity {
	t.Helper()
	validator, err := handoff.NewValidator()
	require.NoError(t, err)
	executor := agent.NewExecutor(llm, tool, nil, handoff.NewBuilder(), validator)
	return NewAgentStepActivity(executor)
}

// TestAgentStep_RetryDoesNotDuplicateToolCalls replays the same activity input
// twice, exactly as Temporal does on a retry. The idempotency key must reach
// the Tool Gateway unchanged, which lets it serve the cached result instead of
// executing the tool a second time.
func TestAgentStep_RetryDoesNotDuplicateToolCalls(t *testing.T) {
	llm := &fixedLLM{response: &client.ChatResponse{
		Content: "calling tool",
		ToolCalls: []client.ToolCall{{
			ID:        "tc-1",
			Name:      "sandbox.exec",
			Arguments: json.RawMessage(`{"command":"echo hi"}`),
		}},
		Usage: client.LLMUsage{TotalCostUSD: 0.01},
	}}
	tool := newIdempotentTool()
	activity := newTestActivity(t, llm, tool)

	input := agent.StepInput{
		TaskRunID:      "0190f0e0-0000-7000-8000-000000000001",
		StepIndex:      1,
		IdempotencyKey: "same-key",
		Messages:       []client.ChatMessage{{Role: "user", Content: "do it"}},
		MaxCostUSD:     3.0,
		MaxSteps:       60,
	}

	first, err := activity.Execute(context.Background(), input)
	require.NoError(t, err)
	require.NotEmpty(t, first.RecentCalls)

	second, err := activity.Execute(context.Background(), input)
	require.NoError(t, err)
	require.NotEmpty(t, second.RecentCalls)

	require.Equal(t, 1, tool.executions, "tool must execute once despite the retry")
	require.Equal(t, []string{"same-key", "same-key"}, tool.keys,
		"the retry must reuse the same idempotency key")
	require.Equal(t, 2, llm.calls, "the LLM call itself is retried")
}

// TestAgentStep_DifferentKeysExecuteTwice proves the deduplication is keyed by
// the idempotency key: a new step with a new key executes the tool again.
func TestAgentStep_DifferentKeysExecuteTwice(t *testing.T) {
	llm := &fixedLLM{response: &client.ChatResponse{
		ToolCalls: []client.ToolCall{{
			ID:        "tc-1",
			Name:      "sandbox.exec",
			Arguments: json.RawMessage(`{"command":"echo hi"}`),
		}},
		Usage: client.LLMUsage{TotalCostUSD: 0.01},
	}}
	tool := newIdempotentTool()
	activity := newTestActivity(t, llm, tool)

	base := agent.StepInput{
		TaskRunID:  "0190f0e0-0000-7000-8000-000000000001",
		StepIndex:  1,
		Messages:   []client.ChatMessage{{Role: "user", Content: "do it"}},
		MaxCostUSD: 3.0,
		MaxSteps:   60,
	}

	first := base
	first.IdempotencyKey = "key-1"
	_, err := activity.Execute(context.Background(), first)
	require.NoError(t, err)

	second := base
	second.IdempotencyKey = "key-2"
	_, err = activity.Execute(context.Background(), second)
	require.NoError(t, err)

	require.Equal(t, 2, tool.executions)
}
