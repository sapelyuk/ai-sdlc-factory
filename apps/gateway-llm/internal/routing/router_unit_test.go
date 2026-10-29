// Package routing provides unit tests for the LLM Gateway router without
// requiring Docker. A catalogSource stub supplies model profiles and a
// callRepo stub records persistence operations so the full routing loop
// (fallback, cooldown, dual cost, partial finalization) is exercised.
package routing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/providers"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/repository"
)

// ── Stub implementations ──────────────────────────────────────

// stubCatalog implements catalogSource.
type stubCatalog struct {
	profiles map[string][]domain.ModelProfile
	err      error
}

func (s *stubCatalog) For(ctx context.Context, profileName string) ([]domain.ModelProfile, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.profiles[profileName], nil
}

// stubRepo implements callRepo.
type stubRepo struct {
	logicalCalls []*domain.LogicalCall
	attempts     []*domain.Attempt
	health       map[repository.ModelKey]*domain.ModelHealth
	finished     []*domain.LogicalCall
	created      []*domain.LogicalCall
	// workspaceID mirrors the tenant the real repository reads from the
	// context, so audit events carry a valid workspace.
	workspaceID uuid.UUID
}

func newStubRepo() *stubRepo {
	return &stubRepo{
		health:      make(map[repository.ModelKey]*domain.ModelHealth),
		workspaceID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	}
}

func (s *stubRepo) HealthFor(ctx context.Context, keys []repository.ModelKey) (map[repository.ModelKey]*domain.ModelHealth, error) {
	out := make(map[repository.ModelKey]*domain.ModelHealth, len(keys))
	for _, k := range keys {
		if h := s.health[k]; h != nil {
			cp := *h
			out[k] = &cp
		}
	}
	return out, nil
}

// CreateLogicalCall mirrors the real repository: it is detached from the
// caller context via context.WithoutCancel so a canceled request never loses
// the attribution row.
func (s *stubRepo) CreateLogicalCall(ctx context.Context, call *domain.LogicalCall) error {
	s.created = append(s.created, call)
	// Assign an ID if not already set (mimics DB behavior).
	if call.ID == uuid.Nil {
		call.ID = uuid.New()
	}
	// The real repository stamps the tenant taken from the request context.
	if call.WorkspaceID == uuid.Nil {
		call.WorkspaceID = s.workspaceID
	}
	s.logicalCalls = append(s.logicalCalls, call)
	return nil
}

// FinishLogicalCall mirrors the real repository: it is detached from the
// caller context via context.WithoutCancel so a canceled request can still
// finalize the row. The stub checks ctx.Err() only when the context has NOT
// been detached — this allows tests to verify the defer's detachment.
func (s *stubRepo) FinishLogicalCall(ctx context.Context, call *domain.LogicalCall) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.finished = append(s.finished, call)
	return nil
}

func (s *stubRepo) InsertAttempt(ctx context.Context, a *domain.Attempt) error {
	s.attempts = append(s.attempts, a)
	return nil
}

func (s *stubRepo) RecordSuccess(ctx context.Context, key repository.ModelKey) error {
	return nil
}

func (s *stubRepo) RecordAvailabilityFailure(ctx context.Context, key repository.ModelKey, errType string, cooldownUntil time.Time) error {
	h := s.health[key]
	if h == nil {
		h = &domain.ModelHealth{Provider: key.Provider, ModelID: key.ModelID}
	}
	h.LastErrorType = &errType
	h.CooldownUntil = &cooldownUntil
	h.ConsecutiveFailures++
	s.health[key] = h
	return nil
}

func (s *stubRepo) RecordConfigError(ctx context.Context, key repository.ModelKey, errType string) error {
	h := s.health[key]
	if h == nil {
		h = &domain.ModelHealth{Provider: key.Provider, ModelID: key.ModelID}
	}
	h.LastErrorType = &errType
	s.health[key] = h
	return nil
}

