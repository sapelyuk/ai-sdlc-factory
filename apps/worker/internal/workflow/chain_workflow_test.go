package workflow

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// ChainSuite tests ChainWorkflow with the child TaskRunWorkflow mocked, so
// the routing loop, the return-to-sender logic and the routing audit events
// are verified in isolation (issue #21).
type ChainSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env           *testsuite.TestWorkflowEnvironment
	auditEvents   *mockAuditActivity
	taskRunInputs []TaskRunInput
}

func TestChainSuite(t *testing.T) {
	suite.Run(t, new(ChainSuite))
}

func (s *ChainSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.auditEvents = &mockAuditActivity{}
	s.taskRunInputs = nil
	s.env.RegisterActivityWithOptions(s.auditEvents.Execute, activity.RegisterOptions{
		Name: AuditRecordActivityName,
	})
}

func (s *ChainSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// mockTaskRun queues one mocked result per expected TaskRun execution and
// captures every child workflow input for assertions.
func (s *ChainSuite) mockTaskRun(results ...*TaskRunResult) {
	for _, r := range results {
		res := r
		inputs := &s.taskRunInputs
		s.env.OnWorkflow(TaskRunWorkflow, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				*inputs = append(*inputs, args.Get(1).(TaskRunInput))
			}).
			Return(res, nil).Once()
	}
}

// routedEvents returns the handoff_routed events recorded by the chain.
func (s *ChainSuite) routedEvents() []audit.Event {
	var out []audit.Event
	for _, ev := range s.auditEvents.events {
		if ev.Action == sdkaudit.ActionHandoffRouted {
			out = append(out, ev)
		}
	}
	return out
}

// abortedEvents returns the chain_aborted events recorded by the chain.
func (s *ChainSuite) abortedEvents() []audit.Event {
	var out []audit.Event
	for _, ev := range s.auditEvents.events {
		if ev.Action == sdkaudit.ActionChainAborted {
			out = append(out, ev)
		}
	}
	return out
}

// chainTeam builds a resolved team with the four standard MVP roles.
func chainTeam(topology string) handoff.TeamConfig {
	return handoff.TeamConfig{
		ID:       "team-1",
		Topology: topology,
		Lead:     &handoff.TeamMember{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
		Members: []handoff.TeamMember{
			{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
			{AgentID: "agent-architect", RoleName: "software-architect", Profile: "arch-strong"},
			{AgentID: "agent-dev", RoleName: "software-engineer", Profile: "code-fast"},
			{AgentID: "agent-qa", RoleName: "qa-engineer", Profile: "code-fast"},
		},
		ReviewMatrix: handoff.ReviewMatrix{
			RequiredReviewers: 1,
			AllowedRoles:      []string{"software-architect", "qa-engineer"},
		},
	}
}

// chainPkg builds a complete package stamped with the given source agent.
func chainPkg(goal, source string) *handoff.HandoffPackage {
	return &handoff.HandoffPackage{
		Goal:                 goal,
		ContextRefs:          []string{"sem://project/1"},
		AcceptanceCriteria:   []string{"tests pass"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
		Metadata:             &handoff.HandoffMetadata{SourceAgentID: source},
	}
}

// withSuggestion sets the next-agent hint on a package.
func withSuggestion(pkg *handoff.HandoffPackage, suggested string) *handoff.HandoffPackage {
	pkg.Metadata.SuggestedNextAgentID = suggested
	return pkg
}

func (s *ChainSuite) chainInput(team handoff.TeamConfig, overrides ...func(*ChainInput)) ChainInput {
	in := ChainInput{
		WorkspaceID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		Team:              team,
		TaskDescription:   "build the feature",
		DefaultMaxCostUSD: 10,
		DefaultMaxSteps:   10,
	}
	for _, override := range overrides {
		override(&in)
	}
	return in
}

func (s *ChainSuite) TestOrchestrated_LeadPackageIsTerminal() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{
		Handoff:      chainPkg("Design reviewed", "agent-pm"),
		Steps:        2,
		TotalCostUSD: 0.03,
	})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.NotNil(res)
	s.Equal("Design reviewed", res.FinalHandoff.Goal)
	s.Equal("agent-pm", res.TerminalAgentID)
	s.Equal(1, res.Steps)
	s.Equal(0.03, res.TotalCostUSD)

	routed := s.routedEvents()
	s.Require().Len(routed, 1)
	s.Equal(routed[0].Details["terminal"], true)
	s.Equal(routed[0].ActorID, sdkaudit.SystemActor("worker"))
}

func (s *ChainSuite) TestOrchestrated_MemberPackageRoutesThroughLead() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Architecture ready", "agent-architect"), TotalCostUSD: 0.01},
		&TaskRunResult{Handoff: chainPkg("Plan approved", "agent-pm"), TotalCostUSD: 0.02},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-architect"
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("agent-pm", res.TerminalAgentID)
	s.Equal(2, res.Steps)
	s.Equal(0.03, res.TotalCostUSD)

	s.Require().Len(s.taskRunInputs, 2)
	second := s.taskRunInputs[1]
	s.Equal("agent-pm", second.AgentID)
	// The lead received the architect package with no rework feedback.
	s.Equal("agent-architect", second.IncomingHandoff.Metadata.SourceAgentID)
	s.Empty(second.IncomingFeedback)
	s.Equal(1, second.ChainStep)

	routed := s.routedEvents()
	s.Require().Len(routed, 2)
	s.Equal(routed[0].Details["from_agent_id"], "agent-architect")
	s.Equal(routed[0].Details["to_agent_id"], "agent-pm")
	s.Equal(routed[1].Details["terminal"], true)
}

