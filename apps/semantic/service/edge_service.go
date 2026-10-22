// Package service contains business logic for the semantic graph.
package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
)

// EdgeService provides edge operations.
type EdgeService struct {
	repo EdgeRepository
}

// EdgeRepository defines the interface for edge data access.
type EdgeRepository interface {
	Create(ctx context.Context, input domain.CreateEdgeInput) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// NewEdgeService creates a new EdgeService.
func NewEdgeService(r EdgeRepository) *EdgeService {
	return &EdgeService{repo: r}
}

// Create validates and inserts a new edge.
func (s *EdgeService) Create(ctx context.Context, input domain.CreateEdgeInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	return s.repo.Create(ctx, input)
}

// Delete removes an edge by its UUID.
func (s *EdgeService) Delete(ctx context.Context, id string) error {
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return domain.ErrInvalidInput
	}
	return s.repo.Delete(ctx, parsedID)
}