// ── Test helpers ──────────────────────────────────────────────

// makeProfile creates a catalog entry with default capabilities.
func makeProfile(profile, provider, modelID string, priority int) domain.ModelProfile {
	return domain.ModelProfile{
		ProfileName: profile,
		Provider:    provider,
		ModelID:     modelID,
		Priority:    priority,
		Enabled:     true,
	}
}

// modelKey returns the ModelKey for a provider+model pair.
func modelKey(provider, modelID string) repository.ModelKey {
	return repository.ModelKey{Provider: provider, ModelID: modelID}
}

// chatReq is the minimal conversation for router tests.
func chatReq() domain.ChatRequest {
	return domain.ChatRequest{
		Messages: []domain.ChatMessage{{Role: "user", Content: "hi"}},
	}
}

// ── Tests ─────────────────────────────────────────────────────

// TestRouterUnknownProfile covers the catalog branch: an unknown profile
// propagates ErrUnknownProfile through the router to the handler.
func TestRouterUnknownProfile(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{}, err: domain.ErrUnknownProfile}
	repo := newStubRepo()
	rtr := New(cat, repo, nil)

	_, err := rtr.Chat(context.Background(), "nonexistent", chatReq(), CallMeta{})
	if !errors.Is(err, domain.ErrUnknownProfile) {
		t.Fatalf("err = %v, want ErrUnknownProfile", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("logical calls created = %d, want 0 for an unknown profile", len(repo.created))
	}
}

// TestRouterCatalogLoadError covers catalog load failure (e.g. DB down).
func TestRouterCatalogLoadError(t *testing.T) {
	cat := &stubCatalog{err: errors.New("db error")}
	repo := newStubRepo()
	rtr := New(cat, repo, nil)

	_, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if err == nil {
		t.Fatal("expected error from catalog failure")
	}
	if len(repo.created) != 0 {
		t.Errorf("logical calls created = %d, want 0 for catalog failure", len(repo.created))
	}
}

// TestRouterSuccess covers the happy path: a single provider succeeds on
// the first attempt.
func TestRouterSuccess(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": succeededProvider("anthropic", "pong", 10, 5),
	})

	runID := uuid.New()
	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{RunID: &runID})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Content != "pong" {
		t.Errorf("content = %q, want pong", res.Content)
	}
	if res.LogicalCall.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", res.LogicalCall.Status)
	}
	if res.LogicalCall.ModelIDUsed != "model-a" {
		t.Errorf("model_used = %q, want model-a", res.LogicalCall.ModelIDUsed)
	}
	if res.LogicalCall.ProviderUsed != "anthropic" {
		t.Errorf("provider_used = %q, want anthropic", res.LogicalCall.ProviderUsed)
	}
	if res.LogicalCall.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", res.LogicalCall.AttemptCount)
	}
	if res.LogicalCall.TotalTokensIn != 10 || res.LogicalCall.TotalTokensOut != 5 {
		t.Errorf("total tokens = %d/%d, want 10/5", res.LogicalCall.TotalTokensIn, res.LogicalCall.TotalTokensOut)
	}
	if len(repo.attempts) != 1 {
		t.Errorf("attempts = %d, want 1", len(repo.attempts))
	}
	if repo.attempts[0].Status != "succeeded" {
		t.Errorf("attempt status = %q, want succeeded", repo.attempts[0].Status)
	}
	if len(repo.finished) != 1 {
		t.Errorf("finished calls = %d, want 1", len(repo.finished))
	}
	if repo.finished[0].Status != "succeeded" {
		t.Errorf("finished status = %q, want succeeded", repo.finished[0].Status)
	}
}

