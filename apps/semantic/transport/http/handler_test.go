package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withTenant adds tenant identity to the context, as TenantContextMiddleware does.
func withTenant(ctx context.Context, workspaceID, authorID uuid.UUID) context.Context {
	return identity.WithTenant(ctx, workspaceID, authorID)
}

// ── Mocks ─────────────────────────────────────────────────────

// mockNodeRepo implements the node repository interface for testing.
// It emulates RLS by scoping every operation to the workspace from the context.
type mockNodeRepo struct {
	nodes     map[uuid.UUID]*domain.SemNode
	createErr error
	listErr   error
	updateErr error
	getErr    error
}

func newMockNodeRepo() *mockNodeRepo {
	return &mockNodeRepo{nodes: make(map[uuid.UUID]*domain.SemNode)}
}

func (m *mockNodeRepo) Create(ctx context.Context, node *domain.SemNode) error {
	if m.createErr != nil {
		return m.createErr
	}
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return err
	}
	if node.WorkspaceID != workspaceID {
		return errors.New("node workspace does not match request tenant")
	}
	if node.ID == uuid.Nil {
		node.ID = uuid.Must(uuid.NewV7())
	}
	node.CreatedAt = time.Now().UTC()
	node.UpdatedAt = node.CreatedAt
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
	if m.listErr != nil {
		return nil, m.listErr
	}
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*domain.SemNode, 0, len(m.nodes))
	for _, n := range m.nodes {
		if n.WorkspaceID != workspaceID {
			continue
		}
		if layer != "" && n.Layer != layer {
			continue
		}
		result = append(result, n)
	}
	return result, nil
}

func (m *mockNodeRepo) Update(ctx context.Context, id uuid.UUID, input domain.UpdateNodeInput) (*domain.SemNode, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	node, ok := m.nodes[id]
	if !ok || node.WorkspaceID != workspaceID {
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
	if m.getErr != nil {
		return nil, m.getErr
	}
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return nil, err
	}
	node, ok := m.nodes[id]
	if !ok || node.WorkspaceID != workspaceID {
		return nil, domain.ErrNotFound
	}
	return node, nil
}

// mockEdgeRepo implements the edge repository interface for testing.
type mockEdgeRepo struct {
	edges     map[uuid.UUID]*domain.SemEdge
	createErr error
	deleteErr error
}

func newMockEdgeRepo() *mockEdgeRepo {
	return &mockEdgeRepo{edges: make(map[uuid.UUID]*domain.SemEdge)}
}

func (m *mockEdgeRepo) Create(ctx context.Context, input domain.CreateEdgeInput) error {
	if m.createErr != nil {
		return m.createErr
	}
	if _, err := identity.WorkspaceID(ctx); err != nil {
		return err
	}
	srcID, _ := uuid.Parse(input.SrcID)
	dstID, _ := uuid.Parse(input.DstID)
	edge := &domain.SemEdge{
		ID:        uuid.Must(uuid.NewV7()),
		SrcID:     srcID,
		DstID:     dstID,
		Kind:      input.Kind,
		Props:     input.Props,
		ValidFrom: time.Now().UTC(),
	}
	m.edges[edge.ID] = edge
	return nil
}

func (m *mockEdgeRepo) Delete(ctx context.Context, id uuid.UUID) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	if _, err := identity.WorkspaceID(ctx); err != nil {
		return err
	}
	if _, ok := m.edges[id]; !ok {
		return domain.ErrEdgeNotFound
	}
	delete(m.edges, id)
	return nil
}

// ── Test helpers ──────────────────────────────────────────────

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

// newTestRouter builds the same route table as NewRouter, but with mock services.
func newTestRouter(nodeSvc *service.NodeService, edgeSvc *service.EdgeService, schema *jsonschema.Schema) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(TenantContextMiddleware)

		r.Post("/v1/nodes", createNodeHandler(nodeSvc, schema))
		r.Get("/v1/nodes", listNodesHandler(nodeSvc))
		r.Patch("/v1/nodes/{id}", updateNodeHandler(nodeSvc))
		r.Post("/v1/edges", createEdgeHandler(edgeSvc))
		r.Delete("/v1/edges/{id}", deleteEdgeHandler(edgeSvc))
	})
	return r
}

// newTenantRequest builds a request carrying valid tenant headers.
func newTenantRequest(method, target, body string, workspaceID uuid.UUID) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Workspace-ID", workspaceID.String())
	req.Header.Set("X-Author-ID", uuid.New().String())
	return req
}

