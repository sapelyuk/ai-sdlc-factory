package gates

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInMemoryStoreCRUD(t *testing.T) {
	store := NewInMemoryStore()
	ws := uuid.New()
	id := uuid.New()
	req := ApprovalRequest{
		ID:          id,
		WorkspaceID: ws,
		GateName:    "prod_approved",
		Status:      ApprovalPending,
		RequestedBy: uuid.New(),
		RiskLevel:   "R3",
		CreatedAt:   time.Now().UTC(),
	}

	// Create.
	if err := store.Create(context.Background(), req); err != nil {
		t.Fatal(err)
	}

	// Get.
	got, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.GateName != "prod_approved" {
		t.Errorf("got gate=%q", got.GateName)
	}

	// List.
	all, err := store.List(context.Background(), ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1, got %d", len(all))
	}

	// List filtered.
	pending := ApprovalPending
	filtered, err := store.List(context.Background(), ws, &pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(filtered))
	}

	// List for other workspace.
	other, err := store.List(context.Background(), uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("expected 0 for other workspace, got %d", len(other))
	}

	// Decide.
	if err := store.Decide(context.Background(), id, true, uuid.New(), "ok"); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(context.Background(), id)
	if got.Status != ApprovalApproved {
		t.Errorf("expected approved, got %s", got.Status)
	}

	// Decide on non-pending.
	err = store.Decide(context.Background(), id, false, uuid.New(), "again")
	if err != ErrNotPending {
		t.Errorf("expected ErrNotPending, got %v", err)
	}
}

func TestInMemoryStoreExpire(t *testing.T) {
	store := NewInMemoryStore()
	id := uuid.New()
	req := ApprovalRequest{
		ID:          id,
		WorkspaceID: uuid.New(),
		GateName:    "prod_approved",
		Status:      ApprovalPending,
	}
	_ = store.Create(context.Background(), req)

	if err := store.Expire(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), id)
	if got.Status != ApprovalExpired {
		t.Errorf("expected expired, got %s", got.Status)
	}

	// Expire a decided request: it stays decided.
	_ = store.Create(context.Background(), ApprovalRequest{ID: uuid.New(), WorkspaceID: uuid.New(), GateName: "x", Status: ApprovalPending})
	if err := store.Expire(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(context.Background(), id)
	if got.Status != ApprovalExpired {
		t.Errorf("expected still expired, got %s", got.Status)
	}
}

func TestInMemoryStoreNotFound(t *testing.T) {
	store := NewInMemoryStore()
	_, err := store.Get(context.Background(), uuid.New())
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := store.Expire(context.Background(), uuid.New()); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if err := store.Decide(context.Background(), uuid.New(), true, uuid.New(), ""); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestHumanApprovalGateStates(t *testing.T) {
	store := NewInMemoryStore()
	ws := uuid.New()
	id := uuid.New()

	// Pending.
	_ = store.Create(context.Background(), ApprovalRequest{
		ID: id, WorkspaceID: ws, GateName: "prod_approved", Status: ApprovalPending,
	})
	g := HumanApprovalGate{Name: "prod_approved", Store: store}
	facts := Facts{EvidenceApprovalID: id.String()}

	res, err := g.Evaluate(context.Background(), facts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPending() {
		t.Errorf("expected pending, got %+v", res)
	}

	// Approved.
	_ = store.Decide(context.Background(), id, true, uuid.New(), "ok")
	res, _ = g.Evaluate(context.Background(), facts)
	if !res.IsPassed() {
		t.Errorf("expected passed, got %+v", res)
	}

	// Rejected (new request).
	id2 := uuid.New()
	_ = store.Create(context.Background(), ApprovalRequest{
		ID: id2, WorkspaceID: ws, GateName: "prod_approved", Status: ApprovalPending,
	})
	_ = store.Decide(context.Background(), id2, false, uuid.New(), "no")
	res, _ = g.Evaluate(context.Background(), Facts{EvidenceApprovalID: id2.String()})
	if !res.Rejected {
		t.Errorf("expected rejected, got %+v", res)
	}
	if res.Reason != "no" {
		t.Errorf("expected reason 'no', got %q", res.Reason)
	}

	// Expired.
	id3 := uuid.New()
	_ = store.Create(context.Background(), ApprovalRequest{
		ID: id3, WorkspaceID: ws, GateName: "prod_approved", Status: ApprovalPending,
	})
	_ = store.Expire(context.Background(), id3)
	res, _ = g.Evaluate(context.Background(), Facts{EvidenceApprovalID: id3.String()})
	if !res.TimeoutExpired {
		t.Errorf("expected timeout, got %+v", res)
	}
}

func TestHumanApprovalGateDeadline(t *testing.T) {
	store := NewInMemoryStore()
	id := uuid.New()
	past := time.Now().UTC().Add(-1 * time.Hour)
	_ = store.Create(context.Background(), ApprovalRequest{
		ID:          id,
		WorkspaceID: uuid.New(),
		GateName:    "prod_approved",
		Status:      ApprovalPending,
		DeadlineAt:  &past,
	})
	g := HumanApprovalGate{Name: "prod_approved", Store: store}
	res, err := g.Evaluate(context.Background(), Facts{EvidenceApprovalID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimeoutExpired {
		t.Errorf("expected timeout for past deadline, got %+v", res)
	}
}

func TestHumanApprovalGateInvalidID(t *testing.T) {
	store := NewInMemoryStore()
	g := HumanApprovalGate{Name: "prod_approved", Store: store}
	_, err := g.Evaluate(context.Background(), Facts{EvidenceApprovalID: "not-a-uuid"})
	if err == nil {
		t.Error("expected error for invalid UUID")
	}
	// Missing approval ID.
	_, err = g.Evaluate(context.Background(), Facts{})
	if err == nil {
		t.Error("expected error for missing approval ID")
	}
}

func TestHumanApprovalGateMissingRequest(t *testing.T) {
	store := NewInMemoryStore()
	g := HumanApprovalGate{Name: "prod_approved", Store: store}
	_, err := g.Evaluate(context.Background(), Facts{EvidenceApprovalID: uuid.New().String()})
	if err == nil {
		t.Error("expected error for missing request")
	}
}

func TestBuildApprovalRequest(t *testing.T) {
	ws := uuid.New()
	r := uuid.New()
	req := BuildApprovalRequest(ws, "prod_approved", r, 0, map[string]any{"k": "v"})
	if req.Status != ApprovalPending {
		t.Errorf("expected pending, got %s", req.Status)
	}
	if req.RiskLevel != "R3" {
		t.Errorf("expected R3, got %s", req.RiskLevel)
	}
	if req.DeadlineAt == nil {
		t.Fatal("deadline is nil")
	}
	// Default timeout is 24 hours.
	if d := time.Until(*req.DeadlineAt); d < 23*time.Hour || d > 24*time.Hour {
		t.Errorf("expected ~24h deadline, got %v", d)
	}
	if req.Context["k"] != "v" {
		t.Errorf("context not carried: %v", req.Context)
	}

	// Custom timeout.
	req = BuildApprovalRequest(ws, "x", r, 2*time.Hour, nil)
	if d := time.Until(*req.DeadlineAt); d < 1*time.Hour || d > 3*time.Hour {
		t.Errorf("expected ~2h deadline, got %v", d)
	}
}
