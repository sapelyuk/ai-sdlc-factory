package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	agentctx "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/context"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// newMockLLMServer returns a server that responds with the given content and optional tool_calls.
func newMockLLMServer(content string, toolCalls []client.ToolCall) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := client.ChatResponse{
			LogicalCallID: "call-1",
			Status:        "succeeded",
			ModelUsed:     "test-model",
			ProviderUsed:  "test",
			Content:       content,
			ToolCalls:     toolCalls,
			Usage:         client.LLMUsage{CostUSD: 0.01, TotalCostUSD: 0.01},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newMockToolServer returns a server that responds with a fixed tool result.
func newMockToolServer(resultText string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolResult := client.ToolResult{
			Content: []client.ToolContent{{Type: "text", Text: resultText}},
		}
		resultJSON, _ := json.Marshal(toolResult)
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  json.RawMessage(resultJSON),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newMockSemServer returns a server that returns empty semantic nodes.
func newMockSemServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.SemNode{})
	}))
}

// newMockKBSrv returns a server that returns empty KB fragments.
func newMockKBSrv() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]client.KBFragment{})
	}))
}

func TestExecutor_ExecuteStep_NoToolCalls_ValidHandoff(t *testing.T) {
	handoffJSON := `{
		"goal": "Test goal",
		"context_refs": ["https://example.com/1"],
		"acceptance_criteria": ["criterion"],
		"expected_output_schema": {"type": "object"}
	}`

	llmSrv := newMockLLMServer(handoffJSON, nil)
	defer llmSrv.Close()
	semSrv := newMockSemServer()
	defer semSrv.Close()
	kbSrv := newMockKBSrv()
	defer kbSrv.Close()

	hb := handoff.NewBuilder()
	hv, err := handoff.NewValidator()
	if err != nil {
		t.Fatalf("validator: %v", err)
	}

	exec := NewExecutor(
		client.NewLLMClient(llmSrv.URL),
		client.NewToolClient("http://unused:9999"),
		agentctx.NewBuilder(client.NewSemanticClient(semSrv.URL), client.NewKnowledgeClient(kbSrv.URL)),
		hb, hv,
	)

	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:  "run-1",
		StepIndex:  0,
		Profile:    "code-fast",
		TaskDesc:   "do something",
		MaxCostUSD: 10.0,
		MaxSteps:   10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff == nil {
		t.Fatal("expected handoff to be set")
	}
	if result.Handoff.Goal != "Test goal" {
		t.Errorf("goal = %q, want %q", result.Handoff.Goal, "Test goal")
	}
	if result.BudgetExceeded {
		t.Error("budget should not be exceeded")
	}
}

func TestExecutor_ExecuteStep_ToolCallsPresent(t *testing.T) {
	toolCall := client.ToolCall{
		ID:        "tc-1",
		Name:      "sandbox.exec",
		Arguments: json.RawMessage(`{"command":"echo hello"}`),
	}

	llmSrv := newMockLLMServer("I will use a tool", []client.ToolCall{toolCall})
	defer llmSrv.Close()
	toolSrv := newMockToolServer("hello")
	defer toolSrv.Close()
	semSrv := newMockSemServer()
	defer semSrv.Close()
	kbSrv := newMockKBSrv()
	defer kbSrv.Close()

	hb := handoff.NewBuilder()
	hv, _ := handoff.NewValidator()

	exec := NewExecutor(
		client.NewLLMClient(llmSrv.URL),
		client.NewToolClient(toolSrv.URL),
		agentctx.NewBuilder(client.NewSemanticClient(semSrv.URL), client.NewKnowledgeClient(kbSrv.URL)),
		hb, hv,
	)

	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:  "run-2",
		StepIndex:  0,
		Profile:    "code-fast",
		TaskDesc:   "run echo",
		Tools:      []client.ToolDef{{Name: "sandbox.exec"}},
		MaxCostUSD: 10.0,
		MaxSteps:   10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Handoff != nil {
		t.Error("handoff should be nil when tool calls present")
	}
	// Expect: system + user + assistant + tool = 4 messages.
	if len(result.Messages) != 4 {
		t.Errorf("messages len = %d, want 4", len(result.Messages))
	}
}

func TestExecutor_ExecuteStep_BudgetExceeded(t *testing.T) {
	handoffJSON := `{
		"goal": "Test",
		"context_refs": ["https://example.com/1"],
		"acceptance_criteria": ["c"],
		"expected_output_schema": {"type": "object"}
	}`

	// LLM server that always returns high cost.
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := client.ChatResponse{
			LogicalCallID: "call-1",
			Status:        "succeeded",
			Content:       handoffJSON,
			Usage:         client.LLMUsage{CostUSD: 100.0, TotalCostUSD: 100.0},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer llmSrv.Close()
	semSrv := newMockSemServer()
	defer semSrv.Close()
	kbSrv := newMockKBSrv()
	defer kbSrv.Close()

	hb := handoff.NewBuilder()
	hv, _ := handoff.NewValidator()

	exec := NewExecutor(
		client.NewLLMClient(llmSrv.URL),
		client.NewToolClient("http://unused:9999"),
		agentctx.NewBuilder(client.NewSemanticClient(semSrv.URL), client.NewKnowledgeClient(kbSrv.URL)),
		hb, hv,
	)

	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:      "run-3",
		StepIndex:      0,
		Profile:        "code-fast",
		TaskDesc:       "do something",
		MaxCostUSD:     1.0,
		MaxSteps:       10,
		BudgetSnapshot: BudgetSnapshot{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.BudgetExceeded {
		t.Fatal("expected BudgetExceeded=true")
	}
	if result.BudgetExceededReason == "" {
		t.Error("expected non-empty BudgetExceededReason")
	}
}

func TestExecutor_ExecuteStep_InvalidHandoff_ContinuesLoop(t *testing.T) {
	// LLM returns non-JSON content, which cannot be parsed as handoff.
	llmSrv := newMockLLMServer("This is plain text, not JSON.", nil)
	defer llmSrv.Close()
	semSrv := newMockSemServer()
	defer semSrv.Close()
	kbSrv := newMockKBSrv()
	defer kbSrv.Close()

	hb := handoff.NewBuilder()
	hv, _ := handoff.NewValidator()

	exec := NewExecutor(
		client.NewLLMClient(llmSrv.URL),
		client.NewToolClient("http://unused:9999"),
		agentctx.NewBuilder(client.NewSemanticClient(semSrv.URL), client.NewKnowledgeClient(kbSrv.URL)),
		hb, hv,
	)

	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:  "run-4",
		StepIndex:  0,
		Profile:    "code-fast",
		TaskDesc:   "do something",
		MaxCostUSD: 10.0,
		MaxSteps:   10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Handoff should be nil (agent must regenerate).
	if result.Handoff != nil {
		t.Error("expected nil handoff for non-JSON content")
	}
	// Messages should contain the assistant message for the next iteration.
	if len(result.Messages) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(result.Messages))
	}
}
