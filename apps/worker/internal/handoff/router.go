package handoff

import (
	"errors"
	"fmt"
)

// Team topologies as declared in the registry.
const (
	TopologyPipeline     = "sequential"
	TopologyOrchestrated = "orchestrated"
)

// TeamMember is a resolved team member: a concrete agent instance with the
// LLM profile it should run under. The orchestrator resolves team roles to
// concrete agents before starting the chain workflow.
type TeamMember struct {
	AgentID  string `json:"agent_id"`
	RoleName string `json:"role_name"`
	Profile  string `json:"profile"`
}

// TeamConfig is the worker-side projection of a registry Team entity with
// resolved agent IDs.
type TeamConfig struct {
	ID           string       `json:"id"`
	Topology     string       `json:"topology"`
	Lead         *TeamMember  `json:"lead,omitempty"`
	Members      []TeamMember `json:"members"`
	ReviewMatrix ReviewMatrix `json:"review_matrix"`
}

// FindMember returns the team member with the given agent ID.
func (t *TeamConfig) FindMember(agentID string) *TeamMember {
	for i := range t.Members {
		if t.Members[i].AgentID == agentID {
			return &t.Members[i]
		}
	}
	return nil
}

// FindReviewer returns the first team member whose role is allowed to review.
func (t *TeamConfig) FindReviewer() *TeamMember {
	for i := range t.Members {
		if t.ReviewMatrix.AllowsRole(t.Members[i].RoleName) {
			return &t.Members[i]
		}
	}
	return nil
}

// RouteResult describes where a handoff package is routed next.
type RouteResult struct {
	NextAgentIDs []string // empty when the chain is complete
	Terminal     bool     // true when the handoff is final (returns to a human)
}

// Router decides which agent receives a handoff package next. Route is a
// pure function of the package and the team config: it performs no I/O, so
// workflows can call it directly.
type Router interface {
	Route(pkg *HandoffPackage, team *TeamConfig) (*RouteResult, error)
}

// RouterFor returns the router for the given team topology (team.schema.json).
// Unknown topologies are rejected instead of silently falling back: choosing
// a routing policy for a topology is an explicit contract, not a guess.
func RouterFor(topology string) (Router, error) {
	switch topology {
	case TopologyPipeline:
		return PipelineRouter{}, nil
	case TopologyOrchestrated:
		return OrchestratedRouter{}, nil
	default:
		return nil, fmt.Errorf("handoff: unsupported team topology %q", topology)
	}
}

var (
	// ErrNilPackage is returned when the handoff package is missing.
	ErrNilPackage = errors.New("handoff: package is nil")
	// ErrNilTeam is returned when the team config is missing.
	ErrNilTeam = errors.New("handoff: team config is nil")
	// ErrUnknownAgent is returned when routing targets an agent outside the team.
	ErrUnknownAgent = errors.New("handoff: agent is not a team member")
	// ErrUnknownSource is returned when the package source is outside the team.
	ErrUnknownSource = errors.New("handoff: source agent is not a team member")
	// ErrNoLead is returned when orchestrated routing has no lead agent.
	ErrNoLead = errors.New("handoff: team has no lead agent")
)

// PipelineRouter routes handoff packages along a sequential pipeline: the
// producing agent may suggest the next agent, otherwise the team review
// matrix picks the next reviewer.
type PipelineRouter struct{}

// NewPipelineRouter returns a Router for the sequential (pipeline) topology.
func NewPipelineRouter() Router { return PipelineRouter{} }

// Route implements Router for the pipeline topology.
func (PipelineRouter) Route(pkg *HandoffPackage, team *TeamConfig) (*RouteResult, error) {
	if pkg == nil {
		return nil, ErrNilPackage
	}
	if team == nil {
		return nil, ErrNilTeam
	}
	if pkg.Metadata == nil || pkg.Metadata.SourceAgentID == "" {
		return nil, ErrUnknownSource
	}
	if team.FindMember(pkg.Metadata.SourceAgentID) == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, pkg.Metadata.SourceAgentID)
	}

	// 1. The producing agent knows the next agent.
	if suggested := pkg.Metadata.SuggestedNextAgentID; suggested != "" {
		if team.FindMember(suggested) == nil {
			return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, suggested)
		}
		if suggested == pkg.Metadata.SourceAgentID {
			return nil, fmt.Errorf("%w: %s routes to itself", ErrUnknownAgent, suggested)
		}
		return &RouteResult{NextAgentIDs: []string{suggested}}, nil
	}

	// 2. The review matrix decides who reviews next. The source agent's own
	// role is excluded: a reviewer finishing its review completes the chain.
	src := team.FindMember(pkg.Metadata.SourceAgentID)
	if team.ReviewMatrix.RequiresReview() && (src == nil || !team.ReviewMatrix.AllowsRole(src.RoleName)) {
		if reviewer := team.FindReviewer(); reviewer != nil {
			return &RouteResult{NextAgentIDs: []string{reviewer.AgentID}}, nil
		}
	}

	// 3. No suggestion and no required review: the chain is complete.
	return &RouteResult{Terminal: true}, nil
}

// OrchestratedRouter routes every handoff package through the team lead.
// The lead (PM) decides whether to return the package to the author or
// forward it to another agent; when the lead produced the package the chain
// is complete.
type OrchestratedRouter struct{}

// NewOrchestratedRouter returns a Router for the orchestrated topology.
func NewOrchestratedRouter() Router { return OrchestratedRouter{} }

// Route implements Router for the orchestrated topology.
func (OrchestratedRouter) Route(pkg *HandoffPackage, team *TeamConfig) (*RouteResult, error) {
	if pkg == nil {
		return nil, ErrNilPackage
	}
	if team == nil {
		return nil, ErrNilTeam
	}
	if pkg.Metadata == nil || pkg.Metadata.SourceAgentID == "" {
		return nil, ErrUnknownSource
	}
	if team.FindMember(pkg.Metadata.SourceAgentID) == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSource, pkg.Metadata.SourceAgentID)
	}
	if team.Lead == nil || team.Lead.AgentID == "" {
		return nil, ErrNoLead
	}

	// The lead produced the package: its decision is final.
	if team.Lead.AgentID == pkg.Metadata.SourceAgentID {
		return &RouteResult{Terminal: true}, nil
	}

	// Everyone else routes through the lead.
	return &RouteResult{NextAgentIDs: []string{team.Lead.AgentID}}, nil
}
