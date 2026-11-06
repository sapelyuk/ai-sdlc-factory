package gates

import (
	"testing"
	"time"
)

func TestRegistryDefault(t *testing.T) {
	reg := DefaultRegistry()
	gates := reg.List()
	if len(gates) != 11 {
		t.Fatalf("expected 11 gates, got %d", len(gates))
	}
	// Check we have the expected types.
	humanCount, agentCount, autoCount := 0, 0, 0
	for _, g := range gates {
		switch g.Type {
		case GateTypeHumanApproval:
			humanCount++
		case GateTypeAgentReview:
			agentCount++
		case GateTypeAuto:
			autoCount++
		default:
			t.Errorf("gate %q has unknown type %q", g.Name, g.Type)
		}
	}
	if humanCount != 3 {
		t.Errorf("expected 3 human_approval, got %d", humanCount)
	}
	if agentCount != 2 {
		t.Errorf("expected 2 agent_review, got %d", agentCount)
	}
	if autoCount != 6 {
		t.Errorf("expected 6 auto, got %d", autoCount)
	}
}

func TestRegistryLookup(t *testing.T) {
	reg := DefaultRegistry()
	g, ok := reg.Get(GateCIGreen)
	if !ok {
		t.Fatal("gate not found")
	}
	if g.Type != GateTypeAuto {
		t.Errorf("expected auto, got %s", g.Type)
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected not found for missing gate")
	}
}

func TestRegistryLookupError(t *testing.T) {
	reg := DefaultRegistry()
	_, err := reg.Lookup("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	var uge UnknownGateError
	if !errorsAs(err, &uge) {
		t.Errorf("expected UnknownGateError, got %T", err)
	}
}

func TestRegistryTypeOf(t *testing.T) {
	reg := DefaultRegistry()
	if gt, ok := reg.TypeOf(GateCIGreen); !ok || gt != GateTypeAuto {
		t.Errorf("expected auto, got %v %v", gt, ok)
	}
	if _, ok := reg.TypeOf("missing"); ok {
		t.Error("expected false for missing")
	}
}

func TestRegistryApprovalTimeout(t *testing.T) {
	reg := DefaultRegistry()
	// Human gate with default timeout.
	if d := reg.ApprovalTimeout(GateProdApproved, 0); d != DefaultApprovalTimeout {
		t.Errorf("expected 24h, got %v", d)
	}
	// Override wins.
	if d := reg.ApprovalTimeout(GateRequirementsApproved, time.Hour); d != time.Hour {
		t.Errorf("expected 1h, got %v", d)
	}
	// Non-human gate.
	if d := reg.ApprovalTimeout(GateCIGreen, time.Hour); d != 0 {
		t.Errorf("expected 0 for auto gate, got %v", d)
	}
	// Unknown gate.
	if d := reg.ApprovalTimeout("missing", time.Hour); d != 0 {
		t.Errorf("expected 0 for unknown gate, got %v", d)
	}
}

func TestRegistryListSorted(t *testing.T) {
	reg := DefaultRegistry()
	list := reg.List()
	for i := 1; i < len(list); i++ {
		if list[i].Name < list[i-1].Name {
			t.Errorf("list not sorted at index %d: %q < %q", i, list[i].Name, list[i-1].Name)
		}
	}
}

func TestRegistryRegisterReplace(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Gate{Name: "x", Type: GateTypeAuto})
	reg.Register(Gate{Name: "x", Type: GateTypeHumanApproval})
	g, _ := reg.Get("x")
	if g.Type != GateTypeHumanApproval {
		t.Errorf("expected replaced type, got %s", g.Type)
	}
}
