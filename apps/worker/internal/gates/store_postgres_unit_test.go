package gates

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
)

// --- marshalContext ---

func TestMarshalContext_Empty(t *testing.T) {
	data, err := marshalContext(nil)
	if err != nil {
		t.Fatalf("marshalContext(nil): %v", err)
	}
	if string(data) != "{}" {
		t.Errorf("expected %q, got %q", "{}", string(data))
	}
}

func TestMarshalContext_NonEmpty(t *testing.T) {
	ctx := map[string]any{"key": "value", "num": 42}
	data, err := marshalContext(ctx)
	if err != nil {
		t.Fatalf("marshalContext: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}

// --- nullUUID ---

func TestNullUUID_Nil(t *testing.T) {
	if nullUUID(uuid.Nil) != nil {
		t.Error("expected nil for uuid.Nil")
	}
}

func TestNullUUID_Valid(t *testing.T) {
	id := uuid.New()
	result := nullUUID(id)
	if result == nil {
		t.Fatal("expected uuid for non-nil UUID")
	}
	if result.(uuid.UUID) != id {
		t.Errorf("expected %s, got %v", id, result)
	}
}

// --- nullUUIDPtr ---

func TestNullUUIDPtr_Nil(t *testing.T) {
	if nullUUIDPtr(nil) != nil {
		t.Error("expected nil for nil pointer")
	}
}

func TestNullUUIDPtr_NilUUID(t *testing.T) {
	id := uuid.Nil
	if nullUUIDPtr(&id) != nil {
		t.Error("expected nil for uuid.Nil pointer")
	}
}

func TestNullUUIDPtr_Valid(t *testing.T) {
	expected := uuid.New()
	result := nullUUIDPtr(&expected)
	if result == nil {
		t.Fatal("expected uuid for valid pointer")
	}
	if result.(uuid.UUID) != expected {
		t.Errorf("expected %s, got %v", expected, result)
	}
}

// --- UnknownGateError.Error ---

func TestUnknownGateError_Message(t *testing.T) {
	e := UnknownGateError{Name: "missing_gate"}
	msg := e.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
	if msg != "gates: unknown gate missing_gate" {
		t.Errorf("unexpected message: %q", msg)
	}
}

// --- Service.Registry getter ---

func TestServiceRegistry(t *testing.T) {
	svc := NewService()
	reg := svc.Registry()
	if reg == nil {
		t.Fatal("Registry() returned nil")
	}
	if _, ok := reg.Get(GateCIGreen); !ok {
		t.Error("expected ci_green in default registry")
	}
}

// --- scanApproval with mock row ---

type mockScanner struct {
	values []any
	err    error
}

func (m *mockScanner) Scan(dest ...any) error {
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
			case *ApprovalStatus:
				*v = ApprovalStatus(m.values[i].(string))
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

func TestScanApproval_Basic(t *testing.T) {
	id := uuid.New()
	wid := uuid.New()
	now := time.Now().UTC()
	req := new(ApprovalRequest)
	row := &mockScanner{
		values: []any{
			id, wid, "ci_green", "approved", wid,
			nil, "R2", nil, []byte("{}"),
			"", "wf-123", nil, nil,
			nil, now, now, nil,
		},
	}
	if err := scanApproval(row, req); err != nil {
		t.Fatalf("scanApproval: %v", err)
	}
	if req.ID != id {
		t.Errorf("ID = %s, want %s", req.ID, id)
	}
	if req.Status != ApprovalApproved {
		t.Errorf("Status = %s, want %s", req.Status, ApprovalApproved)
	}
}

func TestScanApproval_WithRunID(t *testing.T) {
	id := uuid.New()
	wid := uuid.New()
	now := time.Now().UTC()
	runID := uuid.New()
	req := new(ApprovalRequest)
	row := &mockScanner{
		values: []any{
			id, wid, "ci_green", "pending", wid,
			nil, "R3", now, []byte("{}"),
			"", "wf-123", nil, nil,
			runID, now, now, nil,
		},
	}
	if err := scanApproval(row, req); err != nil {
		t.Fatalf("scanApproval: %v", err)
	}
	if req.RunID == nil {
		t.Fatal("expected RunID to be set")
	}
	if *req.RunID != runID {
		t.Errorf("RunID = %s, want %s", *req.RunID, runID)
	}
}

func TestScanApproval_ScanError(t *testing.T) {
	req := new(ApprovalRequest)
	row := &mockScanner{
		err: errors.New("scan failed"),
	}
	if err := scanApproval(row, req); err == nil {
		t.Error("expected error from scan")
	}
}

// --- PostgresStore driven by a fake pool ---
//
// The store talks to the database through a small pool interface, so the SQL
// paths (tenant context, insert, scan, decide, expire) are covered without a
// Docker daemon. The testcontainers test still guards the real schema.

// fakePool implements pgxPool.
type fakePool struct {
	tx       pgx.Tx
	beginErr error
	row      pgx.Row
}

func (f *fakePool) Begin(context.Context) (pgx.Tx, error) {
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return f.tx, nil
}

func (f *fakePool) QueryRow(context.Context, string, ...any) pgx.Row { return f.row }

// fakeTx implements pgx.Tx with configurable behavior.
type fakeTx struct {
	setConfigErr error
	execErr      error
	execTag      string
	queryErr     error
	queryRow     pgx.Row
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
func (f *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row {
	if f.queryRow != nil {
		return f.queryRow
	}
	return &mockScanner{err: errors.New("no mock configured")}
}

// fakeRows implements pgx.Rows over a slice of preloaded scanners.
type fakeRows struct {
	data   []*mockScanner
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
func (f *fakeBatchResults) QueryRow() pgx.Row                { return &mockScanner{} }
func (f *fakeBatchResults) Exec() (pgconn.CommandTag, error) { return pgconn.CommandTag{}, nil }

// storeRowValues builds the 17 scan values of approvalColumns.
func storeRowValues(id, workspaceID uuid.UUID, status string) []any {
	now := time.Now().UTC()
	return []any{
		id, workspaceID, "prod_approved", status, workspaceID,
		nil, "R3", nil, []byte(`{"from":"deploy"}`),
		"", "wf-1", nil, nil,
		nil, now, now, nil,
	}
}

func TestStoreCreate_HappyPath(t *testing.T) {
	tx := &fakeTx{}
	store := &PostgresStore{pool: &fakePool{tx: tx}}
	req := BuildApprovalRequest(uuid.New(), GateProdApproved, uuid.New(), time.Hour, map[string]any{"a": 1})

	if err := store.Create(context.Background(), req); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !tx.committed {
		t.Error("Create should commit the transaction")
	}
}

func TestStoreCreate_TypedNilPool(t *testing.T) {
	// NewPostgresStore(nil) must keep the guard working rather than panic on a
	// typed-nil interface value.
	store := NewPostgresStore(nil)
	if err := store.Create(context.Background(), ApprovalRequest{WorkspaceID: uuid.New()}); err == nil {
		t.Fatal("expected an error without a pool")
	}
}

func TestStoreCreate_ContextMarshalError(t *testing.T) {
	store := &PostgresStore{pool: &fakePool{tx: &fakeTx{}}}
	req := ApprovalRequest{
		ID: uuid.New(), WorkspaceID: uuid.New(),
		// A channel cannot be marshaled: the insert must not be attempted.
		Context: map[string]any{"bad": make(chan int)},
	}
	if err := store.Create(context.Background(), req); err == nil {
		t.Fatal("expected a context encoding error")
	}
}

func TestStoreCreate_ExecError(t *testing.T) {
	tx := &fakeTx{execErr: errors.New("duplicate key")}
	store := &PostgresStore{pool: &fakePool{tx: tx}}
	req := BuildApprovalRequest(uuid.New(), GateProdApproved, uuid.New(), time.Hour, nil)

	err := store.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected Create to fail")
	}
	if !strings.Contains(err.Error(), "create approval") {
		t.Errorf("error %q should wrap the create failure", err.Error())
	}
}

func TestStoreGet_HappyPath(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	tx := &fakeTx{queryRow: &mockScanner{values: storeRowValues(id, ws, "pending")}}
	store := &PostgresStore{pool: &fakePool{
		tx:  tx,
		row: &mockScanner{values: []any{ws}},
	}}

	got, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != id {
		t.Errorf("Get returned ID %s, want %s", got.ID, id)
	}
}

func TestStoreGet_NotFound(t *testing.T) {
	id := uuid.New()
	store := &PostgresStore{pool: &fakePool{
		tx:  &fakeTx{},
		row: &mockScanner{err: pgx.ErrNoRows},
	}}

	if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

func TestStoreGet_ScanError(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	store := &PostgresStore{pool: &fakePool{
		tx:  &fakeTx{queryRow: &mockScanner{err: errors.New("connection reset")}},
		row: &mockScanner{values: []any{ws}},
	}}

	if _, err := store.Get(context.Background(), id); err == nil {
		t.Fatal("expected a scan error")
	}
}

func TestStoreList_HappyPath(t *testing.T) {
	ws := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	tx := &fakeTx{rows: &fakeRows{data: []*mockScanner{
		{values: storeRowValues(id1, ws, "pending")},
		{values: storeRowValues(id2, ws, "approved")},
	}}}
	store := &PostgresStore{pool: &fakePool{tx: tx}}

	status := ApprovalPending
	out, err := store.List(context.Background(), ws, &status)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("List returned %d rows, want 2", len(out))
	}
	if !tx.committed {
		t.Error("List should commit the transaction")
	}
}

func TestStoreList_NoFilter(t *testing.T) {
	ws := uuid.New()
	store := &PostgresStore{pool: &fakePool{tx: &fakeTx{}}}

	out, err := store.List(context.Background(), ws, nil)
	if err != nil {
		t.Fatalf("List without a filter: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("List returned %d rows, want 0", len(out))
	}
}

func TestStoreList_QueryError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{queryErr: errors.New("boom")}
	store := &PostgresStore{pool: &fakePool{tx: tx}}

	if _, err := store.List(context.Background(), ws, nil); err == nil {
		t.Fatal("expected a query error")
	}
	if !tx.rolledBack {
		t.Error("List should roll back on a query error")
	}
}

func TestStoreList_ScanError(t *testing.T) {
	ws := uuid.New()
	tx := &fakeTx{rows: &fakeRows{data: []*mockScanner{{err: errors.New("bad row")}}}}
	store := &PostgresStore{pool: &fakePool{tx: tx}}

	if _, err := store.List(context.Background(), ws, nil); err == nil {
		t.Fatal("expected a scan error")
	}
}

func TestStoreDecide_HappyPath(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	tx := &fakeTx{}
	store := &PostgresStore{pool: &fakePool{tx: tx, row: &mockScanner{values: []any{ws}}}}

	if err := store.Decide(context.Background(), id, true, uuid.New(), "ok"); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !tx.committed {
		t.Error("Decide should commit the transaction")
	}
}

func TestStoreDecide_Rejected(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	store := &PostgresStore{pool: &fakePool{tx: &fakeTx{}, row: &mockScanner{values: []any{ws}}}}

	if err := store.Decide(context.Background(), id, false, uuid.New(), "no"); err != nil {
		t.Fatalf("Decide(reject): %v", err)
	}
}

func TestStoreDecide_NotPending(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	tx := &fakeTx{execTag: "UPDATE 0"}
	store := &PostgresStore{pool: &fakePool{tx: tx, row: &mockScanner{values: []any{ws}}}}

	err := store.Decide(context.Background(), id, true, uuid.New(), "late")
	if !errors.Is(err, ErrNotPending) {
		t.Errorf("Decide error = %v, want ErrNotPending", err)
	}
}

func TestStoreDecide_WorkspaceNotFound(t *testing.T) {
	store := &PostgresStore{pool: &fakePool{tx: &fakeTx{}, row: &mockScanner{err: pgx.ErrNoRows}}}

	err := store.Decide(context.Background(), uuid.New(), true, uuid.New(), "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Decide error = %v, want ErrNotFound", err)
	}
}

func TestStoreDecide_ExecError(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	tx := &fakeTx{execErr: errors.New("deadlock")}
	store := &PostgresStore{pool: &fakePool{tx: tx, row: &mockScanner{values: []any{ws}}}}

	if err := store.Decide(context.Background(), id, true, uuid.New(), ""); err == nil {
		t.Fatal("expected Decide to fail")
	}
}

func TestStoreExpire_HappyPath(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	tx := &fakeTx{}
	store := &PostgresStore{pool: &fakePool{tx: tx, row: &mockScanner{values: []any{ws}}}}

	if err := store.Expire(context.Background(), id); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if !tx.committed {
		t.Error("Expire should commit the transaction")
	}
}

func TestStoreExpire_QueryError(t *testing.T) {
	id := uuid.New()
	ws := uuid.New()
	tx := &fakeTx{execErr: errors.New("read only")}
	store := &PostgresStore{pool: &fakePool{tx: tx, row: &mockScanner{values: []any{ws}}}}

	if err := store.Expire(context.Background(), id); err == nil {
		t.Fatal("expected Expire to fail")
	}
}

func TestStoreExpire_WorkspaceLookupError(t *testing.T) {
	store := &PostgresStore{pool: &fakePool{tx: &fakeTx{}, row: &mockScanner{err: errors.New("connection bad")}}}

	err := store.Expire(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected a workspace lookup error")
	}
	if !strings.Contains(err.Error(), "resolve workspace") {
		t.Errorf("error %q should mention the workspace lookup", err.Error())
	}
}

func TestStoreInTenant_BeginError(t *testing.T) {
	store := &PostgresStore{pool: &fakePool{beginErr: errors.New("pool exhausted")}}

	err := store.inTenant(context.Background(), uuid.New(), func(pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("expected Begin to fail")
	}
	if !strings.Contains(err.Error(), "begin transaction") {
		t.Errorf("error %q should mention begin transaction", err.Error())
	}
}

func TestStoreInTenant_SetConfigError(t *testing.T) {
	tx := &fakeTx{setConfigErr: errors.New("permission denied")}
	store := &PostgresStore{pool: &fakePool{tx: tx}}

	err := store.inTenant(context.Background(), uuid.New(), func(pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("expected set_config to fail")
	}
	if !strings.Contains(err.Error(), "set tenant context") {
		t.Errorf("error %q should mention the tenant context", err.Error())
	}
}

func TestStoreInTenant_CommitError(t *testing.T) {
	store := &PostgresStore{pool: &fakePool{tx: &commitFailTx{fakeTx: fakeTx{}}}}

	err := store.inTenant(context.Background(), uuid.New(), func(pgx.Tx) error { return nil })
	if err == nil {
		t.Fatal("expected Commit to fail")
	}
	if !strings.Contains(err.Error(), "commit transaction") {
		t.Errorf("error %q should mention commit transaction", err.Error())
	}
}

// commitFailTx fails only on Commit so the commit wrapper is covered.
type commitFailTx struct{ fakeTx }

func (c *commitFailTx) Commit(context.Context) error { return errors.New("connection lost") }
