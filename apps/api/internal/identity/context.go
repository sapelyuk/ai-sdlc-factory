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
	userRoleKey
)

var ErrMissingTenantContext = errors.New("tenant context is missing")

// WithTenant sets workspace and author IDs in the context (legacy mode).
func WithTenant(ctx context.Context, workspaceID, authorID uuid.UUID) context.Context {
	ctx = context.WithValue(ctx, workspaceIDKey, workspaceID)
	return context.WithValue(ctx, authorIDKey, authorID)
}

// WithJWT sets workspace, author (from JWT sub), and role in the context.
func WithJWT(ctx context.Context, workspaceID, userID uuid.UUID, role string) context.Context {
	ctx = context.WithValue(ctx, workspaceIDKey, workspaceID)
	ctx = context.WithValue(ctx, authorIDKey, userID)
	return context.WithValue(ctx, userRoleKey, role)
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

// UserRole returns the RBAC role stored in the context (empty string if not set).
func UserRole(ctx context.Context) string {
	role, _ := ctx.Value(userRoleKey).(string)
	return role
}
