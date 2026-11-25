package handoff

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveTeamConfig_HappyPath verifies the core happy path: a single-agent
// orchestrated team is resolved from registry-shaped input and ToolsPolicy is
// propagated from the matching role spec.
func TestResolveTeamConfig_HappyPath(t *testing.T) {
	spec := TeamSpec{
		Topology: "orchestrated",
		Members: []TeamMemberSpec{
			{RoleName: "software-engineer", Count: 1},
		},
		ReviewMatrix: ReviewMatrixSpec{
			RequiredReviewers: 1,
			AllowedRoles:      []string{"qa-engineer", "software-engineer"},
		},
	}
	roles := []RoleSpec{
		{Name: "software-engineer", Profile: "code-fast", ToolsPolicy: "write_capable"},
	}
	agents := []MemberAgent{
		{AgentID: "agent-dev", RoleName: "software-engineer", Lead: true},
	}

	team, err := ResolveTeamConfig("team-1", spec, roles, agents)
	require.NoError(t, err)

	assert.Equal(t, "team-1", team.ID)
	assert.Equal(t, "orchestrated", team.Topology)
	require.Len(t, team.Members, 1)
	assert.Equal(t, "agent-dev", team.Members[0].AgentID)
	assert.Equal(t, "software-engineer", team.Members[0].RoleName)
	assert.Equal(t, "code-fast", team.Members[0].Profile)
	assert.Equal(t, "write_capable", team.Members[0].ToolsPolicy)

	assert.Equal(t, "agent-dev", team.Lead.AgentID)
	assert.Equal(t, "code-fast", team.Lead.Profile)
	assert.Equal(t, "write_capable", team.Lead.ToolsPolicy)
	assert.Equal(t, 1, team.ReviewMatrix.RequiredReviewers)
	assert.Equal(t, []string{"qa-engineer", "software-engineer"}, team.ReviewMatrix.AllowedRoles)
}

// TestResolveTeamConfig_SequentialMultiMember validates a sequential team with
// multiple agents and count > 1.
func TestResolveTeamConfig_SequentialMultiMember(t *testing.T) {
	spec := TeamSpec{
		Topology: "sequential",
		Members: []TeamMemberSpec{
			{RoleName: "backend-developer", Count: 2},
		},
		ReviewMatrix: ReviewMatrixSpec{
			RequiredReviewers: 1,
			AllowedRoles:      []string{"backend-developer"},
		},
	}
	roles := []RoleSpec{
		{Name: "backend-developer", Profile: "code-strong", ToolsPolicy: "write_capable"},
	}
	agents := []MemberAgent{
		{AgentID: "dev-a", RoleName: "backend-developer"},
		{AgentID: "dev-b", RoleName: "backend-developer"},
	}

	team, err := ResolveTeamConfig("team-2", spec, roles, agents)
	require.NoError(t, err)

	require.Len(t, team.Members, 2)
	assert.Equal(t, "dev-a", team.Members[0].AgentID)
	assert.Equal(t, "dev-b", team.Members[1].AgentID)
	assert.Equal(t, "write_capable", team.Members[0].ToolsPolicy)
	assert.Equal(t, "write_capable", team.Members[1].ToolsPolicy)
	// Sequential topology does not require a lead.
	assert.Nil(t, team.Lead)
}

// TestResolveTeamConfig_MultiRole validates that each role's ToolsPolicy is
// correctly propagated to its members.
func TestResolveTeamConfig_MultiRole(t *testing.T) {
	spec := TeamSpec{
		Topology: "orchestrated",
		Members: []TeamMemberSpec{
			{RoleName: "backend-developer", Count: 1},
			{RoleName: "qa-engineer", Count: 1},
		},
		ReviewMatrix: ReviewMatrixSpec{
			RequiredReviewers: 2,
			AllowedRoles:      []string{"qa-engineer"},
		},
	}
	roles := []RoleSpec{
		{Name: "backend-developer", ToolsPolicy: "write_capable"},
		{Name: "qa-engineer", ToolsPolicy: "read_only"},
	}
	agents := []MemberAgent{
		{AgentID: "dev-a", RoleName: "backend-developer", Lead: true},
		{AgentID: "qa-a", RoleName: "qa-engineer"},
	}

	team, err := ResolveTeamConfig("team-3", spec, roles, agents)
	require.NoError(t, err)

	require.Len(t, team.Members, 2)
	dev := team.Members[0]
	qa := team.Members[1]
	assert.Equal(t, "dev-a", dev.AgentID)
	assert.Equal(t, "write_capable", dev.ToolsPolicy)
	assert.Equal(t, "qa-a", qa.AgentID)
	assert.Equal(t, "read_only", qa.ToolsPolicy)
}

// TestResolveTeamConfig_DefaultCount validates that Count=0 defaults to 1.
func TestResolveTeamConfig_DefaultCount(t *testing.T) {
	spec := TeamSpec{
		Topology: "sequential",
		Members: []TeamMemberSpec{
			{RoleName: "analyst", Count: 0},
		},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "analyst"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "analyst"}}

	_, err := ResolveTeamConfig("team-dc", spec, roles, agents)
	assert.NoError(t, err)

	// Count=2 should reject a single agent.
	spec2 := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "analyst", Count: 2}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	agents2 := []MemberAgent{{AgentID: "a1", RoleName: "analyst"}}
	_, err = ResolveTeamConfig("team-dc2", spec2, roles, agents2)
	assert.ErrorIs(t, err, ErrAgentCountMismatch)
}

