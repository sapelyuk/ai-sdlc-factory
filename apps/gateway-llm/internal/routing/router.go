// Package routing implements profile-based routing with per-(provider, model)
// fallback and cooldown, per ADR-021.
package routing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/providers"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/repository"
)

// CallMeta carries attribution identifiers for a logical call.
type CallMeta struct {
	RunID   *uuid.UUID
	TaskID  *uuid.UUID
	AgentID *uuid.UUID
}

// Result is the outcome of a routed chat call. It is returned both on success
// and on total failure so that the handler can expose logical_call_id.
type Result struct {
	LogicalCall *domain.LogicalCall
	Content     string
}

// catalogSource abstracts the model catalog for testability.
type catalogSource interface {
	For(ctx context.Context, profileName string) ([]domain.ModelProfile, error)
}

// callRepo abstracts persistence operations needed by the router.
type callRepo interface {
	HealthFor(ctx context.Context, keys []repository.ModelKey) (map[repository.ModelKey]*domain.ModelHealth, error)
	CreateLogicalCall(ctx context.Context, call *domain.LogicalCall) error
	FinishLogicalCall(ctx context.Context, call *domain.LogicalCall) error
	InsertAttempt(ctx context.Context, a *domain.Attempt) error
	RecordSuccess(ctx context.Context, key repository.ModelKey) error
	RecordAvailabilityFailure(ctx context.Context, key repository.ModelKey, errType string, cooldownUntil time.Time) error
	RecordConfigError(ctx context.Context, key repository.ModelKey, errType string) error
}

// Router routes a chat request across the models of a capability profile.
type Router struct {
	catalog   catalogSource
	repo      callRepo
	providers map[string]providers.Provider
	now       func() time.Time
}