func (s *ChainSuite) TestSequential_ReviewMatrixPicksReviewer() {
	team := chainTeam(handoff.TopologyPipeline)
	team.Lead = nil
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Implemented", "agent-dev")},
		&TaskRunResult{Handoff: chainPkg("Reviewed", "agent-architect")},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-dev"
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("agent-architect", res.TerminalAgentID)
	s.Require().Len(s.taskRunInputs, 2)
	s.Equal("agent-architect", s.taskRunInputs[1].AgentID)
}

// TestChainWorkflow_ReviewerNotFound_Aborts verifies that a review that cannot
// be performed stops the chain instead of silently completing it (P9).
func (s *ChainSuite) TestChainWorkflow_ReviewerNotFound_Aborts() {
	team := chainTeam(handoff.TopologyPipeline)
	team.Lead = nil
	// The matrix allows architect and QA roles, but this team has no member
	// with either role: the required review cannot happen.
	team.Members = []handoff.TeamMember{
		{AgentID: "agent-pm", RoleName: "product-manager", Profile: "pm-fast"},
		{AgentID: "agent-dev", RoleName: "software-engineer", Profile: "code-fast"},
	}
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("Implemented", "agent-dev")})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-dev"
	}))
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "reviewer")

	aborted := s.abortedEvents()
	s.Require().Len(aborted, 1)
	s.Equal(sdkaudit.SystemActor("worker"), aborted[0].ActorID)
	s.Equal("reviewer_not_found", aborted[0].Details["reason"])
	s.Equal("agent-dev", aborted[0].Details["from_agent_id"])
	s.Equal("Implemented", aborted[0].Details["goal"])
	s.EqualValues(0, aborted[0].Details["chain_step"])
}

