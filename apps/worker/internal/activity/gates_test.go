package activity

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
)

func TestGateCheckActivityAutoPass(t *testing.T) {
	svc := gates.NewService()
	a := NewGateCheckActivity(svc)
	out, err := a.Execute(context.Background(), GateCheckInput{
		WorkspaceID: uuid.New(),
		GateName:    gates.GateCIGreen,
		Facts: gates.Facts{
			gates.EvidenceCISuccess:           true,
			gates.EvidenceConventionalCommits: true,
			gates.EvidenceCoverage:            0.9,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Approved {
		t.Errorf("expected approved, got %+v", out)
	}
	if out.GateType != string(gates.GateTypeAuto) {
		t.Errorf("expected auto, got %s", out.GateType)
	}
}

func TestGateCheckActivityAutoBlocked(t *testing.T) {
	svc := gates.NewService()
	a := NewGateCheckActivity(svc)
	out, err := a.Execute(context.Background(), GateCheckInput{
		WorkspaceID: uuid.New(),
		GateName:    gates.GateCIGreen,
		Facts:       gates.Facts{gates.EvidenceCISuccess: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Approved {
		t.Error("expected not approved")
	}
	if out.Reason == "" {
		t.Error("expected a blocking reason")
	}
}

func TestGateCheckActivityHumanPending(t *testing.T) {
	svc := gates.NewService(gates.WithApprovalStore(gates.NewInMemoryStore()))
	a := NewGateCheckActivity(svc)
	out, err := a.Execute(context.Background(), GateCheckInput{
		WorkspaceID: uuid.New(),
		GateName:    gates.GateProdApproved,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Pending {
		t.Errorf("expected pending, got %+v", out)
	}
	if out.GateType != string(gates.GateTypeHumanApproval) {
		t.Errorf("expected human_approval, got %s", out.GateType)
	}
}

func TestGateCheckActivityHumanApproved(t *testing.T) {
	store := gates.NewInMemoryStore()
	id := uuid.New()
	_ = store.Create(context.Background(), gates.ApprovalRequest{
		ID: id, WorkspaceID: uuid.New(), GateName: gates.GateProdApproved,
		Status: gates.ApprovalApproved,
	})
	svc := gates.NewService(gates.WithApprovalStore(store))
	a := NewGateCheckActivity(svc)
	out, err := a.Execute(context.Background(), GateCheckInput{
		WorkspaceID: uuid.New(),
		GateName:    gates.GateProdApproved,
		Facts:       gates.Facts{gates.EvidenceApprovalID: id.String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Approved {
		t.Errorf("expected approved, got %+v", out)
	}
}

func TestGateCheckActivityUnknownGate(t *testing.T) {
	svc := gates.NewService()
	a := NewGateCheckActivity(svc)
	_, err := a.Execute(context.Background(), GateCheckInput{
		WorkspaceID: uuid.New(),
		GateName:    "no_such_gate",
	})
	if err == nil {
		t.Fatal("expected error for unknown gate")
	}
}

func TestApprovalCreateActivity(t *testing.T) {
	store := gates.NewInMemoryStore()
	a := NewApprovalCreateActivity(store)
	ws := uuid.New()
	out, err := a.Execute(context.Background(), ApprovalCreateInput{
		WorkspaceID: ws,
		GateName:    gates.GateProdApproved,
		RequestedBy: uuid.New(),
		RiskLevel:   "R3",
		Timeout:     2 * time.Hour,
		WorkflowID:  "wf-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ApprovalID == "" {
		t.Fatal("expected approval id")
	}
	got, err := store.Get(context.Background(), uuid.MustParse(out.ApprovalID))
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != gates.ApprovalPending {
		t.Errorf("expected pending, got %s", got.Status)
	}
	if got.WorkflowID != "wf-1" {
		t.Errorf("expected wf-1, got %s", got.WorkflowID)
	}
	if got.DeadlineAt == nil {
		t.Fatal("deadline is nil")
	}
	if d := time.Until(*got.DeadlineAt); d < time.Hour || d > 3*time.Hour {
		t.Errorf("expected ~2h deadline, got %v", d)
	}
}

func TestApprovalPollActivity(t *testing.T) {
	store := gates.NewInMemoryStore()
	id := uuid.New()
	_ = store.Create(context.Background(), gates.ApprovalRequest{
		ID: id, WorkspaceID: uuid.New(), GateName: "x", Status: gates.ApprovalPending,
	})
	a := NewApprovalPollActivity(store)

	out, err := a.Execute(context.Background(), ApprovalPollInput{ApprovalID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != string(gates.ApprovalPending) {
		t.Errorf("expected pending, got %s", out.Status)
	}

	_ = store.Decide(context.Background(), id, false, uuid.New(), "no way")
	out, _ = a.Execute(context.Background(), ApprovalPollInput{ApprovalID: id.String()})
	if out.Status != string(gates.ApprovalRejected) {
		t.Errorf("expected rejected, got %s", out.Status)
	}
	if out.Reason != "no way" {
		t.Errorf("expected reason 'no way', got %q", out.Reason)
	}
}

func TestApprovalPollActivityInvalidID(t *testing.T) {
	a := NewApprovalPollActivity(gates.NewInMemoryStore())
	_, err := a.Execute(context.Background(), ApprovalPollInput{ApprovalID: "not-a-uuid"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApprovalPollActivityMissing(t *testing.T) {
	a := NewApprovalPollActivity(gates.NewInMemoryStore())
	_, err := a.Execute(context.Background(), ApprovalPollInput{ApprovalID: uuid.New().String()})
	if err == nil {
		t.Fatal("expected error for missing request")
	}
}

func TestApprovalExpireActivity(t *testing.T) {
	store := gates.NewInMemoryStore()
	id := uuid.New()
	_ = store.Create(context.Background(), gates.ApprovalRequest{
		ID: id, WorkspaceID: uuid.New(), GateName: "x", Status: gates.ApprovalPending,
	})
	a := NewApprovalExpireActivity(store)
	if err := a.Execute(context.Background(), ApprovalExpireInput{ApprovalID: id.String()}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), id)
	if got.Status != gates.ApprovalExpired {
		t.Errorf("expected expired, got %s", got.Status)
	}
}

func TestApprovalExpireActivityInvalidID(t *testing.T) {
	a := NewApprovalExpireActivity(gates.NewInMemoryStore())
	err := a.Execute(context.Background(), ApprovalExpireInput{ApprovalID: "bad"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApprovalDecideActivity(t *testing.T) {
	store := gates.NewInMemoryStore()
	id := uuid.New()
	_ = store.Create(context.Background(), gates.ApprovalRequest{
		ID: id, WorkspaceID: uuid.New(), GateName: "x", Status: gates.ApprovalPending,
	})
	a := NewApprovalDecideActivity(store)
	err := a.Execute(context.Background(), ApprovalDecideInput{
		ApprovalID: id.String(), Approved: true, DecidedBy: uuid.New(), Comment: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(context.Background(), id)
	if got.Status != gates.ApprovalApproved {
		t.Errorf("expected approved, got %s", got.Status)
	}
	if got.Comment != "ok" {
		t.Errorf("expected comment 'ok', got %q", got.Comment)
	}
}

// A decision that arrived through another channel (the API) is not an error:
// the activity is idempotent.
func TestApprovalDecideActivityNotPending(t *testing.T) {
	store := gates.NewInMemoryStore()
	id := uuid.New()
	_ = store.Create(context.Background(), gates.ApprovalRequest{
		ID: id, WorkspaceID: uuid.New(), GateName: "x", Status: gates.ApprovalPending,
	})
	_ = store.Decide(context.Background(), id, true, uuid.New(), "")
	a := NewApprovalDecideActivity(store)
	err := a.Execute(context.Background(), ApprovalDecideInput{
		ApprovalID: id.String(), Approved: false, DecidedBy: uuid.New(),
	})
	if err != nil {
		t.Fatalf("expected idempotent success, got %v", err)
	}
}

func TestApprovalDecideActivityStoreError(t *testing.T) {
	a := NewApprovalDecideActivity(&failingStore{err: errors.New("db down")})
	err := a.Execute(context.Background(), ApprovalDecideInput{
		ApprovalID: uuid.New().String(), Approved: true, DecidedBy: uuid.New(),
	})
	if err == nil {
		t.Fatal("expected store error to propagate")
	}
}

// failingStore is an ApprovalStore whose every method fails.
type failingStore struct{ err error }

func (s *failingStore) Create(context.Context, gates.ApprovalRequest) error { return s.err }
func (s *failingStore) Get(context.Context, uuid.UUID) (gates.ApprovalRequest, error) {
	return gates.ApprovalRequest{}, s.err
}
func (s *failingStore) List(context.Context, uuid.UUID, *gates.ApprovalStatus) ([]gates.ApprovalRequest, error) {
	return nil, s.err
}
func (s *failingStore) Decide(context.Context, uuid.UUID, bool, uuid.UUID, string) error {
	return s.err
}
func (s *failingStore) Expire(context.Context, uuid.UUID) error { return s.err }
