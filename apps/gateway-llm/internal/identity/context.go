// Package identity provides tenant context helpers (workspace_id, author_id).
package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type contextKey uint8

const (
	workspaceIDKey contextKey = iota
	authorIDKey
)

// ErrMissingTenantContext is returned when tenant headers were not provided.
var ErrMissingTenantContext = errors.New("tenant context is missing")

// WithTenant sets workspace and author IDs in the context.
func WithTenant(ctx context.Context, workspaceID, authorID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, workspaceIDKey, workspaceID)
	return context.WithValue(ctx, authorIDKey, authorID)
}

// WorkspaceID returns the workspace ID from the context.
func WorkspaceID(ctx context.Context) (uuid.UUID, error) {
	id, ok := ctx.Value(workspaceIDKey).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, ErrMissingTenantContext
	}
	return id, nil
}

// AuthorID returns the author ID from the context.
func AuthorID(ctx context.Context) (uuid.UUID, error) {
	id, ok := ctx.Value(authorIDKey).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, ErrMissingTenantContext
	}
	return id, nil
}
