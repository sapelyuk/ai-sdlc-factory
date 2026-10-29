// Package audit provides the global tamper-evident audit log of the
// ai-sdlc-factory platform (issue #14).
//
// Every record stores
//
//	hash_n = SHA256(hash_{n-1} || id || workspace_id || actor_id || action ||
//	resource_id || details || created_at)
//
// so recomputing the chain detects any modification, deletion or insertion of
// historical records. Each workspace keeps its own linear chain. Writers are
// serialized per workspace by a transaction-scoped advisory lock plus a
// FOR UPDATE lock on the chain head, so several services (LLM Gateway, Tool
// Gateway, Agent Runtime, Sandbox Manager) can append to the same log
// concurrently without forks and without deadlocks.
//
// Secrets never reach the log: Details are passed through Redact before they
// are hashed and stored.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Actions written to the global log. Values are part of the event contract:
// queries, dashboards and policy checks filter on them.
const (
	ActionLLMCall         = "llm_call"
	ActionToolCall        = "tool_call"
	ActionSandboxExec     = "sandbox_exec"
	ActionSandboxCreate   = "sandbox_create"
	ActionSandboxDestroy  = "sandbox_destroy"
	ActionRunStarted      = "run_started"
	ActionTaskCompleted   = "task_completed"
	ActionHandoffCreated  = "handoff_created"
	ActionApprovalDecided = "approval_decided"
)

// Actor ID prefixes. An actor is "agent:<uuid>", "human:<uuid>",
// "system:<service>" or "service:<name>".
const (
	ActorPrefixAgent   = "agent:"
	ActorPrefixHuman   = "human:"
	ActorPrefixSystem  = "system:"
	ActorPrefixService = "service:"
)

// Sentinel errors returned by Log.
var (
	ErrMissingWorkspace = errors.New("audit: workspace ID is required")
	ErrMissingActor     = errors.New("audit: actor ID is required")
	ErrMissingAction    = errors.New("audit: action is required")
)

// Event is one audit-worthy action performed by an actor.
type Event struct {
	WorkspaceID uuid.UUID
	ActorID     string
	Action      string
	// IdempotencyKey deduplicates retries of the same logical event. It is
	// scoped to a workspace and is included in the hash chain.
	IdempotencyKey string
	// ResourceID is a free-form identifier of the affected resource: run id,
	// task id, container id, tool name.
	ResourceID string
	// Details is arbitrary structured context. Values matching a secret key
	// name or a known credential format are replaced with RedactedValue
	// before the record is hashed and stored.
	Details map[string]any
}

// Record is a persisted audit event.
type Record struct {
	ID             uuid.UUID
	WorkspaceID    uuid.UUID
	ActorID        string
	Action         string
	IdempotencyKey string
	ResourceID     string
	// Details holds the exact JSON bytes that were hashed. It is never
	// re-encoded after persistence, so verification is byte-exact.
	Details  json.RawMessage
	PrevHash *string
	Hash     string
	// CreatedAt is truncated to microseconds, matching the PostgreSQL
	// timestamptz precision, so a row read back verifies identically.
	CreatedAt time.Time
}

