// Package repository provides persistence for the approval queue (FR-OPS-1).
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/approvals/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
)

// ApprovalRepository is the persistence contract of the approval queue.
type ApprovalRepository interface {
	List(ctx context.Context, status *domain.Status) ([]*domain.ApprovalRequest, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.ApprovalRequest, error)
	Approve(ctx context.Context, id uuid.UUID, decidedBy uuid.UUID, comment string) error
	Reject(ctx context.Context, id uuid.UUID, decidedBy uuid.UUID, comment string) error
}

// PostgresApprovalRepository stores approval requests in the `approvals`
// table. The table is RLS-protected, so every operation opens a transaction
// and sets the tenant context (same pattern as the Registry repositories).
type PostgresApprovalRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresApprovalRepository creates the repository.
func NewPostgresApprovalRepository(pool *pgxpool.Pool) *PostgresApprovalRepository {
	return &PostgresApprovalRepository{pool: pool}
}

var _ ApprovalRepository = (*PostgresApprovalRepository)(nil)

const approvalColumns = `id, workspace_id, gate_name, status, requested_by,
	approved_by, risk_level, deadline_at, context, comment,
	COALESCE(workflow_id, ''), task_run_id, stage_run_id,
	created_at, updated_at, decided_at`

// List returns the requests of the active workspace, newest first, optionally
// filtered by status.
func (r *PostgresApprovalRepository) List(ctx context.Context, status *domain.Status) ([]*domain.ApprovalRequest, error) {
	var out []*domain.ApprovalRequest
	err := r.inTenant(ctx, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		query := `SELECT ` + approvalColumns + `
			FROM approvals
			WHERE workspace_id = $1`
		args := []any{workspaceID}
		if status != nil {
			query += ` AND status = $2`
			args = append(args, string(*status))
		}
		query += ` ORDER BY deadline_at NULLS LAST, created_at DESC`
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			req, err := scanApproval(rows)
			if err != nil {
				return err
			}
			out = append(out, req)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list approvals: %w", err)
	}
	return out, nil
}

// GetByID returns one request from the active workspace.
func (r *PostgresApprovalRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.ApprovalRequest, error) {
	var req *domain.ApprovalRequest
	err := r.inTenant(ctx, func(tx pgx.Tx, _ uuid.UUID) error {
		row := tx.QueryRow(ctx,
			`SELECT `+approvalColumns+` FROM approvals WHERE id = $1`, id)
		var err error
		req, err = scanApproval(row)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get approval: %w", err)
	}
	return req, nil
}

// Approve records a positive decision. Only a pending request can be approved:
// an expired one stays expired, because the owner answered after the deadline
// and SDLC_STATE_MACHINE.md section 7 forbids the action (ADR-013 for R3).
func (r *PostgresApprovalRepository) Approve(ctx context.Context, id uuid.UUID, decidedBy uuid.UUID, comment string) error {
	return r.decide(ctx, id, domain.StatusApproved, decidedBy, comment)
}

// Reject records a negative decision. A decided request cannot be rejected.
func (r *PostgresApprovalRepository) Reject(ctx context.Context, id uuid.UUID, decidedBy uuid.UUID, comment string) error {
	return r.decide(ctx, id, domain.StatusRejected, decidedBy, comment)
}

func (r *PostgresApprovalRepository) decide(
	ctx context.Context,
	id uuid.UUID,
	status domain.Status,
	decidedBy uuid.UUID,
	comment string,
) error {
	now := time.Now().UTC()
	var affected int64
	err := r.inTenant(ctx, func(tx pgx.Tx, _ uuid.UUID) error {
		tag, err := tx.Exec(ctx, `
			UPDATE approvals
			SET status = $2, approved_by = $3, comment = $4,
			    updated_at = $5, decided_at = $5
			WHERE id = $1 AND status = 'pending'`,
			id, string(status), decidedBy, comment, now)
		if err != nil {
			return err
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return fmt.Errorf("decide approval %s: %w", id, err)
	}
	if affected == 0 {
		// Either the row does not exist in this workspace or it is already
		// decided; the handler distinguishes the two with GetByID.
		return errNotPending
	}
	return nil
}

var errNotPending = errors.New("approval request is not pending")

// ErrNotPending is returned when a decision targets a non-pending request.
var ErrNotPending = errNotPending

type scanner interface {
	Scan(dest ...any) error
}

func scanApproval(row scanner) (*domain.ApprovalRequest, error) {
	var (
		req         domain.ApprovalRequest
		status      string
		contextJSON []byte
	)
	err := row.Scan(
		&req.ID, &req.WorkspaceID, &req.GateName, &status, &req.RequestedBy,
		&req.ApprovedBy, &req.RiskLevel, &req.DeadlineAt, &contextJSON,
		&req.Comment, &req.WorkflowID, &req.TaskRunID, &req.StageRunID,
		&req.CreatedAt, &req.UpdatedAt, &req.DecidedAt,
	)
	if err != nil {
		return nil, err
	}
	req.Status = domain.Status(status)
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &req.Context); err != nil {
			return nil, fmt.Errorf("decode approval context %s: %w", req.ID, err)
		}
	}
	return &req, nil
}

// inTenant runs fn inside a transaction scoped to the caller's workspace.
func (r *PostgresApprovalRepository) inTenant(ctx context.Context, fn func(pgx.Tx, uuid.UUID) error) error {
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin approval transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	if err := fn(tx, workspaceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
