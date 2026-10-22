package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/service"
	"github.com/stretchr/testify/assert"
)

func withTenant(ctx context.Context, workspaceID, authorID uuid.UUID) context.Context {
	return identity.WithTenant(ctx, workspaceID, authorID)
}

// mockNodeRepo implements the node repository interface for testing.
type mockNodeRepo struct {
	nodes    map[uuid.UUID]*domain.SemNode
	listFunc func(ctx context.Context, layer string) ([]*domain.SemNode, error)
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{nodes: make(map[uuid.UUID]*domain.SemNode)}
}

func (m *mockNodeRepo) Create(ctx context.Context, node *domain.SemNode) error {
	node.ID = uuid.Must(uuid.NewV7())
	node.CreatedAt = time.Now().UTC()
	node.UpdatedAt = time.Now().UTC()
	if node.Version == 0 {
		node.Version = 1
	}
	if node.Status == "" {
		node.Status = "draft"
	}
	m.nodes[node.ID] = node
	return nil
}

func (m *mockNodeRepo) List(ctx context.Context, layer string) ([]*domain.SemNode, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, layer)
	}
	var result []*domain.SemNode
	for _, n := range m.nodes {
		if layer == "" || n.Layer == layer {
			result = append(result, n)
		}
	}
	return result, nil
}

func (m *mockNodeRepo) Update(ctx context.Context, id uuid.UUID, input domain.UpdateNodeInput) (*domain.SemNode, error) {
	node, ok := m.nodes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	if domain.ImmutableStatuses[node.Status] {
		return nil, domain.ErrForbidden
	}
	if input.Kind != "" {
		node.Kind = input.Kind
	}
	if input.Label != "" {
		node.Label = input.Label
	}
	if input.Props != nil {
		node.Props = input.Props
	}
	node.UpdatedAt = time.Now().UTC()
	return node, nil
}

func (m *mockNodeRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.SemNode, error) {
	node, ok := m.nodes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return node, nil
}

// mockEdgeRepo implements the edge repository interface for testing.
type mockEdgeRepo struct {
	edges map[uuid.UUID]*domain.SemEdge
}

func newMockEdgeRepo() *mockEdgeRepo {
	return &mockEdgeRepo{edges: make(map[uuid.UUID]*domain.SemEdge)}
}

func (m *mockEdgeRepo) Create(ctx context.Context, input domain.CreateEdgeInput) error {
	return nil
}

func (m *mockEdgeRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

// newTestNodeService creates a NodeService with a mock repo.
func newTestNodeService() (*service.NodeService, *mockNodeRepo) {
	repo := newMockNodeRepo()
	return service.NewNodeService(repo), repo
}

// newTestEdgeService creates an EdgeService with a mock repo.
func newTestEdgeService() (*service.EdgeService, *mockEdgeRepo) {
	repo := newMockEdgeRepo()
	return service.NewEdgeService(repo), repo
}

// ── Tests ─────────────────────────────────────────────────────

func TestCreateNodeHandler_ValidRequest_Returns201(t *testing.T) {
	svc, _ := newTestNodeService()
	handler := createNodeHandler(svc, nil)

	body := `{"layer":"SLP","kind":"Test","label":"Test Node","props":{"key":"val"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-ID", uuid.New().String())
	req.Header.Set("X-Author-ID", uuid.New().String())
	// Manually add tenant context.
	wsid, _ := uuid.Parse(req.Header.Get("X-Workspace-ID"))
	aid, _ := uuid.Parse(req.Header.Get("X-Author-ID"))
	req = req.WithContext(withTenant(req.Context(), wsid, aid))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var node domain.SemNode
	err := json.Unmarshal(rec.Body.Bytes(), &node)
	assert.NoError(t, err)
	assert.Equal(t, "SLP", node.Layer)
	assert.Equal(t, "Test", node.Kind)
	assert.NotEqual(t, uuid.Nil, node.ID)
}

func TestCreateNodeHandler_InvalidLayer_Returns422(t *testing.T) {
	svc, _ := newTestNodeService()
	handler := createNodeHandler(svc, nil)

	body := `{"layer":"INVALID","kind":"Test","label":"Test Node","props":{}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-ID", uuid.New().String())
	req.Header.Set("X-Author-ID", uuid.New().String())
	wsid, _ := uuid.Parse(req.Header.Get("X-Workspace-ID"))
	aid, _ := uuid.Parse(req.Header.Get("X-Author-ID"))
	req = req.WithContext(withTenant(req.Context(), wsid, aid))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var errResp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &errResp)
	assert.Contains(t, errResp["error"], "layer")
}

func TestUpdateNodeHandler_ActiveNode_Returns403(t *testing.T) {
	svc, repo := newTestNodeService()

	// Create a node with status 'active' (immutable/approved via review).
	node := &domain.SemNode{
		ID:          uuid.Must(uuid.NewV7()),
		WorkspaceID: uuid.New(),
		Layer:       "SLP",
		Kind:        "ActiveTest",
		Label:       "Active Node",
		Version:     1,
		Status:      "active",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	repo.nodes[node.ID] = node

	handler := updateNodeHandler(svc)

	body := `{"kind":"NewKind"}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/nodes/"+node.ID.String(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-ID", node.WorkspaceID.String())
	req.Header.Set("X-Author-ID", uuid.New().String())

	// Set chi URL param for the route.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", node.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	wsid, _ := uuid.Parse(req.Header.Get("X-Workspace-ID"))
	aid, _ := uuid.Parse(req.Header.Get("X-Author-ID"))
	req = req.WithContext(withTenant(req.Context(), wsid, aid))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestListNodesHandler_FilterByLayer(t *testing.T) {
	svc, repo := newTestNodeService()

	wsid := uuid.New()
	repo.nodes[uuid.Must(uuid.NewV7())] = &domain.SemNode{
		ID: uuid.Must(uuid.NewV7()), WorkspaceID: wsid, Layer: "SLP", Kind: "Role",
		Label: "Role", Version: 1, Status: "draft",
	}
	repo.nodes[uuid.Must(uuid.NewV7())] = &domain.SemNode{
		ID: uuid.Must(uuid.NewV7()), WorkspaceID: wsid, Layer: "SLPr", Kind: "Order",
		Label: "Order", Version: 1, Status: "draft",
	}

	handler := listNodesHandler(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/nodes?layer=SLP", nil)
	req.Header.Set("X-Workspace-ID", wsid.String())
	req.Header.Set("X-Author-ID", uuid.New().String())
	wsid2, _ := uuid.Parse(req.Header.Get("X-Workspace-ID"))
	aid, _ := uuid.Parse(req.Header.Get("X-Author-ID"))
	req = req.WithContext(withTenant(req.Context(), wsid2, aid))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var nodes []*domain.SemNode
	json.Unmarshal(rec.Body.Bytes(), &nodes)
	for _, n := range nodes {
		assert.Equal(t, "SLP", n.Layer)
	}
}
