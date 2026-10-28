package routing

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// auditWorkspaceID matches stubRepo.workspaceID so Query targets the correct tenant.
var auditWorkspaceID = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// auditEnv builds a router wired to an in-memory audit store.
func auditEnv(t *testing.T, fakes ...*fakeProvider) (*Router, *sdkaudit.MemoryStore) {
	t.Helper()

	profiles := map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}
	cat := &stubCatalog{profiles: profiles}
	rtr := New(cat, newStubRepo(), buildProviders(fakes...))
	store := &sdkaudit.MemoryStore{}
	rtr.WithAuditor(sdkaudit.NewLoggerWithStore(store))
	return rtr, store
}

func chatMeta() CallMeta {
	runID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	taskID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	agentID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	return CallMeta{RunID: &runID, TaskID: &taskID, AgentID: &agentID}
}

// TestAuditLLMCallSuccess asserts one llm_call event is written when a logical
// call succeeds, with the attribution the run console needs.
func TestAuditLLMCallSuccess(t *testing.T) {
	rtr, store := auditEnv(t, succeededProvider("anthropic", "pong", 10, 5))

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	records, err := store.Query(context.Background(), auditWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want exactly 1", len(records))
	}
	rec := records[0]
	if rec.Action != sdkaudit.ActionLLMCall {
		t.Errorf("action = %q, want %q", rec.Action, sdkaudit.ActionLLMCall)
	}
	if rec.ResourceID != res.LogicalCall.ID.String() {
		t.Errorf("resource_id = %q, want logical call id %q", rec.ResourceID, res.LogicalCall.ID)
	}
	if rec.ActorID != sdkaudit.AgentActor(*chatMeta().AgentID) {
		t.Errorf("actor_id = %q, want the calling agent", rec.ActorID)
	}

	details := rec.DetailsMap()
	for key, want := range map[string]any{
		"status":          "succeeded",
		"profile":         "code-strong",
		"provider":        "anthropic",
		"model":           "model-a",
		"attempt_count":   float64(1),
		"final_tokens_in": float64(10),
	} {
		if got := details[key]; got != want {
			t.Errorf("details[%q] = %v, want %v", key, got, want)
		}
	}
	if details["run_id"] != "44444444-4444-4444-4444-444444444444" {
		t.Errorf("details[run_id] = %v", details["run_id"])
	}
	if _, ok := details["error"]; ok {
		t.Errorf("a successful call must not carry an error detail: %v", details["error"])
	}
	if err := sdkaudit.VerifyChain(records); err != nil {
		t.Fatalf("audit chain must verify: %v", err)
	}
}

// TestAuditLLMCallAllFailed covers the failure path: one event with status
// failed and the last provider error.
func TestAuditLLMCallAllFailed(t *testing.T) {
	rtr, store := auditEnv(t, failingProvider("anthropic", func(string) error {
		return errors.New("upstream exploded")
	}))

	_, err := rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta())
	if !errors.Is(err, domain.ErrAllFailed) {
		t.Fatalf("Chat error = %v, want ErrAllFailed", err)
	}

	records, err := store.Query(context.Background(), auditWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want exactly 1", len(records))
	}
	details := records[0].DetailsMap()
	if details["status"] != "failed" {
		t.Errorf("status = %v, want failed", details["status"])
	}
	if msg, _ := details["error"].(string); !strings.Contains(msg, "upstream exploded") {
		t.Errorf("error detail = %v, want the provider failure", details["error"])
	}
}

// TestAuditLLMCallOnceOnDeferredFinalizer covers the provider-panic path. The
// logical call is finalized by the deferred func on a detached context, and the
// audit log must receive exactly one event for it.
func TestAuditLLMCallOnceOnDeferredFinalizer(t *testing.T) {
	rtr, store := auditEnv(t, &fakeProvider{
		name: "anthropic",
		handler: func(string, domain.ChatRequest) (domain.ChatResponse, error) {
			panic("provider bug: nil pointer dereference")
		},
	})

	defer func() {
		if recover() == nil {
			t.Fatal("expected the provider panic to propagate")
		}
		records, err := store.Query(context.Background(), auditWorkspaceID)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 {
			t.Fatalf("audit records = %d, want exactly 1 from the deferred finalizer", len(records))
		}
		if got := records[0].DetailsMap()["status"]; got != "failed" {
			t.Errorf("status = %v, want failed", got)
		}
	}()

	_, _ = rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta())
}

