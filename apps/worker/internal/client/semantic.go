package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// SemanticClient calls the Semantic Service.
type SemanticClient struct {
	baseURL string
	http    *http.Client
}

// NewSemanticClient creates a new Semantic Service client.
func NewSemanticClient(baseURL string) *SemanticClient {
	return &SemanticClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// SemNode represents a semantic layer node.
type SemNode struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	Props    json.RawMessage `json:"props,omitempty"`
	Layer    string          `json:"layer,omitempty"`
}

// GetSubgraph retrieves nodes from the semantic layer using list endpoint.
// The Semantic Service currently supports GET /v1/nodes with query params.
func (c *SemanticClient) GetSubgraph(ctx context.Context, rootID string, depth int) ([]SemNode, error) {
	// The semantic service does not yet support root/depth traversal;
	// fall back to listing all nodes filtered by kind.
	// This is a minimal implementation; full traversal is tracked under #40.
	url := fmt.Sprintf("%s/v1/nodes", c.baseURL)
	if rootID != "" {
		url = fmt.Sprintf("%s/v1/nodes?root=%s&depth=%d", c.baseURL, rootID, depth)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create semantic request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call Semantic Service: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read semantic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Semantic Service returned %d: %s", resp.StatusCode, string(data))
	}

	var nodes []SemNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("unmarshal semantic nodes: %w", err)
	}
	return nodes, nil
}
