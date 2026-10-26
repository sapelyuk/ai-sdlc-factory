// Package audit writes an immutable, hash-chained log of every tool call.
//
// Each record stores hash_n = SHA256(hash_{n-1} || id || agent_id ||
// tool_name || result || timestamp). Recomputing the chain detects any
// modification or deletion of historical records.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Audit results.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied"
	ResultError   = "error"
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
// The first record uses an empty prevHash.
func Hash(prevHash string, rec Record) string {
	h := sha256.New()
	for _, part := range []string{
		prevHash,
		rec.ID.String(),
		rec.AgentID.String(),
		rec.ToolName,
		rec.Result,
		rec.CreatedAt.UTC().Format(time.RFC3339Nano),
	} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Verify recomputes the chain and returns an error at the first broken link.
func Verify(records []Record) error {
	prev := ""
	for i, rec := range records {
		want := Hash(prev, rec)
		if rec.HashChain != want {
			return fmt.Errorf("audit: hash chain broken at record %d (%s)", i, rec.ID)
		}
		prev = rec.HashChain
	}
	return nil
}

// MemoryStore is an in-memory Store used by tests and local development.
type MemoryStore struct {
	mu      sync.Mutex
	records []Record
}

// Append implements Store.
func (m *MemoryStore) Append(_ context.Context, _ uuid.UUID, build func(prevHash string) (Record, error)) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prev := ""
	if n := len(m.records); n > 0 {
		prev = m.records[n-1].HashChain
	}
	rec, err := build(prev)
	if err != nil {
		return Record{}, err
	}
	m.records = append(m.records, rec)
	return rec, nil
}

// Records returns a copy of the stored records in insertion order.
func (m *MemoryStore) Records() []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.records...)
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
	case ResultSuccess, ResultDenied, ResultError:
		return true
	default:
		return false
	}
}
