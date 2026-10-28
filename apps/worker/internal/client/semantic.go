package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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
	ID    string          `json:"id"`
	Kind  string          `json:"kind"`
	Name  string          `json:"name,omitempty"`
	Label string          `json:"label,omitempty"`
	Props json.RawMessage `json:"props,omitempty"`
	Layer string          `json:"layer,omitempty"`
}

// GetSubgraph retrieves the semantic subgraph around rootID.
// The Semantic Service exposes GET /v1/nodes?root=<uuid>&depth=<n>, which
// returns the root node and all nodes reachable within depth hops.
func (c *SemanticClient) GetSubgraph(ctx context.Context, rootID string, depth int) ([]SemNode, error) {
	u := c.baseURL + "/v1/nodes"
	if rootID != "" {
		query := url.Values{}
		query.Set("root", rootID)
		query.Set("depth", strconv.Itoa(depth))
		u += "?" + query.Encode()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create semantic request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call Semantic Service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read semantic response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("semantic service returned %d: %s", resp.StatusCode, string(data))
	}

	var nodes []SemNode
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, fmt.Errorf("unmarshal semantic nodes: %w", err)
	}
	return nodes, nil
}
