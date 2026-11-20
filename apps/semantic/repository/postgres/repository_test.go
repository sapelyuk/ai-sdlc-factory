package repository

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

type tenantPoolStub struct {
	tx        *tenantTxStub
	beginErr  error
	beginCall int
}

func (p *tenantPoolStub) Begin(context.Context) (pgx.Tx, error) {
	p.beginCall++
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

type tenantTxStub struct {
	pgx.Tx
	execErr      error
	execErrAt    int
	execTags     []pgconn.CommandTag
	execArgs     [][]any
	execCalls    int
	queryErr     error
	rows         pgx.Rows
	queryRows    []pgx.Row
	queryRowCall int
	querySQL     []string
	queryArgs    [][]any
	commitErr    error
	rollbackCnt  int
}

func (tx *tenantTxStub) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	tx.execCalls++
	tx.execArgs = append(tx.execArgs, args)
	if tx.execErr != nil && (tx.execErrAt == 0 || tx.execErrAt == tx.execCalls) {
		return pgconn.CommandTag{}, tx.execErr
	}
	if tx.execCalls <= len(tx.execTags) {
		return tx.execTags[tx.execCalls-1], nil
	}
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (tx *tenantTxStub) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	tx.querySQL = append(tx.querySQL, query)
	tx.queryArgs = append(tx.queryArgs, args)
	if tx.queryRowCall >= len(tx.queryRows) {
		return scanRowStub{err: errors.New("unexpected QueryRow")}
	}
	row := tx.queryRows[tx.queryRowCall]
	tx.queryRowCall++
	return row
}

func (tx *tenantTxStub) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return tx.rows, tx.queryErr
}

func (tx *tenantTxStub) Commit(context.Context) error {
	return tx.commitErr
}

func (tx *tenantTxStub) Rollback(context.Context) error {
	tx.rollbackCnt++
	return nil
}

type scanRowStub struct {
	values []any
	err    error
}

func (r scanRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("Scan received %d destinations, want %d", len(dest), len(r.values))
	}
	for i := range dest {
		target := reflect.ValueOf(dest[i]).Elem()
		value := reflect.ValueOf(r.values[i])
		if !value.Type().AssignableTo(target.Type()) {
			return fmt.Errorf("cannot scan %s into %s", value.Type(), target.Type())
		}
		target.Set(value)
	}
	return nil
}

type rowsStub struct {
	pgx.Rows
	rows  [][]any
	index int
	err   error
}

func (r *rowsStub) Next() bool {
	r.index++
	return r.index < len(r.rows)
}

func (r *rowsStub) Scan(dest ...any) error {
	if r.index < 0 || r.index >= len(r.rows) {
		return errors.New("Scan called without current row")
	}
	return scanRowStub{values: r.rows[r.index]}.Scan(dest...)
}

func (r *rowsStub) Err() error {
	return r.err
}

func (r *rowsStub) Close() {}

func nodeRowValues(id, workspaceID uuid.UUID, status string) []any {
	now := time.Now().UTC()
	return []any{id, workspaceID, "SLP", "Role", "A role", map[string]any{"active": true}, 1, status, now, now}
}

func rowsWithRows(rows ...[]any) *rowsStub {
	return &rowsStub{rows: rows, index: -1}
}

func TestNodeRepositoryCreate(t *testing.T) {
	workspaceID, nodeID := uuid.New(), uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())

	t.Run("inserts and assigns generated ID", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{values: []any{nodeID}}}}
		repo := newNodeRepository(&tenantPoolStub{tx: tx})
		node := &domain.SemNode{WorkspaceID: workspaceID, Layer: "SLP", Kind: "Role", Label: "A role", Version: 1}
		err := repo.Create(ctx, node)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if tx.queryRowCall != 1 || node.ID != nodeID {
			t.Fatalf("insert calls = %d, node ID = %s; want 1, %s", tx.queryRowCall, node.ID, nodeID)
		}
		if len(tx.queryArgs[0]) != 7 || tx.queryArgs[0][6] != "draft" {
			t.Fatalf("insert args = %#v; want default draft status", tx.queryArgs[0])
		}
	})

	t.Run("rejects workspace mismatch", func(t *testing.T) {
		tx := &tenantTxStub{}
		err := newNodeRepository(&tenantPoolStub{tx: tx}).Create(ctx, &domain.SemNode{WorkspaceID: uuid.New()})
		if err == nil || !strings.Contains(err.Error(), "workspace does not match") {
			t.Fatalf("Create() error = %v, want workspace mismatch", err)
		}
	})

	t.Run("returns insert failure", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{err: errors.New("insert failed")}}}
		err := newNodeRepository(&tenantPoolStub{tx: tx}).Create(ctx, &domain.SemNode{WorkspaceID: workspaceID})
		if err == nil || !strings.Contains(err.Error(), "create node") {
			t.Fatalf("Create() error = %v, want wrapped insert error", err)
		}
	})
}

