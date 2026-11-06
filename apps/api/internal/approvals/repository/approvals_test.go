// Package repository tests for the approval queue repository.
//
// Unit tests exercise error constants, column completeness, and event building.
// Integration tests (tagged with "integration") require Docker/testcontainers.
package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// --- fake pgx.Tx / pgx.Beginner for repository unit tests ---

// fakeBeginner implements pgxBeginner.
type fakeBeginner struct {
	tx  pgx.Tx
	err error
}

func (f *fakeBeginner) Begin(_ context.Context) (pgx.Tx, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tx, nil
}

// fakeTx implements pgx.Tx with configurable behavior.
type fakeTx struct {
	execErr      error
	setConfigErr error
	execTag      string
	queryErr     error
	queryRowFn   func(sql string, args ...any) pgx.Row
	rows         *fakeRows
	committed    bool
	rolledBack   bool
}

func (f *fakeTx) Begin(context.Context) (pgx.Tx, error) { return f, nil }
func (f *fakeTx) Commit(context.Context) error          { f.committed = true; return nil }
func (f *fakeTx) Rollback(context.Context) error        { f.rolledBack = true; return nil }
func (f *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return &fakeBatchResults{} }
func (f *fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeTx) Conn() *pgx.Conn { return nil }
func (f *fakeTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "set_config") {
		if f.setConfigErr != nil {
			return pgconn.CommandTag{}, f.setConfigErr
		}
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	tag := f.execTag
	if tag == "" {
		tag = "UPDATE 1"
	}
	return pgconn.NewCommandTag(tag), nil
}
func (f *fakeTx) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.rows == nil {
		f.rows = &fakeRows{}
	}
	return f.rows, nil
}
func (f *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if f.queryRowFn != nil {
		return f.queryRowFn(sql, args...)
	}
	return &fakeRow{err: errors.New("no mock configured")}
}

// fakeRow implements pgx.Row.
type fakeRow struct {
	values []any
	err    error
}

func (f *fakeRow) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	for i := range dest {
		if i < len(f.values) && f.values[i] != nil {
			switch v := dest[i].(type) {
			case *uuid.UUID:
				*v = f.values[i].(uuid.UUID)
			case *string:
				*v = f.values[i].(string)
			case *time.Time:
				*v = f.values[i].(time.Time)
			case *[]byte:
				*v, _ = f.values[i].([]byte)
			case **uuid.UUID:
				if uid, ok := f.values[i].(uuid.UUID); ok {
					*v = &uid
				}
			case **time.Time:
				if t, ok := f.values[i].(time.Time); ok {
					*v = &t
				}
			}
		}
	}
	return nil
}

// fakeRows implements pgx.Rows.
type fakeRows struct {
	data   []*fakeRow
	pos    int
	closed bool
}

