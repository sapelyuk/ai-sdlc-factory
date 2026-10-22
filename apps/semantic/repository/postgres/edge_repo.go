// Package repository provides PostgreSQL data access for the semantic graph.
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
)

// EdgeRepository provides data access for sem_edges.
type EdgeRepository struct {
	pool *pgxpool.Pool
}

// NewEdgeRepository creates a new EdgeRepository.
func NewEdgeRepository(pool *pgxpool.Pool) *EdgeRepository {
	return &EdgeRepository{pool: pool}
}

// Create inserts a new edge.
func (r *EdgeRepository) Create(ctx context.Context, input domain.CreateEdgeInput) error {
	srcID, err := uuid.Parse(input.SrcID)
	if err != nil {
		return fmt.Errorf("parse src_id: %w", err)
	}
	dstID, err := uuid.Parse(input.DstID)
	if err != nil {
		return fmt.Errorf("parse dst_id: %w", err)
	}

	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO sem_edges (workspace_id, src_id, dst_id, kind, props, valid_from)
			VALUES ($1, $2, $3, $4, $5, now())`,
			workspaceID, srcID, dstID, input.Kind, input.Props)
		if err != nil {
			return fmt.Errorf("create edge: %w", err)
		}
		return nil
	})
}

// Delete removes an edge by its UUID.
func (r *EdgeRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		result, err := tx.Exec(ctx, `
			DELETE FROM sem_edges WHERE id = $1 AND workspace_id = $2`,
			id, workspaceID)
		if err != nil {
			return fmt.Errorf("delete edge: %w", err)
		}
		rows := result.RowsAffected()
		if rows == 0 {
			return domain.ErrEdgeNotFound
		}
		return nil
	})
}

// ── Transaction helper ────────────────────────────────────────

// inTenantTx begins a transaction with RLS context set.
// PostgreSQL utility statements (SET LOCAL) do not accept bind parameters in
// the extended query protocol, so we use set_config(..., true) which is
// semantically identical to SET LOCAL and works with parameterized queries.
func inTenantTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx, uuid.UUID) error) error {
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	if err := fn(tx, workspaceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// IsNotFound reports whether err is a not-found domain error.
func IsNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrEdgeNotFound)
}

// IsForbidden reports whether err is a forbidden domain error.
func IsForbidden(err error) bool {
	return errors.Is(err, domain.ErrForbidden)
}

// IsConflict reports whether err is a conflict domain error.
func IsConflict(err error) bool {
	return errors.Is(err, domain.ErrConflict)
}