// TestRouterFallbackOn404_NoCooldown covers ADR-021: a 404 falls back
// without burning the failure counter.
func TestRouterFallbackOn404_NoCooldown(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {
			makeProfile("code-strong", "anthropic", "model-a", 0),
			makeProfile("code-strong", "openai", "model-b", 1),
		},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": failingProvider("anthropic", func(modelID string) error {
			return &providers.HTTPError{StatusCode: 404, Provider: "anthropic", ModelID: modelID}
		}),
		"openai": succeededProvider("openai", "fallback answer", 120, 60),
	})

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Content != "fallback answer" {
		t.Errorf("content = %q, want fallback answer", res.Content)
	}
	if res.LogicalCall.ModelIDUsed != "model-b" {
		t.Errorf("model_used = %q, want model-b", res.LogicalCall.ModelIDUsed)
	}
	if res.LogicalCall.AttemptCount != 2 {
		t.Errorf("attempt_count = %d, want 2", res.LogicalCall.AttemptCount)
	}

	// 404 must not burn the failure counter.
	h := repo.health[modelKey("anthropic", "model-a")]
	if h == nil {
		t.Fatal("expected a model_health row for anthropic/model-a")
	}
	if h.ConsecutiveFailures != 0 {
		t.Errorf("consecutive_failures = %d, want 0", h.ConsecutiveFailures)
	}
	if h.LastErrorType == nil || *h.LastErrorType != "failed_404" {
		t.Errorf("last_error_type = %v, want failed_404", h.LastErrorType)
	}
}

// TestRouterFallbackOn429_WithCooldown covers the availability branch of
// ADR-021: a 429 falls back AND opens a cooldown window.
func TestRouterFallbackOn429_WithCooldown(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {
			makeProfile("code-strong", "anthropic", "model-a", 0),
			makeProfile("code-strong", "openai", "model-b", 1),
		},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": failingProvider("anthropic", func(modelID string) error {
			return &providers.HTTPError{StatusCode: 429, Provider: "anthropic", ModelID: modelID}
		}),
		"openai": succeededProvider("openai", "rate limit bypassed", 10, 5),
	})

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.LogicalCall.ModelIDUsed != "model-b" {
		t.Errorf("model_used = %q, want model-b", res.LogicalCall.ModelIDUsed)
	}

	h := repo.health[modelKey("anthropic", "model-a")]
	if h == nil {
		t.Fatal("expected a model_health row for anthropic/model-a")
	}
	if h.ConsecutiveFailures != 1 {
		t.Errorf("consecutive_failures = %d, want 1", h.ConsecutiveFailures)
	}
	if h.CooldownUntil == nil {
		t.Fatal("cooldown_until is nil, want a cooldown window")
	}
	if h.LastErrorType == nil || *h.LastErrorType != "failed_429" {
		t.Errorf("last_error_type = %v, want failed_429", h.LastErrorType)
	}
}

// TestRouterAllFailed covers the case where every attempt fails: the logical
// call is persisted as 'failed' and ErrAllFailed is returned.
func TestRouterAllFailed(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {
			makeProfile("code-strong", "anthropic", "model-a", 0),
			makeProfile("code-strong", "openai", "model-b", 1),
		},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": failingProvider("anthropic", func(modelID string) error {
			return &providers.HTTPError{StatusCode: 500, Provider: "anthropic", ModelID: modelID}
		}),
		"openai": failingProvider("openai", func(modelID string) error {
			return &providers.HTTPError{StatusCode: 500, Provider: "openai", ModelID: modelID}
		}),
	})

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if err == nil {
		t.Fatal("expected an error from all-failed routing")
	}
	if !errors.Is(err, domain.ErrAllFailed) {
		t.Errorf("err = %v, want ErrAllFailed", err)
	}
	if res == nil {
		t.Fatal("expected a Result even on all-failed routing")
	}
	if res.LogicalCall.Status != "failed" {
		t.Errorf("status = %q, want failed", res.LogicalCall.Status)
	}
	if res.LogicalCall.AttemptCount != 2 {
		t.Errorf("attempt_count = %d, want 2", res.LogicalCall.AttemptCount)
	}
	if len(repo.attempts) != 2 {
		t.Errorf("attempts = %d, want 2", len(repo.attempts))
	}
	if len(repo.finished) != 1 {
		t.Errorf("finished calls = %d, want 1", len(repo.finished))
	}
	if repo.finished[0].Status != "failed" {
		t.Errorf("finished status = %q, want failed", repo.finished[0].Status)
	}
}

