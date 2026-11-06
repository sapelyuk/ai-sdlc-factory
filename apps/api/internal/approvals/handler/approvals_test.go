package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/repository"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// newRequestWithID creates an httptest.Request with the chi route context
// populated so that chi.URLParam(r, "id") returns the given UUID string.
func newRequestWithID(method, path string, id uuid.UUID, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// mockApprovalRepo is a minimal in-memory repository for handler tests.
type mockApprovalRepo struct {
	items       map[uuid.UUID]*domain.ApprovalRequest
	decided     bool
	selfApprove bool
}

func newMockApprovalRepo() *mockApprovalRepo {
	return &mockApprovalRepo{
		items: make(map[uuid.UUID]*domain.ApprovalRequest),
	}
}

func (m *mockApprovalRepo) List(_ context.Context, status *domain.Status) ([]*domain.ApprovalRequest, error) {
	var out []*domain.ApprovalRequest
	for _, r := range m.items {
		if status != nil && r.Status != *status {
			continue
		}
		out = append(out, r)
	}
	if out == nil {
		out = []*domain.ApprovalRequest{}
	}
	return out, nil
}

func (m *mockApprovalRepo) GetByID(_ context.Context, id uuid.UUID) (*domain.ApprovalRequest, error) {
	r, ok := m.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r, nil
}

func (m *mockApprovalRepo) Approve(_ context.Context, id uuid.UUID, decidedBy uuid.UUID, comment string) error {
	if m.selfApprove {
		return repository.ErrSelfApproval
	}
	r, ok := m.items[id]
	if !ok {
		return domain.ErrNotFound
	}
	if m.decided {
		return repository.ErrNotPending
	}
	r.Status = domain.StatusApproved
	r.ApprovedBy = &decidedBy
	r.Comment = comment
	return nil
}

func (m *mockApprovalRepo) Reject(_ context.Context, id uuid.UUID, decidedBy uuid.UUID, comment string) error {
	if m.selfApprove {
		return repository.ErrSelfApproval
	}
	r, ok := m.items[id]
	if !ok {
		return domain.ErrNotFound
	}
	if m.decided {
		return repository.ErrNotPending
	}
	r.Status = domain.StatusRejected
	r.ApprovedBy = &decidedBy
	r.Comment = comment
	return nil
}

var _ repository.ApprovalRepository = (*mockApprovalRepo)(nil)

// --- Handler tests ---

func TestList_Empty_Returns200(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil)
	w := httptest.NewRecorder()

	h.List(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("List() status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp []domain.ApprovalRequest
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("List() failed to unmarshal: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("List() len = %d, want 0", len(resp))
	}
}

func TestList_InvalidStatus_Returns400(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals?status=invalid", nil)
	w := httptest.NewRecorder()

	h.List(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("List() status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGet_NotFound_Returns404(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	id := uuid.New()
	req := newRequestWithID(http.MethodGet, "/api/v1/approvals/"+id.String(), id, nil)
	w := httptest.NewRecorder()

	h.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Get() status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestGet_OK_Returns200(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusPending,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}

	req := newRequestWithID(http.MethodGet, "/api/v1/approvals/"+id.String(), id, nil)
	w := httptest.NewRecorder()

	h := NewApprovalHandler(repo)
	h.Get(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Get() status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp domain.ApprovalRequest
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Get() failed to unmarshal: %v", err)
	}
	if resp.ID != id {
		t.Errorf("Get() ID = %s, want %s", resp.ID, id)
	}
}

func TestApprove_HappyPath(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusPending,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "LGTM"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader(body))
	w := httptest.NewRecorder()

	h := NewApprovalHandler(repo)
	h.Approve(w, req)

	// Without JWT context, the handler returns 400 (missing AuthorID).
	if w.Code != http.StatusBadRequest {
		t.Errorf("Approve() without JWT status = %d, want 400 (missing auth context)", w.Code)
	}
}

func TestReject_HappyPath(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusPending,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "Needs work"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/reject", id, bytes.NewReader(body))
	w := httptest.NewRecorder()

	h := NewApprovalHandler(repo)
	h.Reject(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Reject() without JWT status = %d, want 400 (missing auth context)", w.Code)
	}
}

func TestApprove_InvalidID_Returns400(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/not-a-uuid/approve", uuid.Nil, bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()

	// Override the route context with a non-UUID value.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.Approve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Approve() with invalid UUID status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDecodeBody_MalformedJSON_Returns400(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	id := uuid.New()
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader([]byte("{invalid")))
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Approve() malformed JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDecodeBody_TrailingData_Returns400(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	id := uuid.New()
	body := `{"comment":"ok","unknown_field":"data"}`
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Approve() trailing data status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDecodeBody_EmptyBody_AllowsNil(t *testing.T) {
	var target domain.ApproveDecision
	err := decodeBody(nil, &target)
	if err != nil {
		t.Errorf("decodeBody(nil) error = %v, want nil", err)
	}
}

func TestDecodeBody_WhitespaceBody_AllowsNil(t *testing.T) {
	var target domain.ApproveDecision
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("  ")))
	err := decodeBody(req, &target)
	if err != nil {
		t.Errorf("decodeBody(whitespace) error = %v, want nil", err)
	}
}

func TestDecodeBody_ValidJSON_NoUnknownFields_OK(t *testing.T) {
	var target domain.ApproveDecision
	body := `{"comment":"ok"}`
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(body)))
	err := decodeBody(req, &target)
	if err != nil {
		t.Errorf("decodeBody(valid) error = %v, want nil", err)
	}
	if target.Comment != "ok" {
		t.Errorf("decodeBody() comment = %q, want %q", target.Comment, "ok")
	}
}

