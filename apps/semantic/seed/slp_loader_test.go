package seed

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type seedPoolStub struct {
	tx       *seedTxStub
	beginErr error
}

func (p *seedPoolStub) Begin(context.Context) (pgx.Tx, error) {
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	return p.tx, nil
}

type seedTxStub struct {
	pgx.Tx
	execErrAt   int
	execErr     error
	insertRows  []int64
	execCalls   int
	commitErr   error
	rollbackCnt int
}

func (tx *seedTxStub) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	tx.execCalls++
	if tx.execCalls == tx.execErrAt {
		return pgconn.CommandTag{}, tx.execErr
	}
	if tx.execCalls%2 == 0 && len(tx.insertRows) > 0 {
		rows := tx.insertRows[0]
		tx.insertRows = tx.insertRows[1:]
		if rows > 0 {
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		}
		return pgconn.NewCommandTag("INSERT 0 0"), nil
	}
	return pgconn.NewCommandTag("SELECT 1"), nil
}

func (tx *seedTxStub) Commit(context.Context) error {
	return tx.commitErr
}

func (tx *seedTxStub) Rollback(context.Context) error {
	tx.rollbackCnt++
	return nil
}

func TestLoadSLPv0ReadsConfiguredFile(t *testing.T) {
	t.Run("reports missing configured file", func(t *testing.T) {
		t.Setenv("SLP_PATH", filepath.Join(t.TempDir(), "missing.yaml"))
		if _, err := LoadSLPv0(context.Background(), nil, uuid.New()); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("LoadSLPv0() error = %v, want configured file not found", err)
		}
	})

	t.Run("reports malformed YAML", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "slp.yaml")
		if err := os.WriteFile(path, []byte("classes: ["), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SLP_PATH", path)
		if _, err := LoadSLPv0(context.Background(), nil, uuid.New()); err == nil || !strings.Contains(err.Error(), "parse SLP YAML") {
			t.Fatalf("LoadSLPv0() error = %v, want YAML parse error", err)
		}
	})

	t.Run("accepts valid document with no classes", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "slp.yaml")
		data := []byte("apiVersion: ai-sdlc-factory/v1\nkind: SLP\nclasses: []\n")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SLP_PATH", path)
		if count, err := LoadSLPv0(context.Background(), nil, uuid.New()); err != nil || count != 0 {
			t.Fatalf("LoadSLPv0() = %d, %v; want 0, nil", count, err)
		}
	})
}

func TestLoadSLPv0ValidatesDocument(t *testing.T) {
	for _, tt := range []struct {
		name string
		doc  SLPCoreDoc
	}{
		{name: "missing API version", doc: slpDoc()},
		{name: "wrong kind", doc: func() SLPCoreDoc { doc := validSLPDoc(); doc.Kind = "Other"; return doc }()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadSLPv0(context.Background(), nil, uuid.New(), tt.doc); err == nil {
				t.Fatal("loadSLPv0() error = nil, want document validation error")
			}
		})
	}
}

func TestLoadSLPv0SeedsClassesIdempotently(t *testing.T) {
	tx := &seedTxStub{insertRows: []int64{1, 0}}
	pool := &seedPoolStub{tx: tx}
	doc := validSLPDoc()
	doc.Classes = []SLPCoreClass{
		{Name: "Role", Description: "A role"},
		{Name: ""},
		{Name: "Role", Description: "A duplicate"},
	}

	count, err := loadSLPv0(context.Background(), pool, uuid.New(), doc)
	if err != nil || count != 1 {
		t.Fatalf("loadSLPv0() = %d, %v; want 1, nil", count, err)
	}
	if tx.execCalls != 4 || tx.rollbackCnt != 2 {
		t.Fatalf("transaction calls = %d exec, %d rollback; want 4, 2", tx.execCalls, tx.rollbackCnt)
	}
}

func TestLoadSLPv0ReturnsTransactionFailures(t *testing.T) {
	t.Run("begin failure", func(t *testing.T) {
		pool := &seedPoolStub{beginErr: errors.New("begin failed")}
		count, err := loadSLPv0(context.Background(), pool, uuid.New(), oneClassDoc())
		if count != 0 || err == nil || !strings.Contains(err.Error(), "begin seed transaction") {
			t.Fatalf("loadSLPv0() = %d, %v; want wrapped begin error", count, err)
		}
	})

	for _, tt := range []struct {
		name      string
		execErrAt int
		commitErr error
	}{
		{name: "set tenant context", execErrAt: 1},
		{name: "insert class", execErrAt: 2},
		{name: "commit", commitErr: errors.New("commit failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tx := &seedTxStub{execErrAt: tt.execErrAt, execErr: errors.New("exec failed"), commitErr: tt.commitErr}
			_, err := loadSLPv0(context.Background(), &seedPoolStub{tx: tx}, uuid.New(), oneClassDoc())
			if err == nil {
				t.Fatal("loadSLPv0() error = nil, want transaction error")
			}
			if tx.rollbackCnt != 1 {
				t.Errorf("rollback calls = %d, want 1", tx.rollbackCnt)
			}
		})
	}
}

func TestFindSLPFile(t *testing.T) {
	t.Run("configured file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "slp.yaml")
		if err := os.WriteFile(path, []byte("kind: SLP"), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SLP_PATH", path)
		got, err := findSLPFile()
		if err != nil || got != path {
			t.Fatalf("findSLPFile() = %q, %v; want %q, nil", got, err, path)
		}
	})

	t.Run("default repository file", func(t *testing.T) {
		t.Setenv("SLP_PATH", "")
		got, err := findSLPFile()
		if err != nil {
			t.Fatalf("findSLPFile() error = %v", err)
		}
		if filepath.Base(got) != "slp-core.yaml" {
			t.Fatalf("findSLPFile() = %q, want slp-core.yaml", got)
		}
	})
}

func slpDoc() SLPCoreDoc {
	return SLPCoreDoc{Kind: "SLP"}
}

func validSLPDoc() SLPCoreDoc {
	return SLPCoreDoc{APIVersion: "ai-sdlc-factory/v1", Kind: "SLP"}
}

func oneClassDoc() SLPCoreDoc {
	doc := validSLPDoc()
	doc.Classes = []SLPCoreClass{{Name: "Role"}}
	return doc
}