// TestResolveTeamConfig_EmptyTeamID.
func TestResolveTeamConfig_EmptyTeamID(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	_, err := ResolveTeamConfig("", spec, nil, nil)
	assert.ErrorIs(t, err, ErrEmptyTeamID)
}

// TestResolveTeamConfig_EmptyMembers.
func TestResolveTeamConfig_EmptyMembers(t *testing.T) {
	spec := TeamSpec{Topology: "sequential", ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1}}
	_, err := ResolveTeamConfig("t1", spec, nil, nil)
	assert.ErrorIs(t, err, ErrEmptyMembers)
}

// TestResolveTeamConfig_UnsupportedTopology.
func TestResolveTeamConfig_UnsupportedTopology(t *testing.T) {
	spec := TeamSpec{
		Topology:     "collaborative",
		Members:      []TeamMemberSpec{{RoleName: "x"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	_, err := ResolveTeamConfig("t1", spec, nil, nil)
	assert.Error(t, err)
}

// TestResolveTeamConfig_UnknownRole.
func TestResolveTeamConfig_UnknownRole(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "unknown-role"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "known-role"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "unknown-role"}}

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	assert.ErrorIs(t, err, ErrUnknownRole)
}

// TestResolveTeamConfig_DuplicateAgent.
func TestResolveTeamConfig_DuplicateAgent(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x", Count: 2}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x"}}
	agents := []MemberAgent{
		{AgentID: "a1", RoleName: "x"},
		{AgentID: "a1", RoleName: "x"},
	}

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	assert.ErrorIs(t, err, ErrDuplicateAgent)
}

// TestResolveTeamConfig_AgentCountMismatch.
func TestResolveTeamConfig_AgentCountMismatch(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x", Count: 2}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "x"}}

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	assert.ErrorIs(t, err, ErrAgentCountMismatch)
}

// TestResolveTeamConfig_MemberWithoutAgent.
func TestResolveTeamConfig_MemberWithoutAgent(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x"}}
	agents := []MemberAgent{} // no agents for declared role

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	assert.ErrorIs(t, err, ErrMemberWithoutAgent)
}

// TestResolveTeamConfig_AgentNotInTeam validates that assigning an agent to a
// role the team does not declare (a specification desync) is rejected. The role
// exists in the registry specs but is absent from TeamSpec.members.
func TestResolveTeamConfig_AgentNotInTeam(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x"}, {Name: "y"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "y"}}

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	assert.ErrorIs(t, err, ErrRoleNotInTeam)
}

// TestResolveTeamConfig_LeadRequired validates that orchestrated teams require
// exactly one lead, and sequential teams accept zero leads.
func TestResolveTeamConfig_LeadRequired(t *testing.T) {
	spec := TeamSpec{
		Topology:     "orchestrated",
		Members:      []TeamMemberSpec{{RoleName: "x"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "x"}} // no lead

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	assert.ErrorIs(t, err, ErrLeadRequired)

	// Two leads should also fail.
	agents2 := []MemberAgent{
		{AgentID: "a1", RoleName: "x", Lead: true},
		{AgentID: "a2", RoleName: "x", Lead: true},
	}
	spec2 := TeamSpec{
		Topology:     "orchestrated",
		Members:      []TeamMemberSpec{{RoleName: "x", Count: 2}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	_, err = ResolveTeamConfig("t2", spec2, roles, agents2)
	assert.ErrorIs(t, err, ErrLeadRequired)
}

// TestResolveTeamConfig_SequentialNoLead validates that sequential topology
// works fine without a lead.
func TestResolveTeamConfig_SequentialNoLead(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x"}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x", Profile: "code-fast", ToolsPolicy: "read_only"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "x"}}

	team, err := ResolveTeamConfig("t1", spec, roles, agents)
	require.NoError(t, err)
	assert.Nil(t, team.Lead)
	assert.Equal(t, "read_only", team.Members[0].ToolsPolicy)
}

// TestResolveTeamConfig_InvalidRequiredReviewers validates that a
// review_matrix.required_reviewers below the team.schema.json minimum of 1 is
// rejected instead of being silently accepted as "no reviews required".
func TestResolveTeamConfig_InvalidRequiredReviewers(t *testing.T) {
	roles := []RoleSpec{{Name: "x"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "x"}}

	for _, reviewers := range []int{0, -1} {
		spec := TeamSpec{
			Topology:     "sequential",
			Members:      []TeamMemberSpec{{RoleName: "x"}},
			ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: reviewers},
		}
		_, err := ResolveTeamConfig("t1", spec, roles, agents)
		require.ErrorIs(t, err, ErrInvalidRequiredReviewers, "required_reviewers=%d", reviewers)
	}
}

// TestResolveTeamConfig_NegativeMemberCount validates that a negative member
// count is a validation error rather than being coerced to a single member.
// Zero stays valid: it is the schema default for an omitted count.
func TestResolveTeamConfig_NegativeMemberCount(t *testing.T) {
	spec := TeamSpec{
		Topology:     "sequential",
		Members:      []TeamMemberSpec{{RoleName: "x", Count: -1}},
		ReviewMatrix: ReviewMatrixSpec{RequiredReviewers: 1},
	}
	roles := []RoleSpec{{Name: "x"}}
	agents := []MemberAgent{{AgentID: "a1", RoleName: "x"}}

	_, err := ResolveTeamConfig("t1", spec, roles, agents)
	require.ErrorIs(t, err, ErrNegativeMemberCount)

	// The zero default still resolves to one member.
	spec.Members[0].Count = 0
	team, err := ResolveTeamConfig("t2", spec, roles, agents)
	require.NoError(t, err)
	require.Len(t, team.Members, 1)
}
