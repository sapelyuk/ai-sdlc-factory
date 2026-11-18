package identity

import (
	"context"
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

func TestTenantIDMissing(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		get  func(context.Context) (uuid.UUID, error)
	}{
		{name: "workspace absent", ctx: context.Background(), get: WorkspaceID},
		{name: "author absent", ctx: context.Background(), get: AuthorID},
		{name: "workspace nil", ctx: context.WithValue(context.Background(), workspaceIDKey, uuid.Nil), get: WorkspaceID},
		{name: "author nil", ctx: context.WithValue(context.Background(), authorIDKey, uuid.Nil), get: AuthorID},
		{name: "workspace wrong type", ctx: context.WithValue(context.Background(), workspaceIDKey, "not a UUID"), get: WorkspaceID},
		{name: "author wrong type", ctx: context.WithValue(context.Background(), authorIDKey, "not a UUID"), get: AuthorID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.get(tt.ctx)
			if got != uuid.Nil {
				t.Errorf("ID = %s, want uuid.Nil", got)
			}
			if err != ErrMissingTenantContext {
				t.Errorf("error = %v, want ErrMissingTenantContext", err)
			}
		})
	}
}