// serve runs the request through the handler and returns the recorded response.
func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// serveWithTenant runs a bare handler (without the middleware) with tenant context.
func serveWithTenant(h http.Handler, req *http.Request, workspaceID uuid.UUID) *httptest.ResponseRecorder {
	req = req.WithContext(withTenant(req.Context(), workspaceID, uuid.New()))
	return serve(h, req)
}

// decodeJSON decodes the recorded response body into T.
func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &value))
	return value
}

// seedNode stores a node directly in the mock repo.
func seedNode(repo *mockNodeRepo, workspaceID uuid.UUID, layer, kind, status string) *domain.SemNode {
	node := &domain.SemNode{
		ID:          uuid.Must(uuid.NewV7()),
		WorkspaceID: workspaceID,
		Layer:       layer,
		Kind:        kind,
		Label:       kind + " label",
		Props:       map[string]any{},
		Version:     1,
		Status:      status,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}
	repo.nodes[node.ID] = node
	return node
}

// loadNodeSchema compiles the real sem-node JSON Schema.
func loadNodeSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	dir, err := config.FindSchemaDir()
	require.NoError(t, err)
	schema, err := jsonschema.Compile(filepath.Join(dir, "sem-node.schema.json"))
	require.NoError(t, err)
	return schema
}

// ── POST /v1/nodes ────────────────────────────────────────────

func TestCreateNodeHandler_ValidRequest_Returns201(t *testing.T) {
	svc, repo := newTestNodeService()
	router := newTestRouter(svc, nil, nil)
	workspaceID := uuid.New()

	body := `{"layer":"SLP","kind":"Test","label":"Test Node","props":{"key":"val"}}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", body, workspaceID))

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	node := decodeJSON[domain.SemNode](t, rec)
	assert.Equal(t, "SLP", node.Layer)
	assert.Equal(t, "Test", node.Kind)
	assert.Equal(t, "Test Node", node.Label)
	assert.Equal(t, "draft", node.Status)
	assert.Equal(t, workspaceID, node.WorkspaceID)
	assert.NotEqual(t, uuid.Nil, node.ID)
	assert.Len(t, repo.nodes, 1)
}

func TestCreateNodeHandler_InvalidLayer_Returns422(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, nil)

	body := `{"layer":"INVALID","kind":"Test","label":"Test Node","props":{}}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", body, uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "layer")
}

func TestCreateNodeHandler_MissingKind_Returns422(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, nil)

	body := `{"layer":"SLP","kind":"","label":"Test Node"}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", body, uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "kind is required")
}

func TestCreateNodeHandler_SchemaViolation_Returns422(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, loadNodeSchema(t))

	// "label" is required by sem-node.schema.json.
	body := `{"layer":"SLP","kind":"Test"}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", body, uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.NotEmpty(t, errResp["error"])
}

func TestCreateNodeHandler_InvalidJSON_Returns400(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, nil)

	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", `{"layer":`, uuid.New()))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "invalid JSON request body")
}

func TestCreateNodeHandler_BodyReadError_Returns400(t *testing.T) {
	svc, _ := newTestNodeService()
	handler := createNodeHandler(svc, nil)

	req := newTenantRequest(http.MethodPost, "/v1/nodes", "", uuid.New())
	req.Body = io.NopCloser(iotest.ErrReader(errors.New("read failure")))
	rec := serveWithTenant(handler, req, uuid.New())

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "read failure")
}

func TestCreateNodeHandler_MissingTenantContext_Returns400(t *testing.T) {
	svc, _ := newTestNodeService()
	handler := createNodeHandler(svc, nil)

	body := `{"layer":"SLP","kind":"Test","label":"Test Node"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/nodes", strings.NewReader(body))
	rec := serve(handler, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "tenant context is missing")
}

func TestCreateNodeHandler_Conflict_Returns409(t *testing.T) {
	svc, repo := newTestNodeService()
	repo.createErr = domain.ErrConflict
	router := newTestRouter(svc, nil, nil)

	body := `{"layer":"SLP","kind":"Test","label":"Test Node"}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", body, uuid.New()))

	require.Equal(t, http.StatusConflict, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "item already exists", errResp["error"])
}

