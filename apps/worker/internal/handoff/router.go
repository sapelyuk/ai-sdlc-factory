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
	AgentID     string `json:"agent_id"`
	RoleName    string `json:"role_name"`
	Profile     string `json:"profile"`
	ToolsPolicy string `json:"tools_policy,omitempty"`
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

// FindReviewers returns up to limit team members whose role is allowed to
// review, in team declaration order. Agents listed in exclude are skipped: the
// author never reviews its own package, and a reviewer that already approved
// the current package must not review it twice.
func (t *TeamConfig) FindReviewers(limit int, exclude ...string) []*TeamMember {
	if limit <= 0 {
		return nil
	}
	skip := make(map[string]struct{}, len(exclude))
	for _, agentID := range exclude {
		skip[agentID] = struct{}{}
	}
	reviewers := make([]*TeamMember, 0, limit)
	for i := range t.Members {
		member := &t.Members[i]
		if _, excluded := skip[member.AgentID]; excluded {
			continue
		}
		if !t.ReviewMatrix.AllowsRole(member.RoleName) {
			continue
		}
		reviewers = append(reviewers, member)
		if len(reviewers) == limit {
			break
		}
	}
	return reviewers
}

// approvedReviewers returns the set of team members that already approved the
// package in the current review round. Only members with an allowed reviewer
// role count: the routing header is runtime state, but the router still
// ignores unknown IDs so a malformed header cannot satisfy the matrix.
func (t *TeamConfig) approvedReviewers(meta *HandoffMetadata) map[string]struct{} {
	approved := make(map[string]struct{})
	for _, agentID := range meta.GetReviewedBy() {
		member := t.FindMember(agentID)
		if member == nil || !t.ReviewMatrix.AllowsRole(member.RoleName) {
			continue
		}
		approved[agentID] = struct{}{}
	}
	return approved
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
	// ErrReviewerNotFound is returned when the review matrix requires an
	// independent review but the team has no eligible reviewer. Routing fails
	// instead of completing the chain silently: a required review that did not
	// happen violates P9 (verification independent of the author).
	ErrReviewerNotFound = errors.New("handoff: no eligible reviewer for the review matrix")
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

	// 2. The review matrix decides who reviews next. A handoff that requires
	// review must never complete the chain before every required reviewer has
	// approved it (P9: verification independent of the author).
	//
	// The matrix is declared in team.schema.json as required_reviewers plus
	// allowed_roles. required_reviewers is the fan-out width: a team that
	// lists `reviewer: [arch, reviewer]` resolves to required_reviewers=2 and
	// both agents receive the package. ALL required reviewers must approve;
	// this is stricter than a quorum and is the safe choice because
	// SYSTEM_DESIGN.md and SDLC_STATE_MACHINE.md do not define a quorum rule.
	// The sequential topology runs the reviewers one after another: the
	// Temporal MVP executes one child workflow per chain step.
	if team.ReviewMatrix.RequiresReview() {
		approved := team.approvedReviewers(pkg.Metadata)
		src := team.FindMember(pkg.Metadata.SourceAgentID)
		// The author never reviews its own package. When the author's role is
		// an allowed reviewer role, the package it produced counts as one
		// completed review (a reviewer finishing its review completes the
		// chain, as before).
		if src != nil && team.ReviewMatrix.AllowsRole(src.RoleName) {
			approved[src.AgentID] = struct{}{}
		}
		remaining := team.ReviewMatrix.RequiredReviewers - len(approved)
		if remaining > 0 {
			exclude := make([]string, 0, len(approved))
			for agentID := range approved {
				exclude = append(exclude, agentID)
			}
			reviewers := team.FindReviewers(remaining, exclude...)
			if len(reviewers) < remaining {
				return nil, fmt.Errorf(
					"%w: required %d, eligible %d",
					ErrReviewerNotFound, team.ReviewMatrix.RequiredReviewers, len(approved)+len(reviewers),
				)
			}
			next := make([]string, len(reviewers))
			for i, reviewer := range reviewers {
				next[i] = reviewer.AgentID
			}
			return &RouteResult{NextAgentIDs: next}, nil
		}
	}

	// 3. No suggestion and no outstanding review: the chain is complete.
	return &RouteResult{Terminal: true}, nil
}

// OrchestratedRouter routes every handoff package through the team lead.
// The lead (PM) decides whether to return the package to the author or
// forward it to another agent; when the lead produced the package the chain
// is complete. In this topology the lead is the independent decision point
// (P9): the review matrix is enforced by PipelineRouter, where no orchestrator
// exists to make the review decision.
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
