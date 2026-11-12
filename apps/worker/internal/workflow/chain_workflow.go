package workflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/activity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/client"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/gates"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/handoff"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// DefaultMaxChainLength bounds the number of TaskRun executions in one chain.
// Every execution counts, including runs that returned a package to its
// sender for rework, so a team that keeps bouncing an incomplete package
// cannot loop forever.
const DefaultMaxChainLength = 5

// GateSpec describes the quality gate that must pass before the chain
// output is accepted as the next stage's input (issue #20).
type GateSpec struct {
	// Name is the gate identifier from the registry (e.g. "ci_green").
	Name string `json:"name"`
	// Facts are the evidence the gate is evaluated against.
	Facts gates.Facts `json:"facts,omitempty"`
	// TimeoutSeconds overrides the default human_approval deadline (0 =
	// DefaultApprovalTimeout). Ignored by auto and agent_review gates.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// RiskLevel pins the autonomy level of the gated action (ADR-013).
	RiskLevel string `json:"risk_level,omitempty"`
}

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
	// Per-run budget overrides (0 = use defaults). MaxCostUSD and MaxSteps are
	// enforced by ChainWorkflow mid-chain across all TaskRuns of this Run
	// (issue #23, NFR-9); they are also forwarded as per-TaskRun limits.
	MaxCostUSD float64 `json:"max_cost_usd,omitempty"`
	MaxSteps   int     `json:"max_steps,omitempty"`
	// DayMaxCostUSD is the workspace day budget (TeamSpec.budgets.
	// day_max_cost_usd). When positive the chain checks the day spend before
	// starting (issue #23). 0 disables the check.
	DayMaxCostUSD float64 `json:"day_max_cost_usd,omitempty"`
	// Defaults are injected from the worker config.
	DefaultMaxCostUSD float64 `json:"default_max_cost_usd"`
	DefaultMaxSteps   int     `json:"default_max_steps"`
	// RequestedBy is the human owner who initiated the chain (for audit and
	// approval queue).
	RequestedBy uuid.UUID `json:"requested_by,omitempty"`
	// WorkflowID is the Temporal workflow execution ID.
	WorkflowID string `json:"workflow_id,omitempty"`
	// Gate is the quality gate that must pass before the chain output is
	// accepted as the next stage's input (issue #20). When nil the chain
	// terminal result is accepted without a gate (R0/R1 autonomy).
	Gate *GateSpec `json:"gate,omitempty"`
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
	// Gate is the outcome of the terminal gate check. It is nil when the
	// chain has no gate (R0/R1 autonomy: the result is accepted directly).
	Gate *GateOutcome `json:"gate,omitempty"`
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

	// Day-level budget gate (issue #23): a workspace that has already spent
	// its day budget must not start new work. Checked once before the chain.
	if input.DayMaxCostUSD > 0 {
		var dayCheck activity.BudgetDailyCheckOutput
		if err := workflow.ExecuteActivity(ctx, BudgetDailyCheckActivityName,
			activity.BudgetDailyCheckInput{
				WorkspaceID: input.WorkspaceID,
				MaxDailyUSD: input.DayMaxCostUSD,
			}).Get(ctx, &dayCheck); err != nil {
			return nil, fmt.Errorf("workflow: day budget check: %w", err)
		}
		if dayCheck.Exceeded {
			recordBudgetExceededAudit(auditCtx, input.WorkspaceID, routerActor, budgetExceededAuditInput{
				Level:   "day",
				Current: dayCheck.SpentToday,
				Max:     input.DayMaxCostUSD,
				Unit:    "usd",
			})
			return nil, fmt.Errorf("workflow: budget_exceeded_daily: spent %.4f >= day limit %.4f",
				dayCheck.SpentToday, input.DayMaxCostUSD)
		}
		if dayCheck.Warning80 {
			recordRunBudgetWarningAudit(auditCtx, input.WorkspaceID, routerActor, budgetWarningAuditInput{
				Level:   "day",
				Current: dayCheck.SpentToday,
				Max:     input.DayMaxCostUSD,
				Unit:    "usd",
			})
		}
	}

	// Chain state.
	currentAgentID := startAgentID
	currentPkg := input.InitialPackage
	var feedback []string
	var steps int
	var totalCost float64
	// totalSteps aggregates the ReAct steps of every TaskRun in the chain and
	// is compared with the run-level MaxSteps budget (issue #23). Note it is
	// distinct from the chain-length counter above.
	var totalSteps int
	// runCostWarned / runStepsWarned make the run-level 80% warning fire once
	// per Run.
	var runCostWarned bool
	var runStepsWarned bool
	// reviewedBy lists the reviewers that approved the current package. A
	// rejection resets it because the reworked package starts a new review
	// round.
	var reviewedBy []string
	// pendingNext holds the remaining agents of a fan-out route. The
	// sequential topology runs multiple required reviewers one after another:
	// the Temporal MVP executes one child workflow per chain step, so parallel
	// review would be a separate contract change.
	var pendingNext []string
	// reviewAuthorID is the author of the package under review. A rejection
	// by any reviewer returns the package to this author, not to the previous
	// reviewer in the fan-out.
	var reviewAuthorID string
	// gateSignals buffers approval decisions that arrived for a request no
	// gate is currently waiting for, so a decision is never dropped on the
	// floor of an unrelated gate (review #119 Major 3.8).
	var gateSignals ApprovalSignalBuffer

	for steps < maxChain {
		agentID := currentAgentID

		chainStep := steps
		result, err := runChainTaskRun(ctx, input, agentID, currentPkg, feedback, chainStep)
		if err != nil {
			return nil, err
		}
		steps++
		totalCost += result.TotalCostUSD
		totalSteps += result.Steps

		// Run-level budget enforcement (issue #23, NFR-9): the chain stops as
		// soon as the accumulated spend or step count reaches the Run limit.
		// The check runs before routing so no further TaskRun is started.
		if input.MaxCostUSD > 0 && totalCost >= input.MaxCostUSD {
			recordChainAbortedAudit(auditCtx, input.WorkspaceID, routerActor, chainAbortInput{
				FromAgentID: agentID,
				ChainStep:   chainStep,
				Goal:        chainGoal(result.Handoff, currentPkg),
				Reason:      abortReasonRunBudgetExceededCost,
			})
			recordBudgetExceededAudit(auditCtx, input.WorkspaceID, routerActor, budgetExceededAuditInput{
				Level:     "run",
				Current:   totalCost,
				Max:       input.MaxCostUSD,
				Unit:      "usd",
				ChainStep: chainStep,
			})
			return nil, fmt.Errorf("workflow: run budget exceeded (cost %.4f >= %.4f) at chain step %d",
				totalCost, input.MaxCostUSD, chainStep)
		}
		if input.MaxSteps > 0 && totalSteps >= input.MaxSteps {
			recordChainAbortedAudit(auditCtx, input.WorkspaceID, routerActor, chainAbortInput{
				FromAgentID: agentID,
				ChainStep:   chainStep,
				Goal:        chainGoal(result.Handoff, currentPkg),
				Reason:      abortReasonRunBudgetExceededSteps,
			})
			recordBudgetExceededAudit(auditCtx, input.WorkspaceID, routerActor, budgetExceededAuditInput{
				Level:     "run",
				Current:   float64(totalSteps),
				Max:       float64(input.MaxSteps),
				Unit:      "steps",
				ChainStep: chainStep,
			})
			return nil, fmt.Errorf("workflow: run budget exceeded (steps %d >= %d) at chain step %d",
				totalSteps, input.MaxSteps, chainStep)
		}
		// Run-level 80% warning (once per Run, once per threshold).
		if input.MaxCostUSD > 0 && !runCostWarned && totalCost >= 0.8*input.MaxCostUSD {
			runCostWarned = true
			recordRunBudgetWarningAudit(auditCtx, input.WorkspaceID, routerActor, budgetWarningAuditInput{
				Level:     "run",
				Current:   totalCost,
				Max:       input.MaxCostUSD,
				Unit:      "usd",
				ChainStep: chainStep,
			})
		}
		if input.MaxSteps > 0 && !runStepsWarned && float64(totalSteps) >= 0.8*float64(input.MaxSteps) {
			runStepsWarned = true
			recordRunBudgetWarningAudit(auditCtx, input.WorkspaceID, routerActor, budgetWarningAuditInput{
				Level:     "run",
				Current:   float64(totalSteps),
				Max:       float64(input.MaxSteps),
				Unit:      "steps",
				ChainStep: chainStep,
			})
		}

		// The receiver found the package incomplete: return it to its
		// author. The package itself is unchanged; the diagnostics travel
		// with the next run as feedback.
		if result.Rejected {
			sender := pkgSourceAgentID(currentPkg)
			// A reviewer that rejects a fan-out returns the package to the
			// original author, not to the previous reviewer.
			if reviewAuthorID != "" {
				sender = reviewAuthorID
			}
			if sender == "" {
				return nil, fmt.Errorf("workflow: rejected package has no source agent to return to")
			}
			if sender == agentID {
				return nil, fmt.Errorf("workflow: agent %s rejected its own package", agentID)
			}
			recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
				FromAgentID:      agentID,
				ToAgentID:        sender,
				ChainStep:        chainStep,
				ReturnedToSender: true,
				Missing:          result.Missing,
				Goal:             currentPkg.Goal,
			})
			// The reworked package is a new review round: approvals of the
			// previous round no longer apply.
			reviewedBy = nil
			pendingNext = nil
			reviewAuthorID = ""
			currentAgentID = sender
			feedback = result.Missing
			continue
		}

		pkg := result.Handoff
		feedback = nil

		// Record the approval before routing: the pipeline router counts the
		// reviewers that already approved the package.
		if isReviewer(&input.Team, agentID) {
			reviewedBy = appendUnique(reviewedBy, agentID)
		} else {
			// A non-reviewer produced a new package: earlier approvals
			// described the previous content, so the review round restarts.
			reviewedBy = nil
		}
		stampReviewedBy(pkg, reviewedBy)

		if len(pendingNext) > 0 {
			// Fan-out in progress: hand the package approved by this reviewer
			// to the next one without re-routing.
			currentAgentID = pendingNext[0]
			pendingNext = pendingNext[1:]
			currentPkg = pkg
			continue
		}
		// The fan-out, if any, is complete.
		reviewAuthorID = ""

		// Route the produced package. The source identity was stamped by the
		// TaskRunWorkflow from its trusted input.
		route, err := router.Route(pkg, &input.Team)
		if err != nil {
			if errors.Is(err, handoff.ErrReviewerNotFound) {
				recordChainAbortedAudit(auditCtx, input.WorkspaceID, routerActor, chainAbortInput{
					FromAgentID: pkgSourceAgentID(pkg),
					ChainStep:   chainStep,
					Goal:        pkg.Goal,
					Reason:      abortReasonReviewerNotFound,
				})
			}
			return nil, fmt.Errorf("workflow: route handoff at chain step %d: %w", chainStep, err)
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
				sender := pkgSourceAgentID(pkg)
				if sender == "" {
					return nil, fmt.Errorf("workflow: incomplete terminal package has no source agent to return to")
				}
				recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
					FromAgentID:      agentID,
					ToAgentID:        sender,
					ChainStep:        chainStep,
					ReturnedToSender: true,
					Missing:          missing,
					Goal:             pkg.Goal,
				})
				// The reworked package is a new review round.
				reviewedBy = nil
				currentAgentID = sender
				currentPkg = pkg
				feedback = missing
				continue
			}
			recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
				FromAgentID: pkgSourceAgentID(pkg),
				ChainStep:   chainStep,
				Terminal:    true,
				Goal:        pkg.Goal,
			})

			// Quality gate: the chain output is the input of the next stage,
			// so the gate of that stage is enforced here before the result
			// leaves the workflow (issue #20, SDLC_STATE_MACHINE.md §4).
			// A blocked gate aborts the chain: the platform must never
			// deliver evidence that did not pass its own check.
			var gateOutcome *GateOutcome
			if input.Gate != nil {
				outcome, gateErr := WaitGate(ctx, WaitGateInput{
					WorkspaceID: input.WorkspaceID,
					Name:        input.Gate.Name,
					Facts:       input.Gate.Facts,
					Timeout:     time.Duration(input.Gate.TimeoutSeconds) * time.Second,
					RequestedBy: input.RequestedBy,
					RiskLevel:   input.Gate.RiskLevel,
					WorkflowID:  workflow.GetInfo(ctx).WorkflowExecution.ID,
					Signals:     &gateSignals,
				})
				if gateErr != nil {
					return nil, fmt.Errorf("workflow: gate %q: %w", input.Gate.Name, gateErr)
				}
				recordGateAudit(auditCtx, input.WorkspaceID, routerActor, gateAuditInput{
					GateName:   input.Gate.Name,
					ChainStep:  chainStep,
					Passed:     outcome.Passed,
					Rejected:   outcome.Rejected,
					Timeout:    outcome.Timeout,
					Reason:     outcome.Reason,
					ApprovalID: outcome.ApprovalID,
					Goal:       pkg.Goal,
				})
				if outcome.IsBlocked() {
					return nil, fmt.Errorf("workflow: gate %q blocked the transition: %s",
						input.Gate.Name, outcome.Reason)
				}
				gateOutcome = &outcome
			}

			return &ChainResult{
				FinalHandoff:    pkg,
				TerminalAgentID: pkgSourceAgentID(pkg),
				Steps:           steps,
				TotalCostUSD:    totalCost,
				Gate:            gateOutcome,
			}, nil
		}

		if len(route.NextAgentIDs) == 0 {
			return nil, fmt.Errorf("workflow: routing returned no next agents")
		}
		if len(route.NextAgentIDs) > 1 {
			// Multiple required reviewers: run them sequentially (the
			// Temporal MVP has one child workflow per step) and accept the
			// package only after every one of them approved it. A rejection
			// returns it to the original author.
			reviewAuthorID = pkgSourceAgentID(pkg)
		}
		currentAgentID = route.NextAgentIDs[0]
		pendingNext = append([]string(nil), route.NextAgentIDs[1:]...)
		currentPkg = pkg
		recordRoutedAudit(auditCtx, input.WorkspaceID, routerActor, routedAuditInput{
			FromAgentID: pkgSourceAgentID(pkg),
			ToAgentID:   route.NextAgentIDs[0],
			ToAgentIDs:  route.NextAgentIDs,
			ChainStep:   chainStep,
			Goal:        pkg.Goal,
		})
	}

	return nil, fmt.Errorf("workflow: chain reached max length %d without a terminal handoff", maxChain)
}

