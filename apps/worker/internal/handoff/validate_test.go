package handoff

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestValidateIncoming_Complete(t *testing.T) {
	pkg := &HandoffPackage{
		Goal:                 "Implement auth",
		ContextRefs:          []string{"sem://project/1", "kb://article/2"},
		AcceptanceCriteria:   []string{"Given X, when Y, then Z"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	missing, err := ValidateIncoming(pkg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want empty", missing)
	}
}

func TestValidateIncoming_Nil(t *testing.T) {
	if _, err := ValidateIncoming(nil); err == nil {
		t.Fatal("expected error for nil package")
	}
}

func TestValidateIncoming_MissingFields(t *testing.T) {
	pkg := &HandoffPackage{}
	missing, err := ValidateIncoming(pkg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"goal", "acceptance_criteria", "context_refs", "expected_output_schema"}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("missing = %v, want %v", missing, want)
	}
}

func TestValidateIncoming_WhitespaceGoal(t *testing.T) {
	pkg := &HandoffPackage{Goal: "   "}
	missing, err := ValidateIncoming(pkg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, m := range missing {
		if m == "goal" {
			found = true
		}
	}
	if !found {
		t.Fatalf("whitespace-only goal must be reported missing, got %v", missing)
	}
}

func TestValidateIncoming_InvalidURIScheme(t *testing.T) {
	pkg := &HandoffPackage{
		Goal:                 "g",
		ContextRefs:          []string{"sem://ok", "ftp://not-allowed"},
		AcceptanceCriteria:   []string{"c"},
		ExpectedOutputSchema: json.RawMessage(`{}`),
	}
	missing, err := ValidateIncoming(pkg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 1 || missing[0] != "context_refs[1] invalid URI scheme" {
		t.Fatalf("missing = %v, want context_refs[1] invalid URI scheme", missing)
	}
}

func TestValidateIncoming_InvalidSchemaJSON(t *testing.T) {
	pkg := &HandoffPackage{
		Goal:                 "g",
		ContextRefs:          []string{"sem://ok"},
		AcceptanceCriteria:   []string{"c"},
		ExpectedOutputSchema: json.RawMessage(`not json`),
	}
	missing, err := ValidateIncoming(pkg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(missing) != 1 || missing[0] != "expected_output_schema" {
		t.Fatalf("missing = %v, want expected_output_schema", missing)
	}
}

func TestBuild_StripsModelMetadata(t *testing.T) {
	content := `{
		"goal": "g",
		"context_refs": ["sem://x"],
		"acceptance_criteria": ["c"],
		"expected_output_schema": {"type": "object"},
		"metadata": {
			"source_agent_id": "agent-impersonated",
			"suggested_next_agent_id": "agent-qa",
			"chain_step": 99
		}
	}`
	pkg, err := NewBuilder().Build(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.Metadata == nil {
		t.Fatal("the next-agent hint must be preserved")
	}
	// Only the hint survives: the producing-agent identity is stamped by the
	// workflow runtime from trusted input, never from model output.
	if pkg.Metadata.SourceAgentID != "" {
		t.Fatalf("source agent from model output = %q, want empty", pkg.Metadata.SourceAgentID)
	}
	if pkg.Metadata.SuggestedNextAgentID != "agent-qa" {
		t.Fatalf("hint = %q, want agent-qa", pkg.Metadata.SuggestedNextAgentID)
	}
	if pkg.Metadata.ChainStep != 0 {
		t.Fatalf("chain step from model output = %d, want 0", pkg.Metadata.ChainStep)
	}
}

func TestBuild_NoMetadata(t *testing.T) {
	content := `{
		"goal": "g",
		"context_refs": ["sem://x"],
		"acceptance_criteria": ["c"],
		"expected_output_schema": {"type": "object"}
	}`
	pkg, err := NewBuilder().Build(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.Metadata != nil {
		t.Fatalf("metadata = %+v, want nil", pkg.Metadata)
	}
}

func TestStamp(t *testing.T) {
	Stamp(nil, "agent-x", 3) // must not panic

	pkg := &HandoffPackage{Metadata: &HandoffMetadata{SuggestedNextAgentID: "agent-qa"}}
	Stamp(pkg, "agent-pm", 2)
	if pkg.Metadata.SourceAgentID != "agent-pm" {
		t.Fatalf("source = %q, want agent-pm", pkg.Metadata.SourceAgentID)
	}
	if pkg.Metadata.SuggestedNextAgentID != "agent-qa" {
		t.Fatalf("hint = %q, want preserved agent-qa", pkg.Metadata.SuggestedNextAgentID)
	}
	if pkg.Metadata.ChainStep != 2 {
		t.Fatalf("chain step = %d, want 2", pkg.Metadata.ChainStep)
	}

	fresh := &HandoffPackage{}
	Stamp(fresh, "agent-pm", 0)
	if fresh.Metadata == nil || fresh.Metadata.SourceAgentID != "agent-pm" {
		t.Fatalf("stamp on nil header = %+v, want initialized header", fresh.Metadata)
	}
}

func TestGetSourceAgentID_NilSafe(t *testing.T) {
	var meta *HandoffMetadata
	if meta.GetSourceAgentID() != "" {
		t.Fatal("nil metadata must return empty source")
	}
	meta = &HandoffMetadata{SourceAgentID: "agent-pm"}
	if meta.GetSourceAgentID() != "agent-pm" {
		t.Fatalf("source = %q, want agent-pm", meta.GetSourceAgentID())
	}
}

func TestWireCopy(t *testing.T) {
	if out, err := WireCopy(nil); err != nil || out != nil {
		t.Fatalf("WireCopy(nil) = (%v, %v), want (nil, nil)", out, err)
	}

	pkg := &HandoffPackage{
		Goal:                 "g",
		ContextRefs:          []string{"sem://x"},
		AcceptanceCriteria:   []string{"c"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
		Metadata:             &HandoffMetadata{SourceAgentID: "agent-pm", ChainStep: 1},
	}
	wire, err := WireCopy(pkg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wire.Metadata != nil {
		t.Fatalf("wire copy metadata = %+v, want nil", wire.Metadata)
	}
	// The original keeps its header.
	if pkg.Metadata == nil || pkg.Metadata.SourceAgentID != "agent-pm" {
		t.Fatalf("original metadata = %+v, want preserved", pkg.Metadata)
	}
	// The copy is deep: mutating it must not touch the original.
	wire.Goal = "changed"
	wire.ContextRefs[0] = "changed"
	if pkg.Goal != "g" || pkg.ContextRefs[0] != "sem://x" {
		t.Fatalf("original was mutated: goal=%q refs=%v", pkg.Goal, pkg.ContextRefs)
	}
}

func TestValidator_Validate_IgnoresMetadata(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	pkg := &HandoffPackage{
		Goal:                 "Build feature X",
		ContextRefs:          []string{"https://example.com/sem/node/1"},
		AcceptanceCriteria:   []string{"Given X, when Y, then Z"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
		Metadata: &HandoffMetadata{
			SourceAgentID:        "agent-pm",
			SuggestedNextAgentID: "agent-qa",
			ChainStep:            1,
		},
	}
	// handoff.schema.json has additionalProperties=false: the stamped
	// package must still validate because the header never reaches the wire.
	if err := v.Validate(pkg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