func TestCreateNodeHandler_RepositoryFailure_Returns500(t *testing.T) {
	svc, repo := newTestNodeService()
	repo.createErr = errors.New("connection reset")
	router := newTestRouter(svc, nil, nil)

	body := `{"layer":"SLP","kind":"Test","label":"Test Node"}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/nodes", body, uuid.New()))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "internal error", errResp["error"])
}

// ── GET /v1/nodes ─────────────────────────────────────────────

func TestListNodesHandler_ReturnsOnlyWorkspaceNodes(t *testing.T) {
	svc, repo := newTestNodeService()
	workspaceID := uuid.New()
	seedNode(repo, workspaceID, "SLP", "Role", "active")
	seedNode(repo, workspaceID, "SLPr", "Order", "draft")
	seedNode(repo, uuid.New(), "SLP", "Foreign", "draft") // another tenant must not leak

	router := newTestRouter(svc, nil, nil)
	rec := serve(router, newTenantRequest(http.MethodGet, "/v1/nodes", "", workspaceID))

	require.Equal(t, http.StatusOK, rec.Code)
	nodes := decodeJSON[[]domain.SemNode](t, rec)
	assert.Len(t, nodes, 2)
	for _, n := range nodes {
		assert.Equal(t, workspaceID, n.WorkspaceID)
	}
}

func TestListNodesHandler_FilterByLayer(t *testing.T) {
	svc, repo := newTestNodeService()
	workspaceID := uuid.New()
	seedNode(repo, workspaceID, "SLP", "Role", "active")
	seedNode(repo, workspaceID, "SLPr", "Order", "draft")

	router := newTestRouter(svc, nil, nil)
	rec := serve(router, newTenantRequest(http.MethodGet, "/v1/nodes?layer=SLP", "", workspaceID))

	require.Equal(t, http.StatusOK, rec.Code)
	nodes := decodeJSON[[]domain.SemNode](t, rec)
	require.Len(t, nodes, 1)
	assert.Equal(t, "SLP", nodes[0].Layer)
}

func TestListNodesHandler_InvalidLayer_Returns422(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, nil)

	rec := serve(router, newTenantRequest(http.MethodGet, "/v1/nodes?layer=INVALID", "", uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "invalid layer")
}

func TestListNodesHandler_RepositoryFailure_Returns500(t *testing.T) {
	svc, repo := newTestNodeService()
	repo.listErr = errors.New("query failed")
	router := newTestRouter(svc, nil, nil)

	rec := serve(router, newTenantRequest(http.MethodGet, "/v1/nodes", "", uuid.New()))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "internal error", errResp["error"])
}

// ── PATCH /v1/nodes/{id} ──────────────────────────────────────

func TestUpdateNodeHandler_DraftNode_Returns200(t *testing.T) {
	svc, repo := newTestNodeService()
	workspaceID := uuid.New()
	node := seedNode(repo, workspaceID, "SLP", "Role", "draft")

	router := newTestRouter(svc, nil, nil)
	body := `{"kind":"Team","label":"Updated label","props":{"key":"val"}}`
	rec := serve(router, newTenantRequest(http.MethodPatch, "/v1/nodes/"+node.ID.String(), body, workspaceID))

	require.Equal(t, http.StatusOK, rec.Code)
	updated := decodeJSON[domain.SemNode](t, rec)
	assert.Equal(t, node.ID, updated.ID)
	assert.Equal(t, "Team", updated.Kind)
	assert.Equal(t, "Updated label", updated.Label)
	assert.Equal(t, map[string]any{"key": "val"}, updated.Props)
	assert.Equal(t, "draft", updated.Status)
}

// In this model an approved node is persisted with status "active"
// (see domain.ImmutableStatuses), so PATCH must be rejected with 403.
func TestUpdateNodeHandler_ApprovedNode_Returns403(t *testing.T) {
	svc, repo := newTestNodeService()
	workspaceID := uuid.New()
	node := seedNode(repo, workspaceID, "SLP", "Role", "active")

	router := newTestRouter(svc, nil, nil)
	body := `{"kind":"NewKind"}`
	rec := serve(router, newTenantRequest(http.MethodPatch, "/v1/nodes/"+node.ID.String(), body, workspaceID))

	require.Equal(t, http.StatusForbidden, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "approved")
	assert.Equal(t, "Role", node.Kind, "immutable node must not be modified")
}

func TestUpdateNodeHandler_NotFound_Returns404(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, nil)

	body := `{"kind":"NewKind"}`
	rec := serve(router, newTenantRequest(http.MethodPatch, "/v1/nodes/"+uuid.New().String(), body, uuid.New()))

	require.Equal(t, http.StatusNotFound, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "item not found", errResp["error"])
}

func TestUpdateNodeHandler_InvalidUUID_Returns422(t *testing.T) {
	svc, _ := newTestNodeService()
	router := newTestRouter(svc, nil, nil)

	body := `{"kind":"NewKind"}`
	rec := serve(router, newTenantRequest(http.MethodPatch, "/v1/nodes/not-a-uuid", body, uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "id must be a valid UUID")
}

func TestUpdateNodeHandler_LabelTooLong_Returns422(t *testing.T) {
	svc, repo := newTestNodeService()
	workspaceID := uuid.New()
	node := seedNode(repo, workspaceID, "SLP", "Role", "draft")

	router := newTestRouter(svc, nil, nil)
	body := `{"label":"` + strings.Repeat("x", 513) + `"}`
	rec := serve(router, newTenantRequest(http.MethodPatch, "/v1/nodes/"+node.ID.String(), body, workspaceID))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "label must be at most 512 characters")
}

func TestUpdateNodeHandler_InvalidJSON_Returns400(t *testing.T) {
	svc, repo := newTestNodeService()
	workspaceID := uuid.New()
	node := seedNode(repo, workspaceID, "SLP", "Role", "draft")

	router := newTestRouter(svc, nil, nil)
	rec := serve(router, newTenantRequest(http.MethodPatch, "/v1/nodes/"+node.ID.String(), `{"kind":`, workspaceID))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "invalid JSON request body")
}

func TestUpdateNodeHandler_MissingID_Returns400(t *testing.T) {
	svc, _ := newTestNodeService()
	handler := updateNodeHandler(svc)

	req := httptest.NewRequest(http.MethodPatch, "/v1/nodes/", strings.NewReader(`{"kind":"NewKind"}`))
	rec := serveWithTenant(handler, req, uuid.New())

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "id is required", errResp["error"])
}

// ── POST /v1/edges ────────────────────────────────────────────

func TestCreateEdgeHandler_ValidRequest_Returns201(t *testing.T) {
	svc, repo := newTestEdgeService()
	router := newTestRouter(nil, svc, nil)

	srcID := uuid.New()
	dstID := uuid.New()
	body := `{"src_id":"` + srcID.String() + `","dst_id":"` + dstID.String() + `","kind":"uses","props":{"weight":1}}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/edges", body, uuid.New()))

	require.Equal(t, http.StatusCreated, rec.Code)
	resp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "created", resp["status"])

	require.Len(t, repo.edges, 1)
	for _, edge := range repo.edges {
		assert.Equal(t, srcID, edge.SrcID)
		assert.Equal(t, dstID, edge.DstID)
		assert.Equal(t, "uses", edge.Kind)
	}
}