// TestChainWorkflow_MultipleReviewers_AllMustApprove verifies the fan-out:
// every required reviewer runs and the chain is terminal only after the last
// approval.
func (s *ChainSuite) TestChainWorkflow_MultipleReviewers_AllMustApprove() {
	team := chainTeam(handoff.TopologyPipeline)
	team.Lead = nil
	team.ReviewMatrix.RequiredReviewers = 2
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Implemented", "agent-dev")},
		&TaskRunResult{Handoff: chainPkg("Architecture approved", "agent-architect")},
		&TaskRunResult{Handoff: chainPkg("QA approved", "agent-qa")},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-dev"
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("agent-qa", res.TerminalAgentID)
	s.Equal(3, res.Steps)

	s.Require().Len(s.taskRunInputs, 3)
	s.Equal("agent-dev", s.taskRunInputs[0].AgentID)
	s.Equal("agent-architect", s.taskRunInputs[1].AgentID)
	s.Equal("agent-qa", s.taskRunInputs[2].AgentID)
	// The second reviewer receives the package approved by the first one.
	s.Equal("agent-architect", s.taskRunInputs[2].IncomingHandoff.Metadata.SourceAgentID)
	s.Equal([]string{"agent-architect"}, s.taskRunInputs[2].IncomingHandoff.Metadata.ReviewedBy)

	routed := s.routedEvents()
	s.Require().Len(routed, 2)
	s.Equal("agent-dev", routed[0].Details["from_agent_id"])
	s.Equal("agent-architect", routed[0].Details["to_agent_id"])
	s.Equal([]interface{}{"agent-architect", "agent-qa"}, routed[0].Details["to_agent_ids"])
	s.Equal(true, routed[1].Details["terminal"])
}

// TestChainWorkflow_SecondReviewerRejects_ReturnsToAuthor verifies that a
// rejection by any reviewer prevents acceptance and returns the package to the
// original author, not to the previous reviewer.
func (s *ChainSuite) TestChainWorkflow_SecondReviewerRejects_ReturnsToAuthor() {
	team := chainTeam(handoff.TopologyPipeline)
	team.Lead = nil
	team.ReviewMatrix.RequiredReviewers = 2
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Implemented", "agent-dev")},
		&TaskRunResult{Handoff: chainPkg("Architecture approved", "agent-architect")},
		&TaskRunResult{Rejected: true, Missing: []string{"acceptance_criteria"}},
		&TaskRunResult{Handoff: chainPkg("Implemented v2", "agent-dev")},
		&TaskRunResult{Handoff: chainPkg("Architecture approved v2", "agent-architect")},
		&TaskRunResult{Handoff: chainPkg("QA approved", "agent-qa")},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-dev"
		in.MaxChainLength = 10
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("agent-qa", res.TerminalAgentID)
	s.Equal(6, res.Steps)

	s.Require().Len(s.taskRunInputs, 6)
	rework := s.taskRunInputs[3]
	s.Equal("agent-dev", rework.AgentID)
	s.Equal("agent-architect", rework.IncomingHandoff.Metadata.SourceAgentID)
	s.Equal([]string{"acceptance_criteria"}, rework.IncomingFeedback)

	routed := s.routedEvents()
	s.Require().Len(routed, 4)
	// The second reviewer rejected: the package goes back to the original
	// author.
	s.True(routed[1].Details["returned_to_sender"].(bool))
	s.Equal("agent-qa", routed[1].Details["from_agent_id"])
	s.Equal("agent-dev", routed[1].Details["to_agent_id"])
	s.Equal([]interface{}{"acceptance_criteria"}, routed[1].Details["missing"])
	// The reworked package is reviewed from scratch by both reviewers.
	s.Equal([]interface{}{"agent-architect", "agent-qa"}, routed[2].Details["to_agent_ids"])
	s.Equal(true, routed[3].Details["terminal"])
}

