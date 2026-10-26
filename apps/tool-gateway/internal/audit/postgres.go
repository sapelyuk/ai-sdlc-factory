package audit

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey serializes audit appends across gateway replicas.
const advisoryLockKey int64 = 0x746f6f6c5f617564 // "tool_aud"

// PostgresStore persists audit records in tool_call_audit_log.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a Store backed by PostgreSQL.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Append writes a record in a transaction holding an advisory lock, so the
// hash chain stays linear even with several gateway replicas.
func (s *PostgresStore) Append(ctx context.Context, workspaceID uuid.UUID, build func(prevHash string) (Record, error)) (Record, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("audit: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockKey); err != nil {
		return Record{}, fmt.Errorf("audit: acquire chain lock: %w", err)
	}

	// RLS on tool_call_audit_log is scoped to the workspace of the call, so
	// the tenant context must be set before the chain head is read. Each
	// workspace therefore has its own linear hash chain.
	// set_config(..., true) is the SET LOCAL equivalent for pooled
	// connections.
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`,
		workspaceID.String()); err != nil {
		return Record{}, fmt.Errorf("audit: set tenant context: %w", err)
	}

	var prev string
	err = tx.QueryRow(ctx, `
		SELECT hash_chain FROM tool_call_audit_log
		WHERE workspace_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, workspaceID).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Record{}, fmt.Errorf("audit: read previous hash: %w", err)
	}

	rec, err := build(prev)
	if err != nil {
		return Record{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO tool_call_audit_log
			(id, workspace_id, agent_id, tool_name, risk_level, result,
			 error_message, hash_chain, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9)`,
		rec.ID, rec.WorkspaceID, rec.AgentID, rec.ToolName, rec.RiskLevel,
		rec.Result, rec.ErrorMessage, rec.HashChain, rec.CreatedAt); err != nil {
		return Record{}, fmt.Errorf("audit: insert record: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Record{}, fmt.Errorf("audit: commit: %w", err)
	}
	return rec, nil
}
