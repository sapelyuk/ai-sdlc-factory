package handoff

import (
	"errors"
	"fmt"
)

// Registry team/role projection types.
//
// The registry domain types (apps/api/internal/registry/domain.TeamSpec and
// .RoleSpec) live in the Platform API Go module, which the worker does not and
// must not import: the worker is a runtime consumer, the API is a control-plane
// service, and coupling them would invert the dependency direction. TeamConfig
// is already documented as the "worker-side projection of a registry Team
// entity", so the resolver lives next to it and consumes JSON-compatible
// mirrors of the registry payloads (packages/contracts/schemas/team.schema.json
// and role.schema.json) rather than the API domain types themselves (issue #135).

// TeamMemberSpec mirrors the "members" entry of team.schema.json: a team
// declares roles (role_name + role_version), never concrete agents.
type TeamMemberSpec struct {
	RoleName    string `json:"role_name"`
	RoleVersion string `json:"role_version"`
	Count       int    `json:"count,omitempty"`
}

// ReviewMatrixSpec mirrors the "review_matrix" object of team.schema.json.
// It is kept separate from the runtime ReviewMatrix so the resolver can accept
// a raw registry payload without assuming any runtime stamping.
type ReviewMatrixSpec struct {
	RequiredReviewers int      `json:"required_reviewers"`
	AllowedRoles      []string `json:"allowed_roles,omitempty"`
}

// TeamSpec is the worker-side mirror of a registry Team.spec. Only the fields
// that project into TeamConfig are carried; budget and knowledge scopes are
// enforced elsewhere (budgets by ChainWorkflow, scopes by the context builder).
//
// It is a one-way projection, not a round-trip-safe model of
// team.schema.json: it silently drops the schema-required "description" field
// and the optional "process_ref", and it carries neither "budget" nor any
// future field. Do not use it to write a team back to the registry; read the
// registry payload with the API domain types for that.
type TeamSpec struct {
	Topology     string           `json:"topology"`
	Members      []TeamMemberSpec `json:"members"`
	ReviewMatrix ReviewMatrixSpec `json:"review_matrix"`
}

// RoleSpec is the worker-side mirror of the registry role fields the resolver
// projects onto a team member.
//
// domain.RoleSpec carries no role identity: the registry stores the role name on
// the enclosing domain.Role entity. The resolver matches TeamSpec members to
// roles by Name, so the caller supplies the name alongside the spec. ToolsPolicy
// is propagated from here onto TeamMember.ToolsPolicy, which ChainWorkflow reads
// into TaskRunInput.ToolsPolicy (issue #133 sanitize phase, issue #135).
//
// Profile is not part of the registry payload: neither domain.RoleSpec nor
// role.schema.json has a "profile" field, so unmarshalling a registry role never
// populates it. It is caller-derived from spec.model.capability (the capability
// enum in role.schema.json, e.g. "code-fast") and exists here only so the caller
// can pass the already-mapped value through to TeamMember.Profile.
type RoleSpec struct {
	Name        string `json:"name"`
	Profile     string `json:"profile,omitempty"`
	ToolsPolicy string `json:"tools_policy,omitempty"`
}

// MemberAgent binds a concrete agent instance to a role declared in TeamSpec.
//
// Registry TeamSpec declares roles only; assigning concrete agents and choosing
// the team lead is the caller's responsibility (full agent provisioning is
// explicitly out of scope for #126/#135). TeamConfig.Members is keyed by
// AgentID and orchestrated routing requires a Lead, so a resolver that ignored
// these would emit an unusable configuration.
type MemberAgent struct {
	RoleName string `json:"role_name"`
	AgentID  string `json:"agent_id"`
	Lead     bool   `json:"lead,omitempty"`
}

// Resolver errors. They are returned, never logged or swallowed, so the API
// path that builds SDLCInput can map them to the caller (issue #135).
var (
	// ErrEmptyTeamID is returned when the team identifier is missing.
	ErrEmptyTeamID = errors.New("handoff: team id is required")
	// ErrEmptyMembers is returned when a team declares no members.
	ErrEmptyMembers = errors.New("handoff: team declares no members")
	// ErrUnknownRole is returned when a member or agent references a role that
	// is not present in the supplied role specs.
	ErrUnknownRole = errors.New("handoff: role is not defined in the registry specs")
	// ErrRoleNotInTeam is returned when an agent is assigned to a role the team
	// does not declare (a specification desync).
	ErrRoleNotInTeam = errors.New("handoff: agent assigned to a role the team does not declare")
	// ErrMemberWithoutAgent is returned when a declared role has no concrete
	// agent assigned to it.
	ErrMemberWithoutAgent = errors.New("handoff: declared role has no agent assigned")
	// ErrAgentCountMismatch is returned when the number of agents assigned to a
	// role differs from the count the team declares.
	ErrAgentCountMismatch = errors.New("handoff: agent count does not match the declared member count")
	// ErrDuplicateAgent is returned when the same agent id is assigned twice.
	ErrDuplicateAgent = errors.New("handoff: duplicate agent id in team")
	// ErrLeadRequired is returned when an orchestrated team has no single lead.
	ErrLeadRequired = errors.New("handoff: orchestrated team requires exactly one lead")
	// ErrInvalidRequiredReviewers is returned when review_matrix.required_reviewers
	// is below the schema minimum of 1 (team.schema.json).
	ErrInvalidRequiredReviewers = errors.New("handoff: review_matrix.required_reviewers must be >= 1")
	// ErrNegativeMemberCount is returned when a team declares a negative member
	// count. The registry schema requires count >= 1 (team.schema.json); zero is
	// only the omitted-field default and is normalized to 1.
	ErrNegativeMemberCount = errors.New("handoff: member count must not be negative")
)

