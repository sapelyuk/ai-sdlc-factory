package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	agentctx "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/context"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// recordingLLMServer answers every request with the given content and counts
// the calls, so a test can prove that a rejected package never reaches the
// model.
func recordingLLMServer(t *testing.T, content string, requests *atomic.Int32, lastBody chan<- []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		requests.Add(1)
		select {
		case lastBody <- body:
		default:
		}
		resp := client.ChatResponse{
			LogicalCallID: "call-1",
			Status:        "succeeded",
			ModelUsed:     "test-model",
			ProviderUsed:  "test",
			Content:       content,
			Usage:         client.LLMUsage{CostUSD: 0.01, TotalCostUSD: 0.01},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newHandoffTestExecutor wires an executor against the given LLM server.
func newHandoffTestExecutor(t *testing.T, llmURL string) *Executor {
	t.Helper()
	semSrv := newMockSemServer()
	t.Cleanup(semSrv.Close)
	kbSrv := newMockKBSrv()
	t.Cleanup(kbSrv.Close)
	hv, err := handoff.NewValidator()
	if err != nil {
		t.Fatalf("validator: %v", err)
	}
	return NewExecutor(
		client.NewLLMClient(llmURL),
		client.NewToolClient("http://unused:9999"),
		agentctx.NewBuilder(client.NewSemanticClient(semSrv.URL), client.NewKnowledgeClient(kbSrv.URL)),
		handoff.NewBuilder(),
		hv,
	)
}

// lastUserMessage decodes the user message of the last recorded request.
func lastUserMessage(t *testing.T, body []byte) string {
	t.Helper()
	var req client.ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decode chat request: %v", err)
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content
		}
	}
	return ""
}

func TestExecutor_ExecuteStep_IncomingHandoff_Rejected(t *testing.T) {
	var requests atomic.Int32
	lastBody := make(chan []byte, 1)
	llmSrv := recordingLLMServer(t, "irrelevant", &requests, lastBody)
	defer llmSrv.Close()

	exec := newHandoffTestExecutor(t, llmSrv.URL)
	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:  "run-1",
		StepIndex:  0,
		Profile:    "code-fast",
		TaskDesc:   "review the feature",
		MaxCostUSD: 10.0,
		MaxSteps:   10,
		IncomingHandoff: &handoff.HandoffPackage{
			Goal:        "",
			ContextRefs: []string{"sem://project/1"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.RejectedMissing) == 0 {
		t.Fatalf("rejected missing = %v, want the incomplete fields", result.RejectedMissing)
	}
	found := false
	for _, m := range result.RejectedMissing {
		if m == "goal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing = %v, want goal", result.RejectedMissing)
	}
	if result.Handoff != nil {
		t.Fatal("a rejected package must not produce a handoff")
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("LLM calls = %d, want 0: rejection must not consume budget", n)
	}
}

func TestExecutor_ExecuteStep_IncomingHandoff_Accepted(t *testing.T) {
	var requests atomic.Int32
	lastBody := make(chan []byte, 1)
	llmSrv := recordingLLMServer(t, "still thinking", &requests, lastBody)
	defer llmSrv.Close()

	exec := newHandoffTestExecutor(t, llmSrv.URL)
	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:  "run-1",
		StepIndex:  0,
		Profile:    "code-fast",
		TaskDesc:   "bare description",
		MaxCostUSD: 10.0,
		MaxSteps:   10,
		IncomingHandoff: &handoff.HandoffPackage{
			Goal:                 "Implement auth",
			ContextRefs:          []string{"sem://project/1"},
			AcceptanceCriteria:   []string{"tests pass"},
			ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
			Metadata:             &handoff.HandoffMetadata{SourceAgentID: "agent-pm"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RejectedMissing != nil {
		t.Fatalf("complete package must not be rejected: %v", result.RejectedMissing)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("LLM calls = %d, want 1", n)
	}
	user := lastUserMessage(t, <-lastBody)
	// The injected prompt carries the structured wire package: the runtime
	// header (source agent, chain step) never reaches the model.
	if !containsAll(user, `"goal":"Implement auth"`, `"acceptance_criteria":["tests pass"]`) {
		t.Fatalf("user message does not contain the handoff package: %q", user)
	}
	if containsAll(user, "source_agent_id", "chain_step") {
		t.Fatalf("wire form must not leak the runtime header: %q", user)
	}
}

func TestExecutor_ExecuteStep_IncomingHandoff_ReworkSkipsRejection(t *testing.T) {
	var requests atomic.Int32
	lastBody := make(chan []byte, 1)
	llmSrv := recordingLLMServer(t, "still thinking", &requests, lastBody)
	defer llmSrv.Close()

	exec := newHandoffTestExecutor(t, llmSrv.URL)
	// The package came back for rework: it is incomplete, but the receiver
	// diagnostics travel with it, so the author fixes it instead of being
	// rejected again.
	result, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:  "run-1",
		StepIndex:  0,
		Profile:    "code-fast",
		TaskDesc:   "bare description",
		MaxCostUSD: 10.0,
		MaxSteps:   10,
		IncomingHandoff: &handoff.HandoffPackage{
			Goal:        "Implement auth",
			ContextRefs: []string{"sem://project/1"},
			Metadata:    &handoff.HandoffMetadata{SourceAgentID: "agent-pm"},
		},
		IncomingFeedback: []string{"acceptance_criteria"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RejectedMissing != nil {
		t.Fatalf("a rework run must not be rejected: %v", result.RejectedMissing)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("LLM calls = %d, want 1", n)
	}
	user := lastUserMessage(t, <-lastBody)
	if !containsAll(user, "acceptance_criteria", "Fix them") {
		t.Fatalf("user message does not contain the rework feedback: %q", user)
	}
}

func TestExecutor_ExecuteStep_FeedbackWithoutPackage(t *testing.T) {
	var requests atomic.Int32
	lastBody := make(chan []byte, 1)
	llmSrv := recordingLLMServer(t, "still thinking", &requests, lastBody)
	defer llmSrv.Close()

	exec := newHandoffTestExecutor(t, llmSrv.URL)
	// No package in flight: the author restarts from the task description
	// with the receiver diagnostics attached.
	_, err := exec.ExecuteStep(context.Background(), StepInput{
		TaskRunID:        "run-1",
		StepIndex:        0,
		Profile:          "code-fast",
		TaskDesc:         "do the work",
		MaxCostUSD:       10.0,
		MaxSteps:         10,
		IncomingFeedback: []string{"goal"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("LLM calls = %d, want 1", n)
	}
	user := lastUserMessage(t, <-lastBody)
	if !containsAll(user, "do the work", "goal") {
		t.Fatalf("user message does not contain the task and feedback: %q", user)
	}
}

func containsAll(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
