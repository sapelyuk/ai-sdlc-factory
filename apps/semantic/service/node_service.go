// Package service contains business logic for the semantic graph.
package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

// NodeService provides node operations.
type NodeService struct {
	repo NodeRepository
}

// NodeRepository defines the interface for node data access.
type NodeRepository interface {
	Create(ctx context.Context, node *domain.SemNode) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.SemNode, error)
	List(ctx context.Context, layer string) ([]*domain.SemNode, error)
	Update(ctx context.Context, id uuid.UUID, input domain.UpdateNodeInput) (*domain.SemNode, error)
}

// NewNodeService creates a new NodeService.
func NewNodeService(r NodeRepository) *NodeService {
	return &NodeService{repo: r}
}

// Create validates and inserts a new node.
func (s *NodeService) Create(ctx context.Context, input domain.CreateNodeInput) (*domain.SemNode, error) {
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	props := input.Props
	if props == nil {
		props = map[string]any{}
	}
	node := &domain.SemNode{
		WorkspaceID: workspaceID,
		Layer:       input.Layer,
		Kind:        input.Kind,
		Label:       input.Label,
		Props:       props,
		Version:     1,
		Status:      input.DefaultStatus(),
	}
	if err := s.repo.Create(ctx, node); err != nil {
		return nil, err
	}
	return node, nil
}

// List returns nodes, optionally filtered by layer.
func (s *NodeService) List(ctx context.Context, layer string) ([]*domain.SemNode, error) {
	if layer != "" && !domain.ValidLayers[layer] {
		return nil, fmt.Errorf("%w: invalid layer %q", domain.ErrInvalidInput, layer)
	}
	return s.repo.List(ctx, layer)
}

// Update modifies an existing node.
func (s *NodeService) Update(ctx context.Context, id string, input domain.UpdateNodeInput) (*domain.SemNode, error) {
	if err := input.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("%w: id must be a valid UUID", domain.ErrInvalidInput)
	}
	return s.repo.Update(ctx, parsedID, input)
}

// GetByID retrieves a node by its UUID.
func (s *NodeService) GetByID(ctx context.Context, id string) (*domain.SemNode, error) {
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("%w: id must be a valid UUID", domain.ErrInvalidInput)
	}
	return s.repo.GetByID(ctx, parsedID)
}
