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

func TestJWTContextStoresWorkspaceAuthorAndRole(t *testing.T) {
	workspaceID, userID := uuid.New(), uuid.New()
	ctx := WithJWT(context.Background(), workspaceID, userID, "owner")

	if got, err := WorkspaceID(ctx); err != nil || got != workspaceID {
		t.Fatalf("WorkspaceID() = %s, %v; want %s", got, err, workspaceID)
	}
	if got, err := AuthorID(ctx); err != nil || got != userID {
		t.Fatalf("AuthorID() = %s, %v; want %s", got, err, userID)
	}
	if got := UserRole(ctx); got != "owner" {
		t.Fatalf("UserRole() = %q, want %q", got, "owner")
	}
}

func TestIdentityContextRejectsWrongValueTypes(t *testing.T) {
	ctx := context.WithValue(context.Background(), workspaceIDKey, "not-a-uuid")
	ctx = context.WithValue(ctx, authorIDKey, 42)

	if _, err := WorkspaceID(ctx); !errors.Is(err, ErrMissingTenantContext) {
		t.Fatalf("WorkspaceID(wrong type) error = %v; want ErrMissingTenantContext", err)
	}
	if _, err := AuthorID(ctx); !errors.Is(err, ErrMissingTenantContext) {
		t.Fatalf("AuthorID(wrong type) error = %v; want ErrMissingTenantContext", err)
	}
}

func TestUserRoleMissingOrWrongType(t *testing.T) {
	if got := UserRole(context.Background()); got != "" {
		t.Fatalf("UserRole(missing) = %q, want empty role", got)
	}

	ctx := context.WithValue(context.Background(), userRoleKey, 123)
	if got := UserRole(ctx); got != "" {
		t.Fatalf("UserRole(wrong type) = %q, want empty role", got)
	}
}
