package gates

import (
	"sort"
	"time"
)

// Gate names from SDLC_STATE_MACHINE.md section 4. These values are part of
// the contract: process specs reference gates by these identifiers.
const (
	GateRequirementsApproved = "requirements_approved"
	GateArchitectureApproved = "architecture_approved"
	GateCIGreen              = "ci_green"
	GateReviewApproved       = "review_approved"
	GateQAPassed             = "qa_passed"
	GateSBOMSigned           = "sbom_signed"
	GateProdApproved         = "prod_approved"
	GateDeployHealthy        = "deploy_healthy"
	GateConformanceOK        = "conformance_ok"
	GateKBPublished          = "kb_published"
	GateSemanticValid        = "semantic_valid"
)

// DefaultApprovalTimeout is the deadline of a human_approval request
// (SDLC_STATE_MACHINE.md section 7: "deadline per StageRun - 24 hours").
// A process stage overrides it through stages[].gate_timeout.
const DefaultApprovalTimeout = 24 * time.Hour

// Registry holds gate definitions by name.
type Registry struct {
	gates map[string]Gate
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{gates: make(map[string]Gate)}
}

// Register adds or replaces a gate definition.
func (r *Registry) Register(g Gate) {
	r.gates[g.Name] = g
}

// Get returns the definition and true, or the zero Gate and false.
func (r *Registry) Get(name string) (Gate, bool) {
	g, ok := r.gates[name]
	return g, ok
}

// Lookup returns the definition or an error. An unknown gate is an error and
// never an implicit pass: a transition whose gate cannot be resolved must not
// proceed silently (P9).
func (r *Registry) Lookup(name string) (Gate, error) {
	g, ok := r.gates[name]
	if !ok {
		return Gate{}, UnknownGateError{Name: name}
	}
	return g, nil
}

// List returns all definitions sorted by name, so the UI ordering is stable.
func (r *Registry) List() []Gate {
	out := make([]Gate, 0, len(r.gates))
	for _, g := range r.gates {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// TypeOf returns the gate type for a name, or false when the gate is unknown.
func (r *Registry) TypeOf(name string) (GateType, bool) {
	g, ok := r.Get(name)
	if !ok {
		return "", false
	}
	return g.Type, true
}

// ApprovalTimeout returns the deadline to apply to a human_approval request:
// the per-stage override wins, then the gate default, then the global 24
// hours. Non-human gates return 0 because they never wait.
func (r *Registry) ApprovalTimeout(name string, override time.Duration) time.Duration {
	g, ok := r.Get(name)
	if !ok || g.Type != GateTypeHumanApproval {
		return 0
	}
	if override > 0 {
		return override
	}
	if g.ApprovalTimeout > 0 {
		return g.ApprovalTimeout
	}
	return DefaultApprovalTimeout
}

// DefaultRegistry returns a registry pre-loaded with every gate of
// SDLC_STATE_MACHINE.md section 4.
func DefaultRegistry() *Registry {
	r := NewRegistry()

	// human_approval gates: R3 transitions that always wait for a named human
	// decision (ADR-013).
	r.Register(Gate{
		Name:            GateRequirementsApproved,
		Type:            GateTypeHumanApproval,
		Description:     "RequirementsReview -> Design: approve plus resolved comments",
		ApprovalTimeout: DefaultApprovalTimeout,
	})
	r.Register(Gate{
		Name:            GateArchitectureApproved,
		Type:            GateTypeHumanApproval,
		Description:     "ArchitectureReview -> Planning: approve plus a valid AM",
		ApprovalTimeout: DefaultApprovalTimeout,
	})
	r.Register(Gate{
		Name:            GateProdApproved,
		Type:            GateTypeHumanApproval,
		Description:     "Release -> Deploy: human approve (R3)",
		ApprovalTimeout: DefaultApprovalTimeout,
	})

	// agent_review gates: an independent reviewer agent must approve.
	r.Register(Gate{
		Name:        GateReviewApproved,
		Type:        GateTypeAgentReview,
		Description: "CodeReview -> QA: approve from a reviewer that is not the author",
	})
	r.Register(Gate{
		Name:        GateKBPublished,
		Type:        GateTypeAgentReview,
		Description: "Diagram -> KB: validator plus review",
	})

	// auto gates: deterministic conditions over evidence facts.
	r.Register(Gate{
		Name:        GateCIGreen,
		Type:        GateTypeAuto,
		Description: "InDev -> CodeReview: all CI checks green",
	})
	r.Register(Gate{
		Name:        GateQAPassed,
		Type:        GateTypeAuto,
		Description: "QA -> Build: tests green, coverage thresholds met",
	})
	r.Register(Gate{
		Name:        GateSBOMSigned,
		Type:        GateTypeAuto,
		Description: "Build -> Release: SBOM present and image signed",
	})
	r.Register(Gate{
		Name:        GateDeployHealthy,
		Type:        GateTypeAuto,
		Description: "Deploy -> Monitoring: healthcheck and metrics normal",
	})
	r.Register(Gate{
		Name:        GateConformanceOK,
		Type:        GateTypeAuto,
		Description: "On merge to main: drift at warning level or below",
	})
	r.Register(Gate{
		Name:        GateSemanticValid,
		Type:        GateTypeAuto,
		Description: "On every SLPr/AM change: inference rules satisfied",
	})

	return r
}

// UnknownGateError reports a gate name that is not in the registry.
type UnknownGateError struct {
	Name string
}

func (e UnknownGateError) Error() string {
	return "gates: unknown gate " + e.Name
}
