package workflow

import (
	"context"
	"errors"
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

// twoGateRunnerInput carries two sequential gate waits that share one
// workflow execution, which is the shape needed to prove that a decision
// delivered for the second request survives the first gate.
type twoGateRunnerInput struct {
	First  WaitGateInput
	Second WaitGateInput
}

// twoGateRunner runs WaitGate twice with a workflow-scoped signal buffer, the
// same way ChainWorkflow does.
func twoGateRunner(ctx workflow.Context, in twoGateRunnerInput) ([]GateOutcome, error) {
	var buf ApprovalSignalBuffer
	first := in.First
	first.Signals = &buf
	second := in.Second
	second.Signals = &buf

	var out []GateOutcome
	for _, i := range []WaitGateInput{first, second} {
		o, err := WaitGate(ctx, i)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
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
	createIDs   []string
	createCalls int
	pollStatus  string
	pollReason  string
	pollErr     error
	pollCalls   int
	expireCalls int
	decideCalls int
	decideErr   error
	decidedOK   *bool
}

// nextApprovalID returns the ID the create activity should hand out. A test
// that needs several requests in one execution queues them up front.
func (s *GateSuite) nextApprovalID() string {
	if len(s.createIDs) > 0 {
		id := s.createIDs[0]
		s.createIDs = s.createIDs[1:]
		return id
	}
	return s.createdID
}

func (s *GateSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.checkOut = gateact.GateCheckOutput{}
	s.checkErr = nil
	s.checkCalls = 0
	s.createdID = uuid.New().String()
	s.createIDs = nil
	s.createCalls = 0
	s.pollStatus = string(gates.ApprovalPending)
	s.pollReason = ""
	s.pollErr = nil
	s.pollCalls = 0
	s.expireCalls = 0
	s.decideCalls = 0
	s.decideErr = nil
	s.decidedOK = nil

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.GateCheckInput) (gateact.GateCheckOutput, error) {
		s.checkCalls++
		return s.checkOut, s.checkErr
	}, tdactivity.RegisterOptions{Name: GateCheckActivityName})

	s.env.RegisterActivityWithOptions(func(_ context.Context, _ gateact.ApprovalCreateInput) (gateact.ApprovalCreateOutput, error) {
		s.createCalls++
		return gateact.ApprovalCreateOutput{ApprovalID: s.nextApprovalID()}, nil
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
		return s.decideErr
	}, tdactivity.RegisterOptions{Name: ApprovalDecideActivityName})

	s.env.RegisterWorkflow(gateRunner)
	s.env.RegisterWorkflow(twoGateRunner)
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

// A decision for a request that no gate is waiting for yet must be kept rather
// than dropped: the gate that owns the request has to resolve from the buffer
// instead of waiting for a re-delivery that never comes (review #119 Major 3.8).
func (s *GateSuite) TestWaitGate_SignalForOtherApproval_NotLost() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	firstID := uuid.New().String()
	secondID := uuid.New().String()
	s.createIDs = []string{firstID, secondID}
	// Nobody answers through the queue: if the early decision were dropped the
	// second gate would only reach its deadline.
	s.pollStatus = string(gates.ApprovalPending)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(gates.ApprovalSignalName, gates.ApprovalDecision{
			ApprovalID: secondID,
			Approved:   true,
			DecidedBy:  uuid.New().String(),
			Reason:     "decided before the gate asked",
		})
	}, 2*time.Second)
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(gates.ApprovalSignalName, gates.ApprovalDecision{
			ApprovalID: firstID,
			Approved:   true,
			DecidedBy:  uuid.New().String(),
		})
	}, 4*time.Second)

	s.env.ExecuteWorkflow(twoGateRunner, twoGateRunnerInput{
		First:  WaitGateInput{WorkspaceID: uuid.New(), Name: gates.GateCIGreen, Timeout: time.Hour},
		Second: WaitGateInput{WorkspaceID: uuid.New(), Name: gates.GateProdApproved, Timeout: time.Hour},
	})
	s.Require().True(s.env.IsWorkflowCompleted())
	s.Require().NoError(s.env.GetWorkflowError())

	var outs []GateOutcome
	s.Require().NoError(s.env.GetWorkflowResult(&outs))
	s.Require().Len(outs, 2)
	s.Equal(firstID, outs[0].ApprovalID)
	s.True(outs[0].IsPassed())
	s.Equal(secondID, outs[1].ApprovalID)
	s.True(outs[1].IsPassed(), "an early decision must not be lost: a timeout proves it was")
	s.False(outs[1].Timeout)
	s.Equal(2, s.decideCalls)
	s.Equal(0, s.pollCalls)
	s.Equal(0, s.expireCalls)
}

