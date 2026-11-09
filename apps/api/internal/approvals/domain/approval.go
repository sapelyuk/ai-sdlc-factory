// Package domain defines the ApprovalRequest type used by the approval queue
// (FR-OPS-1, ADR-013, SDLC_STATE_MACHINE.md section 4).
package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status represents the lifecycle state of an approval request.
type Status string

const (
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusExpired   Status = "expired"
)

// ErrNotFound is returned when an approval request does not exist.
var ErrNotFound = fmt.Errorf("approval request not found")

// ApprovalRequest is the domain model for a human_approval gate request.
type ApprovalRequest struct {
	ID           uuid.UUID     `json:"id"`
	TaskRunID    *uuid.UUID    `json:"task_run_id,omitempty"`
	StageRunID   *uuid.UUID    `json:"stage_run_id,omitempty"`
	RunID        *uuid.UUID    `json:"run_id,omitempty"`
	WorkspaceID  uuid.UUID     `json:"workspace_id"`
	GateName     string        `json:"gate_name"`
	Status       Status        `json:"status"`
	RequestedBy  uuid.UUID     `json:"requested_by"`
	ApprovedBy   *uuid.UUID    `json:"approved_by,omitempty"`
	RiskLevel    string        `json:"risk_level"`
	DeadlineAt   *time.Time    `json:"deadline_at,omitempty"`
	Context      map[string]any `json:"context,omitempty"`
	WorkflowID   string        `json:"workflow_id,omitempty"`
	Comment      string        `json:"comment,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
	DecidedAt    *time.Time    `json:"decided_at,omitempty"`
}

// ApproveDecision is the request body for POST /approve and POST /reject.
type ApproveDecision struct {
	Comment string `json:"comment,omitempty"`
}
