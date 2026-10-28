package activity

import (
	"context"
	"errors"

	"go.temporal.io/sdk/temporal"

	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"

	"github.com/sapelyuk/ai-sdlc-factory/apps/worker/internal/audit"
)

// AuditActivity appends events to the global audit log (issue #14).
//
// It is the only place where the Agent Runtime touches the audit database:
// workflow code must stay deterministic, so every audit-worthy step is
// delegated to this activity.
type AuditActivity struct {
	auditor sdkaudit.Auditor
}

// NewAuditActivity creates the activity handler. A nil auditor is replaced by
// the no-op implementation so a worker started without a database keeps running.
func NewAuditActivity(auditor sdkaudit.Auditor) *AuditActivity {
	if auditor == nil {
		auditor = sdkaudit.Nop()
	}
	return &AuditActivity{auditor: auditor}
}

// Execute appends one event.
//
// A malformed event (unparseable workspace, missing actor or action) is not
// retryable: no retry can turn it into a valid row, so it fails immediately
// instead of consuming the retry budget. A transport or database error stays
// retryable, because the append is serialized per workspace by the shared store.
func (a *AuditActivity) Execute(ctx context.Context, ev audit.Event) error {
	err := a.auditor.Log(ctx, sdkaudit.Event{
		WorkspaceID:    ev.WorkspaceID,
		ActorID:        ev.ActorID,
		Action:         ev.Action,
		IdempotencyKey: ev.IdempotencyKey,
		ResourceID:     ev.ResourceID,
		Details:        ev.Details,
	})
	if errors.Is(err, sdkaudit.ErrMissingWorkspace) ||
		errors.Is(err, sdkaudit.ErrMissingActor) ||
		errors.Is(err, sdkaudit.ErrMissingAction) {
		return temporal.NewNonRetryableApplicationError(err.Error(), "InvalidAuditEvent", err)
	}
	return err
}
