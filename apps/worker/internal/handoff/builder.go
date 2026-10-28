// Package handoff builds and validates agent handoff packages.
package handoff

import (
	"encoding/json"
	"fmt"
)

// HandoffPackage is the structured package for agent-to-agent handoff.
// Matches packages/contracts/schemas/handoff.schema.json.
type HandoffPackage struct {
	Goal                 string            `json:"goal"`
	ContextRefs          []string          `json:"context_refs"`
	AcceptanceCriteria   []string          `json:"acceptance_criteria"`
	Constraints          []string          `json:"constraints,omitempty"`
	DecisionsMade        []string          `json:"decisions_made,omitempty"`
	OpenQuestions        []string          `json:"open_questions,omitempty"`
	ExpectedOutputSchema json.RawMessage   `json:"expected_output_schema"`
}

// Builder creates handoff packages from the LLM final answer.
type Builder struct{}

// NewBuilder creates a new handoff builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Build parses the LLM content as a handoff package.
// The LLM is expected to return a JSON object matching the handoff schema.
// Returns an error if the content cannot be parsed or is incomplete.
func (b *Builder) Build(llmContent string) (*HandoffPackage, error) {
	var pkg HandoffPackage
	if err := json.Unmarshal([]byte(llmContent), &pkg); err != nil {
		return nil, fmt.Errorf("handoff: cannot parse LLM content as JSON: %w", err)
	}
	return &pkg, nil
}