// abortReasonReviewerNotFound is the chain_aborted reason written when the
// review matrix cannot be satisfied.
const abortReasonReviewerNotFound = "reviewer_not_found"

// Run-level budget abort reasons (issue #23, NFR-9).
const (
	abortReasonRunBudgetExceededCost  = "run_budget_exceeded_cost"
	abortReasonRunBudgetExceededSteps = "run_budget_exceeded_steps"
)

// chainGoal returns the goal of the produced package, falling back to the
// goal of the package the agent received. It is nil-safe so the budget abort
// can report a goal even for a rejected or empty handoff.
func chainGoal(pkg, incoming *handoff.HandoffPackage) string {
	if pkg != nil {
		return pkg.Goal
	}
	if incoming != nil {
		return incoming.Goal
	}
	return ""
}

// budgetWarningAuditInput carries the fields of one budget_warning event raised
// by the chain (run- or day-level). The per-step warning is written by the
// TaskRunWorkflow with the same action.
type budgetWarningAuditInput struct {
	Level     string
	Current   float64
	Max       float64
	Unit      string
	ChainStep int
}

// recordRunBudgetWarningAudit appends one budget_warning event for a run- or
// day-level threshold crossing. The actor is the worker: the platform observed
// the crossing, not the agent.
func recordRunBudgetWarningAudit(ctx workflow.Context, workspaceID uuid.UUID, actor string, in budgetWarningAuditInput) {
	key, err := auditIdempotencyKey(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("audit idempotency key failed", "error", err)
		return
	}
	recordAudit(ctx, audit.Event{
		WorkspaceID:    workspaceID,
		ActorID:        actor,
		Action:         sdkaudit.ActionBudgetWarning,
		IdempotencyKey: key,
		ResourceID:     in.Level,
		Details: map[string]any{
			"level":      in.Level,
			"threshold":  "80",
			"current":    in.Current,
			"max":        in.Max,
			"unit":       in.Unit,
			"chain_step": in.ChainStep,
		},
	})
}

