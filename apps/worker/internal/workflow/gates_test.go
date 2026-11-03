package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"
	tdactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	gateact "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
)

// gateRunner is a tiny workflow that executes WaitGate once so the test
// environment can drive it with mocked activities.
func gateRunner(ctx workflow.Context, in WaitGateInput) (GateOutcome, error) {
	return WaitGate(ctx, in)
}

// GateSuite tests the WaitGate workflow helper with mocked gate activities.
type GateSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment

	checkOut    gateact.GateCheckOutput
	checkErr    error
	checkCalls  int
	createdID   string
	createCalls int
	pollStatus  string
	pollReason  string
	pollErr     error
	pollCalls   int
	expireCalls int
	decideCalls int
	decidedOK   *bool
}

func (s *GateSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.checkOut = gateact.GateCheckOutput{}
	s.checkErr = nil
	s.checkCalls = 0
	s.createdID = uuid.New().String()
	s.createCalls = 0
	s.pollStatus = string(gates.ApprovalPending)
	s.pollReason = ""
	s.pollErr = nil
	s.pollCalls = 0
	s.expireCalls = 0
	s.decideCalls = 0
	s.decidedOK = nil

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.GateCheckInput) (gateact.GateCheckOutput, error) {
		s.checkCalls++
		return s.checkOut, s.checkErr
	}, tdactivity.RegisterOptions{Name: GateCheckActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalCreateInput) (gateact.ApprovalCreateOutput, error) {
		s.createCalls++
		return gateact.ApprovalCreateOutput{ApprovalID: s.createdID}, nil
	}, tdactivity.RegisterOptions{Name: ApprovalCreateActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalPollInput) (gateact.ApprovalPollOutput, error) {
		s.pollCalls++
		return gateact.ApprovalPollOutput{Status: s.pollStatus, Reason: s.pollReason}, s.pollErr
	}, tdactivity.RegisterOptions{Name: ApprovalPollActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalExpireInput) error {
		s.expireCalls++
		return nil
	}, tdactivity.RegisterOptions{Name: ApprovalExpireActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, in gateact.ApprovalDecideInput) error {
		s.decideCalls++
		v := in.Approved
		s.decidedOK = &v
		return nil
	}, tdactivity.RegisterOptions{Name: ApprovalDecideActivityName})

	s.env.RegisterWorkflow(gateRunner)
}

func TestGateSuite(t *testing.T) {
	suite.Run(t, new(GateSuite))
}

func (s *GateSuite) run(in WaitGateInput) (GateOutcome, error) {
	s.env.ExecuteWorkflow(gateRunner, in)
	s.True(s.env.IsWorkflowCompleted())
	var out GateOutcome
	err := s.env.GetWorkflowResult(&out)
	if err != nil {
		return GateOutcome{}, err
	}
	return out, nil
}

// An auto gate whose condition holds passes with a single check and never
// touches the approval queue.
func (s *GateSuite) TestAutoGatePassed() {
	s.checkOut = gateact.GateCheckOutput{Approved: true, GateType: string(gates.GateTypeAuto)}
	out, err := s.run(WaitGateInput{WorkspaceID: uuid.New(), Name: gates.GateCIGreen})
	s.NoError(err)
	s.True(out.IsPassed())
	s.Equal(1, s.checkCalls)
	s.Equal(0, s.createCalls)
	s.Equal(0, s.pollCalls)
}

// An auto gate that returns a blocking reason is reported as rejected without
// an approval round.
func (s *GateSuite) TestAutoGateBlocked() {
	s.checkOut = gateact.GateCheckOutput{
		GateType: string(gates.GateTypeAuto),
		Reason:   "CI checks are not green",
	}
	out, err := s.run(WaitGateInput{WorkspaceID: uuid.New(), Name: gates.GateCIGreen})
	s.NoError(err)
	s.True(out.IsBlocked())
	s.Contains(out.Reason, "not green")
	s.Equal(0, s.createCalls)
}

// A human_approval request approved through the queue (poll path) passes.
func (s *GateSuite) TestHumanApprovalPollApproved() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	s.pollStatus = string(gates.ApprovalApproved)

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     time.Hour,
	})
	s.NoError(err)
	s.True(out.IsPassed())
	s.Equal(s.createdID, out.ApprovalID)
	s.Equal(1, s.createCalls)
	s.Equal(1, s.pollCalls)
	s.Equal(0, s.expireCalls)
}

