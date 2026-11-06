package gates

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestServiceDispatchAuto(t *testing.T) {
	svc := NewService()
	res, err := svc.Evaluate(context.Background(), GateCIGreen, Facts{
		EvidenceCISuccess:           true,
		EvidenceConventionalCommits: true,
		EvidenceCoverage:            0.9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPassed() {
		t.Errorf("expected pass, got %+v", res)
	}
}

func TestServiceDispatchAgentReview(t *testing.T) {
	svc := NewService()
	res, err := svc.Evaluate(context.Background(), GateReviewApproved, Facts{
		EvidenceReviewApproved: true,
		EvidenceReviewAuthor:   "a",
		EvidenceReviewReviewer: "b",
		EvidenceReviewRound:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPassed() {
		t.Errorf("expected pass, got %+v", res)
	}
}

func TestServiceDispatchHumanApproval(t *testing.T) {
	store := NewInMemoryStore()
	id := uuid.New()
	_ = store.Create(context.Background(), ApprovalRequest{
		ID: id, WorkspaceID: uuid.New(), GateName: GateProdApproved, Status: ApprovalApproved,
	})
	svc := NewService(WithApprovalStore(store))
	res, err := svc.Evaluate(context.Background(), GateProdApproved, Facts{EvidenceApprovalID: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPassed() {
		t.Errorf("expected pass, got %+v", res)
	}
}

func TestServiceUnknownGate(t *testing.T) {
	svc := NewService()
	_, err := svc.Evaluate(context.Background(), "no_such_gate", Facts{})
	if err == nil {
		t.Fatal("expected error for unknown gate")
	}
	var uge UnknownGateError
	if !errorsAs(err, &uge) {
		t.Errorf("expected UnknownGateError, got %T: %v", err, err)
	}
}

func TestServiceHumanApprovalWithoutStore(t *testing.T) {
	svc := NewService()
	_, err := svc.Evaluate(context.Background(), GateProdApproved, Facts{EvidenceApprovalID: uuid.New().String()})
	if err == nil {
		t.Fatal("expected error for nil store")
	}
}

func TestServiceNilFacts(t *testing.T) {
	svc := NewService()
	res, err := svc.Evaluate(context.Background(), GateCIGreen, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsPassed() {
		t.Error("expected block for empty facts")
	}
}

func TestServiceTypeOf(t *testing.T) {
	svc := NewService()
	if gt, ok := svc.TypeOf(GateCIGreen); !ok || gt != GateTypeAuto {
		t.Errorf("expected auto, got %v %v", gt, ok)
	}
	if _, ok := svc.TypeOf("missing"); ok {
		t.Error("expected false for missing gate")
	}
}

func TestServiceApprovalTimeout(t *testing.T) {
	svc := NewService()
	// Override wins.
	if d := svc.ApprovalTimeout(GateProdApproved, time.Hour); d != time.Hour {
		t.Errorf("expected 1h override, got %v", d)
	}
	// Gate default.
	if d := svc.ApprovalTimeout(GateProdApproved, 0); d != DefaultApprovalTimeout {
		t.Errorf("expected default, got %v", d)
	}
	// Non-human gates return 0.
	if d := svc.ApprovalTimeout(GateCIGreen, time.Hour); d != 0 {
		t.Errorf("expected 0 for auto gate, got %v", d)
	}
}

func TestServiceWithRegistryAndConditions(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Gate{Name: "custom_auto", Type: GateTypeAuto})
	conds := map[string]Condition{
		"custom_auto": func(facts Facts) (bool, string) {
			return facts.GetBool("ok"), "not ok"
		},
	}
	svc := NewService(WithRegistry(reg), WithConditions(conds))
	res, err := svc.Evaluate(context.Background(), "custom_auto", Facts{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsPassed() {
		t.Error("expected pass")
	}
	res, _ = svc.Evaluate(context.Background(), "custom_auto", Facts{"ok": false})
	if res.IsPassed() {
		t.Error("expected block")
	}
}

func TestServiceWithMaxReviewRounds(t *testing.T) {
	svc := NewService(WithMaxReviewRounds(1))
	res, err := svc.Evaluate(context.Background(), GateReviewApproved, Facts{
		EvidenceReviewApproved: true,
		EvidenceReviewAuthor:   "a",
		EvidenceReviewReviewer: "b",
		EvidenceReviewRound:    2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsPassed() {
		t.Error("expected block for round 2 with max 1")
	}
	// Zero or negative override is ignored.
	svc = NewService(WithMaxReviewRounds(0))
	if svc.maxReviewRounds != DefaultMaxReviewRounds {
		t.Errorf("expected default, got %d", svc.maxReviewRounds)
	}
}

func TestServiceUnknownTypeInRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Gate{Name: "weird", Type: GateType("bogus")})
	svc := NewService(WithRegistry(reg))
	_, err := svc.Evaluate(context.Background(), "weird", Facts{})
	if err == nil {
		t.Fatal("expected error")
	}
	var ugte UnknownGateTypeError
	if !errorsAs(err, &ugte) {
		t.Errorf("expected UnknownGateTypeError, got %T", err)
	}
}

func TestServiceAutoGateWithoutCondition(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Gate{Name: "no_cond", Type: GateTypeAuto})
	svc := NewService(WithRegistry(reg))
	_, err := svc.Evaluate(context.Background(), "no_cond", Facts{})
	if err == nil {
		t.Fatal("expected error for auto gate without a condition")
	}
}

func TestErrorEvaluator(t *testing.T) {
	e := errorEvaluator{err: MissingConditionError{Name: "x"}}
	_, err := e.Evaluate(context.Background(), Facts{})
	if err == nil {
		t.Error("expected error")
	}
}
