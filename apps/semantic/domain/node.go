// Package domain defines the core domain models for the semantic graph.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ValidLayers are the allowed semantic layers.
var ValidLayers = map[string]bool{"SLP": true, "SLPr": true, "AM": true}

// ValidStatuses are the allowed node statuses (aligned with migration 0005 CHECK).
var ValidStatuses = map[string]bool{
	"draft": true, "active": true, "deprecated": true, "deleted": true,
}

// ImmutableStatuses are statuses that prevent PATCH updates.
// In this service, 'active' is the immutable state (approved via review, P9).
var ImmutableStatuses = map[string]bool{"active": true}

// ── Domain Errors ─────────────────────────────────────────────

var (
	ErrNotFound     = errors.New("node not found")
	ErrForbidden    = errors.New("operation forbidden: node is active (immutable)")
	ErrConflict     = errors.New("node already exists")
	ErrInvalidInput = errors.New("invalid input")
)

// ── SemNode ───────────────────────────────────────────────────

// SemNode represents a node in the semantic property graph.
type SemNode struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	Layer       string    `json:"layer"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	Props       any       `json:"props"`
	Version     int       `json:"version"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateNodeInput is the input for creating a new node.
type CreateNodeInput struct {
	Layer  string `json:"layer"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Props  any    `json:"props"`
	Status string `json:"status,omitempty"`
}

// Validate checks the input for required fields and valid enums.
func (i *CreateNodeInput) Validate() error {
	if i.Layer == "" {
		return errors.New("layer is required")
	}
	if !ValidLayers[i.Layer] {
		return errors.New("layer must be one of: SLP, SLPr, AM")
	}
	if i.Kind == "" {
		return errors.New("kind is required")
	}
	if i.Label == "" {
		return errors.New("label is required")
	}
	if i.Status != "" && !ValidStatuses[i.Status] {
		return errors.New("status must be one of: draft, active, deprecated, deleted")
	}
	return nil
}

// DefaultStatus returns the default status for a new node.
func (i *CreateNodeInput) DefaultStatus() string {
	if i.Status == "" {
		return "draft"
	}
	return i.Status
}

// UpdateNodeInput is the input for updating an existing node.
type UpdateNodeInput struct {
	Kind  string `json:"kind,omitempty"`
	Label string `json:"label,omitempty"`
	Props any    `json:"props,omitempty"`
}

// Validate checks the update input.
func (i *UpdateNodeInput) Validate() error {
	if i.Kind != "" && len(i.Kind) > 128 {
		return errors.New("kind must be at most 128 characters")
	}
	if i.Label != "" && len(i.Label) > 512 {
		return errors.New("label must be at most 512 characters")
	}
	return nil
}