// TestRouterPartialFinalizationOnContextCancel verifies the critical NFR:
// if the calling context is canceled (client disconnect), the logical call
// is finalized as 'failed' instead of remaining 'partial'.
// The defer finalizer detaches from the canceled context via
// context.WithoutCancel so the write reaches the repo.
func TestRouterPartialFinalizationOnContextCancel(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": &slowProvider{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so the first attempt fails with context error

	_, err := rtr.Chat(ctx, "code-strong", chatReq(), CallMeta{})
	if err == nil {
		t.Fatal("expected an error from canceled context")
	}

	// The logical call must NOT remain in 'partial' state.
	if len(repo.finished) != 1 {
		t.Fatalf("finished calls = %d, want 1 (partial finalization)", len(repo.finished))
	}
	if repo.finished[0].Status != "failed" {
		t.Errorf("finished status = %q, want failed", repo.finished[0].Status)
	}
}

// TestRouterPartialFinalizationOnPanic covers the critical NFR for panics:
// if a provider panics, the deferred finalizer must catch it and finalize
// as 'failed', preventing orphaned 'partial' rows.
func TestRouterPartialFinalizationOnPanic(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": &panickingProvider{},
	})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected a panic from the provider")
		}
		// After the panic the logical call must have been finalized.
		if len(repo.finished) != 1 {
			t.Errorf("finished calls = %d, want 1 (panic finalization)", len(repo.finished))
			return
		}
		if repo.finished[0].Status != "failed" {
			t.Errorf("finished status = %q, want failed", repo.finished[0].Status)
		}
	}()
	// The provider panics, so Chat never returns normally; the deferred
	// recover above asserts the finalization side effect.
	_, _ = rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
}

// TestRouterCooldownSkipsAvailableModels verifies that models whose
// (provider, model) is in cooldown are skipped, and if ALL are in cooldown
// the router returns ErrNoAvailable.
func TestRouterCooldownSkipsAvailableModels(t *testing.T) {
	now := time.Now().UTC()
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {
			makeProfile("code-strong", "anthropic", "model-a", 0),
			makeProfile("code-strong", "openai", "model-b", 1),
		},
	}}
	repo := newStubRepo()
	repo.health[modelKey("anthropic", "model-a")] = &domain.ModelHealth{
		Provider:            "anthropic",
		ModelID:             "model-a",
		CooldownUntil:       &now, // still cooling down
		ConsecutiveFailures: 2,
	}
	rtr := New(cat, repo, map[string]providers.Provider{
		"openai": succeededProvider("openai", "bypassed cooldown", 7, 3),
	})

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.LogicalCall.ModelIDUsed != "model-b" {
		t.Errorf("model_used = %q, want model-b (skipped cooldown)", res.LogicalCall.ModelIDUsed)
	}
}

// TestRouterAllCooldown covers the all-in-cooldown path: no available model
// → ErrNoAvailable. The logical call must not be created for a rejected call.
func TestRouterAllCooldown(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour)
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {
			makeProfile("code-strong", "anthropic", "model-a", 0),
			makeProfile("code-strong", "openai", "model-b", 1),
		},
	}}
	repo := newStubRepo()
	repo.health[modelKey("anthropic", "model-a")] = &domain.ModelHealth{
		Provider: "anthropic", ModelID: "model-a", CooldownUntil: &future,
	}
	repo.health[modelKey("openai", "model-b")] = &domain.ModelHealth{
		Provider: "openai", ModelID: "model-b", CooldownUntil: &future,
	}
	rtr := New(cat, repo, nil)

	_, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if !errors.Is(err, domain.ErrNoAvailable) {
		t.Fatalf("err = %v, want ErrNoAvailable", err)
	}
	if len(repo.created) != 0 {
		t.Errorf("logical calls created = %d, want 0 (all cooldown)", len(repo.created))
	}
}