func TestWriteJSON_VerifyContentType(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("writeJSON Content-Type = %q, want %q", ct, "application/json")
	}
	if w.Code != http.StatusOK {
		t.Errorf("writeJSON status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestWriteError_VerifyFormat(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad input")

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("writeError body not JSON: %v", err)
	}
	if msg, ok := resp["error"]; !ok || msg != "bad input" {
		t.Errorf("writeError body = %v, want {\"error\":\"bad input\"}", resp)
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("writeError status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestParseID_InvalidUUID_ReturnsFalse(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals/bad-id", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "bad-id")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	_, ok := parseID(w, req)
	if ok {
		t.Errorf("parseID(bad) returned ok = true, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("parseID(bad) recorder status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestParseID_ValidUUID_ReturnsTrue(t *testing.T) {
	w := httptest.NewRecorder()
	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals/"+id.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	result, ok := parseID(w, req)
	if !ok {
		t.Errorf("parseID(valid) returned ok = false, want true")
	}
	if result != id {
		t.Errorf("parseID() = %s, want %s", result, id)
	}
}

func TestValidStatus_Pending(t *testing.T) {
	if !validStatus(domain.StatusPending) {
		t.Error("validStatus(pending) = false, want true")
	}
}

func TestValidStatus_Approved(t *testing.T) {
	if !validStatus(domain.StatusApproved) {
		t.Error("validStatus(approved) = false, want true")
	}
}

func TestValidStatus_Rejected(t *testing.T) {
	if !validStatus(domain.StatusRejected) {
		t.Error("validStatus(rejected) = false, want true")
	}
}

func TestValidStatus_Expired(t *testing.T) {
	if !validStatus(domain.StatusExpired) {
		t.Error("validStatus(expired) = false, want true")
	}
}

func TestValidStatus_Unknown(t *testing.T) {
	if validStatus(domain.Status("unknown")) {
		t.Error("validStatus(unknown) = true, want false")
	}
}

// --- decide endpoint tests with auth context ---

func TestApprove_SelfApproval_Returns403(t *testing.T) {
	repo := newMockApprovalRepo()
	repo.selfApprove = true
	id := uuid.New()
	h := NewApprovalHandler(repo)

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "self"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), uuid.New(), id)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Approve(self) status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestApprove_AlreadyDecided_Returns409(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusApproved,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}
	repo.decided = true
	h := NewApprovalHandler(repo)

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "late"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), id, uuid.New())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Approve(already) status = %d, want %d", w.Code, http.StatusConflict)
	}
}