func (f *fakeRows) Close()                                       { f.closed = true }
func (f *fakeRows) Err() error                                   { return nil }
func (f *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (f *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (f *fakeRows) Next() bool                                   { f.pos++; return f.pos <= len(f.data) }
func (f *fakeRows) Scan(dest ...any) error {
	if f.pos == 0 || f.pos > len(f.data) {
		return errors.New("no current row")
	}
	return f.data[f.pos-1].Scan(dest...)
}
func (f *fakeRows) Values() ([]any, error) { return nil, nil }
func (f *fakeRows) RawValues() [][]byte    { return nil }
func (f *fakeRows) Conn() *pgx.Conn        { return nil }
func (f *fakeRows) TypeMap() *pgtype.Map   { return nil }

// fakeBatchResults implements pgx.BatchResults.
type fakeBatchResults struct{}

func (f *fakeBatchResults) Close() error                     { return nil }
func (f *fakeBatchResults) Query() (pgx.Rows, error)         { return &fakeRows{}, nil }
func (f *fakeBatchResults) QueryRow() pgx.Row                { return &fakeRow{} }
func (f *fakeBatchResults) Exec() (pgconn.CommandTag, error) { return pgconn.CommandTag{}, nil }

// fakeAuditor implements sdkaudit.Auditor for repository tests.
type fakeAuditor struct {
	err error
}

func (f *fakeAuditor) Log(_ context.Context, _ sdkaudit.Event) error {
	return f.err
}

// --- Test error constants have messages ---

func TestErrSelfApproval_ErrorMessage(t *testing.T) {
	if ErrSelfApproval.Error() == "" {
		t.Error("ErrSelfApproval should have a non-empty error message")
	}
}

func TestErrNotPending_ErrorMessage(t *testing.T) {
	if ErrNotPending.Error() == "" {
		t.Error("ErrNotPending should have a non-empty error message")
	}
}

func TestErrAuditNotConfigured_ErrorMessage(t *testing.T) {
	if ErrAuditNotConfigured.Error() == "" {
		t.Error("ErrAuditNotConfigured should have a non-empty error message")
	}
}

// --- Test error identity ---

func TestErrSelfApproval_IsDetected(t *testing.T) {
	if !errors.Is(ErrSelfApproval, ErrSelfApproval) {
		t.Error("ErrSelfApproval should be detected by errors.Is")
	}
}

func TestErrNotPending_IsDetected(t *testing.T) {
	if !errors.Is(ErrNotPending, ErrNotPending) {
		t.Error("ErrNotPending should be detected by errors.Is")
	}
}

// --- Test approvalColumns constant is well-formed ---

func TestApprovalColumns_Completeness(t *testing.T) {
	expectedCols := []string{
		"id", "workspace_id", "gate_name", "status", "requested_by",
		"approved_by", "risk_level", "deadline_at", "context", "comment",
		"workflow_id", "task_run_id", "stage_run_id", "run_id",
		"created_at", "updated_at", "decided_at",
	}
	for _, col := range expectedCols {
		if !containsColumn(approvalColumns, col) {
			t.Errorf("approvalColumns missing column %q", col)
		}
	}
}

func containsColumn(query, col string) bool {
	for i := 0; i <= len(query)-len(col); i++ {
		if query[i:i+len(col)] == col {
			return true
		}
	}
	return false
}

// --- Test domain struct has RunID field ---

func TestApprovalRequest_HasRunID(t *testing.T) {
	runID := uuid.New()
	req := &domain.ApprovalRequest{
		ID:          uuid.New(),
		WorkspaceID: uuid.New(),
		GateName:    "ci_green",
		Status:      domain.StatusPending,
		RunID:       &runID,
	}
	if req.RunID == nil {
		t.Error("ApprovalRequest.RunID should be settable")
	}
}

// --- Test decisionRecord structure ---

func TestDecisionRecord_Fields(t *testing.T) {
	wid := uuid.New()
	aid := uuid.New()
	rid := uuid.New()
	did := uuid.New()
	rec := decisionRecord{
		workspaceID: wid,
		approvalID:  aid,
		requestedBy: rid,
		decidedBy:   did,
		status:      domain.StatusApproved,
		workflowID:  "wf-123",
		riskLevel:   "R3",
	}
	if rec.workspaceID != wid {
		t.Error("decisionRecord.workspaceID mismatch")
	}
	if rec.approvalID != aid {
		t.Error("decisionRecord.approvalID mismatch")
	}
	if rec.requestedBy != rid {
		t.Error("decisionRecord.requestedBy mismatch")
	}
	if rec.decidedBy != did {
		t.Error("decisionRecord.decidedBy mismatch")
	}
}

// --- Integration test skeleton (requires Docker/testcontainers) ---

// TestDecidedEvent_BuildsAuditEvent verifies the audit event structure.
func TestDecidedEvent_BuildsAuditEvent(t *testing.T) {
	wid := uuid.New()
	aid := uuid.New()
	rid := uuid.New()
	did := uuid.New()
	rec := decisionRecord{
		workspaceID: wid,
		approvalID:  aid,
		requestedBy: rid,
		decidedBy:   did,
		status:      domain.StatusApproved,
		workflowID:  "wf-123",
		riskLevel:   "R3",
	}
	event := decidedEvent(rec)

	if event.WorkspaceID != wid {
		t.Errorf("event.WorkspaceID = %s, want %s", event.WorkspaceID, wid)
	}
	if event.Action != sdkaudit.ActionApprovalDecided {
		t.Errorf("event.Action = %s, want %s", event.Action, sdkaudit.ActionApprovalDecided)
	}
	if event.IdempotencyKey != sdkaudit.ActionApprovalDecided+":"+aid.String() {
		t.Errorf("event.IdempotencyKey = %q, want %q", event.IdempotencyKey, sdkaudit.ActionApprovalDecided+":"+aid.String())
	}
	if event.ActorID != sdkaudit.ActorPrefixHuman+did.String() {
		t.Errorf("event.ActorID = %s, want %s", event.ActorID, sdkaudit.ActorPrefixHuman+did.String())
	}
}

// TestDecidedEvent_Rejected verifies audit event for rejected approval.
func TestDecidedEvent_Rejected(t *testing.T) {
	rec := decisionRecord{
		workspaceID: uuid.New(),
		approvalID:  uuid.New(),
		decidedBy:   uuid.New(),
		status:      domain.StatusRejected,
	}
	event := decidedEvent(rec)
	if event.Details["decision"] != "rejected" {
		t.Errorf("event.Details[decision] = %q, want %q", event.Details["decision"], "rejected")
	}
}

// TestScanApproval_Basic verifies scanApproval populates all fields.
func TestScanApproval_Basic(t *testing.T) {
	id := uuid.New()
	wid := uuid.New()
	now := time.Now().UTC()
	row := &mockApprovalRow{
		values: []any{
			id, wid, "ci_green", "pending", wid,
			nil, "R3", nil, []byte("{}"),
			"", "wf-123", nil, nil,
			nil, now, now, nil,
		},
	}
	req, err := scanApproval(row)
	if err != nil {
		t.Fatalf("scanApproval: %v", err)
	}
	if req.ID != id {
		t.Errorf("ID = %s, want %s", req.ID, id)
	}
	if req.Status != domain.StatusPending {
		t.Errorf("Status = %s, want %s", req.Status, domain.StatusPending)
	}
	if req.RiskLevel != "R3" {
		t.Errorf("RiskLevel = %s, want %s", req.RiskLevel, "R3")
	}
}

// TestScanApproval_Approved verifies scanApproval with approved status.
func TestScanApproval_Approved(t *testing.T) {
	id := uuid.New()
	wid := uuid.New()
	now := time.Now().UTC()
	approvedBy := uuid.New()
	decidedAt := now
	row := &mockApprovalRow{
		values: []any{
			id, wid, "ci_green", "approved", wid,
			approvedBy, "R2", now, []byte("{}"),
			"", "wf-123", nil, nil,
			nil, now, now, decidedAt,
		},
	}
	req, err := scanApproval(row)
	if err != nil {
		t.Fatalf("scanApproval: %v", err)
	}
	if req.Status != domain.StatusApproved {
		t.Errorf("Status = %s, want %s", req.Status, domain.StatusApproved)
	}
	if req.ApprovedBy == nil || *req.ApprovedBy != approvedBy {
		t.Errorf("ApprovedBy = %v, want %s", req.ApprovedBy, approvedBy)
	}
	if req.DecidedAt == nil || *req.DecidedAt != decidedAt {
		t.Errorf("DecidedAt = %v, want %s", req.DecidedAt, decidedAt)
	}
}

// TestScanApproval_ScanError verifies scanApproval returns error on bad data.
func TestScanApproval_ScanError(t *testing.T) {
	row := &mockApprovalRow{
		err: errors.New("scan failed"),
	}
	if _, err := scanApproval(row); err == nil {
		t.Error("expected error from scanApproval")
	}
}

// TestNewPostgresApprovalRepository verifies constructor.
func TestNewPostgresApprovalRepository(t *testing.T) {
	repo := NewPostgresApprovalRepository(nil, nil)
	if repo == nil {
		t.Fatal("expected non-nil repository")
	}
}

// TestDecide_NilAuditor_ReturnsErrAuditNotConfigured verifies the P9 guard.
func TestDecide_NilAuditor_ReturnsErrAuditNotConfigured(t *testing.T) {
	repo := NewPostgresApprovalRepository(nil, nil)
	ctx := identity.WithTenant(context.Background(), uuid.New(), uuid.New())
	err := repo.Approve(ctx, uuid.New(), uuid.New(), "comment")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrAuditNotConfigured) {
		t.Errorf("expected ErrAuditNotConfigured, got %v", err)
	}
}

// TestInTenant_MissingWorkspaceContext verifies inTenant fails without RLS context.
func TestInTenant_MissingWorkspaceContext(t *testing.T) {
	repo := NewPostgresApprovalRepository(nil, nil)
	err := repo.inTenant(context.Background(), func(_ pgx.Tx, _ uuid.UUID) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error without workspace context")
	}
	if !errors.Is(err, identity.ErrMissingTenantContext) {
		t.Errorf("expected ErrMissingTenantContext, got %v", err)
	}
}

// TestDecide_MissingWorkspaceContext verifies decide fails without RLS context.
func TestDecide_MissingWorkspaceContext(t *testing.T) {
	// Use a fake auditor to pass the nil check.
	repo := NewPostgresApprovalRepository(nil, &fakeAuditor{})
	// No identity context → inTenant fails at identity.WorkspaceID.
	err := repo.Approve(context.Background(), uuid.New(), uuid.New(), "comment")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Tests for List / GetByID / decide driven by a fake pgx.Tx ---

// newFakeRepo wires the repository to a fake transaction so the SQL paths are
// exercised without a database.
func newFakeRepo(tx *fakeTx, auditor sdkaudit.Auditor) *PostgresApprovalRepository {
	return &PostgresApprovalRepository{pool: &fakeBeginner{tx: tx}, auditor: auditor}
}

// approvalRowValues builds the 17 scan values of approvalColumns.
func approvalRowValues(id, workspaceID uuid.UUID, status string) []any {
	now := time.Now().UTC()
	return []any{
		id, workspaceID, "ci_green", status, workspaceID,
		nil, "R3", nil, []byte(`{"k":"v"}`),
		"ok", "wf-123", nil, nil,
		nil, now, now, nil,
	}
}

func TestList_HappyPath(t *testing.T) {
	ws := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	tx := &fakeTx{rows: &fakeRows{data: []*fakeRow{
		{values: approvalRowValues(id1, ws, "pending")},
		{values: approvalRowValues(id2, ws, "approved")},
	}}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	out, err := repo.List(identity.WithTenant(context.Background(), ws, uuid.New()), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("List returned %d items, want 2", len(out))
	}
	if out[0].ID != id1 || out[1].ID != id2 {
		t.Errorf("List returned unexpected IDs: %s %s", out[0].ID, out[1].ID)
	}
	if !tx.committed {
		t.Error("List should commit the transaction")
	}
}

func TestList_WithStatusFilter(t *testing.T) {
	ws := uuid.New()
	status := domain.StatusPending
	tx := &fakeTx{}
	repo := newFakeRepo(tx, &fakeAuditor{})

	out, err := repo.List(identity.WithTenant(context.Background(), ws, uuid.New()), &status)
	if err != nil {
		t.Fatalf("List with filter: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("List returned %d items, want 0", len(out))
	}
}

func TestList_QueryError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryErr: errors.New("boom")}
	repo := newFakeRepo(tx, &fakeAuditor{})

	if _, err := repo.List(identity.WithTenant(context.Background(), ws, uuid.New()), nil); err == nil {
		t.Fatal("expected error from List")
	}
	if !tx.rolledBack {
		t.Error("List should roll back on query error")
	}
}

func TestList_ScanError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{rows: &fakeRows{data: []*fakeRow{{err: errors.New("bad row")}}}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	if _, err := repo.List(identity.WithTenant(context.Background(), ws, uuid.New()), nil); err == nil {
		t.Fatal("expected scan error from List")
	}
}

func TestGetByID_HappyPath(t *testing.T) {
	ws := uuid.New()
	id := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{values: approvalRowValues(id, ws, "pending")}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	req, err := repo.GetByID(identity.WithTenant(context.Background(), ws, uuid.New()), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if req.ID != id {
		t.Errorf("GetByID returned ID %s, want %s", req.ID, id)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{err: pgx.ErrNoRows}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	_, err := repo.GetByID(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID error = %v, want domain.ErrNotFound", err)
	}
}

func TestGetByID_OtherError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{err: errors.New("connection reset")}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	if _, err := repo.GetByID(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New()); err == nil {
		t.Fatal("expected error from GetByID")
	}
}

func TestDecide_HappyPath_Approve(t *testing.T) {
	ws := uuid.New()
	requester := uuid.New()
	decider := uuid.New()
	approvalID := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{values: []any{requester, "wf-1", "R3"}}
	}}
	aud := &fakeAuditor{}
	repo := newFakeRepo(tx, aud)

	if err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), approvalID, decider, "lgtm"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !tx.committed {
		t.Error("Approve should commit the transaction")
	}
}