func TestNodeRepositoryGetByID(t *testing.T) {
	workspaceID, nodeID := uuid.New(), uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())

	t.Run("returns scanned node", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{values: nodeRowValues(nodeID, workspaceID, "draft")}}}
		got, err := newNodeRepository(&tenantPoolStub{tx: tx}).GetByID(ctx, nodeID)
		if err != nil || got == nil || got.ID != nodeID {
			t.Fatalf("GetByID() = %#v, %v", got, err)
		}
	})

	for _, tt := range []struct {
		name string
		err  error
		want error
	}{
		{name: "not found", err: pgx.ErrNoRows, want: domain.ErrNotFound},
		{name: "database error", err: errors.New("query failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{err: tt.err}}}
			_, err := newNodeRepository(&tenantPoolStub{tx: tx}).GetByID(ctx, nodeID)
			if err == nil {
				t.Fatal("GetByID() error = nil")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("GetByID() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNodeRepositoryList(t *testing.T) {
	workspaceID, nodeID := uuid.New(), uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())

	for _, tt := range []struct {
		name  string
		layer string
		root  string
	}{
		{name: "all nodes"},
		{name: "layer filter", layer: "SLP"},
		{name: "subgraph with layer", layer: "SLP", root: nodeID.String()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &tenantTxStub{rows: rowsWithRows(nodeRowValues(nodeID, workspaceID, "draft"))}
			got, err := newNodeRepository(&tenantPoolStub{tx: tx}).List(ctx, tt.layer, tt.root, 2)
			if err != nil || len(got) != 1 || got[0].ID != nodeID {
				t.Fatalf("List() = %#v, %v", got, err)
			}
		})
	}

	t.Run("invalid root", func(t *testing.T) {
		_, err := newNodeRepository(&tenantPoolStub{tx: &tenantTxStub{}}).List(ctx, "", "invalid", 1)
		if !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("List(invalid root) error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		tx := &tenantTxStub{queryErr: errors.New("query failed")}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).List(ctx, "", "", 0)
		if err == nil || !strings.Contains(err.Error(), "list nodes") {
			t.Fatalf("List() error = %v, want wrapped query error", err)
		}
	})

	t.Run("scan error", func(t *testing.T) {
		tx := &tenantTxStub{rows: rowsWithRows([]any{"wrong"})}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).List(ctx, "", "", 0)
		if err == nil || !strings.Contains(err.Error(), "scan node") {
			t.Fatalf("List() error = %v, want wrapped scan error", err)
		}
	})

	t.Run("row iteration error", func(t *testing.T) {
		tx := &tenantTxStub{rows: &rowsStub{index: -1, err: errors.New("iterate failed")}}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).List(ctx, "", "", 0)
		if err == nil || !strings.Contains(err.Error(), "iterate nodes") {
			t.Fatalf("List() error = %v, want wrapped iteration error", err)
		}
	})
}

func TestNodeRepositoryUpdate(t *testing.T) {
	workspaceID, nodeID := uuid.New(), uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())

	t.Run("updates supplied fields", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{
			scanRowStub{values: []any{"draft"}},
			scanRowStub{values: nodeRowValues(nodeID, workspaceID, "draft")},
		}}
		got, err := newNodeRepository(&tenantPoolStub{tx: tx}).Update(ctx, nodeID,
			domain.UpdateNodeInput{Kind: "NewRole", Label: "New label", Props: map[string]any{"x": true}})
		if err != nil || got == nil || got.ID != nodeID {
			t.Fatalf("Update() = %#v, %v", got, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{err: pgx.ErrNoRows}}}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).Update(ctx, nodeID, domain.UpdateNodeInput{})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Update() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("status query failure", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{err: errors.New("query failed")}}}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).Update(ctx, nodeID, domain.UpdateNodeInput{})
		if err == nil || !strings.Contains(err.Error(), "check node status") {
			t.Fatalf("Update() error = %v, want wrapped status query error", err)
		}
	})

	t.Run("immutable status", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{scanRowStub{values: []any{"active"}}}}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).Update(ctx, nodeID, domain.UpdateNodeInput{Kind: "X"})
		if !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("Update() error = %v, want ErrForbidden", err)
		}
	})

	t.Run("update statement failure", func(t *testing.T) {
		tx := &tenantTxStub{execErr: errors.New("update failed"), execErrAt: 2, queryRows: []pgx.Row{scanRowStub{values: []any{"draft"}}}}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).Update(ctx, nodeID, domain.UpdateNodeInput{Kind: "X"})
		if err == nil || !strings.Contains(err.Error(), "update node") {
			t.Fatalf("Update() error = %v, want wrapped update error", err)
		}
	})

	t.Run("updated row scan failure", func(t *testing.T) {
		tx := &tenantTxStub{queryRows: []pgx.Row{
			scanRowStub{values: []any{"draft"}},
			scanRowStub{err: errors.New("scan failed")},
		}}
		_, err := newNodeRepository(&tenantPoolStub{tx: tx}).Update(ctx, nodeID, domain.UpdateNodeInput{})
		if err == nil || !strings.Contains(err.Error(), "scan failed") {
			t.Fatalf("Update() error = %v, want scan error", err)
		}
	})
}

