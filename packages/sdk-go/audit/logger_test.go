package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

var testWorkspace = uuid.MustParse("11111111-1111-1111-1111-111111111111")

func testEvent(action string) Event {
	return Event{
		WorkspaceID: testWorkspace,
		ActorID:     AgentActor(uuid.MustParse("22222222-2222-2222-2222-222222222222")),
		Action:      action,
		ResourceID:  "run-1",
		Details:     map[string]any{"key": "value"},
	}
}

func fixedClock(t *testing.T) (*Logger, *MemoryStore) {
	t.Helper()
	store := &MemoryStore{}
	logger := NewLoggerWithStore(store)
	base := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	counter := 0
	logger.now = func() time.Time {
		counter++
		return base.Add(time.Duration(counter) * time.Second)
	}
	return logger, store
}

// TestChainLinksTwoRecords is the core issue #14 requirement: the second
// record must chain from the hash of the first one.
func TestChainLinksTwoRecords(t *testing.T) {
	logger, store := fixedClock(t)
	ctx := context.Background()

	first, err := logger.Append(ctx, testEvent(ActionToolCall))
	if err != nil {
		t.Fatalf("Append first: %v", err)
	}
	second, err := logger.Append(ctx, testEvent(ActionLLMCall))
	if err != nil {
		t.Fatalf("Append second: %v", err)
	}

	if first.PrevHash != nil {
		t.Fatal("first record must start the chain (prev_hash IS NULL)")
	}
	if second.PrevHash == nil || *second.PrevHash != first.Hash {
		t.Fatalf("second.prev_hash = %v, want %s", second.PrevHash, first.Hash)
	}
	if first.Hash == second.Hash {
		t.Fatal("chained records must not share a hash")
	}
	if want := Hash(&first.Hash, second); second.Hash != want {
		t.Fatalf("second hash must be recomputable: want %s, got %s", want, second.Hash)
	}
	if err := VerifyChain(store.Records(testWorkspace)); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if err := logger.Verify(ctx, testWorkspace); err != nil {
		t.Fatalf("Logger.Verify: %v", err)
	}
}