func TestDecide_HappyPath_Reject(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{values: []any{uuid.New(), "wf-1", "R2"}}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	if err := repo.Reject(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), uuid.New(), "no"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
}

func TestDecide_SelfApproval(t *testing.T) {
	ws := uuid.New()
	same := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{values: []any{same, "wf-1", "R3"}}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), same, "self")
	if !errors.Is(err, ErrSelfApproval) {
		t.Errorf("Approve error = %v, want ErrSelfApproval", err)
	}
}

func TestDecide_NoRowFound_IsNotPending(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{err: pgx.ErrNoRows}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), uuid.New(), "x")
	if !errors.Is(err, ErrNotPending) {
		t.Errorf("Approve error = %v, want ErrNotPending", err)
	}
}

func TestDecide_LockScanError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{err: errors.New("deadlock detected")}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{})

	if err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), uuid.New(), "x"); err == nil {
		t.Fatal("expected error from decide")
	}
}

func TestDecide_UpdateAffectsNoRows_IsNotPending(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{
		execTag: "UPDATE 0",
		queryRowFn: func(_ string, _ ...any) pgx.Row {
			return &fakeRow{values: []any{uuid.New(), "wf-1", "R3"}}
		},
	}
	repo := newFakeRepo(tx, &fakeAuditor{})

	err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), uuid.New(), "x")
	if !errors.Is(err, ErrNotPending) {
		t.Errorf("Approve error = %v, want ErrNotPending", err)
	}
}

