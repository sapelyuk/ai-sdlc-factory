package workflow

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// DefaultMaxChainLength bounds the number of TaskRun executions in one chain.
// Every execution counts, including runs that returned a package to its
// sender for rework, so a team that keeps bouncing an incomplete package
// cannot loop forever.
const DefaultMaxChainLength = 5

// ChainInput is the input for ChainWorkflow: one team resolving a task
// through a chain of agent handoffs (issue #21).
type ChainInput struct {
	// WorkspaceID is the tenant that owns the chain. It is mandatory: every
	// audit event of every TaskRun lands in that workspace's chain.
	WorkspaceID uuid.UUID `json:"workspace_id"`
	// RunID is the SDLC run that owns this chain (LLM attribution).
	RunID string `json:"run_id,omitempty"`
	// TaskID is the task being executed (LLM attribution).
	TaskID string `json:"task_id,omitempty"`
	// ProjectID is the semantic-layer project scope (optional).
	ProjectID string `json:"project_id,omitempty"`
	// Team is the resolved team configuration: concrete agent IDs, roles,
	// profiles and the review matrix.
	Team handoff.TeamConfig `json:"team"`
	// InitialAgentID is the agent that starts the chain. When empty the team
	// lead starts (orchestrated topology).
	InitialAgentID string `json:"initial_agent_id,omitempty"`
	// InitialPackage is an optional package produced outside the chain (for
	// example by a preceding PM run). When nil the first agent works from
	// TaskDescription.
	InitialPackage *handoff.HandoffPackage `json:"initial_package,omitempty"`
	// TaskDescription is the natural-language description of the task. It is
	// the task source for the first agent; later agents work from the
	// handoff package they receive.
	TaskDescription string `json:"task_description"`
	// Tools is the list of MCP tools available to the agents.
	Tools []client.ToolDef `json:"tools,omitempty"`
	// MaxChainLength overrides the default chain length (0 = use default).
	MaxChainLength int `json:"max_chain_length,omitempty"`
	// Profile is the fallback LLM profile for members that declare none.
	Profile string `json:"profile,omitempty"`
	// Per-run budget overrides (0 = use defaults).
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
	MaxSteps   int     `json:"max_steps,omitempty"`
	// Defaults are injected from the worker config.
	DefaultMaxCostUSD float64 `json:"default_max_cost_usd"`
	DefaultMaxSteps   int     `json:"default_max_steps"`
}

// ChainResult is the outcome of a completed handoff chain.
type ChainResult struct {
	// FinalHandoff is the package of the agent that finished the chain,
	// stamped with the runtime routing metadata of the last execution.
	FinalHandoff *handoff.HandoffPackage `json:"final_handoff,omitempty"`
	// TerminalAgentID is the agent that produced the final package.
	TerminalAgentID string `json:"terminal_agent_id,omitempty"`
	// Steps is the total number of TaskRun executions, including runs that
	// returned a package to its sender.
	Steps int `json:"steps"`
	// TotalCostUSD is the accumulated LLM cost of all runs.
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// ChainWorkflow routes handoff packages between the agents of one team
// (issue #21): each agent runs TaskRunWorkflow, the produced package is
// validated by the receiver (inside its TaskRunWorkflow) and routed by the
// team router until a terminal package is produced.
//
// Routing decisions and returns-to-sender are recorded in the audit log as
// handoff_routed events; incomplete packages are returned to their author
// (SYSTEM_DESIGN.md §8.4).
func ChainWorkflow(ctx workflow.Context, input ChainInput) (*ChainResult, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    2 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    2 * time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	if input.WorkspaceID == uuid.Nil {
		return nil, fmt.Errorf("workflow: workspace_id is required")
	}
	router, err := handoff.RouterFor(input.Team.Topology)
	if err != nil {
		return nil, err
	}

	// Resolve the starting agent. Without an explicit start the chain begins
	// at the team lead (orchestrated topology).
	startAgentID := input.InitialAgentID
	if startAgentID == "" && input.Team.Lead != nil {
		startAgentID = input.Team.Lead.AgentID
	}
	if startAgentID == "" {
		return nil, fmt.Errorf("workflow: chain has no initial agent and the team has no lead")
	}

	maxChain := input.MaxChainLength
	if maxChain <= 0 {
		maxChain = DefaultMaxChainLength
	}

	// Audit context: routing events are small writes that must never stall
	// the chain.
	auditCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	})
	routerActor := sdkaudit.SystemActor("worker")

	// Chain state.
	currentAgentID := startAgentID
	currentPkg := input.InitialPackage
	var feedback []string
	var steps int
	var totalCost float64

	for step := 0; step < maxChain; step++ {
		result, err := runChainTaskRun(ctx, input, currentAgentID, currentPkg, feedback, step)
		if err != nil {
			return nil, err
		}
		steps++
		totalCost += result.TotalCostUSD

		// The receiver found the package incomplete: return it to its
		// author. The package itself is unchanged; the diagnostics travel
		// with the next run as feedback.
		if result.Rejected {
			sender := currentPkg.Metadata.GetSourceAgentID()
			if sender == "" {
				return nil, fmt.Errorf("workflow: rejected package has no source agent to return to")
			}
			if sender == currentAgentID {
				return nil, fmt.Errorf("workflow: agent %s rejected its own package", currentAgentID)
			}
			recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
				FromAgentID:      currentAgentID,
				ToAgentID:        sender,
				ChainStep:        step,
				ReturnedToSender: true,
				Missing:          result.Missing,
				Goal:             currentPkg.Goal,
			})
			currentAgentID = sender
			feedback = result.Missing
			continue
		}

		pkg := result.Handoff
		feedback = nil

		// Route the produced package. The source identity was stamped by the
		// TaskRunWorkflow from its trusted input.
		route, err := router.Route(pkg, &input.Team)
		if err != nil {
			return nil, fmt.Errorf("workflow: route handoff at chain step %d: %w", step, err)
		}

		if route.Terminal {
			// The chain output is the contract of the next stage: an
			// incomplete package must never leave the chain, so the
			// receiver check runs for the terminal package as well
			// (SYSTEM_DESIGN.md §8.4). The package goes back to its author
			// with the diagnostics as feedback.
			missing, err := handoff.ValidateIncoming(pkg)
			if err != nil {
				return nil, fmt.Errorf("workflow: validate terminal handoff: %w", err)
			}
			if len(missing) > 0 {
				sender := pkg.Metadata.GetSourceAgentID()
				if sender == "" {
					return nil, fmt.Errorf("workflow: incomplete terminal package has no source agent to return to")
				}
				recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
					FromAgentID:      currentAgentID,
					ToAgentID:        sender,
					ChainStep:        step,
					ReturnedToSender: true,
					Missing:          missing,
					Goal:             pkg.Goal,
				})
				currentAgentID = sender
				currentPkg = pkg
				feedback = missing
				continue
			}
			recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
				FromAgentID: pkg.Metadata.GetSourceAgentID(),
				ChainStep:   step,
				Terminal:    true,
				Goal:        pkg.Goal,
			})
			return &ChainResult{
				FinalHandoff:    pkg,
				TerminalAgentID: pkg.Metadata.GetSourceAgentID(),
				Steps:           steps,
				TotalCostUSD:    totalCost,
			}, nil
		}

		// A single next agent is the MVP contract; multiple parallel
		// receivers need an explicit contract change.
		if len(route.NextAgentIDs) != 1 {
			return nil, fmt.Errorf("workflow: routing returned %d next agents, expected 1", len(route.NextAgentIDs))
		}
		next := route.NextAgentIDs[0]
		recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
			FromAgentID: pkg.Metadata.GetSourceAgentID(),
			ToAgentID:   next,
			ChainStep:   step,
			Goal:        pkg.Goal,
		})
		currentAgentID = next
		currentPkg = pkg
	}

	return nil, fmt.Errorf("workflow: chain reached max length %d without a terminal handoff", maxChain)
}

