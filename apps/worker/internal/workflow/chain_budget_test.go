package workflow

import (
	"context"

	tdactivity "github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
	"go.temporal.io/sdk/activity"
)

// budgetWarningEvents returns the budget_warning events recorded by the chain.
func (s *ChainSuite) budgetWarningEvents() []audit.Event {
	var out []audit.Event
	for _, ev := range s.auditEvents.events {
		if ev.Action == sdkaudit.ActionBudgetWarning {
			out = append(out, ev)
		}
	}
	return out
}

// budgetExceededEvents returns the budget_exceeded events recorded by the chain.
func (s *ChainSuite) budgetExceededEvents() []audit.Event {
	var out []audit.Event
	for _, ev := range s.auditEvents.events {
		if ev.Action == sdkaudit.ActionBudgetExceeded {
			out = append(out, ev)
		}
	}
	return out
}

// TestRunBudgetExceededCost_AbortsMidChain verifies that the chain stops as
// soon as the accumulated TaskRun cost reaches the Run limit, before starting
// another agent (issue #23, NFR-9).
func (s *ChainSuite) TestRunBudgetExceededCost_AbortsMidChain() {
	team := chainTeam(handoff.TopologyOrchestrated)
	// architect -> lead -> ... : the first package is not terminal, so the
	// chain would continue, but the cost limit stops it after the second run.
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Architecture ready", "agent-architect"), Steps: 3, TotalCostUSD: 60},
		&TaskRunResult{Handoff: chainPkg("Plan approved", "agent-pm"), Steps: 2, TotalCostUSD: 50},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-architect"
		in.MaxCostUSD = 100
	}))
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "run budget exceeded (cost 110.0000 >= 100.0000) at chain step 1")

	// The abort is attributable: chain_aborted with the run budget reason and
	// a budget_exceeded event are both written before the workflow fails.
	aborted := s.abortedEvents()
	s.Require().Len(aborted, 1)
	s.Equal("run_budget_exceeded_cost", aborted[0].Details["reason"])
	exceeded := s.budgetExceededEvents()
	s.Require().Len(exceeded, 1)
	s.Equal("run", exceeded[0].Details["level"])
	s.Equal("usd", exceeded[0].Details["unit"])
	s.Equal(float64(110), exceeded[0].Details["current"])

	// No third TaskRun was started after the breach.
	s.Len(s.taskRunInputs, 2)
}

// TestRunBudgetExceededSteps_AbortsMidChain is the step-counter counterpart of
// the cost test.
func (s *ChainSuite) TestRunBudgetExceededSteps_AbortsMidChain() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Architecture ready", "agent-architect"), Steps: 6, TotalCostUSD: 0.1},
		&TaskRunResult{Handoff: chainPkg("Plan approved", "agent-pm"), Steps: 6, TotalCostUSD: 0.1},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-architect"
		in.MaxSteps = 10
	}))
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "run budget exceeded (steps 12 >= 10) at chain step 1")

	aborted := s.abortedEvents()
	s.Require().Len(aborted, 1)
	s.Equal("run_budget_exceeded_steps", aborted[0].Details["reason"])
	exceeded := s.budgetExceededEvents()
	s.Require().Len(exceeded, 1)
	s.Equal("steps", exceeded[0].Details["unit"])
}

// TestRunBudgetWarning80_AuditEmitted verifies the run-level 80% warning fires
// once per Run when the aggregate crosses the threshold (issue #23).
func (s *ChainSuite) TestRunBudgetWarning80_AuditEmitted() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.mockTaskRun(
		&TaskRunResult{Handoff: chainPkg("Architecture ready", "agent-architect"), Steps: 1, TotalCostUSD: 85},
		&TaskRunResult{Handoff: chainPkg("Plan approved", "agent-pm"), Steps: 1, TotalCostUSD: 5},
	)

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.InitialAgentID = "agent-architect"
		in.MaxCostUSD = 100
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	warnings := s.budgetWarningEvents()
	s.Require().Len(warnings, 1, "the 80% run warning must fire exactly once")
	s.Equal("run", warnings[0].Details["level"])
	s.Equal("usd", warnings[0].Details["unit"])
	s.Equal(float64(85), warnings[0].Details["current"])
	s.Equal(float64(100), warnings[0].Details["max"])
	s.Equal(sdkaudit.SystemActor("worker"), warnings[0].ActorID)
}

// TestDayBudgetExceeded_FailsFastBeforeChain verifies that a spent day budget
// aborts the chain before any TaskRun is executed (issue #23).
func (s *ChainSuite) TestDayBudgetExceeded_FailsFastBeforeChain() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.env.RegisterActivityWithOptions(func(_ context.Context, in tdactivity.BudgetDailyCheckInput) (*tdactivity.BudgetDailyCheckOutput, error) {
		s.Require().Equal(50.0, in.MaxDailyUSD)
		return &tdactivity.BudgetDailyCheckOutput{SpentToday: 60, Exceeded: true}, nil
	}, activity.RegisterOptions{Name: BudgetDailyCheckActivityName})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.DayMaxCostUSD = 50
	}))
	s.True(s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	s.Require().Error(err)
	s.Contains(err.Error(), "budget_exceeded_daily")

	// No agent work started, and the stop is in the audit chain.
	s.Empty(s.taskRunInputs)
	exceeded := s.budgetExceededEvents()
	s.Require().Len(exceeded, 1)
	s.Equal("day", exceeded[0].Details["level"])
}

// TestDayBudgetWarning80_AuditEmitted verifies the day-level 80% warning is
// recorded before the chain starts.
func (s *ChainSuite) TestDayBudgetWarning80_AuditEmitted() {
	team := chainTeam(handoff.TopologyOrchestrated)
	s.env.RegisterActivityWithOptions(func(_ context.Context, _ tdactivity.BudgetDailyCheckInput) (*tdactivity.BudgetDailyCheckOutput, error) {
		return &tdactivity.BudgetDailyCheckOutput{SpentToday: 45, Warning80: true}, nil
	}, activity.RegisterOptions{Name: BudgetDailyCheckActivityName})
	s.mockTaskRun(&TaskRunResult{
		Handoff:      chainPkg("Design reviewed", "agent-pm"),
		Steps:        1,
		TotalCostUSD: 0.01,
	})

	s.env.ExecuteWorkflow(ChainWorkflow, s.chainInput(team, func(in *ChainInput) {
		in.DayMaxCostUSD = 50
	}))
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	warnings := s.budgetWarningEvents()
	s.Require().Len(warnings, 1)
	s.Equal("day", warnings[0].Details["level"])
	s.Equal(float64(45), warnings[0].Details["current"])
}
