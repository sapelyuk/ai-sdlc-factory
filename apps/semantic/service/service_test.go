package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

type nodeRepositoryStub struct {
	createErr error
	getErr    error
	listErr   error
	updateErr error
	node      *domain.SemNode
	nodes     []*domain.SemNode

	createdNode *domain.SemNode
	gotID       uuid.UUID
	listLayer   string
	listRoot    string
	listDepth   int
	updateID    uuid.UUID
	updateInput domain.UpdateNodeInput
}

func (r *nodeRepositoryStub) Create(_ context.Context, node *domain.SemNode) error {
	r.createdNode = node
	return r.createErr
}

func (r *nodeRepositoryStub) GetByID(_ context.Context, id uuid.UUID) (*domain.SemNode, error) {
	r.gotID = id
	return r.node, r.getErr
}

func (r *nodeRepositoryStub) List(_ context.Context, layer, root string, depth int) ([]*domain.SemNode, error) {
	r.listLayer, r.listRoot, r.listDepth = layer, root, depth
	return r.nodes, r.listErr
}

func (r *nodeRepositoryStub) Update(_ context.Context, id uuid.UUID, input domain.UpdateNodeInput) (*domain.SemNode, error) {
	r.updateID, r.updateInput = id, input
	return r.node, r.updateErr
}

type edgeRepositoryStub struct {
	createErr error
	deleteErr error
	created   domain.CreateEdgeInput
	deletedID uuid.UUID
}

func (r *edgeRepositoryStub) Create(_ context.Context, input domain.CreateEdgeInput) error {
	r.created = input
	return r.createErr
}

func (r *edgeRepositoryStub) Delete(_ context.Context, id uuid.UUID) error {
	r.deletedID = id
	return r.deleteErr
}