func TestIdempotencyKeyReturnsExistingRecord(t *testing.T) {
	logger, store := fixedClock(t)
	ctx := context.Background()
	ev := testEvent(ActionRunStarted)
	ev.IdempotencyKey = "run-started-1"

	first, err := logger.Append(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	ev.Details = map[string]any{"different": "payload"}
	retry, err := logger.Append(ctx, ev)
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != first.ID || retry.Hash != first.Hash {
		t.Fatalf("retry returned a different record: first=%+v retry=%+v", first, retry)
	}
	if store.Count() != 1 {
		t.Fatalf("append count = %d, want 1", store.Count())
	}
	if err := logger.Verify(ctx, testWorkspace); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestVerifyDetectsTamperedDetails proves tamper evidence: rewriting the
// details payload of a persisted record breaks the chain.
func TestVerifyDetectsTamperedDetails(t *testing.T) {
	logger, store := fixedClock(t)
	ctx := context.Background()

	for _, action := range []string{ActionRunStarted, ActionToolCall, ActionTaskCompleted} {
		if _, err := logger.Append(ctx, testEvent(action)); err != nil {
			t.Fatalf("Append %s: %v", action, err)
		}
	}

	records := store.Records(testWorkspace)
	if err := VerifyChain(records); err != nil {
		t.Fatalf("chain must verify before tampering: %v", err)
	}

	// Attacker edits the details of the middle record, keeping the stored hash.
	var payload map[string]any
	if err := json.Unmarshal(records[1].Details, &payload); err != nil {
		t.Fatal(err)
	}
	payload["key"] = "attacker-controlled"
	tampered, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	records[1].Details = tampered

	err = VerifyChain(records)
	if err == nil {
		t.Fatal("expected a broken chain after editing details")
	}
	if !strings.Contains(err.Error(), "record 1") {
		t.Fatalf("error must point at the tampered record, got %v", err)
	}

	// Deleting a record is detected as well: the successor's prev_hash no
	// longer matches the recomputed chain head.
	records = store.Records(testWorkspace)
	if err := VerifyChain(append(records[:1], records[2:]...)); err == nil {
		t.Fatal("expected a broken chain after deleting a record")
	}
}

// TestVerifyDetectsRehashedTampering covers the stronger attack: the attacker
// recomputes the hash of the edited record. The successor still fails because
// it chains from the original hash.
func TestVerifyDetectsRehashedTampering(t *testing.T) {
	logger, store := fixedClock(t)
	ctx := context.Background()

	first, err := logger.Append(ctx, testEvent(ActionRunStarted))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logger.Append(ctx, testEvent(ActionToolCall)); err != nil {
		t.Fatal(err)
	}

	records := store.Records(testWorkspace)
	records[0].Details = json.RawMessage(`{"key":"forged","z":1}`)
	records[0].Hash = Hash(nil, records[0])

	err = VerifyChain(records)
	if err == nil {
		t.Fatal("expected a broken chain when a successor still points at the original hash")
	}
	if !strings.Contains(err.Error(), "record 1") {
		t.Fatalf("error must point at the successor of the forged record, got %v", err)
	}
	_ = first
}

func TestVerifyRejectsChainHeadWithPrevHash(t *testing.T) {
	logger, store := fixedClock(t)
	if _, err := logger.Append(context.Background(), testEvent(ActionToolCall)); err != nil {
		t.Fatal(err)
	}
	records := store.Records(testWorkspace)
	bogus := "0000000000000000000000000000000000000000000000000000000000000000"
	records[0].PrevHash = &bogus
	if err := VerifyChain(records); err == nil {
		t.Fatal("expected an error when the first record claims a predecessor")
	}
}

func TestVerifyEmptyChainIsOK(t *testing.T) {
	if err := VerifyChain(nil); err != nil {
		t.Fatalf("VerifyChain(nil) = %v, want nil", err)
	}
}

func TestValidateRejectsIncompleteEvents(t *testing.T) {
	cases := []struct {
		name  string
		event Event
		want  error
	}{
		{"no workspace", Event{ActorID: "agent:x", Action: ActionToolCall}, ErrMissingWorkspace},
		{"no actor", Event{WorkspaceID: testWorkspace, Action: ActionToolCall}, ErrMissingActor},
		{"no action", Event{WorkspaceID: testWorkspace, ActorID: "agent:x"}, ErrMissingAction},
	}
	logger := NewLoggerWithStore(&MemoryStore{})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := logger.Log(context.Background(), tc.event)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Log error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestConcurrentAppendsKeepChainLinear is the in-process analogue of the
// FOR UPDATE guarantee: 50 writers must produce one linear, verifiable chain.
func TestConcurrentAppendsKeepChainLinear(t *testing.T) {
	logger, store := fixedClock(t)

	const writers = 50
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			if _, err := logger.Append(context.Background(), testEvent(ActionToolCall)); err != nil {
				t.Errorf("Append: %v", err)
			}
		}()
	}
	wg.Wait()

	records := store.Records(testWorkspace)
	if len(records) != writers {
		t.Fatalf("records = %d, want %d", len(records), writers)
	}
	if err := VerifyChain(records); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

// TestWorkspacesHaveSeparateChains guards against cross-tenant chaining,
// which would leak the existence of another workspace's activity.
func TestWorkspacesHaveSeparateChains(t *testing.T) {
	logger, store := fixedClock(t)
	other := uuid.MustParse("99999999-9999-9999-9999-999999999999")

	evA := testEvent(ActionToolCall)
	evB := testEvent(ActionToolCall)
	evB.WorkspaceID = other

	if _, err := logger.Append(context.Background(), evA); err != nil {
		t.Fatal(err)
	}
	firstB, err := logger.Append(context.Background(), evB)
	if err != nil {
		t.Fatal(err)
	}
	if firstB.PrevHash != nil {
		t.Fatal("a new workspace must start its own chain")
	}
	if err := logger.Verify(context.Background(), other); err != nil {
		t.Fatalf("Verify other workspace: %v", err)
	}
	if got := len(store.Records(testWorkspace)); got != 1 {
		t.Fatalf("workspace A records = %d, want 1", got)
	}
}

func TestStoreFailureIsPropagated(t *testing.T) {
	store := &MemoryStore{}
	store.FailWith(errors.New("disk full"))
	logger := NewLoggerWithStore(store)

	if err := logger.Log(context.Background(), testEvent(ActionToolCall)); err == nil {
		t.Fatal("expected the store error to be propagated")
	}
	if err := logger.Verify(context.Background(), testWorkspace); err == nil {
		t.Fatal("expected Verify to surface the query error")
	}
}

func TestNopAuditorDiscards(t *testing.T) {
	if err := Nop().Log(context.Background(), testEvent(ActionToolCall)); err != nil {
		t.Fatalf("Nop must never fail: %v", err)
	}
}

func TestActorHelpers(t *testing.T) {
	id := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if got := AgentActor(id); got != "agent:22222222-2222-2222-2222-222222222222" {
		t.Fatalf("AgentActor = %q", got)
	}
	if got := SystemActor("agent-runtime"); got != "system:agent-runtime" {
		t.Fatalf("SystemActor = %q", got)
	}
}

// TestHashIsSensitiveToFieldBoundaries proves the zero-byte separators: moving
// bytes between two adjacent fields must change the digest.
func TestHashIsSensitiveToFieldBoundaries(t *testing.T) {
	base := Record{
		ID:          uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"),
		WorkspaceID: testWorkspace,
		ActorID:     "agent",
		Action:      "x",
		ResourceID:  "run",
		Details:     json.RawMessage(`{}`),
		CreatedAt:   time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC),
	}
	shifted := base
	shifted.ActorID = "agentx"
	shifted.Action = ""

	if Hash(nil, base) == Hash(nil, shifted) {
		t.Fatal("hash must depend on field boundaries, not only on the concatenated bytes")
	}
}

func TestCanonicalJSONIsStable(t *testing.T) {
	a, err := CanonicalJSON(map[string]any{"b": 1, "a": 2})
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalJSON(map[string]any{"a": 2, "b": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("canonical JSON must be key-sorted: %s vs %s", a, b)
	}
	empty, err := CanonicalJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != "{}" {
		t.Fatalf("nil details must encode as an empty object, got %s", empty)
	}
}

func TestDetailsMapRoundTrip(t *testing.T) {
	rec := Record{Details: json.RawMessage(`{"result":"denied"}`)}
	if got := rec.DetailsMap()["result"]; got != "denied" {
		t.Fatalf("DetailsMap = %v", got)
	}
	if got := (Record{}).DetailsMap(); len(got) != 0 {
		t.Fatalf("empty details must decode to an empty map, got %v", got)
	}
	if got := (Record{Details: json.RawMessage(`"broken"`)}).DetailsMap(); len(got) != 0 {
		t.Fatalf("invalid JSON must decode to an empty map, got %v", got)
	}
}

func TestTimestampMatchesDatabasePrecision(t *testing.T) {
	logger, store := fixedClock(t)
	if _, err := logger.Append(context.Background(), testEvent(ActionToolCall)); err != nil {
		t.Fatal(err)
	}
	rec := store.Records(testWorkspace)[0]
	if rec.CreatedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("created_at must be truncated to microseconds, got %d ns", rec.CreatedAt.Nanosecond())
	}
}