// TestAuditNotWrittenWhenNoLogicalCall asserts that a request rejected before a
// logical call exists produces no event, so the log has no phantom entries.
func TestAuditNotWrittenWhenNoLogicalCall(t *testing.T) {
	cat := &stubCatalog{err: domain.ErrUnknownProfile}
	store := &sdkaudit.MemoryStore{}
	rtr := New(cat, newStubRepo(), nil)
	rtr.WithAuditor(sdkaudit.NewLoggerWithStore(store))

	if _, err := rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta()); err == nil {
		t.Fatal("expected the catalog error")
	}

	records, err := store.Query(context.Background(), auditWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("audit records = %d, want 0", len(records))
	}
}

// failingAuditor reports every write.
type failingAuditor struct{}

func (failingAuditor) Log(context.Context, sdkaudit.Event) error {
	return errors.New("audit log is down")
}

// ctxSpyAuditor records the state of the context it was asked to write on.
type ctxSpyAuditor struct{ ctxErr error }

func (a *ctxSpyAuditor) Log(ctx context.Context, _ sdkaudit.Event) error {
	a.ctxErr = ctx.Err()
	return nil
}

// TestAuditWriteSurvivesClientDisconnect is the durability guarantee: a call
// that spent budget must reach the audit log even when the request context is
// already canceled, so the event is written on a detached context.
func TestAuditWriteSurvivesClientDisconnect(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}}
	spy := &ctxSpyAuditor{}
	rtr := New(cat, newStubRepo(), buildProviders(failingProvider("anthropic", func(string) error {
		return errors.New("provider went away")
	})))
	rtr.WithAuditor(spy)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := rtr.Chat(ctx, "code-strong", chatReq(), chatMeta()); !errors.Is(err, domain.ErrAllFailed) {
		t.Fatalf("Chat error = %v, want ErrAllFailed", err)
	}
	if spy.ctxErr != nil {
		t.Fatalf("audit write ran on a dead context (%v), the event is lost", spy.ctxErr)
	}
}

// TestAuditFailureDoesNotBreakChat is the availability guarantee: a broken
// audit log must not turn a successful generation into an agent error.
func TestAuditFailureDoesNotBreakChat(t *testing.T) {
	cat := &stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}}
	rtr := New(cat, newStubRepo(), buildProviders(succeededProvider("anthropic", "pong", 1, 1)))
	rtr.WithAuditor(failingAuditor{})

	res, err := rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta())
	if err != nil {
		t.Fatalf("Chat must succeed despite the audit failure: %v", err)
	}
	if res.Content != "pong" {
		t.Fatalf("content = %q", res.Content)
	}
}

// TestRouterAuditorDefaultsToNop covers a router that never gets WithAuditor:
// Log must be a no-op, not a nil-pointer dereference.
func TestRouterAuditorDefaultsToNop(t *testing.T) {
	rtr := New(&stubCatalog{profiles: map[string][]domain.ModelProfile{
		"code-strong": {makeProfile("code-strong", "anthropic", "model-a", 0)},
	}}, newStubRepo(), buildProviders(succeededProvider("anthropic", "pong", 1, 1)))

	if _, err := rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	rtr.WithAuditor(nil)
	if rtr.auditor == nil {
		t.Fatal("WithAuditor(nil) must restore the no-op auditor")
	}
}

// TestLLMCallDetailsRedactsSecrets proves rule 4 of issue #14 end to end: a
// provider error that echoes a credential is stored redacted.
func TestLLMCallDetailsRedactsSecrets(t *testing.T) {
	const secret = "ghp_supersecrettoken0123456789"
	rtr, store := auditEnv(t, failingProvider("anthropic", func(string) error {
		return errors.New("provider rejected " + secret)
	}))

	_, _ = rtr.Chat(context.Background(), "code-strong", chatReq(), chatMeta())

	records, err := store.Query(context.Background(), auditWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(records))
	}
	if strings.Contains(string(records[0].Details), secret) {
		t.Fatalf("secret leaked into the audit log: %s", records[0].Details)
	}
	if !strings.Contains(string(records[0].Details), sdkaudit.RedactedValue) {
		t.Fatalf("expected %s in the stored details: %s", sdkaudit.RedactedValue, records[0].Details)
	}
}
