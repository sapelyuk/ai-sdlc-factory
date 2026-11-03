package gates

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ApprovalStatus enumerates the lifecycle states of a human_approval request.
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "pending"
	ApprovalApproved ApprovalStatus = "approved"
	ApprovalRejected ApprovalStatus = "rejected"
	ApprovalExpired  ApprovalStatus = "expired"
)

// ApprovalRequest is the persisted state of a human_approval gate request.
// It is stored in the `approvals` table and accessed both by the API (owner
// reads/writes) and the worker (reads status, writes expiry).
type ApprovalRequest struct {
	ID          uuid.UUID      `json:"id"`
	TaskRunID   *uuid.UUID     `json:"task_run_id,omitempty"`
	StageRunID  *uuid.UUID     `json:"stage_run_id,omitempty"`
	RunID       *uuid.UUID     `json:"run_id,omitempty"`
	WorkspaceID uuid.UUID      `json:"workspace_id"`
	GateName    string         `json:"gate_name"`
	Status      ApprovalStatus `json:"status"`
	RequestedBy uuid.UUID      `json:"requested_by"`
	ApprovedBy  *uuid.UUID     `json:"approved_by,omitempty"`
	RiskLevel   string         `json:"risk_level"`
	DeadlineAt  *time.Time     `json:"deadline_at,omitempty"`
	Context     map[string]any `json:"context,omitempty"`
	WorkflowID  string         `json:"workflow_id,omitempty"`
	Comment     string         `json:"comment,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DecidedAt   *time.Time     `json:"decided_at,omitempty"`
}

// ApprovalStore is the persistence interface for approval requests.
// The worker uses a Postgres implementation; the API uses a parallel
// repository over the same `approvals` table.
type ApprovalStore interface {
	// Create inserts a new pending request.
	Create(ctx context.Context, req ApprovalRequest) error
	// Get retrieves a request by ID.
	Get(ctx context.Context, id uuid.UUID) (ApprovalRequest, error)
	// List returns requests matching the workspace and optional status.
	List(ctx context.Context, workspaceID uuid.UUID, status *ApprovalStatus) ([]ApprovalRequest, error)
	// Decide records a human decision.
	Decide(ctx context.Context, id uuid.UUID, approved bool, decidedBy uuid.UUID, comment string) error
	// Expire marks a pending request as expired (used by the workflow timer).
	Expire(ctx context.Context, id uuid.UUID) error
}

// ErrNotFound is returned when an approval request does not exist.
var ErrNotFound = fmt.Errorf("gates: approval request not found")

// ErrNotPending is returned when an operation requires the request to be
// pending but it is not.
var ErrNotPending = fmt.Errorf("gates: approval request is not pending")

// InMemoryStore is a thread-safe in-memory implementation for unit tests
// and the Temporal test environment.
type InMemoryStore struct {
	mu    sync.RWMutex
	items map[uuid.UUID]ApprovalRequest
}

// NewInMemoryStore creates an empty in-memory store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{items: make(map[uuid.UUID]ApprovalRequest)}
}

func (s *InMemoryStore) Create(_ context.Context, req ApprovalRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	req.UpdatedAt = time.Now().UTC()
	s.items[req.ID] = req
	return nil
}

func (s *InMemoryStore) Get(_ context.Context, id uuid.UUID) (ApprovalRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.items[id]
	if !ok {
		return ApprovalRequest{}, ErrNotFound
	}
	return req, nil
}

func (s *InMemoryStore) List(_ context.Context, workspaceID uuid.UUID, status *ApprovalStatus) ([]ApprovalRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ApprovalRequest
	for _, req := range s.items {
		if req.WorkspaceID != workspaceID {
			continue
		}
		if status != nil && req.Status != *status {
			continue
		}
		out = append(out, req)
	}
	return out, nil
}

func (s *InMemoryStore) Expire(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.items[id]
	if !ok {
		return ErrNotFound
	}
	if req.Status != ApprovalPending {
		// A decision arrived before the timer fired: the decision wins.
		return nil
	}
	req.Status = ApprovalExpired
	now := time.Now().UTC()
	req.UpdatedAt = now
	s.items[id] = req
	return nil
}

func (s *InMemoryStore) Decide(_ context.Context, id uuid.UUID, approved bool, decidedBy uuid.UUID, comment string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	req, ok := s.items[id]
	if !ok {
		return ErrNotFound
	}
	if req.Status != ApprovalPending {
		return ErrNotPending
	}
	status := ApprovalRejected
	if approved {
		status = ApprovalApproved
	}
	req.Status = status
	req.ApprovedBy = &decidedBy
	req.Comment = comment
	now := time.Now().UTC()
	req.UpdatedAt = now
	req.DecidedAt = &now
	s.items[id] = req
	return nil
}

// HumanApprovalGate evaluates a human_approval gate: it checks the current
// status of the persisted approval request and whether the deadline has passed.
type HumanApprovalGate struct {
	// Name is the gate name from the registry.
	Name string
	// Store provides access to the approval request.
	Store ApprovalStore
}

// Evaluate checks the approval request identified by facts["approval.id"].
// Returns:
//   - Result{Approved: true} when the request is approved;
//   - Result{Rejected: true} when rejected or expired;
//   - Result{} (empty) when still pending — the workflow should keep waiting;
//   - error on data access failure.
func (g HumanApprovalGate) Evaluate(ctx context.Context, facts Facts) (Result, error) {
	if g.Store == nil {
		return Result{}, fmt.Errorf("gates: approval store is not configured")
	}
	approvalID, err := uuid.Parse(facts.GetString(EvidenceApprovalID))
	if err != nil || approvalID == uuid.Nil {
		return Result{}, fmt.Errorf("gates: %s is not a valid UUID: %q",
			EvidenceApprovalID, facts.GetString(EvidenceApprovalID))
	}

	req, err := g.Store.Get(ctx, approvalID)
	if err != nil {
		return Result{}, fmt.Errorf("gates: lookup approval %s: %w", approvalID, err)
	}

	switch req.Status {
	case ApprovalApproved:
		return Result{Approved: true}, nil
	case ApprovalRejected:
		return Result{Rejected: true, Reason: req.Comment}, nil
	case ApprovalExpired:
		return Result{TimeoutExpired: true, Reason: "approval deadline exceeded"}, nil
	}

	// Still pending: check the deadline.
	if req.DeadlineAt != nil && time.Now().UTC().After(*req.DeadlineAt) {
		return Result{TimeoutExpired: true, Reason: "approval deadline exceeded"}, nil
	}

	// Not yet decided — the workflow must keep waiting (await signal or timer).
	return Result{}, nil
}

// BuildApprovalRequest creates a new ApprovalRequest with the configured
// deadline. If timeout is zero, DefaultApprovalTimeout (24 hours) is used.
func BuildApprovalRequest(
	workspaceID uuid.UUID,
	gateName string,
	requestedBy uuid.UUID,
	timeout time.Duration,
	ctxData map[string]any,
) ApprovalRequest {
	t := timeout
	if t == 0 {
		t = DefaultApprovalTimeout
	}
	deadline := time.Now().UTC().Add(t)
	return ApprovalRequest{
		ID:          uuid.New(),
		WorkspaceID: workspaceID,
		GateName:    gateName,
		Status:      ApprovalPending,
		RequestedBy: requestedBy,
		RiskLevel:   "R3",
		DeadlineAt:  &deadline,
		Context:     ctxData,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
}