func TestEdgeRepositoryCreateAndDelete(t *testing.T) {
	workspaceID, edgeID := uuid.New(), uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())
	input := domain.CreateEdgeInput{SrcID: uuid.New().String(), DstID: uuid.New().String(), Kind: "relates-to"}

	for _, tt := range []struct {
		name  string
		input domain.CreateEdgeInput
	}{
		{name: "invalid source ID", input: domain.CreateEdgeInput{SrcID: "bad", DstID: input.DstID, Kind: input.Kind}},
		{name: "invalid destination ID", input: domain.CreateEdgeInput{SrcID: input.SrcID, DstID: "bad", Kind: input.Kind}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := newEdgeRepository(&tenantPoolStub{tx: &tenantTxStub{}}).Create(ctx, tt.input); err == nil {
				t.Fatal("Create() error = nil, want UUID parse error")
			}
		})
	}

	t.Run("creates edge", func(t *testing.T) {
		tx := &tenantTxStub{}
		err := newEdgeRepository(&tenantPoolStub{tx: tx}).Create(ctx, input)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	})

	t.Run("insert error", func(t *testing.T) {
		tx := &tenantTxStub{execErr: errors.New("insert failed"), execErrAt: 2}
		err := newEdgeRepository(&tenantPoolStub{tx: tx}).Create(ctx, input)
		if err == nil || !strings.Contains(err.Error(), "create edge") {
			t.Fatalf("Create() error = %v, want wrapped insert error", err)
		}
	})

	t.Run("delete error", func(t *testing.T) {
		tx := &tenantTxStub{execErr: errors.New("delete failed"), execErrAt: 2}
		err := newEdgeRepository(&tenantPoolStub{tx: tx}).Delete(ctx, edgeID)
		if err == nil || !strings.Contains(err.Error(), "delete edge") {
			t.Fatalf("Delete() error = %v, want wrapped delete error", err)
		}
	})

	t.Run("delete missing edge", func(t *testing.T) {
		tx := &tenantTxStub{execTags: []pgconn.CommandTag{
			pgconn.NewCommandTag("SELECT 1"), pgconn.NewCommandTag("DELETE 0"),
		}}
		err := newEdgeRepository(&tenantPoolStub{tx: tx}).Delete(ctx, edgeID)
		if !errors.Is(err, domain.ErrEdgeNotFound) {
			t.Fatalf("Delete() error = %v, want ErrEdgeNotFound", err)
		}
	})

	t.Run("deletes edge", func(t *testing.T) {
		tx := &tenantTxStub{execTags: []pgconn.CommandTag{
			pgconn.NewCommandTag("SELECT 1"), pgconn.NewCommandTag("DELETE 1"),
		}}
		err := newEdgeRepository(&tenantPoolStub{tx: tx}).Delete(ctx, edgeID)
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
	})
}

