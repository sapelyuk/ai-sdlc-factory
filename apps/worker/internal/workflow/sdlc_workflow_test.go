package workflow

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// SDLCWorkflowSuite tests the gated and non-gated paths of SDLCWorkflow
// with mocked child workflows (issue #126).
type SDLCWorkflowSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env         *testsuite.TestWorkflowEnvironment
	auditEvents *mockAuditActivity
}

func TestSDLCWorkflowSuite(t *testing.T) {
	suite.Run(t, new(SDLCWorkflowSuite))
}

func (s *SDLCWorkflowSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.auditEvents = &mockAuditActivity{}
	s.env.RegisterActivityWithOptions(s.auditEvents.Execute, activity.RegisterOptions{
		Name: AuditRecordActivityName,
	})
	// Only register SDLCWorkflow itself. TaskRunWorkflow and ChainWorkflow
	// are mocked via OnWorkflow so their internal activities (AgentStep, etc.)
	// do not need to be registered.
	s.env.RegisterWorkflow(SDLCWorkflow)
}

func (s *SDLCWorkflowSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// sdlcPkg builds a minimal valid handoff package.
func sdlcPkg(goal string) *handoff.HandoffPackage {
	return &handoff.HandoffPackage{
		Goal:                 goal,
		ContextRefs:          []string{"sem://project/1"},
		AcceptanceCriteria:   []string{"criterion"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
		Metadata:             &handoff.HandoffMetadata{SourceAgentID: "agent-dev"},
	}
}

// sdlcSingleAgentTeam builds a minimal single-agent team wrapping agent-dev.
func sdlcSingleAgentTeam() *handoff.TeamConfig {
	return &handoff.TeamConfig{
		ID:       "team-sdlc-1",
		Topology: handoff.TopologyOrchestrated,
		Lead:     &handoff.TeamMember{AgentID: "agent-dev", RoleName: "software-engineer", Profile: "code-fast"},
		Members: []handoff.TeamMember{
			{AgentID: "agent-dev", RoleName: "software-engineer", Profile: "code-fast"},
		},
	}
}

// TestSDLCWorkflow_NoGate_BackwardCompat verifies that when Gate is nil the
// workflow calls TaskRunWorkflow directly and does not invoke ChainWorkflow.
func (s *SDLCWorkflowSuite) TestSDLCWorkflow_NoGate_BackwardCompat() {
	// Mock TaskRunWorkflow as child: return a valid handoff.
	expectedHandoff := sdlcPkg("SDLC done")
	s.env.OnWorkflow(TaskRunWorkflow, mock.Anything, mock.Anything).
		Return(&TaskRunResult{Handoff: expectedHandoff, TotalCostUSD: 0.01}, nil).Once()

	input := SDLCInput{
		TaskRunID:         "sdlc-nogate-1",
		WorkspaceID:       uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		Profile:           "code-fast",
		AgentID:           "agent-dev",
		TaskDesc:          "implement feature",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
		Gate:              nil,
	}

	s.env.ExecuteWorkflow(SDLCWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *handoff.HandoffPackage
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Require().NotNil(result)
	s.Equal("SDLC done", result.Goal)

	// The mock was set with .Once(); AssertExpectations in AfterTest verifies
	// it was consumed exactly once. ChainWorkflow is not mocked here, so if
	// it had been called the test would have panicked.
}

// TestSDLCWorkflow_WithGate_GoesThroughChainWorkflow verifies that when Gate
// is set the workflow routes through ChainWorkflow and returns the final
// handoff from ChainResult.
func (s *SDLCWorkflowSuite) TestSDLCWorkflow_WithGate_GoesThroughChainWorkflow() {
	validPkg := sdlcPkg("gated output")
	chainResult := &ChainResult{
		FinalHandoff:    validPkg,
		TerminalAgentID: "agent-dev",
		Steps:           1,
		TotalCostUSD:    0.02,
		Gate:            &GateOutcome{Passed: true},
	}

	// Capture the ChainInput passed by SDLCWorkflow.
	var capturedInput *ChainInput
	s.env.OnWorkflow(ChainWorkflow, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			in := args.Get(1).(ChainInput)
			capturedInput = &in
		}).
		Return(chainResult, nil).Once()

	input := SDLCInput{
		TaskRunID:         "sdlc-gate-1",
		WorkspaceID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		RunID:             "run-1",
		TaskID:            "task-1",
		AgentID:           "agent-dev",
		Profile:           "code-fast",
		TaskDesc:          "implement feature",
		ProjectID:         "proj-1",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
		Gate:              &GateSpec{Name: gates.GateCIGreen},
		Team:              sdlcSingleAgentTeam(),
		RequestedBy:       uuid.MustParse("33333333-3333-4333-8333-333333333333"),
	}

	s.env.ExecuteWorkflow(SDLCWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *handoff.HandoffPackage
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Require().NotNil(result)
	s.Equal("gated output", result.Goal)

	// Verify the ChainInput was populated correctly from SDLCInput.
	s.Require().NotNil(capturedInput, "ChainWorkflow must have been called")
	s.Equal("run-1", capturedInput.RunID)
	s.Equal("task-1", capturedInput.TaskID)
	s.Equal("agent-dev", capturedInput.InitialAgentID)
	s.Equal("implement feature", capturedInput.TaskDescription)
	s.Require().NotNil(capturedInput.Gate)
	s.Equal(gates.GateCIGreen, capturedInput.Gate.Name)
	s.Equal(uuid.MustParse("33333333-3333-4333-8333-333333333333"), capturedInput.RequestedBy)

	// TaskRunWorkflow was not called directly — ChainWorkflow mock was
	// consumed instead. The ChainWorkflow mock was set with .Once();
	// AssertExpectations in AfterTest verifies it was consumed.
}

// TestSDLCWorkflow_FailingGate_BlocksTransition verifies that when
// ChainWorkflow returns a gate-blocked error, SDLCWorkflow propagates it.
func (s *SDLCWorkflowSuite) TestSDLCWorkflow_FailingGate_BlocksTransition() {
	gateErr := fmt.Errorf(`workflow: gate "ci_green" blocked the transition: ci red`)

	s.env.OnWorkflow(ChainWorkflow, mock.Anything, mock.Anything).
		Return((*ChainResult)(nil), gateErr).Once()

	input := SDLCInput{
		TaskRunID:         "sdlc-gate-fail-1",
		WorkspaceID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		AgentID:           "agent-dev",
		Profile:           "code-fast",
		TaskDesc:          "implement feature",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
		Gate:              &GateSpec{Name: gates.GateCIGreen},
		Team:              sdlcSingleAgentTeam(),
	}

	s.env.ExecuteWorkflow(SDLCWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())

	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	// The error message must contain "gate" (from the wrapped chain error)
	// and the reason "ci red".
	errMsg := err.Error()
	s.Contains(errMsg, "gate")
	s.Contains(errMsg, "ci red")
}

// TestSDLCWorkflow_GateSetButTeamMissing_Error verifies that when Gate is set
// but Team is nil, the workflow fails fast with a descriptive error.
func (s *SDLCWorkflowSuite) TestSDLCWorkflow_GateSetButTeamMissing_Error() {
	input := SDLCInput{
		TaskRunID:         "sdlc-noteam-1",
		WorkspaceID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		AgentID:           "agent-dev",
		Profile:           "code-fast",
		TaskDesc:          "implement feature",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
		Gate:              &GateSpec{Name: gates.GateCIGreen},
		Team:              nil,
	}

	s.env.ExecuteWorkflow(SDLCWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())

	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "Team is required when Gate is set")
}

// TestSDLCWorkflow_GatedPathReturnsNilHandoff_Error verifies the defensive
// check for a chain result that carries no handoff.
func (s *SDLCWorkflowSuite) TestSDLCWorkflow_GatedPathReturnsNilHandoff_Error() {
	s.env.OnWorkflow(ChainWorkflow, mock.Anything, mock.Anything).
		Return(&ChainResult{FinalHandoff: nil, Gate: &GateOutcome{Passed: true}}, nil).Once()

	input := SDLCInput{
		TaskRunID:         "sdlc-nilhandoff-1",
		WorkspaceID:       uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		AgentID:           "agent-dev",
		Profile:           "code-fast",
		TaskDesc:          "implement feature",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
		Gate:              &GateSpec{Name: gates.GateCIGreen},
		Team:              sdlcSingleAgentTeam(),
	}

	s.env.ExecuteWorkflow(SDLCWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())

	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "produced no handoff")
}