// budgetExceededAuditInput carries the fields of one budget_exceeded event
// raised when a hard budget limit is reached.
type budgetExceededAuditInput struct {
	Level     string
	Current   float64
	Max       float64
	Unit      string
	ChainStep int
}

// recordBudgetExceededAudit appends one budget_exceeded event. It is written
// before the workflow returns the abort error, so the stop is attributable in
// the audit chain even though the workflow fails.
func recordBudgetExceededAudit(ctx workflow.Context, workspaceID uuid.UUID, actor string, in budgetExceededAuditInput) {
	key, err := auditIdempotencyKey(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("audit idempotency key failed", "error", err)
		return
	}
	recordAudit(ctx, audit.Event{
		WorkspaceID:    workspaceID,
		ActorID:        actor,
		Action:         sdkaudit.ActionBudgetExceeded,
		IdempotencyKey: key,
		ResourceID:     in.Level,
		Details: map[string]any{
			"level":      in.Level,
			"current":    in.Current,
			"max":        in.Max,
			"unit":       in.Unit,
			"chain_step": in.ChainStep,
		},
	})
}

// pkgSourceAgentID returns the package source from the runtime header, empty
// when the package or header is absent.
func pkgSourceAgentID(pkg *handoff.HandoffPackage) string {
	if pkg == nil {
		return ""
	}
	return pkg.Metadata.GetSourceAgentID()
}

