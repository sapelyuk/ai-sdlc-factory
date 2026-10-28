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

// ToolClient calls the Tool Gateway via JSON-RPC MCP.
type ToolClient struct {
	baseURL string
	http    *http.Client
}

// NewToolClient creates a new Tool Gateway client.
func NewToolClient(baseURL string) *ToolClient {
	return &ToolClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// rpcRequest is a JSON-RPC 2.0 request.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is a JSON-RPC 2.0 response.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolCallParams is the params for tools/call.
type ToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Meta carries transport-level call metadata (_meta in MCP).
	Meta map[string]any `json:"_meta,omitempty"`
}

// IdempotencyMetaKey is the _meta field the Tool Gateway uses to deduplicate
// retried tool calls.
const IdempotencyMetaKey = "idempotency_key"

// TaskRunMetaKey is the _meta field carrying the task run attribution.
const TaskRunMetaKey = "task_run_id"

// ToolResult is the result of a tool call.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ToolContent is a content block in a tool result.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Call invokes a tool on the Tool Gateway.
//
// taskRunID and idempotencyKey are forwarded in the MCP _meta field. The
// workflow derives idempotencyKey deterministically per step, so a retried
// activity reuses the same key and the gateway can return the cached result
// instead of executing the tool twice.
func (c *ToolClient) Call(
	ctx context.Context,
	toolName string,
	args json.RawMessage,
	taskRunID string,
	idempotencyKey string,
) (*ToolResult, error) {
	params := ToolCallParams{Name: toolName, Arguments: args}
	meta := map[string]any{}
	if taskRunID != "" {
		meta[TaskRunMetaKey] = taskRunID
	}
	if idempotencyKey != "" {
		meta[IdempotencyMetaKey] = idempotencyKey
	}
	if len(meta) > 0 {
		params.Meta = meta
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal tool params: %w", err)
	}

	reqBody := rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  paramsJSON,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal RPC request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create tool request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call Tool Gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read tool response: %w", err)
	}

	var rpcResp rpcResponse
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return nil, fmt.Errorf("unmarshal RPC response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("tool RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	var result ToolResult
	if err := json.Unmarshal(rpcResp.Result, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tool result: %w", err)
	}
	return &result, nil
}
