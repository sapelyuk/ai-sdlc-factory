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

// KnowledgeClient calls the Knowledge Service.
type KnowledgeClient struct {
	baseURL string
	http    *http.Client
}

// NewKnowledgeClient creates a new Knowledge Service client.
func NewKnowledgeClient(baseURL string) *KnowledgeClient {
	return &KnowledgeClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// SearchRequest is the POST /v1/search payload.
type SearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
}

// KBFragment is a single knowledge fragment returned by the search.
type KBFragment struct {
	DocID   string  `json:"doc_id"`
	Chunk   string  `json:"chunk"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet"`
}

// Search sends a search request to the Knowledge Service.
func (c *KnowledgeClient) Search(ctx context.Context, query string, topK int) ([]KBFragment, error) {
	if topK <= 0 {
		topK = 5
	}

	body, err := json.Marshal(SearchRequest{Query: query, TopK: topK})
	if err != nil {
		return nil, fmt.Errorf("marshal search request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/search", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create knowledge request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call Knowledge Service: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read knowledge response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Knowledge Service returned %d: %s", resp.StatusCode, string(data))
	}

	var fragments []KBFragment
	if err := json.Unmarshal(data, &fragments); err != nil {
		return nil, fmt.Errorf("unmarshal knowledge fragments: %w", err)
	}
	return fragments, nil
}
