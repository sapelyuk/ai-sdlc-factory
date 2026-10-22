// Package domain defines edge domain models.
package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ── Domain Errors ─────────────────────────────────────────────

var (
	ErrEdgeNotFound = errors.New("edge not found")
)

// ── SemEdge ───────────────────────────────────────────────────

// SemEdge represents an edge in the semantic property graph.
type SemEdge struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	SrcID       uuid.UUID  `json:"src_id"`
	DstID       uuid.UUID  `json:"dst_id"`
	Kind        string     `json:"kind"`
	Props       any        `json:"props"`
	ValidFrom   time.Time  `json:"valid_from"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
}

// CreateEdgeInput is the input for creating a new edge.
type CreateEdgeInput struct {
	SrcID string `json:"src_id"`
	DstID string `json:"dst_id"`
	Kind  string `json:"kind"`
	Props any    `json:"props,omitempty"`
}

// Validate checks the input for required fields.
func (i *CreateEdgeInput) Validate() error {
	if i.SrcID == "" {
		return errors.New("src_id is required")
	}
	if i.DstID == "" {
		return errors.New("dst_id is required")
	}
	if i.Kind == "" {
		return errors.New("kind is required")
	}
	return nil
}
