// Package identity carries the caller identity of an MCP request.
//
// Agents authenticate to the Tool Gateway by sending a set of headers. The
// identity is attached to the request context and is used by the policy
// engine and the audit log.
package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// Headers understood by the Tool Gateway.
const (
	HeaderWorkspaceID   = "X-Workspace-ID"
	HeaderAgentID       = "X-Agent-ID"
	HeaderAuthorID      = "X-Author-ID"
	HeaderApproverID    = "X-Approver-ID"
	HeaderAgentRole     = "X-Agent-Role"
	HeaderAutonomyLevel = "X-Autonomy-Level"
)

// Identity is the authenticated caller of a tool call.
type Identity struct {
	WorkspaceID   uuid.UUID
	AgentID       uuid.UUID
	AuthorID      uuid.UUID
	ApproverID    uuid.UUID
	Role          string
	AutonomyLevel string
}

// ErrMissingIdentity is returned when no identity is present in the context.
var ErrMissingIdentity = errors.New("missing caller identity")

type contextKey struct{}

// With returns a context carrying id.
func With(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// From returns the identity stored in ctx.
func From(ctx context.Context) (Identity, error) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	if !ok {
		return Identity{}, ErrMissingIdentity
	}
	return id, nil
}

// FromHeaders parses and validates the identity headers.
//
// X-Workspace-ID, X-Agent-ID and X-Agent-Role are mandatory. X-Author-ID
// defaults to the agent ID, X-Autonomy-Level defaults to R0 (least privilege)
// and X-Approver-ID is optional and only meaningful for R3 actions.
func FromHeaders(h http.Header) (Identity, error) {
	workspaceID, err := requiredUUID(h, HeaderWorkspaceID)
	if err != nil {
		return Identity{}, err
	}
	agentID, err := requiredUUID(h, HeaderAgentID)
	if err != nil {
		return Identity{}, err
	}

	role := h.Get(HeaderAgentRole)
	if role == "" {
		return Identity{}, fmt.Errorf("%s header is required", HeaderAgentRole)
	}

	authorID := agentID
	if raw := h.Get(HeaderAuthorID); raw != "" {
		authorID, err = uuid.Parse(raw)
		if err != nil {
			return Identity{}, fmt.Errorf("%s must be a valid UUID: %w", HeaderAuthorID, err)
		}
	}

	var approverID uuid.UUID
	if raw := h.Get(HeaderApproverID); raw != "" {
		approverID, err = uuid.Parse(raw)
		if err != nil {
			return Identity{}, fmt.Errorf("%s must be a valid UUID: %w", HeaderApproverID, err)
		}
	}

	autonomy := h.Get(HeaderAutonomyLevel)
	if autonomy == "" {
		autonomy = "R0"
	}
	if !ValidRiskLevel(autonomy) {
		return Identity{}, fmt.Errorf("%s must be one of R0, R1, R2, R3", HeaderAutonomyLevel)
	}

	return Identity{
		WorkspaceID:   workspaceID,
		AgentID:       agentID,
		AuthorID:      authorID,
		ApproverID:    approverID,
		Role:          role,
		AutonomyLevel: autonomy,
	}, nil
}

func requiredUUID(h http.Header, name string) (uuid.UUID, error) {
	raw := h.Get(name)
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%s header is required", name)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be a valid UUID: %w", name, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s must not be the nil UUID", name)
	}
	return id, nil
}

// ValidRiskLevel reports whether level is one of R0, R1, R2, R3.
func ValidRiskLevel(level string) bool {
	switch level {
	case "R0", "R1", "R2", "R3":
		return true
	default:
		return false
	}
}
