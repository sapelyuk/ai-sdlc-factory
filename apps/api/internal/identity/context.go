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

var ErrMissingTenantContext = errors.New("tenant context is missing")

func WithTenant(ctx context.Context, workspaceID, authorID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, workspaceIDKey, workspaceID)
	return context.WithValue(ctx, authorIDKey, authorID)
}

func WorkspaceID(ctx context.Context) (uuid.UUID, error) {
	id, ok := ctx.Value(workspaceIDKey).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, ErrMissingTenantContext
	}
	return id, nil
}

func AuthorID(ctx context.Context) (uuid.UUID, error) {
	id, ok := ctx.Value(authorIDKey).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, ErrMissingTenantContext
	}
	return id, nil
}
