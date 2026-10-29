package activity

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

type recordingAuditor struct {
	event sdkaudit.Event
	err   error
}

func (a *recordingAuditor) Log(_ context.Context, event sdkaudit.Event) error {
	a.event = event
	return a.err
}

func TestAuditActivityForwardsIdempotentEvent(t *testing.T) {
	auditor := &recordingAuditor{}
	workspaceID := uuid.New()
	event := audit.Event{
		WorkspaceID:    workspaceID,
		ActorID:        sdkaudit.SystemActor("worker"),
		Action:         sdkaudit.ActionRunStarted,
		IdempotencyKey: "run-started-1",
		ResourceID:     "task-run-1",
		Details:        map[string]any{"profile": "code-fast"},
	}

	if err := NewAuditActivity(auditor).Execute(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if auditor.event.WorkspaceID != workspaceID ||
		auditor.event.ActorID != event.ActorID ||
		auditor.event.Action != event.Action ||
		auditor.event.IdempotencyKey != event.IdempotencyKey ||
		auditor.event.ResourceID != event.ResourceID ||
		auditor.event.Details["profile"] != "code-fast" {
		t.Fatalf("audit event = %+v", auditor.event)
	}
}

func TestAuditActivityRejectsInvalidEventsWithoutRetry(t *testing.T) {
	tests := []struct {
		name  string
		event sdkaudit.Event
	}{
		{name: "workspace", event: sdkaudit.Event{ActorID: "system:worker", Action: sdkaudit.ActionRunStarted}},
		{name: "actor", event: sdkaudit.Event{WorkspaceID: uuid.New(), Action: sdkaudit.ActionRunStarted}},
		{name: "action", event: sdkaudit.Event{WorkspaceID: uuid.New(), ActorID: "system:worker"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity := NewAuditActivity(sdkaudit.NewLoggerWithStore(&sdkaudit.MemoryStore{}))
			if err := activity.Execute(context.Background(), audit.Event{
				WorkspaceID: test.event.WorkspaceID,
				ActorID:     test.event.ActorID,
				Action:      test.event.Action,
			}); err == nil {
				t.Fatal("expected invalid audit event error")
			}
		})
	}
}

func TestAuditActivityPropagatesStorageFailure(t *testing.T) {
	sentinel := errors.New("audit storage unavailable")
	activity := NewAuditActivity(&recordingAuditor{err: sentinel})
	err := activity.Execute(context.Background(), audit.Event{
		WorkspaceID: uuid.New(),
		ActorID:     sdkaudit.SystemActor("worker"),
		Action:      sdkaudit.ActionRunStarted,
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
}

func TestAuditActivityAllowsNilAuditor(t *testing.T) {
	activity := NewAuditActivity(nil)
	err := activity.Execute(context.Background(), audit.Event{
		WorkspaceID: uuid.New(),
		ActorID:     sdkaudit.SystemActor("worker"),
		Action:      sdkaudit.ActionRunStarted,
	})
	if err != nil {
		t.Fatal(err)
	}
}