// TestRouterDualCost verifies dual-level cost accounting: a rate-limited
// first attempt that carried usage is accounted in total_cost_usd while
// final_cost_usd reflects only the successful attempt.
func TestRouterDualCost(t *testing.T) {
	costIn := 10.0
	costOut := 20.0
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {
			{
				ProfileName:    "code-strong",
				Provider:       "anthropic",
				ModelID:        "model-a",
				Priority:       0,
				Enabled:        true,
				CostPerMtokIn:  &costIn,
				CostPerMtokOut: &costOut,
			},
			{
				ProfileName:    "code-strong",
				Provider:       "openai",
				ModelID:        "model-b",
				Priority:       1,
				Enabled:        true,
				CostPerMtokIn:  floatPtr(2.0),
				CostPerMtokOut: floatPtr(4.0),
			},
		},
	}}
	repo := newStubRepo()
	rtr := New(cat, repo, map[string]providers.Provider{
		"anthropic": failingProvider("anthropic", func(modelID string) error {
			return &providers.UsageError{
				Err:       &providers.HTTPError{StatusCode: 429, Provider: "anthropic", ModelID: modelID},
				TokensIn:  1000,
				TokensOut: 500,
			}
		}),
		"openai": succeededProvider("openai", "done", 2000, 1000),
	})

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), CallMeta{})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	// Attempt 1: 1000/1e6*10 + 500/1e6*20 = 0.02 (failed, with usage)
	// Attempt 2: 2000/1e6*2  + 1000/1e6*4  = 0.008 (succeeded)
	// total = 0.028
	const wantFinal, wantTotal = 0.008, 0.028
	if res.LogicalCall.FinalCostUSD == nil {
		t.Fatal("final_cost_usd is nil")
	}
	if got := *res.LogicalCall.FinalCostUSD; !nearlyEqual(got, wantFinal) {
		t.Errorf("final_cost_usd = %v, want %v", got, wantFinal)
	}
	if !nearlyEqual(res.LogicalCall.TotalCostUSD, wantTotal) {
		t.Errorf("total_cost_usd = %v, want %v", res.LogicalCall.TotalCostUSD, wantTotal)
	}
	if res.LogicalCall.TotalTokensIn != 3000 || res.LogicalCall.TotalTokensOut != 1500 {
		t.Errorf("total tokens = %d/%d, want 3000/1500", res.LogicalCall.TotalTokensIn, res.LogicalCall.TotalTokensOut)
	}
	if len(repo.attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(repo.attempts))
	}
	if repo.attempts[0].Status != "failed_429" {
		t.Errorf("attempt 1 status = %q, want failed_429", repo.attempts[0].Status)
	}
	if repo.attempts[0].CostUSD == nil || !nearlyEqual(*repo.attempts[0].CostUSD, 0.02) {
		t.Errorf("attempt 1 cost = %v, want 0.02", repo.attempts[0].CostUSD)
	}
	if repo.attempts[1].Status != "succeeded" {
		t.Errorf("attempt 2 status = %q, want succeeded", repo.attempts[1].Status)
	}
}

// floatPtr returns a pointer to the float64 value.
func floatPtr(v float64) *float64 { return &v }

// ── Slow / panicking providers for NFR tests ──────────────────

// slowProvider never returns — it waits for context cancellation.
type slowProvider struct{}

func (slowProvider) Name() string { return "anthropic" }

func (p slowProvider) Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
	<-ctx.Done()
	return domain.ChatResponse{}, ctx.Err()
}

// panickingProvider panics on Chat.
type panickingProvider struct{}

func (panickingProvider) Name() string { return "openai" }

func (p panickingProvider) Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
	panic("provider bug: nil pointer dereference")
}
