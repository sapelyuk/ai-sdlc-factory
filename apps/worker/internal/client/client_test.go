package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolClient_Call_ForwardsIdempotencyMeta(t *testing.T) {
	var params map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Params map[string]any `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		params = body.Params
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer srv.Close()

	c := NewToolClient(srv.URL)
	result, err := c.Call(
		context.Background(),
		"sandbox.exec",
		json.RawMessage(`{"command":"ls"}`),
		"run-1",
		"key-1",
	)
	require.NoError(t, err)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "ok", result.Content[0].Text)

	require.Equal(t, "sandbox.exec", params["name"])
	meta, ok := params["_meta"].(map[string]any)
	require.True(t, ok, "_meta must be present")
	assert.Equal(t, "key-1", meta[IdempotencyMetaKey])
	assert.Equal(t, "run-1", meta[TaskRunMetaKey])
}

func TestLLMClient_Chat_SendsIdempotencyHeaderAndAttribution(t *testing.T) {
	var (
		gotHeader string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Idempotency-Key")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ChatResponse{
			LogicalCallID: "call-1",
			Status:        "succeeded",
			Content:       "hello",
			Usage:         LLMUsage{TotalCostUSD: 0.01},
		})
	}))
	defer srv.Close()

	runID, taskID, agentID := "run-1", "task-1", "agent-1"
	c := NewLLMClient(srv.URL)
	resp, err := c.Chat(context.Background(), &ChatRequest{
		Profile:        "code-fast",
		Messages:       []ChatMessage{{Role: "user", Content: "hi"}},
		RunID:          &runID,
		TaskID:         &taskID,
		AgentID:        &agentID,
		IdempotencyKey: "key-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "call-1", resp.LogicalCallID)

	assert.Equal(t, "key-1", gotHeader)
	assert.Equal(t, "run-1", gotBody["run_id"])
	assert.Equal(t, "task-1", gotBody["task_id"])
	assert.Equal(t, "agent-1", gotBody["agent_id"])
	assert.NotContains(t, gotBody, "idempotency_key", "the key travels in the header only")
}

func TestSemanticClient_GetSubgraph_SendsRootAndDepth(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]SemNode{
			{ID: "n1", Kind: "Role", Label: "Backend Developer", Layer: "SLP"},
		})
	}))
	defer srv.Close()

	c := NewSemanticClient(srv.URL)
	nodes, err := c.GetSubgraph(context.Background(), "0190f0e0-0000-7000-8000-000000000001", 2)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "Backend Developer", nodes[0].Label)

	assert.Equal(t, "depth=2&root=0190f0e0-0000-7000-8000-000000000001", query)
}

func TestSemanticClient_GetSubgraph_NonOKStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := NewSemanticClient(srv.URL)
	_, err := c.GetSubgraph(context.Background(), "root", 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "semantic service returned 500")
}

func TestKnowledgeClient_Search_SendsQueryAndTopK(t *testing.T) {
	var body SearchRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]KBFragment{
			{DocID: "d1", Snippet: "snippet", Score: 0.9},
		})
	}))
	defer srv.Close()

	c := NewKnowledgeClient(srv.URL)
	frags, err := c.Search(context.Background(), "budget", 3)
	require.NoError(t, err)
	require.Len(t, frags, 1)
	assert.Equal(t, SearchRequest{Query: "budget", TopK: 3}, body)
}