func TestTenantTransaction(t *testing.T) {
	workspaceID := uuid.New()
	ctx := identity.WithTenant(context.Background(), workspaceID, uuid.New())

	t.Run("requires tenant context before beginning", func(t *testing.T) {
		pool := &tenantPoolStub{tx: &tenantTxStub{}}
		err := inTenantTx(context.Background(), pool, func(pgx.Tx, uuid.UUID) error { return nil })
		if !errors.Is(err, identity.ErrMissingTenantContext) || pool.beginCall != 0 {
			t.Fatalf("inTenantTx() = %v with %d begins; want missing context and no begin", err, pool.beginCall)
		}
	})

	t.Run("begin failure", func(t *testing.T) {
		pool := &tenantPoolStub{beginErr: errors.New("begin failed")}
		err := inTenantTx(ctx, pool, func(pgx.Tx, uuid.UUID) error { return nil })
		if err == nil || pool.beginCall != 1 {
			t.Fatalf("inTenantTx() = %v, begins %d; want wrapped begin error and one begin", err, pool.beginCall)
		}
	})

	t.Run("tenant context setup failure", func(t *testing.T) {
		tx := &tenantTxStub{execErr: errors.New("set failed")}
		err := inTenantTx(ctx, &tenantPoolStub{tx: tx}, func(pgx.Tx, uuid.UUID) error { return nil })
		if err == nil || !contains(err.Error(), "set tenant context") {
			t.Fatalf("inTenantTx() error = %v, want wrapped set-context error", err)
		}
		if tx.rollbackCnt != 1 {
			t.Errorf("rollback calls = %d, want 1", tx.rollbackCnt)
		}
	})

	t.Run("callback failure", func(t *testing.T) {
		tx := &tenantTxStub{}
		wantErr := errors.New("operation failed")
		err := inTenantTx(ctx, &tenantPoolStub{tx: tx}, func(_ pgx.Tx, gotWorkspaceID uuid.UUID) error {
			if gotWorkspaceID != workspaceID {
				t.Errorf("callback workspace = %s, want %s", gotWorkspaceID, workspaceID)
			}
			return wantErr
		})
		if !errors.Is(err, wantErr) || tx.rollbackCnt != 1 {
			t.Fatalf("inTenantTx() = %v, rollbacks %d; want callback error and rollback", err, tx.rollbackCnt)
		}
	})

	t.Run("commit failure", func(t *testing.T) {
		tx := &tenantTxStub{commitErr: errors.New("commit failed")}
		err := inTenantTx(ctx, &tenantPoolStub{tx: tx}, func(pgx.Tx, uuid.UUID) error { return nil })
		if err == nil || !contains(err.Error(), "commit transaction") {
			t.Fatalf("inTenantTx() error = %v, want wrapped commit error", err)
		}
	})

	t.Run("commits success", func(t *testing.T) {
		tx := &tenantTxStub{}
		called := false
		if err := inTenantTx(ctx, &tenantPoolStub{tx: tx}, func(_ pgx.Tx, gotWorkspaceID uuid.UUID) error {
			called = true
			if gotWorkspaceID != workspaceID {
				t.Errorf("callback workspace = %s, want %s", gotWorkspaceID, workspaceID)
			}
			return nil
		}); err != nil {
			t.Fatalf("inTenantTx() error = %v", err)
		}
		if !called || tx.rollbackCnt != 1 {
			t.Fatalf("callback called = %v, deferred rollback calls = %d; want true, 1", called, tx.rollbackCnt)
		}
	})
}

func TestRepositoryErrorPredicates(t *testing.T) {
	tests := []struct {
		name string
		err  error
		fn   func(error) bool
		want bool
	}{
		{name: "node not found", err: domain.ErrNotFound, fn: IsNotFound, want: true},
		{name: "edge not found", err: fmt.Errorf("wrapped: %w", domain.ErrEdgeNotFound), fn: IsNotFound, want: true},
		{name: "forbidden", err: domain.ErrForbidden, fn: IsForbidden, want: true},
		{name: "conflict", err: domain.ErrConflict, fn: IsConflict, want: true},
		{name: "unrelated", err: errors.New("other"), fn: IsNotFound, want: false},
		{name: "nil", err: nil, fn: IsForbidden, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.fn(tt.err); got != tt.want {
				t.Errorf("predicate(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestScanNode(t *testing.T) {
	id, workspaceID := uuid.New(), uuid.New()
	now := time.Now().UTC()
	props := map[string]any{"enabled": true}
	row := scanRowStub{values: []any{id, workspaceID, "SLP", "Role", "A role", props, 2, "active", now, now}}
	got, err := scanNode(row)
	if err != nil {
		t.Fatalf("scanNode() error = %v", err)
	}
	if got.ID != id || got.WorkspaceID != workspaceID || got.Layer != "SLP" || got.Kind != "Role" ||
		got.Label != "A role" || got.Version != 2 || got.Status != "active" {
		t.Fatalf("scanNode() = %#v", got)
	}
	if got.CreatedAt != now || got.UpdatedAt != now {
		t.Errorf("scanNode() timestamps = %s, %s; want %s", got.CreatedAt, got.UpdatedAt, now)
	}

	wantErr := errors.New("scan failed")
	if _, err := scanNode(scanRowStub{err: wantErr}); !errors.Is(err, wantErr) {
		t.Errorf("scanNode(error) = %v, want %v", err, wantErr)
	}
}

func TestJoinStrings(t *testing.T) {
	for _, tt := range []struct {
		parts []string
		sep   string
		want  string
	}{
		{parts: nil, sep: ",", want: ""},
		{parts: []string{"only"}, sep: ",", want: "only"},
		{parts: []string{"a", "b", "c"}, sep: " / ", want: "a / b / c"},
	} {
		if got := joinStrings(tt.parts, tt.sep); got != tt.want {
			t.Errorf("joinStrings(%v, %q) = %q, want %q", tt.parts, tt.sep, got, tt.want)
		}
	}
}

func contains(s, part string) bool {
	return strings.Contains(s, part)
}
