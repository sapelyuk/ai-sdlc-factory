package workflow

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"

	gateact "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// ChainGateSuite runs the real ChainWorkflow with a mocked TaskRun child and
// mocked gate activities, so the gate is verified at the transition boundary
// (issue #20).
type ChainGateSuite struct {
	ChainSuite
	checkOut gateact.GateCheckOutput
	checkErr error
}

func TestChainGateSuite(t *testing.T) {
	suite.Run(t, new(ChainGateSuite))
}

func (s *ChainGateSuite) SetupTest() {
	s.ChainSuite.SetupTest()
	s.checkOut = gateact.GateCheckOutput{}
	s.checkErr = nil
	s.registerGateActivities()
}

func (s *ChainGateSuite) registerGateActivities() {
	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.GateCheckInput) (gateact.GateCheckOutput, error) {
		return s.checkOut, s.checkErr
	}, activity.RegisterOptions{Name: GateCheckActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalCreateInput) (gateact.ApprovalCreateOutput, error) {
		return gateact.ApprovalCreateOutput{ApprovalID: uuid.New().String()}, nil
	}, activity.RegisterOptions{Name: ApprovalCreateActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalPollInput) (gateact.ApprovalPollOutput, error) {
		return gateact.ApprovalPollOutput{Status: string(gates.ApprovalPending)}, nil
	}, activity.RegisterOptions{Name: ApprovalPollActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalExpireInput) error {
		return nil
	}, activity.RegisterOptions{Name: ApprovalExpireActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalDecideInput) error {
		return nil
	}, activity.RegisterOptions{Name: ApprovalDecideActivityName})
}

// gateEvents returns the gate_checked audit events recorded by the chain.
func (s *ChainGateSuite) gateEvents() int {
	n := 0
	for _, ev := range s.auditEvents.events {
		if ev.Action == sdkaudit.ActionGateChecked {
			n++
		}
	}
	return n
}

// Without a gate the chain behaves exactly as before issue #20.
func (s *ChainGateSuite) TestNoGateKeepsLegacyBehaviour() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("Done", "agent-pm"), TotalCostUSD: 0.01})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Nil(res.Gate)
	s.Equal(0, s.gateEvents())
}

// A passing auto gate is recorded and the chain result carries the outcome.
func (s *ChainGateSuite) TestAutoGatePassed() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("Done", "agent-pm"), TotalCostUSD: 0.01})
	s.checkOut = gateact.GateCheckOutput{Approved: true, GateType: string(gates.GateTypeAuto)}

	in := s.chainInput(team, func(ci *ChainInput) {
		ci.Gate = &GateSpec{Name: gates.GateCIGreen}
	})
	s.env.ExecuteWorkflow(ChainWorkflow, in)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var res *ChainResult
	s.NoError(s.env.GetWorkflowResult(&res))
	s.Require().NotNil(res.Gate)
	s.True(res.Gate.IsPassed())
	s.Equal(1, s.gateEvents())
}

// A failed auto gate aborts the chain: the platform must not hand incomplete
// evidence to the next stage.
func (s *ChainGateSuite) TestAutoGateBlocked() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("Done", "agent-pm"), TotalCostUSD: 0.01})
	s.checkOut = gateact.GateCheckOutput{GateType: string(gates.GateTypeAuto), Reason: "CI checks are not green"}

	in := s.chainInput(team, func(ci *ChainInput) {
		ci.Gate = &GateSpec{Name: gates.GateCIGreen}
	})
	s.env.ExecuteWorkflow(ChainWorkflow, in)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "gate \"ci_green\" blocked the transition")
	s.Contains(err.Error(), "CI checks are not green")
	s.Equal(1, s.gateEvents())
}

// A pending human_approval gate blocks the chain until the queue answers; with
// the poll stubbed to "pending" the deadline path resolves the wait.
func (s *ChainGateSuite) TestHumanGatePendingBlocksOnDeadline() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("Done", "agent-pm"), TotalCostUSD: 0.01})
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}

	in := s.chainInput(team, func(ci *ChainInput) {
		ci.Gate = &GateSpec{Name: gates.GateProdApproved, TimeoutSeconds: 60}
	})
	s.env.ExecuteWorkflow(ChainWorkflow, in)
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "gate \"prod_approved\" blocked the transition")
	s.Equal(1, s.gateEvents())
}

// A gate activity failure is a workflow failure, never an implicit approval.
func (s *ChainGateSuite) TestGateErrorFailsChain() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(&TaskRunResult{Handoff: chainPkg("Done", "agent-pm"), TotalCostUSD: 0.01})
	s.checkErr = context.DeadlineExceeded

	in := s.chainInput(team, func(ci *ChainInput) {
		ci.Gate = &GateSpec{Name: gates.GateCIGreen}
	})
	s.env.ExecuteWorkflow(ChainWorkflow, in)
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}