// DetailsMap decodes the stored JSON payload into a map. A record without
// details decodes into an empty map.
func (r Record) DetailsMap() map[string]any {
	if len(r.Details) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(r.Details, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// Store appends records atomically. Append must serialize concurrent writers
// of the given workspace and pass the previous chain hash (nil for the first
// record) to build.
type Store interface {
	Append(ctx context.Context, workspaceID uuid.UUID, idempotencyKey string, build func(prevHash *string) (Record, error)) (Record, error)
	// Query returns all records of a workspace ordered by the chain position.
	Query(ctx context.Context, workspaceID uuid.UUID) ([]Record, error)
}

// Auditor is the minimal surface services depend on. It is satisfied by
// *Logger and by the Nop implementation, so a service can run without a
// configured audit database.
type Auditor interface {
	Log(ctx context.Context, ev Event) error
}

// Logger validates events, computes the hash chain and persists records.
type Logger struct {
	store Store
	now   func() time.Time
}

// NewLogger creates a Logger backed by PostgreSQL.
func NewLogger(pool *pgxpool.Pool) *Logger {
	return &Logger{store: NewPostgresStore(pool), now: func() time.Time { return time.Now().UTC() }}
}

// NewLoggerWithStore creates a Logger backed by a custom Store. It is used by
// tests and by adapters that wrap the chain primitives.
func NewLoggerWithStore(store Store) *Logger {
	return &Logger{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Log validates and appends one event. It reports errors instead of panicking
// so that callers can decide how loud a failed audit write should be.
func (l *Logger) Log(ctx context.Context, ev Event) error {
	_, err := l.Append(ctx, ev)
	return err
}

// Append validates and appends one event, returning the stored record.
func (l *Logger) Append(ctx context.Context, ev Event) (Record, error) {
	if err := Validate(ev); err != nil {
		return Record{}, err
	}
	return l.store.Append(ctx, ev.WorkspaceID, ev.IdempotencyKey, func(prevHash *string) (Record, error) {
		return l.build(ev, prevHash)
	})
}

// build assembles a Record from an event: canonical JSON details, a v7 UUID,
// a truncated timestamp and the chain hash.
func (l *Logger) build(ev Event, prevHash *string) (Record, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Record{}, fmt.Errorf("audit: generate ID: %w", err)
	}
	details, err := CanonicalJSON(Redact(ev.Details))
	if err != nil {
		return Record{}, fmt.Errorf("audit: encode details: %w", err)
	}
	rec := Record{
		ID:             id,
		WorkspaceID:    ev.WorkspaceID,
		ActorID:        ev.ActorID,
		Action:         ev.Action,
		IdempotencyKey: ev.IdempotencyKey,
		ResourceID:     ev.ResourceID,
		Details:        details,
		PrevHash:       prevHash,
		CreatedAt:      l.now().UTC().Truncate(time.Microsecond),
	}
	rec.Hash = Hash(prevHash, rec)
	return rec, nil
}

// Records returns all records of a workspace in chain order.
func (l *Logger) Records(ctx context.Context, workspaceID uuid.UUID) ([]Record, error) {
	records, err := l.store.Query(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	return records, nil
}

// Verify recomputes the stored chain and fails on the first broken link.
func (l *Logger) Verify(ctx context.Context, workspaceID uuid.UUID) error {
	records, err := l.Records(ctx, workspaceID)
	if err != nil {
		return err
	}
	return VerifyChain(records)
}

// Validate reports events that cannot be audited.
func Validate(ev Event) error {
	if ev.WorkspaceID == uuid.Nil {
		return ErrMissingWorkspace
	}
	if ev.ActorID == "" {
		return ErrMissingActor
	}
	if ev.Action == "" {
		return ErrMissingAction
	}
	return nil
}

// Hash computes the chain hash of rec given the previous hash. The first
// record of a chain uses a nil prevHash.
//
// Fields are separated by a zero byte so that no concatenation of two field
// values can produce the same input as another record (length-extension and
// field-shift attacks).
func Hash(prev *string, rec Record) string {
	h := sha256.New()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	if prev != nil {
		write(*prev)
	}
	write(rec.ID.String())
	write(rec.WorkspaceID.String())
	write(rec.ActorID)
	write(rec.Action)
	write(rec.IdempotencyKey)
	write(rec.ResourceID)
	write(string(rec.Details))
	write(rec.CreatedAt.UTC().Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyChain recomputes the chain and returns an error at the first broken
// link. records must be in chain order (created_at, id).
func VerifyChain(records []Record) error {
	if len(records) == 0 {
		return nil
	}
	var prev *string
	for i, rec := range records {
		if i == 0 {
			if rec.PrevHash != nil {
				return fmt.Errorf("audit: first record %s must not have prev_hash", rec.ID)
			}
		} else {
			if rec.PrevHash == nil {
				return fmt.Errorf("audit: record %d (%s) is missing prev_hash", i, rec.ID)
			}
			if *rec.PrevHash != *prev {
				return fmt.Errorf("audit: hash chain broken at record %d (%s): prev_hash mismatch", i, rec.ID)
			}
		}
		if want := Hash(prev, rec); rec.Hash != want {
			return fmt.Errorf("audit: hash chain broken at record %d (%s): hash mismatch", i, rec.ID)
		}
		if len(rec.Hash) != sha256.Size*2 {
			return fmt.Errorf("audit: record %d (%s): hash is not a SHA-256 hex digest", i, rec.ID)
		}
		current := rec.Hash
		prev = &current
	}
	return nil
}

// CanonicalJSON encodes details deterministically: encoding/json sorts map
// keys, so the same logical payload always hashes to the same value.
//
// It is the single source of truth for the on-wire form of details. PostgreSQL
// jsonb uses a different key order (shortest key first) and inserts spaces, so
// records read back from the database are re-encoded with this function before
// their hash is recomputed.
func CanonicalJSON(details map[string]any) (json.RawMessage, error) {
	if details == nil {
		return json.RawMessage("{}"), nil
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// CanonicalizeJSON re-encodes a JSON object payload that was produced
// elsewhere (for example by PostgreSQL jsonb) into the canonical form used for
// hashing. Numbers keep their literal representation, so a value survives the
// database round trip byte-for-byte.
func CanonicalizeJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("audit: details is not a JSON object: %w", err)
	}
	return CanonicalJSON(payload)
}

// AgentActor formats an agent actor ID.
func AgentActor(agentID uuid.UUID) string { return ActorPrefixAgent + agentID.String() }

// SystemActor formats a system actor ID for a platform service.
func SystemActor(service string) string { return ActorPrefixSystem + service }
