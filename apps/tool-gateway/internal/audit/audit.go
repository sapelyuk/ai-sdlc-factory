// Package audit writes Tool Gateway calls into the platform-wide
// tamper-evident audit log (issue #14).
//
// The hash chain, the storage, the tenant scoping and the secret redaction are
// implemented once for all services in
// github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit. This package only
// maps the gateway's tool-call vocabulary (tool name, R0-R3 risk level,
// result) onto the global event contract, so a tool call becomes one
// action="tool_call" row of `audit_log` next to LLM calls, sandbox events and
// run transitions.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// Audit results.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
	ResultError   = "error"
	ResultStub    = "stub"
)

// Detail keys of a tool_call event. All four keys are always written: the hash
// covers the exact JSON payload, so a record read back from the database has to
// re-encode to the same bytes.
const (
	detailToolName     = "tool_name"
	detailRiskLevel    = "risk_level"
	detailResult       = "result"
	detailErrorMessage = "error_message"
)

// Entry is a tool call to be audited.
type Entry struct {
	WorkspaceID  uuid.UUID
	AgentID      uuid.UUID
	ToolName     string
	RiskLevel    string
	Result       string
	ErrorMessage string
}

// Record is a persisted audit entry.
type Record struct {
	ID           uuid.UUID
	WorkspaceID  uuid.UUID
	AgentID      uuid.UUID
	ToolName     string
	RiskLevel    string
	Result       string
	ErrorMessage string
	HashChain    string
	CreatedAt    time.Time
}

// Store appends records atomically. Append must serialize concurrent writers
// for the workspace and pass the previous hash to build.
type Store interface {
	Append(ctx context.Context, workspaceID uuid.UUID, build func(prevHash string) (Record, error)) (Record, error)
	// Query returns all records for a workspace, ordered by created_at.
	Query(ctx context.Context, workspaceID uuid.UUID) ([]Record, error)
}

// details renders the tool-call fields as the global event payload.
func (r Record) details() map[string]any {
	return map[string]any{
		detailToolName:     r.ToolName,
		detailRiskLevel:    r.RiskLevel,
		detailResult:       r.Result,
		detailErrorMessage: r.ErrorMessage,
	}
}

// toSDK renders a tool record as a shared record so the chain can be recomputed
// by the shared package. prevHash is empty for the head of a chain.
func (r Record) toSDK(prevHash string) sdkaudit.Record {
	rec := sdkaudit.Record{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		ActorID:     sdkaudit.AgentActor(r.AgentID),
		Action:      sdkaudit.ActionToolCall,
		ResourceID:  r.ToolName,
		Hash:        r.HashChain,
		CreatedAt:   r.CreatedAt,
	}
	if prevHash != "" {
		prev := prevHash
		rec.PrevHash = &prev
	}
	// The payload is re-encoded rather than copied, because the gateway record
	// keeps the decoded fields. CanonicalJSON sorts keys, which is the exact
	// form hashed on insert.
	details, err := sdkaudit.CanonicalJSON(r.details())
	if err != nil {
		// Every field is a string, so encoding cannot fail. Fall back to an
		// empty object instead of panicking inside the audit path.
		details = json.RawMessage("{}")
	}
	rec.Details = details
	return rec
}

// fromSDK decodes a shared record back into the tool-call view.
func fromSDK(rec sdkaudit.Record) Record {
	out := Record{
		ID:          rec.ID,
		WorkspaceID: rec.WorkspaceID,
		CreatedAt:   rec.CreatedAt,
		HashChain:   rec.Hash,
		// The tool name is the resource id of a tool_call event; the payload
		// stays the source of truth and the column is the fallback.
		ToolName: rec.ResourceID,
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Details, &payload); err == nil {
		out.ToolName = stringField(payload, detailToolName, out.ToolName)
		out.RiskLevel = stringField(payload, detailRiskLevel, "")
		out.Result = stringField(payload, detailResult, "")
		out.ErrorMessage = stringField(payload, detailErrorMessage, "")
	}
	if agent, err := parseAgentActor(rec.ActorID); err == nil {
		out.AgentID = agent
	}
	return out
}

func stringField(payload map[string]any, key, fallback string) string {
	if value, ok := payload[key].(string); ok {
		return value
	}
	return fallback
}

// parseAgentActor extracts the agent UUID from an "agent:<uuid>" actor id.
func parseAgentActor(actorID string) (uuid.UUID, error) {
	if !strings.HasPrefix(actorID, sdkaudit.ActorPrefixAgent) {
		return uuid.Nil, errors.New("audit: actor is not an agent")
	}
	return uuid.Parse(strings.TrimPrefix(actorID, sdkaudit.ActorPrefixAgent))
}

// Logger validates entries and computes the hash chain.
type Logger struct {
	store Store
	now   func() time.Time
}

// NewLogger creates a Logger backed by store.
func NewLogger(store Store) *Logger {
	return &Logger{store: store, now: func() time.Time { return time.Now().UTC() }}
}

