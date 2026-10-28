package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/agent"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
)

// mockAgentStepActivity is the mock activity function registered with the test env.
type mockAgentStepActivity struct {
	mock.Mock
}

func (m *mockAgentStepActivity) Execute(ctx context.Context, input agent.StepInput) (*agent.StepResult, error) {
	args := m.Called(ctx, input)
	if res := args.Get(0); res != nil {
		return res.(*agent.StepResult), args.Error(1)
	}
	return nil, args.Error(1)
}

// TaskRunSuite tests TaskRunWorkflow with mocked activities.
type TaskRunSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env      *testsuite.TestWorkflowEnvironment
	activity *mockAgentStepActivity
}

func (s *TaskRunSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.activity = &mockAgentStepActivity{}
	// Register with name "AgentStep" to match the workflow's ExecuteActivity call.
	s.env.RegisterActivityWithOptions(s.activity.Execute, activity.RegisterOptions{
		Name: "AgentStep",
	})
	s.env.RegisterWorkflow(TaskRunWorkflow)
}

func (s *TaskRunSuite) AfterTest(suiteName, testName string) {
	s.activity.AssertExpectations(s.T())
}

func TestTaskRunSuite(t *testing.T) {
	suite.Run(t, new(TaskRunSuite))
}

// TestTaskRunWorkflow_HappyPath simulates a workflow that completes after 2 steps.
func (s *TaskRunSuite) TestTaskRunWorkflow_HappyPath() {
	step1Result := &agent.StepResult{
		Messages: []client.ChatMessage{
			{Role: "system", Content: "context"},
			{Role: "user", Content: "do task"},
			{Role: "assistant", Content: "I will use a tool"},
		},
		Usage: client.LLMUsage{TotalCostUSD: 0.01},
	}
	step2Result := &agent.StepResult{
		Messages: []client.ChatMessage{
			{Role: "system", Content: "context"},
			{Role: "user", Content: "do task"},
			{Role: "assistant", Content: "I will use a tool"},
			{Role: "tool", Content: "result"},
		},
		Usage: client.LLMUsage{TotalCostUSD: 0.01},
		Handoff: &handoff.HandoffPackage{
			Goal:                 "Done",
			ContextRefs:          []string{"https://example.com/1"},
			AcceptanceCriteria:   []string{"criterion"},
			ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}

	s.activity.On("Execute", mock.Anything, mock.Anything).Return(step1Result, nil).Once()
	s.activity.On("Execute", mock.Anything, mock.Anything).Return(step2Result, nil).Once()

	input := TaskRunInput{
		TaskRunID:         "test-run-1",
		Profile:           "code-fast",
		TaskDescription:   "do task",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
	}

	s.env.ExecuteWorkflow(TaskRunWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *handoff.HandoffPackage
	s.NoError(s.env.GetWorkflowResult(&result))
	s.NotNil(result)
	s.Equal("Done", result.Goal)
}

// TestTaskRunWorkflow_BudgetExceeded tests that the workflow stops on budget exceeded.
func (s *TaskRunSuite) TestTaskRunWorkflow_BudgetExceeded() {
	s.activity.On("Execute", mock.Anything, mock.Anything).
		Return(&agent.StepResult{
			Messages:             []client.ChatMessage{{Role: "user", Content: "task"}},
			Usage:                client.LLMUsage{TotalCostUSD: 0.01},
			BudgetExceeded:       true,
			BudgetExceededReason: "budget: max cost USD exceeded",
		}, nil)

	input := TaskRunInput{
		TaskRunID:         "test-run-2",
		Profile:           "code-fast",
		TaskDescription:   "do task",
		DefaultMaxCostUSD: 1.0,
		DefaultMaxSteps:   50,
	}

	s.env.ExecuteWorkflow(TaskRunWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())

	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "budget exceeded")
}

// TestTaskRunWorkflow_MaxStepsReached tests that the workflow fails when max steps is exhausted.
func (s *TaskRunSuite) TestTaskRunWorkflow_MaxStepsReached() {
	s.activity.On("Execute", mock.Anything, mock.Anything).
		Return(&agent.StepResult{
			Messages: []client.ChatMessage{{Role: "user", Content: "task"}},
			Usage:    client.LLMUsage{TotalCostUSD: 0.01},
		}, nil).Times(3)

	input := TaskRunInput{
		TaskRunID:         "test-run-3",
		Profile:           "code-fast",
		TaskDescription:   "do task",
		DefaultMaxCostUSD: 100.0,
		DefaultMaxSteps:   3,
	}

	s.env.ExecuteWorkflow(TaskRunWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())

	err := s.env.GetWorkflowError()
	s.Error(err)
	s.Contains(err.Error(), "max steps")
}

// TestSDLCWorkflow_Minimal tests that SDLCWorkflow calls TaskRunWorkflow as a child.
func (s *TaskRunSuite) TestSDLCWorkflow_Minimal() {
	expectedHandoff := &handoff.HandoffPackage{
		Goal:                 "SDLC done",
		ContextRefs:          []string{"https://example.com/1"},
		AcceptanceCriteria:   []string{"criterion"},
		ExpectedOutputSchema: json.RawMessage(`{"type":"object"}`),
	}

	s.activity.On("Execute", mock.Anything, mock.Anything).
		Return(&agent.StepResult{
			Messages: []client.ChatMessage{{Role: "user", Content: "task"}},
			Usage:    client.LLMUsage{TotalCostUSD: 0.01},
			Handoff:  expectedHandoff,
		}, nil)

	input := SDLCInput{
		TaskRunID:         "sdlc-run-1",
		Profile:           "code-fast",
		TaskDesc:          "do task",
		DefaultMaxCostUSD: 10.0,
		DefaultMaxSteps:   50,
	}

	s.env.ExecuteWorkflow(SDLCWorkflow, input)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result *handoff.HandoffPackage
	s.NoError(s.env.GetWorkflowResult(&result))
	s.NotNil(result)
	s.Equal("SDLC done", result.Goal)
}
