// Package handoff builds and validates agent handoff packages.
package handoff

import (
	"encoding/json"
	"fmt"
)

// HandoffMetadata carries runtime routing information. It is NOT part of the
// wire contract (handoff.schema.json): it is stamped by the workflow runtime
// and stripped before validation and signing.
type HandoffMetadata struct {
	// SourceAgentID is the agent that produced the package.
	SourceAgentID string `json:"source_agent_id,omitempty"`
	// SuggestedNextAgentID is the next agent suggested by the producing agent.
	SuggestedNextAgentID string `json:"suggested_next_agent_id,omitempty"`
	// ChainStep is the 0-based position of the producing agent in the chain.
	ChainStep int `json:"chain_step,omitempty"`
}

// GetSourceAgentID returns the source agent ID, empty when the header is
// absent, so callers never dereference a nil metadata pointer.
func (m *HandoffMetadata) GetSourceAgentID() string {
	if m == nil {
		return ""
	}
	return m.SourceAgentID
}

// HandoffPackage is the structured package for agent-to-agent handoff.
// Matches packages/contracts/schemas/handoff.schema.json.
type HandoffPackage struct {
	Goal                 string          `json:"goal"`
	ContextRefs          []string        `json:"context_refs"`
	AcceptanceCriteria   []string        `json:"acceptance_criteria"`
	Constraints          []string        `json:"constraints,omitempty"`
	DecisionsMade        []string        `json:"decisions_made,omitempty"`
	OpenQuestions        []string        `json:"open_questions,omitempty"`
	ExpectedOutputSchema json.RawMessage `json:"expected_output_schema"`

	// Metadata is the runtime routing header; never persisted on the wire.
	Metadata *HandoffMetadata `json:"metadata,omitempty"`
}

// ReviewMatrix mirrors the registry review matrix: how many reviewers the
// handoff must pass through and which roles are allowed to review.
type ReviewMatrix struct {
	RequiredReviewers int      `json:"required_reviewers"`
	AllowedRoles      []string `json:"allowed_roles"`
}

// RequiresReview reports whether a handoff must pass through a review step.
func (r ReviewMatrix) RequiresReview() bool { return r.RequiredReviewers > 0 }

// AllowsRole reports whether the given role is an allowed reviewer role.
func (r ReviewMatrix) AllowsRole(roleName string) bool {
	for _, role := range r.AllowedRoles {
		if role == roleName {
			return true
		}
	}
	return false
}

// Builder creates handoff packages from the LLM final answer.
type Builder struct{}

// NewBuilder creates a new handoff builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Build parses the LLM content as a handoff package.
// The LLM is expected to return a JSON object matching the handoff schema.
//
// The model output may carry a metadata header proposing the next agent. Only
// the suggestion is honoured: the source-agent identity is stamped by the
// workflow runtime from trusted input and is never taken from model output,
// so a prompt-injected model cannot route a package on behalf of another
// agent. Build strips the model-provided header and keeps only the hint.
func (b *Builder) Build(llmContent string) (*HandoffPackage, error) {
	var pkg HandoffPackage
	if err := json.Unmarshal([]byte(llmContent), &pkg); err != nil {
		return nil, fmt.Errorf("handoff: cannot parse LLM content as JSON: %w", err)
	}
	var hint string
	if pkg.Metadata != nil {
		hint = pkg.Metadata.SuggestedNextAgentID
	}
	pkg.Metadata = nil
	if hint != "" {
		pkg.Metadata = &HandoffMetadata{SuggestedNextAgentID: hint}
	}
	return &pkg, nil
}

// Stamp fills the runtime routing header of a package produced by an agent.
// sourceAgentID comes from the trusted workflow input; the optional
// next-agent hint the producing agent proposed in its answer is preserved.
// chainStep is the 0-based position of this execution in the chain.
func Stamp(pkg *HandoffPackage, sourceAgentID string, chainStep int) {
	if pkg == nil {
		return
	}
	if pkg.Metadata == nil {
		pkg.Metadata = &HandoffMetadata{}
	}
	pkg.Metadata.SourceAgentID = sourceAgentID
	pkg.Metadata.ChainStep = chainStep
}

// WireCopy returns a deep copy of the package without the runtime metadata
// header. It is the form that is validated against the wire schema, signed
// and handed to the next agent: handoff.schema.json has
// additionalProperties=false, so the header must never reach the wire.
func WireCopy(pkg *HandoffPackage) (*HandoffPackage, error) {
	if pkg == nil {
		return nil, nil
	}
	saved := pkg.Metadata
	pkg.Metadata = nil
	defer func() { pkg.Metadata = saved }()
	data, err := json.Marshal(pkg)
	if err != nil {
		return nil, fmt.Errorf("handoff: marshal wire copy: %w", err)
	}
	var out HandoffPackage
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("handoff: unmarshal wire copy: %w", err)
	}
	return &out, nil
}
