package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore appends records to the global audit_log table (migration
// 0014). It is the only implementation of Store used in production.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a Store backed by PostgreSQL.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// chainLockName namespaces the per-workspace advisory lock so it cannot
// collide with advisory locks taken by other features.
const chainLockName = "ai-sdlc-factory:audit-chain:"

// Append writes one record in a transaction that holds the workspace chain:
//
//  1. `set_config('app.current_workspace_id', ...)` scopes the transaction to
//     the tenant, because audit_log is protected by RLS and a permissive
//     policy also gates INSERT.
//  2. `pg_advisory_xact_lock` serializes appends of the workspace across
//     services and replicas. FOR UPDATE alone cannot protect the first record
//     of an empty chain (there is no row to lock) and would let two writers
//     pick different heads.
//  3. `SELECT ... FOR UPDATE` locks the chain head, so a reader that bypassed
//     the advisory lock still cannot fork the chain.
//
// The lock is always taken in the same order (advisory, then row), so
// concurrent writers queue instead of deadlocking.
func (s *PostgresStore) Append(ctx context.Context, workspaceID uuid.UUID, build func(prevHash *string) (Record, error)) (Record, error) {
	if s.pool == nil {
		return Record{}, errors.New("audit: postgres store requires a connection pool")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("audit: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return Record{}, fmt.Errorf("audit: set tenant context: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, chainLockName+workspaceID.String()); err != nil {
		return Record{}, fmt.Errorf("audit: acquire chain lock: %w", err)
	}

	var prev *string
	err = tx.QueryRow(ctx, `
		SELECT hash FROM audit_log
		WHERE workspace_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE`, workspaceID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Record{}, fmt.Errorf("audit: read chain head: %w", err)
	}

	rec, err := build(prev)
	if err != nil {
		return Record{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log
			(id, workspace_id, actor_id, action, resource_id, details, prev_hash, hash, created_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)`,
		rec.ID, rec.WorkspaceID, rec.ActorID, rec.Action, rec.ResourceID,
		rec.Details, rec.PrevHash, rec.Hash, rec.CreatedAt); err != nil {
		return Record{}, fmt.Errorf("audit: insert record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Record{}, fmt.Errorf("audit: commit: %w", err)
	}
	return rec, nil
}

// Query returns all records of a workspace in chain order. The tenant context
// is set inside a transaction so the RLS policy applies on pooled connections.
func (s *PostgresStore) Query(ctx context.Context, workspaceID uuid.UUID) ([]Record, error) {
	if s.pool == nil {
		return nil, errors.New("audit: postgres store requires a connection pool")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("audit: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return nil, fmt.Errorf("audit: set tenant context: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT id, workspace_id, actor_id, action, COALESCE(resource_id, ''),
		       details, prev_hash, hash, created_at
		FROM audit_log
		WHERE workspace_id = $1
		ORDER BY created_at, id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("audit: query: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var rec Record
		if err := rows.Scan(&rec.ID, &rec.WorkspaceID, &rec.ActorID, &rec.Action,
			&rec.ResourceID, &rec.Details, &rec.PrevHash, &rec.Hash, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("audit: scan: %w", err)
		}
		// PostgreSQL jsonb re-serializes the payload (key order and spacing
		// differ from encoding/json), so the stored bytes must be brought back
		// to the canonical form that was hashed at insert time. Without this,
		// verification would report a false broken chain for every record.
		canonical, err := CanonicalizeJSON(rec.Details)
		if err != nil {
			return nil, fmt.Errorf("audit: canonicalize details of %s: %w", rec.ID, err)
		}
		rec.Details = canonical
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("audit: rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("audit: commit read transaction: %w", err)
	}
	return out, nil
}
