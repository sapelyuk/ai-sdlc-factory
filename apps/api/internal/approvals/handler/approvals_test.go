package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/repository"
)

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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals/"+id.String(), nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/approvals/"+id.String(), nil)
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+id.String()+"/approve", bytes.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+id.String()+"/reject", bytes.NewReader(body))
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/not-a-uuid/approve", bytes.NewReader([]byte("{}")))
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Approve() with invalid UUID status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDecodeBody_MalformedJSON_Returns400(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+uuid.New().String()+"/approve", bytes.NewReader([]byte("{invalid")))
	w := httptest.NewRecorder()

	h.Approve(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Approve() malformed JSON status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDecodeBody_TrailingData_Returns400(t *testing.T) {
	repo := newMockApprovalRepo()
	h := NewApprovalHandler(repo)
	body := `{"comment":"ok","unknown_field":"data"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+uuid.New().String()+"/approve", bytes.NewReader([]byte(body)))
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