// A decision that cannot be persisted must fail the workflow: continuing would
// let the gate act on a decision the approval queue never recorded
// (review #119 Major 3.7).
func (s *GateSuite) TestWaitGate_SignalReceived_PersistFails_ReturnsError() {
	s.checkOut = gateact.GateCheckOutput{Pending: true, GateType: string(gates.GateTypeHumanApproval)}
	s.decideErr = errors.New("database unreachable")

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(gates.ApprovalSignalName, gates.ApprovalDecision{
			ApprovalID: s.createdID,
			Approved:   true,
			DecidedBy:  uuid.New().String(),
		})
	}, 5*time.Second)

	s.env.ExecuteWorkflow(gateRunner, WaitGateInput{
		WorkspaceID: uuid.New(), Name: gates.GateProdApproved, Timeout: time.Hour,
	})
	s.Require().True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "persist decision")
	s.Greater(s.decideCalls, 0)
	// The gate must not fall back to polling after the decision was lost.
	s.Equal(0, s.pollCalls)
	s.Equal(0, s.expireCalls)
}

// The signal buffer itself: a stored decision is handed out once, an unknown
// ID is a miss, and a decision without an ID is dropped.
func TestApprovalSignalBuffer(t *testing.T) {
	id := uuid.NewString()
	other := uuid.NewString()

	var buf ApprovalSignalBuffer
	if _, ok := buf.take(id); ok {
		t.Error("take on an empty buffer must miss")
	}

	buf.put(gates.ApprovalDecision{ApprovalID: id, Approved: true})
	d, ok := buf.take(id)
	if !ok || !d.Approved {
		t.Fatalf("take after put = (%v,%v), want the buffered decision", d, ok)
	}
	if _, ok := buf.take(id); ok {
		t.Error("take must consume the buffered decision")
	}

	// A duplicate delivery overwrites the earlier one, keeping the latest.
	buf.put(gates.ApprovalDecision{ApprovalID: id, Approved: true, Reason: "first"})
	buf.put(gates.ApprovalDecision{ApprovalID: id, Approved: false, Reason: "second"})
	d, ok = buf.take(id)
	if !ok || d.Reason != "second" {
		t.Errorf("take after duplicate put = (%v,%q), want the latest decision", ok, d.Reason)
	}

	// A decision with no ID cannot be routed to any gate.
	buf.put(gates.ApprovalDecision{Approved: true})
	if _, ok := buf.take(""); ok {
		t.Error("a decision without an approval ID must not be buffered")
	}

	// Different IDs do not interfere.
	buf.put(gates.ApprovalDecision{ApprovalID: id, Approved: true})
	buf.put(gates.ApprovalDecision{ApprovalID: other, Approved: false})
	if _, ok := buf.take(other); !ok {
		t.Error("take(other) should hit")
	}
	if _, ok := buf.take(id); !ok {
		t.Error("take(id) should still hit after an unrelated take")
	}

	// A nil buffer must be safe to use: WaitGate only substitutes a local one
	// when the caller passed none.
	var nilBuf *ApprovalSignalBuffer
	nilBuf.put(gates.ApprovalDecision{ApprovalID: id})
	if _, ok := nilBuf.take(id); ok {
		t.Error("a nil buffer must never return a decision")
	}
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