// A rejection carries the reviewer comment as the blocking reason.
func (s *GateSuite) TestHumanApprovalPollRejected() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	s.pollStatus = string(gates.ApprovalRejected)
	s.pollReason = "schema is wrong"

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     time.Hour,
	})
	s.NoError(err)
	s.True(out.Rejected)
	s.Equal("schema is wrong", out.Reason)
}

// A request already expired in the queue blocks the transition.
func (s *GateSuite) TestHumanApprovalPollExpired() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	s.pollStatus = string(gates.ApprovalExpired)

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     time.Hour,
	})
	s.NoError(err)
	s.True(out.Timeout)
}

// Nobody answers before the deadline: the workflow expires the request and
// blocks the transition (SDLC_STATE_MACHINE.md §7).
func (s *GateSuite) TestHumanApprovalDeadline() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	// The queue never receives a decision.
	s.pollStatus = string(gates.ApprovalPending)

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     90 * time.Second,
	})
	s.NoError(err)
	s.True(out.Timeout)
	s.Equal(1, s.expireCalls)
	s.Greater(s.pollCalls, 0)
}

// A decision delivered as a Temporal signal resolves the wait immediately and
// is persisted to the queue.
func (s *GateSuite) TestHumanApprovalSignal() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}

	decider := uuid.New()
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(gates.ApprovalSignalName, gates.ApprovalDecision{
			ApprovalID: s.createdID,
			Approved:   true,
			DecidedBy:  decider.String(),
			Reason:     "looks good to me",
		})
	}, 5*time.Second)

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     time.Hour,
	})
	s.NoError(err)
	s.True(out.IsPassed())
	s.Equal(1, s.decideCalls)
	s.Require().NotNil(s.decidedOK)
	s.True(*s.decidedOK)
	s.Equal(0, s.pollCalls)
}

// A rejection delivered as a signal blocks the transition.
func (s *GateSuite) TestHumanApprovalSignalRejected() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(gates.ApprovalSignalName, gates.ApprovalDecision{
			ApprovalID: s.createdID,
			Approved:   false,
			Reason:     "not this way",
		})
	}, 5*time.Second)

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     time.Hour,
	})
	s.NoError(err)
	s.True(out.Rejected)
	s.Equal("not this way", out.Reason)
	s.Equal(1, s.decideCalls)
	s.Require().NotNil(s.decidedOK)
	s.False(*s.decidedOK)
}

// A signal that belongs to a different approval request must not resolve the
// wait: the workflow keeps polling until the real decision arrives.
func (s *GateSuite) TestHumanApprovalSignalForOtherRequestIgnored() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	s.pollStatus = string(gates.ApprovalApproved)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(gates.ApprovalSignalName, gates.ApprovalDecision{
			ApprovalID: uuid.New().String(),
			Approved:   true,
		})
	}, 2*time.Second)

	out, err := s.run(WaitGateInput{
		WorkspaceID: uuid.New(),
		Name:        gates.GateProdApproved,
		Timeout:     time.Hour,
	})
	s.NoError(err)
	s.True(out.IsPassed())
	s.Equal(0, s.decideCalls)
	s.Greater(s.pollCalls, 0)
}

// A failing initial check is a workflow error: the platform must not treat a
// broken evaluation as an approval.
func (s *GateSuite) TestCheckErrorFailsWorkflow() {
	s.checkErr = context.DeadlineExceeded
	s.env.ExecuteWorkflow(gateRunner, WaitGateInput{
		WorkspaceID: uuid.New(), Name: gates.GateCIGreen,
	})
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

// A create failure is a workflow error too.
func (s *GateSuite) TestCreateErrorFailsWorkflow() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalCreateInput) (gateact.ApprovalCreateOutput, error) {
		return gateact.ApprovalCreateOutput{}, context.DeadlineExceeded
	}, tdactivity.RegisterOptions{Name: ApprovalCreateActivityName})

	s.env.ExecuteWorkflow(gateRunner, WaitGateInput{
		WorkspaceID: uuid.New(), Name: gates.GateProdApproved, Timeout: time.Hour,
	})
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func TestGateOutcomePredicates(t *testing.T) {
	passed := GateOutcome{Passed: true}
	if !passed.IsPassed() {
		t.Error("expected passed")
	}
	if passed.IsBlocked() {
		t.Error("expected not blocked")
	}
	rejected := GateOutcome{Rejected: true}
	if !rejected.IsBlocked() {
		t.Error("expected blocked")
	}
	expired := GateOutcome{Timeout: true}
	if !expired.IsBlocked() {
		t.Error("expected blocked")
	}
}
