// Package policy enforces OPA policies (R0-R3) before every tool call.
//
// The gateway talks to OPA over its HTTP API. When OPA_URL is empty the same
// Rego files are evaluated in-process, which keeps unit and integration tests
// hermetic without weakening the production path.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DecisionPath is the OPA document queried for every tool call.
const DecisionPath = "v1/data/ai_sdlc_factory/autonomy/decision"

// ErrUnavailable marks a policy engine that could not answer.
var ErrUnavailable = errors.New("policy engine unavailable")

// Request is the input document passed to OPA.
type Request struct {
	Role          string   `json:"role"`
	Tool          string   `json:"tool"`
	ToolRiskLevel string   `json:"tool_risk_level"`
	AutonomyLevel string   `json:"autonomy_level"`
	AuthorID      string   `json:"author_id"`
	ApproverID    string   `json:"approver_id"`
	RoleTools     []string `json:"role_tools,omitempty"`
	RoleDeny      []string `json:"role_deny,omitempty"`
}

// Decision is the OPA decision for one tool call.
type Decision struct {
	Allow     bool   `json:"allow"`
	RiskLevel string `json:"risk_level"`
	Reason    string `json:"reason"`
}

// Authorizer decides whether a tool call is allowed.
type Authorizer interface {
	Authorize(ctx context.Context, req Request) (Decision, error)
}

// Readier is implemented by engines that can report readiness.
type Readier interface {
	Ready(ctx context.Context) error
}

// OPA is an HTTP client for the OPA data API.
type OPA struct {
	baseURL string
	client  *http.Client
}

// NewOPA creates an OPA client. An empty baseURL is rejected.
func NewOPA(baseURL string, timeout time.Duration) (*OPA, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("OPA URL is empty")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &OPA{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

// Authorize sends the input document to OPA and returns its decision.
// Any transport or decoding failure is reported as ErrUnavailable so callers
// can fail closed with 503 instead of silently allowing the call.
func (o *OPA) Authorize(ctx context.Context, req Request) (Decision, error) {
	payload, err := json.Marshal(map[string]Request{"input": req})
	if err != nil {
		return Decision{}, fmt.Errorf("marshal OPA input: %w", err)
	}

	url := o.baseURL + "/" + DecisionPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Decision{}, fmt.Errorf("create OPA request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Decision{}, fmt.Errorf("%w: OPA returned %d: %s",
			ErrUnavailable, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out struct {
		Result *Decision `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Decision{}, fmt.Errorf("%w: decode OPA response: %v", ErrUnavailable, err)
	}
	if out.Result == nil {
		return Decision{
			Allow:     false,
			RiskLevel: req.ToolRiskLevel,
			Reason:    "policy returned no decision",
		}, nil
	}
	return *out.Result, nil
}

// Ready checks the OPA health endpoint.
func (o *OPA) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.baseURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("create OPA health request: %w", err)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("OPA health: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("OPA health: status %d", resp.StatusCode)
	}
	return nil
}

type decisionContextKey struct{}

// WithDecision stores an already computed decision in ctx.
func WithDecision(ctx context.Context, d Decision) context.Context {
	return context.WithValue(ctx, decisionContextKey{}, d)
}

// DecisionFrom returns a decision previously stored in ctx.
func DecisionFrom(ctx context.Context) (Decision, bool) {
	d, ok := ctx.Value(decisionContextKey{}).(Decision)
	return d, ok
}

type decisionsContextKey struct{}

// WithDecisions stores decisions for a batch of tool calls, keyed by tool name.
func WithDecisions(ctx context.Context, decisions map[string]Decision) context.Context {
	return context.WithValue(ctx, decisionsContextKey{}, decisions)
}

// DecisionsFrom returns the batch decisions stored in ctx.
func DecisionsFrom(ctx context.Context) (map[string]Decision, bool) {
	d, ok := ctx.Value(decisionsContextKey{}).(map[string]Decision)
	return d, ok
}