// isReviewer reports whether the agent has a role allowed to review and the
// team actually requires a review. A non-reviewer never adds an approval to
// the package.
func isReviewer(team *handoff.TeamConfig, agentID string) bool {
	if team == nil || !team.ReviewMatrix.RequiresReview() {
		return false
	}
	member := team.FindMember(agentID)
	return member != nil && team.ReviewMatrix.AllowsRole(member.RoleName)
}

// appendUnique appends id unless it is already present. Review approvals are a
// set: a reviewer that runs twice must not count as two independent approvals.
func appendUnique(ids []string, id string) []string {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

// stampReviewedBy writes the current approval set into the runtime header so
// the router can count completed reviews. The slice is copied because the
// package crosses a child workflow boundary.
func stampReviewedBy(pkg *handoff.HandoffPackage, reviewedBy []string) {
	if pkg == nil {
		return
	}
	if pkg.Metadata == nil {
		pkg.Metadata = &handoff.HandoffMetadata{}
	}
	pkg.Metadata.ReviewedBy = append([]string(nil), reviewedBy...)
}

// routedAuditInput carries the fields of one handoff_routed audit event.
type routedAuditInput struct {
	FromAgentID      string
	ToAgentID        string
	ToAgentIDs       []string
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
	if len(in.ToAgentIDs) > 0 {
		details["to_agent_ids"] = in.ToAgentIDs
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

// chainAbortInput carries the fields of one chain_aborted audit event.
type chainAbortInput struct {
	FromAgentID string
	ChainStep   int
	Goal        string
	Reason      string
}

// recordChainAbortedAudit appends one chain_aborted event. The chain is
// aborted by the platform, so the actor is the worker. The event is written
// before the workflow returns the routing error, keeping the failure
// attributable in the audit chain.
func recordChainAbortedAudit(ctx workflow.Context, workspaceID uuid.UUID, actor string, in chainAbortInput) {
	key, err := auditIdempotencyKey(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("audit idempotency key failed", "error", err)
		return
	}
	recordAudit(ctx, audit.Event{
		WorkspaceID:    workspaceID,
		ActorID:        actor,
		Action:         sdkaudit.ActionChainAborted,
		IdempotencyKey: key,
		ResourceID:     in.FromAgentID,
		Details: map[string]any{
			"from_agent_id": in.FromAgentID,
			"chain_step":    in.ChainStep,
			"goal":          in.Goal,
			"reason":        in.Reason,
		},
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

	toolsPolicy := ""
	if member := input.Team.FindMember(agentID); member != nil {
		toolsPolicy = member.ToolsPolicy
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
		ToolsPolicy:       toolsPolicy,
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

// gateAuditInput carries the fields of one gate_checked audit event.
type gateAuditInput struct {
	GateName   string
	ChainStep  int
	Passed     bool
	Rejected   bool
	Timeout    bool
	Reason     string
	ApprovalID string
	Goal       string
}

// recordGateAudit appends one gate_checked event. The actor is the worker:
// the platform evaluates the gate, not the agent.
func recordGateAudit(ctx workflow.Context, workspaceID uuid.UUID, actor string, in gateAuditInput) {
	key, err := auditIdempotencyKey(ctx)
	if err != nil {
		workflow.GetLogger(ctx).Warn("audit idempotency key failed", "error", err)
		return
	}
	details := map[string]any{
		"gate_name":  in.GateName,
		"chain_step": in.ChainStep,
		"goal":       in.Goal,
	}
	if in.Passed {
		details["result"] = "passed"
	}
	if in.Rejected {
		details["result"] = "rejected"
		details["reason"] = in.Reason
	}
	if in.Timeout {
		details["result"] = "timeout"
		details["reason"] = in.Reason
	}
	if in.ApprovalID != "" {
		details["approval_id"] = in.ApprovalID
	}
	recordAudit(ctx, audit.Event{
		WorkspaceID:    workspaceID,
		ActorID:        actor,
		Action:         sdkaudit.ActionGateChecked,
		IdempotencyKey: key,
		ResourceID:     in.GateName,
		Details:        details,
	})
}