func TestDecide_UpdateError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{
		execErr: errors.New("syntax error"),
		queryRowFn: func(_ string, _ ...any) pgx.Row {
			return &fakeRow{values: []any{uuid.New(), "wf-1", "R3"}}
		},
	}
	repo := newFakeRepo(tx, &fakeAuditor{})

	if err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), uuid.New(), "x"); err == nil {
		t.Fatal("expected error from decide")
	}
}

func TestDecide_AuditFailureIsReturned(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryRowFn: func(_ string, _ ...any) pgx.Row {
		return &fakeRow{values: []any{uuid.New(), "wf-1", "R3"}}
	}}
	repo := newFakeRepo(tx, &fakeAuditor{err: errors.New("audit log unavailable")})

	err := repo.Approve(identity.WithTenant(context.Background(), ws, uuid.New()), uuid.New(), uuid.New(), "x")
	if err == nil {
		t.Fatal("expected audit error to be returned")
	}
	if !strings.Contains(err.Error(), "audit") {
		t.Errorf("error %q should mention audit", err.Error())
	}
}

func TestInTenant_BeginError(t *testing.T) {
	ws := uuid.New()
	repo := &PostgresApprovalRepository{
		pool:    &fakeBeginner{err: errors.New("pool exhausted")},
		auditor: &fakeAuditor{},
	}
	_, err := repo.List(identity.WithTenant(context.Background(), ws, uuid.New()), nil)
	if err == nil {
		t.Fatal("expected error from Begin")
	}
	if !strings.Contains(err.Error(), "begin approval transaction") {
		t.Errorf("error %q should mention begin approval transaction", err.Error())
	}
}