func TestNodeServiceCreate(t *testing.T) {
	workspaceID := uuid.New()
	repo := &nodeRepositoryStub{node: &domain.SemNode{ID: uuid.New()}}
	service := NewNodeService(repo)

	if _, err := service.Create(context.Background(), domain.CreateNodeInput{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create(invalid) error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.Create(context.Background(), validNodeInput()); !errors.Is(err, identity.ErrMissingTenantContext) {
		t.Fatalf("Create(without tenant) error = %v, want missing tenant error", err)
	}

	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())
	input := validNodeInput()
	node, err := service.Create(ctx, input)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if node != repo.createdNode || node.WorkspaceID != workspaceID || node.Status != "draft" {
		t.Fatalf("created node = %#v; want repository node with tenant and default status", node)
	}
	if node.Props == nil {
		t.Fatal("Create() left nil props nil")
	}

	repo.createErr = errors.New("insert failed")
	if _, err := service.Create(ctx, input); !errors.Is(err, repo.createErr) {
		t.Fatalf("Create(repository failure) error = %v, want %v", err, repo.createErr)
	}
}

func TestNodeServiceList(t *testing.T) {
	rootID := uuid.New()
	repo := &nodeRepositoryStub{nodes: []*domain.SemNode{{ID: uuid.New()}}}
	service := NewNodeService(repo)

	if _, err := service.List(context.Background(), "unknown", "", 0); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("List(invalid layer) error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.List(context.Background(), "", "not-a-uuid", 1); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("List(invalid root) error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.List(context.Background(), "SLP", "", 5); err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if repo.listLayer != "SLP" || repo.listRoot != "" || repo.listDepth != 0 {
		t.Fatalf("List(all) delegated (%q, %q, %d)", repo.listLayer, repo.listRoot, repo.listDepth)
	}

	for _, tt := range []struct {
		name      string
		depth     int
		wantDepth int
	}{
		{name: "non-positive defaults to one", depth: 0, wantDepth: 1},
		{name: "negative defaults to one", depth: -2, wantDepth: 1},
		{name: "depth is capped", depth: maxSubgraphDepth + 1, wantDepth: maxSubgraphDepth},
		{name: "depth within limit is preserved", depth: 3, wantDepth: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.List(context.Background(), "", rootID.String(), tt.depth); err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if repo.listRoot != rootID.String() || repo.listDepth != tt.wantDepth {
				t.Errorf("delegated root/depth = %q/%d, want %q/%d",
					repo.listRoot, repo.listDepth, rootID, tt.wantDepth)
			}
		})
	}

	repo.listErr = errors.New("query failed")
	if _, err := service.List(context.Background(), "", "", 0); !errors.Is(err, repo.listErr) {
		t.Fatalf("List(repository failure) error = %v", err)
	}
}

func TestNodeServiceUpdateAndGet(t *testing.T) {
	id := uuid.New()
	repo := &nodeRepositoryStub{node: &domain.SemNode{ID: id}}
	service := NewNodeService(repo)

	if _, err := service.Update(context.Background(), id.String(), domain.UpdateNodeInput{Kind: strings.Repeat("x", 129)}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Update(invalid input) error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.Update(context.Background(), "bad-id", domain.UpdateNodeInput{Kind: "NewKind"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Update(invalid ID) error = %v, want ErrInvalidInput", err)
	}
	if _, err := service.GetByID(context.Background(), "bad-id"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("GetByID(invalid ID) error = %v, want ErrInvalidInput", err)
	}

	input := domain.UpdateNodeInput{Kind: "NewKind", Label: "New label"}
	if got, err := service.Update(context.Background(), id.String(), input); err != nil || got != repo.node {
		t.Fatalf("Update() = %p, %v; want repository node and nil error", got, err)
	}
	if repo.updateID != id || repo.updateInput != input {
		t.Fatalf("Update delegated (%s, %#v), want (%s, %#v)", repo.updateID, repo.updateInput, id, input)
	}
	if got, err := service.GetByID(context.Background(), id.String()); err != nil || got != repo.node || repo.gotID != id {
		t.Fatalf("GetByID() = %p, %v (delegated ID %s)", got, err, repo.gotID)
	}

	repo.updateErr = errors.New("update failed")
	if _, err := service.Update(context.Background(), id.String(), input); !errors.Is(err, repo.updateErr) {
		t.Fatalf("Update(repository failure) error = %v", err)
	}
	repo.getErr = errors.New("read failed")
	if _, err := service.GetByID(context.Background(), id.String()); !errors.Is(err, repo.getErr) {
		t.Fatalf("GetByID(repository failure) error = %v", err)
	}
}

func TestEdgeServiceCreateAndDelete(t *testing.T) {
	id := uuid.New()
	repo := &edgeRepositoryStub{}
	service := NewEdgeService(repo)

	if err := service.Create(context.Background(), domain.CreateEdgeInput{}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Create(invalid) error = %v, want ErrInvalidInput", err)
	}
	input := domain.CreateEdgeInput{SrcID: id.String(), DstID: uuid.New().String(), Kind: "relates-to"}
	if err := service.Create(context.Background(), input); err != nil || repo.created != input {
		t.Fatalf("Create() = %v, delegated input %#v", err, repo.created)
	}
	if err := service.Delete(context.Background(), "bad-id"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("Delete(invalid ID) error = %v, want ErrInvalidInput", err)
	}
	if err := service.Delete(context.Background(), id.String()); err != nil || repo.deletedID != id {
		t.Fatalf("Delete() = %v, delegated ID %s", err, repo.deletedID)
	}

	repo.createErr = errors.New("insert failed")
	if err := service.Create(context.Background(), input); !errors.Is(err, repo.createErr) {
		t.Fatalf("Create(repository failure) error = %v", err)
	}
	repo.deleteErr = errors.New("delete failed")
	if err := service.Delete(context.Background(), id.String()); !errors.Is(err, repo.deleteErr) {
		t.Fatalf("Delete(repository failure) error = %v", err)
	}
}

func validNodeInput() domain.CreateNodeInput {
	return domain.CreateNodeInput{Layer: "SLP", Kind: "Role", Label: "Role"}
}