// Record validates and persists an entry.
func (l *Logger) Record(ctx context.Context, e Entry) (Record, error) {
	if e.WorkspaceID == uuid.Nil {
		return Record{}, errors.New("audit: workspace ID is required")
	}
	if e.AgentID == uuid.Nil {
		return Record{}, errors.New("audit: agent ID is required")
	}
	if e.ToolName == "" {
		return Record{}, errors.New("audit: tool name is required")
	}
	if !validRiskLevel(e.RiskLevel) {
		return Record{}, fmt.Errorf("audit: invalid risk level %q", e.RiskLevel)
	}
	if !validResult(e.Result) {
		return Record{}, fmt.Errorf("audit: invalid result %q", e.Result)
	}

	return l.store.Append(ctx, e.WorkspaceID, func(prevHash string) (Record, error) {
		id, err := uuid.NewV7()
		if err != nil {
			return Record{}, fmt.Errorf("audit: generate ID: %w", err)
		}
		rec := Record{
			ID:           id,
			WorkspaceID:  e.WorkspaceID,
			AgentID:      e.AgentID,
			ToolName:     e.ToolName,
			RiskLevel:    e.RiskLevel,
			Result:       e.Result,
			ErrorMessage: e.ErrorMessage,
			// PostgreSQL timestamptz has microsecond precision. Truncate
			// before hashing so a record read back from the database
			// verifies against the same timestamp.
			CreatedAt: l.now().UTC().Truncate(time.Microsecond),
		}
		rec.HashChain = Hash(prevHash, rec)
		return rec, nil
	})
}

// Hash computes the chain hash of rec given the previous hash.
// The first record uses an empty prevHash. The computation is delegated to the
// shared package, so a tool call and an LLM call of the same workspace are
// links of one single chain.
func Hash(prevHash string, rec Record) string {
	return sdkaudit.Hash(hashPointer(prevHash), rec.toSDK(prevHash))
}

// Verify recomputes the chain and returns an error at the first broken link.
func Verify(records []Record) error {
	return sdkaudit.VerifyChain(toSDKChain(records))
}

// toSDKChain rebuilds the shared records, taking each prev_hash from the
// predecessor's hash.
func toSDKChain(records []Record) []sdkaudit.Record {
	out := make([]sdkaudit.Record, 0, len(records))
	prev := ""
	for _, rec := range records {
		out = append(out, rec.toSDK(prev))
		prev = rec.HashChain
	}
	return out
}

// hashPointer returns nil for the empty hash of a chain head.
func hashPointer(prevHash string) *string {
	if prevHash == "" {
		return nil
	}
	return &prevHash
}

// Verify queries all records for the given workspace and verifies the chain.
func (l *Logger) Verify(ctx context.Context, workspaceID uuid.UUID) error {
	records, err := l.store.Query(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("audit: query: %w", err)
	}
	return Verify(records)
}

// MemoryStore is an in-memory Store used by tests and local development. It
// delegates the chain to the shared implementation.
type MemoryStore struct {
	mu    sync.Mutex
	inner *sdkaudit.MemoryStore
	// order remembers the workspace of every append, so Records can return
	// everything in insertion order without knowing the workspaces.
	order []uuid.UUID
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{inner: &sdkaudit.MemoryStore{}}
}

// Append implements Store.
func (m *MemoryStore) Append(ctx context.Context, workspaceID uuid.UUID, build func(prevHash string) (Record, error)) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.inner == nil {
		m.inner = &sdkaudit.MemoryStore{}
	}
	rec, err := m.inner.Append(ctx, workspaceID, func(prev *string) (sdkaudit.Record, error) {
		tool, err := build(derefHash(prev))
		if err != nil {
			return sdkaudit.Record{}, err
		}
		return tool.toSDK(derefHash(prev)), nil
	})
	if err != nil {
		return Record{}, err
	}
	// Track the workspace only once so Records() doesn't return duplicates.
	seen := make(map[uuid.UUID]bool, len(m.order))
	for _, w := range m.order {
		seen[w] = true
	}
	if !seen[workspaceID] {
		m.order = append(m.order, workspaceID)
	}
	return fromSDK(rec), nil
}

// Records returns a copy of the stored records in insertion order.
func (m *MemoryStore) Records() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inner == nil {
		return nil
	}

	var out []Record
	for _, ws := range m.order {
		records, err := m.inner.Query(context.Background(), ws)
		if err != nil {
			return out
		}
		for _, rec := range records {
			out = append(out, fromSDK(rec))
		}
	}
	return out
}

// Query returns all records for the given workspace.
func (m *MemoryStore) Query(_ context.Context, workspaceID uuid.UUID) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inner == nil {
		return nil, nil
	}
	records, err := m.inner.Query(context.Background(), workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		out = append(out, fromSDK(rec))
	}
	return out, nil
}

func derefHash(prev *string) string {
	if prev == nil {
		return ""
	}
	return *prev
}

func validRiskLevel(level string) bool {
	switch level {
	case "R0", "R1", "R2", "R3":
		return true
	default:
		return false
	}
}

func validResult(result string) bool {
	switch result {
	case ResultSuccess, ResultDenied, ResultError, ResultStub:
		return true
	default:
		return false
	}
}