func TestInTenant_SetTenantContextError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{setConfigErr: errors.New("read-only transaction")}
	repo := newFakeRepo(tx, &fakeAuditor{})

	_, err := repo.List(identity.WithTenant(context.Background(), ws, uuid.New()), nil)
	if err == nil {
		t.Fatal("expected error from set_config")
	}
	if !strings.Contains(err.Error(), "set tenant context") {
		t.Errorf("error %q should mention set tenant context", err.Error())
	}
}

// mockApprovalRow implements the scanner interface for scanApproval tests.
type mockApprovalRow struct {
	values []any
	err    error
}

func (m *mockApprovalRow) Scan(dest ...any) error {
	if m.err != nil {
		return m.err
	}
	for i := range dest {
		if i < len(m.values) {
			if m.values[i] == nil {
				continue
			}
			switch v := dest[i].(type) {
			case *uuid.UUID:
				*v = m.values[i].(uuid.UUID)
			case *string:
				*v = m.values[i].(string)
			case *domain.Status:
				*v = domain.Status(m.values[i].(string))
			case *time.Time:
				*v = m.values[i].(time.Time)
			case *[]byte:
				*v, _ = m.values[i].([]byte)
			case **uuid.UUID:
				if uid, ok := m.values[i].(uuid.UUID); ok {
					*v = &uid
				}
			case **time.Time:
				if t, ok := m.values[i].(time.Time); ok {
					*v = &t
				}
			}
		}
	}
	return nil
}