// ResolveTeamConfig projects a registry TeamSpec plus its role specs and the
// caller's concrete agent assignments into a worker-side TeamConfig.
//
// It is a pure function: no database, no NATS, no Temporal. Validation failures
// (unknown role, empty team, specification desync, unsupported topology,
// missing lead, invalid review matrix, negative member count) are returned as
// errors.
//
// ToolsPolicy propagation is the core of issue #135 gap 2: each resolved member
// copies ToolsPolicy from the role spec that matches its role name, so the
// downstream sanitize behavior (ChainWorkflow -> TaskRunInput.ToolsPolicy,
// issue #133) is driven by the registry, not by hand-built wrappers.
func ResolveTeamConfig(teamID string, spec TeamSpec, roles []RoleSpec, agents []MemberAgent) (*TeamConfig, error) {
	if teamID == "" {
		return nil, ErrEmptyTeamID
	}
	if len(spec.Members) == 0 {
		return nil, ErrEmptyMembers
	}
	// review_matrix.required_reviewers is required by team.schema.json with
	// minimum 1, so a value below 1 is an invalid registry payload, not a
	// "no reviews needed" shortcut.
	if spec.ReviewMatrix.RequiredReviewers < 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidRequiredReviewers, spec.ReviewMatrix.RequiredReviewers)
	}
	// Reuse RouterFor as the single source of truth for routable topologies:
	// a topology the worker cannot route must never reach a TeamConfig.
	if _, err := RouterFor(spec.Topology); err != nil {
		return nil, err
	}

	roleByName := make(map[string]RoleSpec, len(roles))
	for _, role := range roles {
		if role.Name == "" {
			return nil, fmt.Errorf("%w: role spec without a name", ErrUnknownRole)
		}
		if _, dup := roleByName[role.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate role %q", ErrUnknownRole, role.Name)
		}
		roleByName[role.Name] = role
	}

	// Declared roles and their desired member counts (schema default count=1).
	type declared struct {
		want int
		got  int
	}
	declaredRoles := make(map[string]*declared, len(spec.Members))
	for _, member := range spec.Members {
		if member.RoleName == "" {
			return nil, fmt.Errorf("%w: team member without a role name", ErrMemberWithoutAgent)
		}
		if _, ok := roleByName[member.RoleName]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownRole, member.RoleName)
		}
		// count is minimum 1 in team.schema.json; zero is only the default for
		// an omitted field. A negative value is an invalid payload and must not
		// be coerced into a one-member role.
		if member.Count < 0 {
			return nil, fmt.Errorf("%w: role %q declares %d", ErrNegativeMemberCount, member.RoleName, member.Count)
		}
		count := member.Count
		if count == 0 {
			count = 1
		}
		entry, ok := declaredRoles[member.RoleName]
		if !ok {
			entry = &declared{}
			declaredRoles[member.RoleName] = entry
		}
		entry.want += count
	}

	seenAgent := make(map[string]struct{}, len(agents))
	leadCount := 0
	members := make([]TeamMember, 0, len(agents))
	var lead *TeamMember
	for _, agent := range agents {
		if agent.AgentID == "" {
			return nil, fmt.Errorf("%w: agent assignment without an agent id", ErrMemberWithoutAgent)
		}
		if _, dup := seenAgent[agent.AgentID]; dup {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateAgent, agent.AgentID)
		}
		seenAgent[agent.AgentID] = struct{}{}

		entry, ok := declaredRoles[agent.RoleName]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrRoleNotInTeam, agent.RoleName)
		}
		role, ok := roleByName[agent.RoleName]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownRole, agent.RoleName)
		}
		entry.got++

		member := TeamMember{
			AgentID:     agent.AgentID,
			RoleName:    agent.RoleName,
			Profile:     role.Profile,
			ToolsPolicy: role.ToolsPolicy,
		}
		members = append(members, member)
		if agent.Lead {
			leadCount++
		}
	}

	// Every declared role must have exactly the declared number of agents.
	for roleName, entry := range declaredRoles {
		if entry.got == 0 {
			return nil, fmt.Errorf("%w: %s", ErrMemberWithoutAgent, roleName)
		}
		if entry.got != entry.want {
			return nil, fmt.Errorf("%w: role %q declares %d, assigned %d",
				ErrAgentCountMismatch, roleName, entry.want, entry.got)
		}
	}

	// Orchestrated routing requires a single lead; other topologies allow zero
	// or one. The lead pointer is resolved by scanning the final members slice,
	// so it never aliases a reallocated backing array.
	if leadCount > 1 || (spec.Topology == TopologyOrchestrated && leadCount == 0) {
		return nil, fmt.Errorf("%w: found %d", ErrLeadRequired, leadCount)
	}
	for i := range members {
		if agentIsLead(agents, members[i].AgentID) {
			lead = &members[i]
			break
		}
	}

	return &TeamConfig{
		ID:       teamID,
		Topology: spec.Topology,
		Lead:     lead,
		Members:  members,
		ReviewMatrix: ReviewMatrix{
			RequiredReviewers: spec.ReviewMatrix.RequiredReviewers,
			AllowedRoles:      append([]string(nil), spec.ReviewMatrix.AllowedRoles...),
		},
	}, nil
}

// agentIsLead reports whether the given agent id was marked as lead.
func agentIsLead(agents []MemberAgent, agentID string) bool {
	for _, agent := range agents {
		if agent.AgentID == agentID {
			return agent.Lead
		}
	}
	return false
}