// New creates a Router.
func New(cat catalogSource, repo callRepo, providers map[string]providers.Provider) *Router {
	return &Router{catalog: cat, repo: repo, providers: providers, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock overrides the clock (used in tests).
func (r *Router) SetClock(fn func() time.Time) { r.now = fn }

// Chat executes the routing loop for a capability profile:
//  1. Load catalog entries for the profile, ordered by priority.
//  2. Skip entries whose (provider, model) is in cooldown.
//  3. Try entries in order. 404/401/403/400 → fallback without cooldown;
//     429/5xx/timeout → cooldown, then fallback.
//
// Dual-level cost accounting (ADR-021): final_cost_usd is the cost of the
// successful attempt, total_cost_usd is the sum over all attempts.
func (r *Router) Chat(ctx context.Context, profileName string, req domain.ChatRequest, meta CallMeta) (*Result, error) {
	entries, err := r.catalog.For(ctx, profileName)
	if err != nil {
		return nil, err
	}

	// Load health state for all candidate (provider, model) pairs.
	keys := make([]repository.ModelKey, 0, len(entries))
	for _, e := range entries {
		keys = append(keys, repository.ModelKey{Provider: e.Provider, ModelID: e.ModelID})
	}
	health, err := r.repo.HealthFor(ctx, keys)
	if err != nil {
		return nil, err
	}

	now := r.now()
	available := make([]domain.ModelProfile, 0, len(entries))
	for _, e := range entries {
		if h := health[repository.ModelKey{Provider: e.Provider, ModelID: e.ModelID}]; h != nil && h.IsCoolingDown(now) {
			continue
		}
		available = append(available, e)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("profile %q: %w", profileName, domain.ErrNoAvailable)
	}

	// Attribution layer: one logical call per request. Created in 'partial'
	// state and finalized at the end of the loop.
	call := &domain.LogicalCall{
		ProfileName: profileName,
		RunID:       meta.RunID,
		TaskID:      meta.TaskID,
		AgentID:     meta.AgentID,
		StartedAt:   now,
	}
	if err := r.repo.CreateLogicalCall(ctx, call); err != nil {
		return nil, err
	}

	// If the calling context is canceled (client disconnect) or a provider
	// panics, this deferred finalizer detaches from the request context and
	// writes the row as 'failed' within a 5-second budget. This prevents
	// orphaned 'partial' rows (NFR-3: cost attribution reliability).
	finalized := false
	defer func() {
		if finalized {
			return
		}
		finalizeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		call.Status = "failed"
		if call.CompletedAt == nil {
			t := r.now()
			call.CompletedAt = &t
		}
		if err := r.repo.FinishLogicalCall(finalizeCtx, call); err != nil {
			log.Printf("ERROR: failed to finalize partial logical call %s: %v", call.ID, err)
		}
	}()

	var lastErr error
	for i, entry := range available {
		key := repository.ModelKey{Provider: entry.Provider, ModelID: entry.ModelID}
		res, attemptErr := r.tryAttempt(ctx, call, i+1, entry, key, req, health)
		if attemptErr == nil {
			finalized = true // tryAttempt already called FinishLogicalCall
			return res, nil
		}
		lastErr = attemptErr
	}

	// Every attempt failed — persist the logical call as failed.
	completed := r.now()
	call.Status = "failed"
	call.AttemptCount = len(available)
	call.CompletedAt = &completed
	if err := r.repo.FinishLogicalCall(ctx, call); err != nil {
		log.Printf("ERROR: failed to persist failed logical call %s: %v", call.ID, err)
	} else {
		finalized = true
	}
	return &Result{LogicalCall: call}, fmt.Errorf("%w: %w", domain.ErrAllFailed, lastErr)
}

// tryAttempt performs one provider attempt and updates health, attempt and
// (on success) logical call records. Returns the result or the attempt error.
func (r *Router) tryAttempt(
	ctx context.Context,
	call *domain.LogicalCall,
	attemptNum int,
	entry domain.ModelProfile,
	key repository.ModelKey,
	req domain.ChatRequest,
	health map[repository.ModelKey]*domain.ModelHealth,
) (*Result, error) {
	started := r.now()

	provider, ok := r.providers[entry.Provider]
	if !ok {
		// Provider not configured on this deployment: config error, no cooldown.
		err := fmt.Errorf("provider %q is not configured", entry.Provider)
		class := providers.ErrUnknown
		if insertErr := r.insertAttempt(ctx, call, attemptNum, entry, class.String(), err.Error(), nil, nil, started, 0); insertErr != nil {
			log.Printf("ERROR: %v", insertErr)
		}
		if err := r.repo.RecordConfigError(ctx, key, class.String()); err != nil {
			log.Printf("ERROR: %v", err)
		}
		return nil, err
	}

	resp, err := provider.Chat(ctx, entry.ModelID, req)
	latencyMs := int(r.now().Sub(started).Milliseconds())

	if err == nil {
		cost := ComputeCost(entry, resp.TokensIn, resp.TokensOut)
		tokensIn, tokensOut := resp.TokensIn, resp.TokensOut
		completed := r.now()
		if insertErr := r.insertAttempt(ctx, call, attemptNum, entry, "succeeded", "", &tokensIn, &tokensOut, started, latencyMs, &cost); insertErr != nil {
			log.Printf("ERROR: %v", insertErr)
		}
		if err := r.repo.RecordSuccess(ctx, key); err != nil {
			log.Printf("ERROR: %v", err)
		}

		// Finalize the logical call: final_* = successful attempt,
		// total_* = sum over all attempts (including this one).
		call.ModelIDUsed = entry.ModelID
		call.ProviderUsed = entry.Provider
		call.Status = "succeeded"
		call.AttemptCount = attemptNum
		call.FinalTokensIn = &tokensIn
		call.FinalTokensOut = &tokensOut
		call.FinalCostUSD = &cost
		call.TotalTokensIn += tokensIn
		call.TotalTokensOut += tokensOut
		call.TotalCostUSD = round4(call.TotalCostUSD + cost)
		call.CompletedAt = &completed
		if err := r.repo.FinishLogicalCall(ctx, call); err != nil {
			return nil, fmt.Errorf("persist succeeded logical call: %w", err)
		}
		return &Result{LogicalCall: call, Content: resp.Content}, nil
	}

	// Failed attempt.
	class := providers.Classify(err)
	tokensIn, tokensOut := 0, 0
	var cost float64

	// If the error carries partial usage (e.g. provider billed tokens before 429),
	// accumulate toward total_cost_usd (NFR-9).
	var usageErr *providers.UsageError
	if errors.As(err, &usageErr) {
		tokensIn = usageErr.TokensIn
		tokensOut = usageErr.TokensOut
		cost = ComputeCost(entry, tokensIn, tokensOut)
	}

	if insertErr := r.insertAttempt(ctx, call, attemptNum, entry, class.String(), err.Error(), &tokensIn, &tokensOut, started, latencyMs, &cost); insertErr != nil {
		log.Printf("ERROR: %v", insertErr)
	}

	// Accumulate partial costs toward total.
	call.TotalTokensIn += tokensIn
	call.TotalTokensOut += tokensOut
	call.TotalCostUSD = round4(call.TotalCostUSD + cost)

	if providers.ShouldCooldown(class) {
		// Availability event: extend exponential backoff (60s → 4h, jitter).
		failures := 1
		if h := health[key]; h != nil {
			failures = h.ConsecutiveFailures + 1
		}
		cooldownUntil := r.now().Add(NextCooldown(failures))
		if err := r.repo.RecordAvailabilityFailure(ctx, key, class.String(), cooldownUntil); err != nil {
			log.Printf("ERROR: %v", err)
		}
	} else {
		// Config/auth event (404/401/403/400): log + alert, no cooldown.
		log.Printf("ALERT: provider %s model %s: %v (class %s, no cooldown)",
			entry.Provider, entry.ModelID, err, class)
		if err := r.repo.RecordConfigError(ctx, key, class.String()); err != nil {
			log.Printf("ERROR: %v", err)
		}
	}
	return nil, err
}

// insertAttempt persists a finished attempt row.
func (r *Router) insertAttempt(
	ctx context.Context,
	call *domain.LogicalCall,
	attemptNum int,
	entry domain.ModelProfile,
	status, errMsg string,
	tokensIn, tokensOut *int,
	started time.Time,
	latencyMs int,
	costUSD ...*float64,
) error {
	completed := r.now()
	attempt := &domain.Attempt{
		LogicalCallID: call.ID,
		AttemptNum:    attemptNum,
		Provider:      entry.Provider,
		ModelID:       entry.ModelID,
		Status:        status,
		ErrorMessage:  &errMsg,
		TokensIn:      tokensIn,
		TokensOut:     tokensOut,
		LatencyMs:     &latencyMs,
		StartedAt:     started,
		CompletedAt:   &completed,
	}
	if len(costUSD) > 0 {
		attempt.CostUSD = costUSD[0]
	}
	return r.repo.InsertAttempt(ctx, attempt)
}

// ComputeCost computes the USD cost of an attempt from catalog pricing.
// Pricing is per million tokens; NULL pricing counts as zero.
func ComputeCost(entry domain.ModelProfile, tokensIn, tokensOut int) float64 {
	var cost float64
	if entry.CostPerMtokIn != nil {
		cost += float64(tokensIn) / 1e6 * *entry.CostPerMtokIn
	}
	if entry.CostPerMtokOut != nil {
		cost += float64(tokensOut) / 1e6 * *entry.CostPerMtokOut
	}
	return round4(cost)
}

// round4 rounds to the NUMERIC(10,4) precision of the cost columns.
func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
