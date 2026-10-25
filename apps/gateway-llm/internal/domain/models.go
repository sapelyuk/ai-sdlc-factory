// Package domain defines core entities for the LLM Gateway.
package domain

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ModelProfile represents a row in llm_model_profiles (global catalog).
type ModelProfile struct {
	ProfileName    string          `json:"profile_name"`
	Provider       string          `json:"provider"`
	ModelID        string          `json:"model_id"`
	Priority       int             `json:"priority"`
	Capabilities   json.RawMessage `json:"capabilities"`
	CostPerMtokIn  *float64        `json:"cost_per_mtok_in"`
	CostPerMtokOut *float64        `json:"cost_per_mtok_out"`
	Enabled        bool            `json:"enabled"`
}

// ValidatePinned checks that model_id does not contain "-latest".
// Returns an error if the model_id uses a floating alias.
func (m ModelProfile) ValidatePinned() error {
	if strings.Contains(m.ModelID, "-latest") {
		return ErrFloatingAlias
	}
	return nil
}

// ModelHealth tracks per-(provider, model) health state.
type ModelHealth struct {
	Provider            string
	ModelID             string
	ConsecutiveFailures int
	CooldownUntil       *time.Time
	LastErrorAt         *time.Time
	LastErrorType       *string
	LastSuccessAt       *time.Time
}

// IsCoolingDown returns true if the model is currently in cooldown.
func (h *ModelHealth) IsCoolingDown(now time.Time) bool {
	return h.CooldownUntil != nil && h.CooldownUntil.After(now)
}

// LogicalCall represents a single logical LLM request (attribution layer).
type LogicalCall struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	RunID          *uuid.UUID
	TaskID         *uuid.UUID
	AgentID        *uuid.UUID
	ProfileName    string
	ModelIDUsed    string
	ProviderUsed   string
	Status         string // succeeded, failed, partial
	AttemptCount   int
	FinalTokensIn  *int
	FinalTokensOut *int
	FinalCostUSD   *float64
	TotalTokensIn  int
	TotalTokensOut int
	TotalCostUSD   float64
	StartedAt      time.Time
	CompletedAt    *time.Time
}

// Attempt represents a single HTTP attempt to a provider (spend layer).
type Attempt struct {
	ID            uuid.UUID
	LogicalCallID uuid.UUID
	AttemptNum    int
	Provider      string
	ModelID       string
	Status        string
	ErrorMessage  *string
	TokensIn      *int
	TokensOut     *int
	CostUSD       *float64
	LatencyMs     *int
	StartedAt     time.Time
	CompletedAt   *time.Time
}

// ChatMessage is a single message in a conversation.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ToolDef is a tool definition passed to the provider.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ChatRequest is the unified request to a provider.
type ChatRequest struct {
	Messages []ChatMessage `json:"messages"`
	Tools    []ToolDef     `json:"tools,omitempty"`
}

// ChatResponse is the unified response from a provider.
type ChatResponse struct {
	Content   string `json:"content"`
	TokensIn  int    `json:"tokens_in"`
	TokensOut int    `json:"tokens_out"`
}

// Errors.
var (
	ErrFloatingAlias  = &DomainError{Message: "model_id must not use -latest alias (ADR-021: pinned versions only)"}
	ErrNoAvailable    = &DomainError{Message: "no available model for the requested profile"}
	ErrUnknownProfile = &DomainError{Message: "unknown model profile"}
	ErrAllFailed      = &DomainError{Message: "all provider attempts failed"}
	ErrNotFound       = &DomainError{Message: "item not found"}
)

// DomainError is a typed domain error.
type DomainError struct {
	Message string
}

func (e *DomainError) Error() string { return e.Message }
