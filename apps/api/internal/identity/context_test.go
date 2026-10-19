package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestTenantContextStoresWorkspaceAndAuthor(t *testing.T) {
	workspaceID, authorID := uuid.New(), uuid.New()
	ctx := WithTenant(context.Background(), workspaceID, authorID)
	if got, err := WorkspaceID(ctx); err != nil || got != workspaceID {
		t.Fatalf("WorkspaceID() = %s, %v; want %s", got, err, workspaceID)
	}
	if got, err := AuthorID(ctx); err != nil || got != authorID {
		t.Fatalf("AuthorID() = %s, %v; want %s", got, err, authorID)
	}
}

func TestTenantContextRejectsMissingAndZeroIDs(t *testing.T) {
	if _, err := WorkspaceID(context.Background()); !errors.Is(err, ErrMissingTenantContext) {
		t.Fatalf("WorkspaceID(empty) error = %v; want ErrMissingTenantContext", err)
	}
	if _, err := AuthorID(context.Background()); !errors.Is(err, ErrMissingTenantContext) {
		t.Fatalf("AuthorID(empty) error = %v; want ErrMissingTenantContext", err)
	}
	if _, err := WorkspaceID(WithTenant(context.Background(), uuid.Nil, uuid.New())); !errors.Is(err, ErrMissingTenantContext) {
		t.Fatalf("WorkspaceID(zero) error = %v; want ErrMissingTenantContext", err)
	}
	ctx := WithTenant(context.Background(), uuid.New(), uuid.Nil)
	if _, err := AuthorID(ctx); !errors.Is(err, ErrMissingTenantContext) {
		t.Fatalf("AuthorID(zero) error = %v; want ErrMissingTenantContext", err)
	}
}