func TestCreateEdgeHandler_InvalidJSON_Returns400(t *testing.T) {
	svc, _ := newTestEdgeService()
	router := newTestRouter(nil, svc, nil)

	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/edges", `{"src_id":`, uuid.New()))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "invalid JSON request body")
}

// Invalid edges are a client error: validation failures must map to 422, not 500.
func TestCreateEdgeHandler_MissingSrcID_Returns422(t *testing.T) {
	svc, repo := newTestEdgeService()
	router := newTestRouter(nil, svc, nil)

	body := `{"dst_id":"` + uuid.New().String() + `","kind":"uses"}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/edges", body, uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Contains(t, errResp["error"], "src_id is required")
	assert.Empty(t, repo.edges, "invalid edge must not be persisted")
}

func TestCreateEdgeHandler_RepositoryFailure_Returns500(t *testing.T) {
	svc, repo := newTestEdgeService()
	repo.createErr = errors.New("foreign key violation")
	router := newTestRouter(nil, svc, nil)

	body := `{"src_id":"` + uuid.New().String() + `","dst_id":"` + uuid.New().String() + `","kind":"uses"}`
	rec := serve(router, newTenantRequest(http.MethodPost, "/v1/edges", body, uuid.New()))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "internal error", errResp["error"])
}

// ── DELETE /v1/edges/{id} ─────────────────────────────────────

func TestDeleteEdgeHandler_ExistingEdge_Returns200(t *testing.T) {
	svc, repo := newTestEdgeService()
	edgeID := uuid.Must(uuid.NewV7())
	repo.edges[edgeID] = &domain.SemEdge{ID: edgeID, Kind: "uses", ValidFrom: time.Now().UTC()}

	router := newTestRouter(nil, svc, nil)
	rec := serve(router, newTenantRequest(http.MethodDelete, "/v1/edges/"+edgeID.String(), "", uuid.New()))

	require.Equal(t, http.StatusOK, rec.Code)
	resp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "deleted", resp["status"])
	assert.NotContains(t, repo.edges, edgeID)
}

func TestDeleteEdgeHandler_NotFound_Returns404(t *testing.T) {
	svc, _ := newTestEdgeService()
	router := newTestRouter(nil, svc, nil)

	rec := serve(router, newTenantRequest(http.MethodDelete, "/v1/edges/"+uuid.New().String(), "", uuid.New()))

	require.Equal(t, http.StatusNotFound, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "item not found", errResp["error"])
}

func TestDeleteEdgeHandler_InvalidUUID_Returns422(t *testing.T) {
	svc, _ := newTestEdgeService()
	router := newTestRouter(nil, svc, nil)

	rec := serve(router, newTenantRequest(http.MethodDelete, "/v1/edges/not-a-uuid", "", uuid.New()))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "invalid input", errResp["error"])
}

func TestDeleteEdgeHandler_MissingID_Returns400(t *testing.T) {
	svc, _ := newTestEdgeService()
	handler := deleteEdgeHandler(svc)

	req := httptest.NewRequest(http.MethodDelete, "/v1/edges/", nil)
	rec := serveWithTenant(handler, req, uuid.New())

	require.Equal(t, http.StatusBadRequest, rec.Code)
	errResp := decodeJSON[map[string]string](t, rec)
	assert.Equal(t, "id is required", errResp["error"])
}

// ── Middleware and router ─────────────────────────────────────

func TestTenantContextMiddleware_ValidHeaders_PropagatesIdentity(t *testing.T) {
	var gotWorkspace, gotAuthor uuid.UUID
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWorkspace, _ = identity.WorkspaceID(r.Context())
		gotAuthor, _ = identity.AuthorID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	workspaceID := uuid.New()
	authorID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
	req.Header.Set("X-Workspace-ID", workspaceID.String())
	req.Header.Set("X-Author-ID", authorID.String())

	rec := serve(TenantContextMiddleware(next), req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, workspaceID, gotWorkspace)
	assert.Equal(t, authorID, gotAuthor)
}

// Missing tenant headers are treated as a malformed request (400), which matches
// the platform-wide convention in apps/api/internal/transport/http/middleware.go.
func TestTenantContextMiddleware_RejectsInvalidHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler must not be called")
	})
	handler := TenantContextMiddleware(next)

	tests := []struct {
		name      string
		workspace string
		author    string
		wantMsg   string
	}{
		{name: "missing workspace header", author: uuid.New().String(), wantMsg: "X-Workspace-ID"},
		{name: "missing author header", workspace: uuid.New().String(), wantMsg: "X-Author-ID"},
		{name: "malformed workspace header", workspace: "not-a-uuid", author: uuid.New().String(), wantMsg: "X-Workspace-ID"},
		{name: "malformed author header", workspace: uuid.New().String(), author: "not-a-uuid", wantMsg: "X-Author-ID"},
		{name: "nil workspace uuid", workspace: uuid.Nil.String(), author: uuid.New().String(), wantMsg: "X-Workspace-ID"},
		{name: "nil author uuid", workspace: uuid.New().String(), author: uuid.Nil.String(), wantMsg: "X-Author-ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/nodes", nil)
			if tt.workspace != "" {
				req.Header.Set("X-Workspace-ID", tt.workspace)
			}
			if tt.author != "" {
				req.Header.Set("X-Author-ID", tt.author)
			}

			rec := serve(handler, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			errResp := decodeJSON[map[string]string](t, rec)
			assert.Contains(t, errResp["error"], tt.wantMsg)
		})
	}
}

func TestValidationMessage_FallsBackToPlainError(t *testing.T) {
	assert.Equal(t, "plain failure", validationMessage(errors.New("plain failure")))
}

func TestNewRouter_HealthEndpoints(t *testing.T) {
	router := NewRouter(nil, nil, nil)
	handler := router.Handler()

	rec := serve(handler, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())

	rec = serve(handler, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ready"}`, rec.Body.String())

	// Semantic routes are protected by the tenant middleware.
	rec = serve(handler, httptest.NewRequest(http.MethodGet, "/v1/nodes", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
