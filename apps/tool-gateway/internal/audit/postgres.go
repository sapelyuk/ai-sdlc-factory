package audit

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdkaudit "github.com/sapelyuk/ai-sdlc-factory/packages/sdk-go/audit"
)

// PostgresStore persists audit records in the global audit_log table
// (migration 0014). Chain serialization (advisory lock plus FOR UPDATE on the
// head), RLS tenant scoping and secret redaction are implemented by the shared
// package; this adapter only translates between the tool-call view and the
// global event contract.
type PostgresStore struct {
	inner *sdkaudit.PostgresStore
}

// NewPostgresStore creates a Store backed by PostgreSQL.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{inner: sdkaudit.NewPostgresStore(pool)}
}

// Append writes one record of the workspace chain.
func (s *PostgresStore) Append(ctx context.Context, workspaceID uuid.UUID, build func(prevHash string) (Record, error)) (Record, error) {
	rec, err := s.inner.Append(ctx, workspaceID, "", func(prev *string) (sdkaudit.Record, error) {
		tool, err := build(derefHash(prev))
		if err != nil {
			return sdkaudit.Record{}, err
		}
		return tool.toSDK(derefHash(prev)), nil
	})
	if err != nil {
		return Record{}, err
	}
	return fromSDK(rec), nil
}

// Query returns all records for a workspace ordered by created_at.
func (s *PostgresStore) Query(ctx context.Context, workspaceID uuid.UUID) ([]Record, error) {
	records, err := s.inner.Query(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	out := make([]Record, 0, len(records))
	for _, rec := range records {
		out = append(out, fromSDK(rec))
	}
	return out, nil
}
