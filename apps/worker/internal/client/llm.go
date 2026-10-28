// Package client provides thin HTTP wrappers for the downstream services.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LLMClient calls the LLM Gateway.
type LLMClient struct {
	baseURL string
	http    *http.Client
}

// NewLLMClient creates a new LLM Gateway client.
func NewLLMClient(baseURL string) *LLMClient {
	return &LLMClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 120 * time.Second},
	}
}

// ChatMessage is a single message in a conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolDef is a tool definition passed to the LLM.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ChatRequest is the POST /v1/llm/chat payload.
type ChatRequest struct {
	Profile  string        `json:"profile"`
	Messages []ChatMessage `json:"messages"`
	Tools    []ToolDef     `json:"tools,omitempty"`
	RunID    *string       `json:"run_id,omitempty"`
	TaskID   *string       `json:"task_id,omitempty"`
	AgentID  *string       `json:"agent_id,omitempty"`

	// IdempotencyKey is sent as the X-Idempotency-Key header. It is not part
	// of the JSON payload: the LLM Gateway treats it as transport metadata and
	// uses it to deduplicate retried logical calls (PR #99).
	IdempotencyKey string `json:"-"`
}

// LLMUsage carries cost and token information from the LLM Gateway.
type LLMUsage struct {
	TokensIn       int     `json:"tokens_in"`
	TokensOut      int     `json:"tokens_out"`
	CostUSD        float64 `json:"cost_usd"`
	TotalTokensIn  int     `json:"total_tokens_in"`
	TotalTokensOut int     `json:"total_tokens_out"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
}

// ToolCall is a tool invocation returned by the LLM.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ChatResponse is the POST /v1/llm/chat response.
type ChatResponse struct {
	LogicalCallID string     `json:"logical_call_id"`
	Status        string     `json:"status"`
	ModelUsed     string     `json:"model_used"`
	ProviderUsed  string     `json:"provider_used"`
	Content       string     `json:"content"`
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`
	Usage         LLMUsage   `json:"usage"`
}

// Chat sends a chat completion request to the LLM Gateway.
func (c *LLMClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/llm/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("X-Idempotency-Key", req.IdempotencyKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call LLM Gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM Gateway returned %d: %s", resp.StatusCode, string(data))
	}

	var out ChatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal chat response: %w", err)
	}
	return &out, nil
}
