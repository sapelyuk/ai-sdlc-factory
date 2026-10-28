package handoff

import (
	"encoding/json"
	"testing"
)

func TestBuilder_Build_Valid(t *testing.T) {
	content := `{
		"goal": "Implement user auth",
		"context_refs": ["https://example.com/sem/node/123"],
		"acceptance_criteria": ["Given valid credentials, when login, then token returned"],
		"constraints": ["Use JWT"],
		"decisions_made": ["ADR-001"],
		"open_questions": [],
		"expected_output_schema": {"type": "object", "properties": {"token": {"type": "string"}}}
	}`

	b := NewBuilder()
	pkg, err := b.Build(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.Goal != "Implement user auth" {
		t.Errorf("goal = %q, want %q", pkg.Goal, "Implement user auth")
	}
	if len(pkg.ContextRefs) != 1 {
		t.Errorf("context_refs len = %d, want 1", len(pkg.ContextRefs))
	}
	if len(pkg.AcceptanceCriteria) != 1 {
		t.Errorf("acceptance_criteria len = %d, want 1", len(pkg.AcceptanceCriteria))
	}
}

func TestBuilder_Build_InvalidJSON(t *testing.T) {
	content := `not a json`

	b := NewBuilder()
	_, err := b.Build(content)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestBuilder_Build_MissingRequired(t *testing.T) {
	// Missing goal and expected_output_schema.
	content := `{"context_refs": ["https://example.com"], "acceptance_criteria": ["c"]}`

	b := NewBuilder()
	pkg, err := b.Build(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pkg.Goal != "" {
		t.Errorf("goal should be empty")
	}
}

func TestValidator_Validate_Valid(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	pkg := &HandoffPackage{
		Goal:                 "Build feature X",
		ContextRefs:          []string{"https://example.com/sem/node/1"},
		AcceptanceCriteria:   []string{"Given X, when Y, then Z"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
	}

	if err := v.Validate(pkg); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidator_Validate_MissingGoal(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	pkg := &HandoffPackage{
		Goal:                 "",
		ContextRefs:          []string{"https://example.com/sem/node/1"},
		AcceptanceCriteria:   []string{"criterion"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
	}

	err = v.Validate(pkg)
	if err == nil {
		t.Fatal("expected validation error for empty goal")
	}
}

func TestValidator_Validate_MissingContextRefs(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	pkg := &HandoffPackage{
		Goal:                 "Goal",
		ContextRefs:          []string{},
		AcceptanceCriteria:   []string{"criterion"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
	}

	err = v.Validate(pkg)
	if err == nil {
		t.Fatal("expected validation error for empty context_refs")
	}
}

func TestValidator_ValidateJSON_Valid(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	raw := json.RawMessage(`{
		"goal": "Test",
		"context_refs": ["https://example.com/1"],
		"acceptance_criteria": ["criterion"],
		"expected_output_schema": {"type": "object"}
	}`)

	if err := v.ValidateJSON(raw); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidator_ValidateJSON_Invalid(t *testing.T) {
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	// Missing required fields.
	raw := json.RawMessage(`{"goal": "Test"}`)

	if err := v.ValidateJSON(raw); err == nil {
		t.Fatal("expected validation error")
	}
}