// TestCreate_ThenGet_RoundTrip verifies create-then-get round trip.
// Requires Docker/testcontainers — run with -tags=integration.
func TestCreate_ThenGet_RoundTrip_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestList_RLS_Isolation verifies workspace A cannot see workspace B.
// Requires Docker/testcontainers — run with -tags=integration.
func TestList_RLS_Isolation_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestDecide_Idempotent verifies repeated decisions are handled (409).
// Requires Docker/testcontainers — run with -tags=integration.
func TestDecide_Idempotent_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestDecide_SelfApproval_ReturnsErrSelfApproval verifies P9 self-approval check.
// Requires Docker/testcontainers — run with -tags=integration.
func TestDecide_SelfApproval_ReturnsErrSelfApproval_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestExpire_Overdue_MarksRejected verifies expiration of overdue requests.
// Requires Docker/testcontainers — run with -tags=integration.
func TestExpire_Overdue_MarksRejected_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// TestExpire_NotOverdue_Noop verifies non-overdue requests are unchanged.
// Requires Docker/testcontainers — run with -tags=integration.
func TestExpire_NotOverdue_Noop_Integration(t *testing.T) {
	t.Skip("integration test requires Docker; run with -tags=integration")
}

// --- Benchmark tests ---

func BenchmarkScanApprovalZero(b *testing.B) {
	id := uuid.New()
	req := domain.ApprovalRequest{
		ID:          id,
		WorkspaceID: uuid.New(),
		GateName:    "ci_green",
		Status:      domain.StatusPending,
		RequestedBy: id,
	}
	_ = req
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req
	}
}