// routedAuditInput carries the fields of one handoff_routed audit event.
type routedAuditInput struct {
	FromAgentID      string
	ToAgentID        string
	ChainStep        int
	Terminal         bool
	ReturnedToSender bool
	Missing          []string
	Goal             string
}

// recordRoutedAudit appends one handoff_routed event. The actor is the
// worker: routing is a platform decision made by the team router, not by an
// agent.
func recordRoutedAudit(ctx workflow.Context, workspaceID uuid.UUID, actor string, in routedAuditInput) {
	key, err := auditIdempotencyKey(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("audit idempotency key failed", "error", err)
		return
	}
	details := map[string]any{
		"from_agent_id": in.FromAgentID,
		"chain_step":    in.ChainStep,
		"terminal":      in.Terminal,
		"goal":          in.Goal,
	}
	if in.ToAgentID != "" {
		details["to_agent_id"] = in.ToAgentID
	}
	if in.ReturnedToSender {
		details["returned_to_sender"] = true
		details["missing"] = in.Missing
	}
	recordAudit(ctx, audit.Event{
		WorkspaceID:    workspaceID,
		ActorID:        actor,
		Action:         sdkaudit.ActionHandoffRouted,
		IdempotencyKey: key,
		// ResourceID is the package source agent: the routed object is the
		// handoff produced by that agent.
		ResourceID: in.FromAgentID,
		Details:    details,
	})
}

// runChainTaskRun starts one TaskRunWorkflow child for the given agent.
func runChainTaskRun(
	ctx workflow.Context,
	input ChainInput,
	agentID string,
	pkg *handoff.HandoffPackage,
	feedback []string,
	chainStep int,
) (*TaskRunResult, error) {
	var taskRunID string
	if err := workflow.SideEffect(ctx, func(workflow.Context) any {
		return uuid.NewString()
	}).Get(&taskRunID); err != nil {
		return nil, fmt.Errorf("workflow: side effect: %w", err)
	}

	profile := input.Profile
	if member := input.Team.FindMember(agentID); member != nil && member.Profile != "" {
		profile = member.Profile
	}

	taskInput := TaskRunInput{
		TaskRunID:         taskRunID,
		WorkspaceID:       input.WorkspaceID,
		RunID:             input.RunID,
		TaskID:            input.TaskID,
		AgentID:           agentID,
		Profile:           profile,
		ProjectID:         input.ProjectID,
		TaskDescription:   input.TaskDescription,
		Tools:             input.Tools,
		MaxCostUSD:        input.MaxCostUSD,
		MaxSteps:          input.MaxSteps,
		DefaultMaxCostUSD: input.DefaultMaxCostUSD,
		DefaultMaxSteps:   input.DefaultMaxSteps,
		ChainStep:         chainStep,
		IncomingHandoff:   pkg,
		IncomingFeedback:  feedback,
	}

	cwo := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: "task-run-" + taskRunID,
	})
	var result *TaskRunResult
	if err := workflow.ExecuteChildWorkflow(cwo, TaskRunWorkflow, taskInput).Get(ctx, &result); err != nil {
		return nil, fmt.Errorf("workflow: task run for agent %s: %w", agentID, err)
	}
	if result == nil {
		return nil, fmt.Errorf("workflow: task run for agent %s returned no result", agentID)
	}
	return result, nil
}
