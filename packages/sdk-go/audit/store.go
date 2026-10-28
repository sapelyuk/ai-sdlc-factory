package audit

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// MemoryStore is an in-memory Store for tests and local development. Each
// workspace keeps its own linear chain, mirroring the PostgreSQL behaviour.
type MemoryStore struct {
	mu       sync.Mutex
	bySpace  map[uuid.UUID][]Record
	failErr  error
	appendOK int
}

// Append implements Store.
func (m *MemoryStore) Append(_ context.Context, workspaceID uuid.UUID, build func(prevHash *string) (Record, error)) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failErr != nil {
		return Record{}, m.failErr
	}

	var prev *string
	if chain := m.bySpace[workspaceID]; len(chain) > 0 {
		head := chain[len(chain)-1].Hash
		prev = &head
	}
	rec, err := build(prev)
	if err != nil {
		return Record{}, err
	}
	if m.bySpace == nil {
		m.bySpace = make(map[uuid.UUID][]Record)
	}
	m.bySpace[workspaceID] = append(m.bySpace[workspaceID], rec)
	m.appendOK++
	return rec, nil
}

// Query implements Store.
func (m *MemoryStore) Query(_ context.Context, workspaceID uuid.UUID) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return nil, m.failErr
	}
	return append([]Record(nil), m.bySpace[workspaceID]...), nil
}

// Records returns a copy of every stored record of the workspace, in chain
// order.
func (m *MemoryStore) Records(workspaceID uuid.UUID) []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.bySpace[workspaceID]...)
}

// Count returns the total number of appended records.
func (m *MemoryStore) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendOK
}

// FailWith makes every operation return err. It is used to check that callers
// surface audit storage failures.
func (m *MemoryStore) FailWith(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failErr = err
}

// nopAuditor discards events. It is used when the audit database is not
// configured (for example in unit tests and local single-binary runs).
type nopAuditor struct{}

// Log implements Auditor.
func (nopAuditor) Log(context.Context, Event) error { return nil }

// Nop returns an Auditor that discards all events.
func Nop() Auditor { return nopAuditor{} }
