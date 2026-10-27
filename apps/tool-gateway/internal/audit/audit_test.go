package audit

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func entry(tool, result string) Entry {
	return Entry{
		WorkspaceID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		AgentID:     uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		ToolName:    tool,
		RiskLevel:   "R1",
		Result:      result,
	}
}

func TestHashChain(t *testing.T) {
	store := &MemoryStore{}
	logger := NewLogger(store)

	first, err := logger.Record(context.Background(), entry("git.push", ResultSuccess))
	if err != nil {
		t.Fatal(err)
	}
	second, err := logger.Record(context.Background(), entry("fs.read", ResultDenied))
	if err != nil {
		t.Fatal(err)
	}

	if first.HashChain == second.HashChain {
		t.Fatal("hashes must differ")
	}
	if first.HashChain != Hash("", first) {
		t.Fatal("first record must chain from the empty hash")
	}
	if second.HashChain != Hash(first.HashChain, second) {
		t.Fatal("second record must chain from the first")
	}
	if err := Verify(store.Records()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	store := &MemoryStore{}
	logger := NewLogger(store)
	for _, tool := range []string{"git.push", "fs.read", "git.diff"} {
		if _, err := logger.Record(context.Background(), entry(tool, ResultSuccess)); err != nil {
			t.Fatal(err)
		}
	}

	records := store.Records()
	records[1].ToolName = "fs.delete"
	if err := Verify(records); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("expected a broken chain error, got %v", err)
	}

	records = store.Records()
	records = append(records[:1], records[2:]...)
	if err := Verify(records); err == nil {
		t.Fatal("expected a broken chain after deleting a record")
	}
}

func TestRecordValidation(t *testing.T) {
	logger := NewLogger(&MemoryStore{})

	tests := []struct {
		name  string
		entry Entry
		want  string
	}{
		{"no workspace", Entry{AgentID: uuid.New(), ToolName: "x", RiskLevel: "R0", Result: ResultSuccess}, "workspace"},
		{"no agent", Entry{WorkspaceID: uuid.New(), ToolName: "x", RiskLevel: "R0", Result: ResultSuccess}, "agent"},
		{"no tool", Entry{WorkspaceID: uuid.New(), AgentID: uuid.New(), RiskLevel: "R0", Result: ResultSuccess}, "tool"},
		{"bad risk", Entry{WorkspaceID: uuid.New(), AgentID: uuid.New(), ToolName: "x", RiskLevel: "R9", Result: ResultSuccess}, "risk"},
		{"bad result", Entry{WorkspaceID: uuid.New(), AgentID: uuid.New(), ToolName: "x", RiskLevel: "R0", Result: "ok"}, "result"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := logger.Record(context.Background(), tc.entry)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestConcurrentAppendsKeepChainLinear(t *testing.T) {
	store := &MemoryStore{}
	logger := NewLogger(store)

	const writers = 25
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			if _, err := logger.Record(context.Background(), entry("git.push", ResultSuccess)); err != nil {
				t.Errorf("Record: %v", err)
			}
		}()
	}
	wg.Wait()

	records := store.Records()
	if len(records) != writers {
		t.Fatalf("records = %d, want %d", len(records), writers)
	}
	if err := Verify(records); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestStoreErrorIsPropagated(t *testing.T) {
	logger := NewLogger(failingStore{})
	if _, err := logger.Record(context.Background(), entry("git.push", ResultSuccess)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestLoggerVerify(t *testing.T) {
	store := &MemoryStore{}
	logger := NewLogger(store)

	// Empty store should verify OK.
	if err := logger.Verify(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111")); err != nil {
		t.Fatalf("Verify empty: %v", err)
	}

	// Add some records.
	for _, tool := range []string{"git.push", "fs.read", "git.diff"} {
		if _, err := logger.Record(context.Background(), entry(tool, ResultSuccess)); err != nil {
			t.Fatal(err)
		}
	}

	// Verify should pass.
	ws := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if err := logger.Verify(context.Background(), ws); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestLoggerVerifyError(t *testing.T) {
	logger := NewLogger(failingStore{})
	err := logger.Verify(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"))
	if err == nil {
		t.Fatal("expected an error from Query")
	}
}

type failingStore struct{}

func (failingStore) Append(context.Context, uuid.UUID, func(string) (Record, error)) (Record, error) {
	return Record{}, errTest
}

func (failingStore) Query(context.Context, uuid.UUID) ([]Record, error) {
	return nil, errTest
}

var errTest = &testError{}

type testError struct{}

func (*testError) Error() string { return "store failed" }
