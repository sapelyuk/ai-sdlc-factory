package identity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestTenantIDs(t *testing.T) {
	workspaceID, authorID := uuid.New(), uuid.New()
	ctx := WithTenant(context.Background(), workspaceID, authorID)

	gotWorkspaceID, err := WorkspaceID(ctx)
	if err != nil || gotWorkspaceID != workspaceID {
		t.Fatalf("WorkspaceID() = %s, %v; want %s, nil", gotWorkspaceID, err, workspaceID)
	}
	gotAuthorID, err := AuthorID(ctx)
	if err != nil || gotAuthorID != authorID {
		t.Fatalf("AuthorID() = %s, %v; want %s, nil", gotAuthorID, err, authorID)
	}
}

func TestTenantIDsRejectMissingAndZeroIDs(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		missingWork bool
		missingAuth bool
	}{
		{name: "empty context", ctx: context.Background(), missingWork: true, missingAuth: true},
		{name: "zero workspace", ctx: WithTenant(context.Background(), uuid.Nil, uuid.New()), missingWork: true},
		{name: "zero author", ctx: WithTenant(context.Background(), uuid.New(), uuid.Nil), missingAuth: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := WorkspaceID(tt.ctx); tt.missingWork {
				if !errors.Is(err, ErrMissingTenantContext) || got != uuid.Nil {
					t.Errorf("WorkspaceID() = %s, %v; want nil UUID and missing-tenant error", got, err)
				}
			} else if err != nil || got == uuid.Nil {
				t.Errorf("WorkspaceID() = %s, %v; want a non-zero ID", got, err)
			}
			if got, err := AuthorID(tt.ctx); tt.missingAuth {
				if !errors.Is(err, ErrMissingTenantContext) || got != uuid.Nil {
					t.Errorf("AuthorID() = %s, %v; want nil UUID and missing-tenant error", got, err)
				}
			} else if err != nil || got == uuid.Nil {
				t.Errorf("AuthorID() = %s, %v; want a non-zero ID", got, err)
			}
		})
	}
}