func (s *ChainSuite) TestSequential_ProducerSuggestionIsFollowed() {
	team := chainTeam(handoff.TopologyPipeline)
	team.Lead = nil
	s.mockTaskRun(
		&TaskRunResult{Handoff: withSuggestion(chainPkg("Implemented", "agent-dev"), "agent-qa")},
		&TaskRunResult{Handoff: chainPkg("QA passed", "agent-qa")},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-dev"
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("agent-qa", res.TerminalAgentID)
	s.Require().Len(s.taskRunInputs, 2)
	s.Equal("agent-qa", s.taskRunInputs[1].AgentID)
}

func (s *ChainSuite) TestRejection_ReturnsPackageToSender() {
	team := chainTeam(handoff.TopologyPipeline)
	team.Lead = nil
	s.mockTaskRun(
		// dev produces a package, the review matrix routes it to the first
		// allowed reviewer (the architect).
		&TaskRunResult{Handoff: chainPkg("Implemented", "agent-dev")},
		// the architect rejects it: incomplete.
		&TaskRunResult{Rejected: true, Missing: []string{"acceptance_criteria"}},
		// dev reworks the same package with the receiver diagnostics.
		&TaskRunResult{Handoff: chainPkg("Implemented v2", "agent-dev"), TotalCostUSD: 0.01},
		// the architect reviews the complete package and finishes the chain.
		&TaskRunResult{Handoff: chainPkg("QA passed", "agent-architect"), TotalCostUSD: 0.02},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-dev"
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("QA passed", res.FinalHandoff.Goal)
	s.Equal("agent-architect", res.TerminalAgentID)
	s.Equal(4, res.Steps)

	// The rework run went back to the author with the package and the
	// receiver diagnostics attached.
	s.Require().Len(s.taskRunInputs, 4)
	rework := s.taskRunInputs[2]
	s.Equal("agent-dev", rework.AgentID)
	s.Equal("agent-dev", rework.IncomingHandoff.Metadata.SourceAgentID)
	s.Equal([]string{"acceptance_criteria"}, rework.IncomingFeedback)
	s.Equal(2, rework.ChainStep)

	routed := s.routedEvents()
	s.Require().Len(routed, 4)
	s.NotContains(routed[0].Details, "returned_to_sender")
	s.True(routed[1].Details["returned_to_sender"].(bool))
	s.Equal(routed[1].Details["to_agent_id"], "agent-dev")
	// Details round-trip through the data converter, so typed slices come
	// back as []interface{}.
	s.Equal([]interface{}{"acceptance_criteria"}, routed[1].Details["missing"])
	s.NotContains(routed[2].Details, "returned_to_sender")
	s.Equal(routed[3].Details["terminal"], true)
}

func (s *ChainSuite) TestTerminalPackage_MustBeComplete() {
	team := chainTeam(handoff.TopologyOrchestrated)
	incomplete := chainPkg("Draft", "agent-pm")
	incomplete.AcceptanceCriteria = nil
	s.mockTaskRun(
		&TaskRunResult{Handoff: incomplete},
		&TaskRunResult{Handoff: chainPkg("Draft v2", "agent-pm")},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Equal("Draft v2", res.FinalHandoff.Goal)
	s.Equal(2, res.Steps)

	// The incomplete terminal package went back to its author as a rework
	// input instead of leaving the chain.
	s.Require().Len(s.taskRunInputs, 2)
	rework := s.taskRunInputs[1]
	s.Equal([]string{"acceptance_criteria"}, rework.IncomingFeedback)
	s.Equal(incomplete, rework.IncomingHandoff)

	routed := s.routedEvents()
	s.Require().Len(routed, 2)
	s.True(routed[0].Details["returned_to_sender"].(bool))
	s.Equal(routed[1].Details["terminal"], true)
}

func (s *ChainSuite) TestMaxChainLength_StopsTheLoop() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("X", "agent-architect")})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-architect"
		in.MaxChainLength = 1
	}))
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "max length 1")
}

func (s *ChainSuite) TestRequiresWorkspace() {
	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(chainTeam(handoff.TopologyOrchestrated), func(in *ChainInput) {
		in.WorkspaceID = uuid.Nil
	}))
	s.Error(s.env.GetWorkflowError())
	s.Empty(s.auditEvents.events)
}

func (s *ChainSuite) TestUnsupportedTopology() {
	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(chainTeam("collaborative")))
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "unsupported team topology")
}

func (s *ChainSuite) TestRejectedPackageWithoutSource() {
	team := chainTeam(handoff.TopologyOrchestrated)
	orphan := chainPkg("Orphan", "")
	orphan.Metadata = nil
	s.mockTaskRun(&TaskRunResult{Rejected: true, Missing: []string{"goal"}})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-qa"
		in.InitialPackage = orphan
	}))
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "no source agent to return to")
}

// TestChainInput_DefaultChainLength guards the default loop bound.
func TestDefaultMaxChainLength(t *testing.T) {
	if DefaultMaxChainLength != 5 {
		t.Fatalf("DefaultMaxChainLength = %d, want 5", DefaultMaxChainLength)
	}
}
