package gates

import (
	"context"
	"time"
)

// Service evaluates gates by name. It resolves the gate definition from the
// registry, selects the matching Evaluator and runs it against the evidence
// facts.
//
// The service is stateless apart from the registry and the approval store, so
// it is safe to share between goroutines and to call from Temporal activities.
type Service struct {
	registry   *Registry
	conditions map[string]Condition
	store      ApprovalStore
	// maxReviewRounds overrides DefaultMaxReviewRounds for agent_review gates.
	maxReviewRounds int
}

// Option customises a Service.
type Option func(*Service)

// WithRegistry replaces the gate registry (default: DefaultRegistry).
func WithRegistry(r *Registry) Option {
	return func(s *Service) { s.registry = r }
}

// WithConditions replaces the auto-gate condition table.
func WithConditions(c map[string]Condition) Option {
	return func(s *Service) { s.conditions = c }
}

// WithApprovalStore sets the store used by human_approval gates.
func WithApprovalStore(st ApprovalStore) Option {
	return func(s *Service) { s.store = st }
}

// WithMaxReviewRounds overrides the agent_review circuit breaker.
func WithMaxReviewRounds(n int) Option {
	return func(s *Service) {
		if n > 0 {
			s.maxReviewRounds = n
		}
	}
}

// NewService builds a gate service. Without WithApprovalStore, human_approval
// gates cannot be evaluated and return an error: a missing queue is never
// treated as an approval.
func NewService(opts ...Option) *Service {
	s := &Service{
		registry:        DefaultRegistry(),
		conditions:      AutoConditions(),
		maxReviewRounds: DefaultMaxReviewRounds,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Registry exposes the gate registry so callers (the API, the UI) can list the
// available gates without duplicating the definitions.
func (s *Service) Registry() *Registry { return s.registry }

// TypeOf returns the gate type for a name.
func (s *Service) TypeOf(name string) (GateType, bool) {
	return s.registry.TypeOf(name)
}

// ApprovalTimeout resolves the deadline for a human_approval request.
func (s *Service) ApprovalTimeout(name string, override time.Duration) time.Duration {
	return s.registry.ApprovalTimeout(name, override)
}

// Evaluate runs the gate identified by name.
//
// An unknown gate is an error: the caller must not proceed with a transition
// whose control cannot be resolved.
func (s *Service) Evaluate(ctx context.Context, name string, facts Facts) (Result, error) {
	if facts == nil {
		facts = Facts{}
	}
	gate, err := s.registry.Lookup(name)
	if err != nil {
		return Result{}, err
	}
	return s.evaluatorFor(gate).Evaluate(ctx, facts)
}

// evaluatorFor builds the Evaluator for one gate definition.
func (s *Service) evaluatorFor(gate Gate) Evaluator {
	switch gate.Type {
	case GateTypeAuto:
		return AutoGate{Name: gate.Name, Condition: s.conditions[gate.Name]}
	case GateTypeAgentReview:
		return AgentReviewGate{Name: gate.Name, MaxRounds: s.maxReviewRounds}
	case GateTypeHumanApproval:
		return HumanApprovalGate{Name: gate.Name, Store: s.store}
	default:
		return errorEvaluator{err: UnknownGateTypeError{Name: gate.Name, Type: gate.Type}}
	}
}

// errorEvaluator reports a configuration failure instead of guessing a result.
type errorEvaluator struct {
	err error
}

func (e errorEvaluator) Evaluate(_ context.Context, _ Facts) (Result, error) {
	return Result{}, e.err
}