func TestApprove_HappyPath_Returns200(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusPending,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}
	h := NewApprovalHandler(repo)

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "LGTM"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), id, uuid.New())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Approve(w, req)

	// Mock repo returns the item after approval, so GetByID finds it → 200.
	if w.Code != http.StatusOK {
		t.Errorf("Approve(happy) status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestReject_SelfApproval_Returns403(t *testing.T) {
	repo := newMockApprovalRepo()
	repo.selfApprove = true
	id := uuid.New()
	h := NewApprovalHandler(repo)

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "self"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/reject", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), uuid.New(), id)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Reject(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Reject(self) status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestReject_AlreadyDecided_Returns409(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusRejected,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}
	repo.decided = true
	h := NewApprovalHandler(repo)

	body, _ := json.Marshal(domain.ApproveDecision{Comment: "late"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/reject", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), id, uuid.New())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Reject(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("Reject(already) status = %d, want %d", w.Code, http.StatusConflict)
	}
}

// --- writeRepositoryError tests ---

func TestWriteRepositoryError_PgError23514(t *testing.T) {
	w := httptest.NewRecorder()
	pgErr := &pgconn.PgError{Code: "23514"}
	writeRepositoryError(w, pgErr)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("writeRepositoryError(PgError 23514) status = %d, want %d", w.Code, http.StatusUnprocessableEntity)
	}
}

func TestWriteRepositoryError_MissingTenantContext(t *testing.T) {
	w := httptest.NewRecorder()
	writeRepositoryError(w, identity.ErrMissingTenantContext)

	if w.Code != http.StatusBadRequest {
		t.Errorf("writeRepositoryError(MissingTenantContext) status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWriteRepositoryError_NotFound(t *testing.T) {
	w := httptest.NewRecorder()
	writeRepositoryError(w, domain.ErrNotFound)

	if w.Code != http.StatusNotFound {
		t.Errorf("writeRepositoryError(NotFound) status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestWriteRepositoryError_GenericError(t *testing.T) {
	w := httptest.NewRecorder()
	writeRepositoryError(w, errors.New("some unknown db error"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("writeRepositoryError(generic) status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// --- decodeBody error path tests ---

func TestDecodeBody_ReadError(t *testing.T) {
	var target domain.ApproveDecision
	req := httptest.NewRequest(http.MethodPost, "/", io.LimitReader(nil, 0))
	// This should not panic
	err := decodeBody(req, &target)
	// nil or EOF is acceptable
	_ = err
}

// --- Handler error paths ---

// errApprovalRepo is a mock repo that always returns errors.
type errApprovalRepo struct{}

func (e *errApprovalRepo) List(_ context.Context, _ *domain.Status) ([]*domain.ApprovalRequest, error) {
	return nil, errors.New("db connection lost")
}
func (e *errApprovalRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.ApprovalRequest, error) {
	return nil, errors.New("db connection lost")
}
func (e *errApprovalRepo) Approve(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) error {
	return errors.New("db connection lost")
}
func (e *errApprovalRepo) Reject(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ string) error {
	return errors.New("db connection lost")
}

var _ repository.ApprovalRepository = (*errApprovalRepo)(nil)

func TestList_RepoError_Returns500(t *testing.T) {
	h := NewApprovalHandler(&errApprovalRepo{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals", nil)
	w := httptest.NewRecorder()

	h.List(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("List(repo error) status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestGet_RepoError_Returns500(t *testing.T) {
	h := NewApprovalHandler(&errApprovalRepo{})
	id := uuid.New()
	req := newRequestWithID(http.MethodGet, "/api/v1/approvals/"+id.String(), id, nil)
	w := httptest.NewRecorder()

	h.Get(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Get(repo error) status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDecide_RepoError_Returns500(t *testing.T) {
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusPending,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}
	h := NewApprovalHandler(&errApprovalRepo{})
	body, _ := json.Marshal(domain.ApproveDecision{Comment: "test"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), id, uuid.New())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Approve(repo error) status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestDecide_UnknownDecisionPath(t *testing.T) {
	// The switch in decide has no default case, but testing the generic
	// error path covers the final writeRepositoryError call.
	repo := newMockApprovalRepo()
	id := uuid.New()
	repo.items[id] = &domain.ApprovalRequest{
		ID:          id,
		Status:      domain.StatusPending,
		GateName:    "ci_green",
		WorkspaceID: uuid.New(),
	}
	h := NewApprovalHandler(&errApprovalRepo{})
	body, _ := json.Marshal(domain.ApproveDecision{Comment: "test"})
	req := newRequestWithID(http.MethodPost, "/api/v1/approvals/"+id.String()+"/reject", id, bytes.NewReader(body))
	ctx := identity.WithTenant(req.Context(), id, uuid.New())
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	h.Reject(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Reject(repo error) status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// brokenWriter always returns an error on Write to cover writeJSON encode error.
type brokenWriter struct {
	http.ResponseWriter
}

func (b *brokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestWriteJSON_EncodeError(t *testing.T) {
	w := &brokenWriter{httptest.NewRecorder()}
	// writeJSON calls encoder.Encode which will fail on brokenWriter.Write.
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})
	// The test just verifies logError is called without panicking.
}

func TestLogError(t *testing.T) {
	// logError should not panic.
	logError("test message", errors.New("test error"))
}
