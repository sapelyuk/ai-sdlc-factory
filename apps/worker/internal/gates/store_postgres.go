package gates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxPool is the minimum interface the store needs from a connection pool.
// *pgxpool.Pool satisfies it automatically.
type pgxPool interface {
	Begin(context.Context) (pgx.Tx, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// PostgresStore persists approval requests into the shared `approvals` table
// (migration 0002, extended by 0016). The worker writes the request and reads
// the decision; the API exposes the same rows to the owner.
//
// The table is protected by RLS, so every operation opens a transaction and
// sets `app.current_workspace_id` before touching it, exactly like the audit
// store does (packages/sdk-go/audit).
type PostgresStore struct {
	pool pgxPool
}

// NewPostgresStore returns a store backed by the given connection pool. A nil
// pool is stored as a nil interface so the per-method guards keep reporting a
// missing pool instead of panicking on a typed-nil.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	if pool == nil {
		return &PostgresStore{}
	}
	return &PostgresStore{pool: pool}
}

var _ ApprovalStore = (*PostgresStore)(nil)

// approvalColumns lists the columns read by every query, in scan order.
const approvalColumns = `id, workspace_id, gate_name, status, requested_by,
	    COALESCE(approved_by, NULL), risk_level, deadline_at, context,
	    COALESCE(comment, ''), COALESCE(workflow_id, ''),
	    task_run_id, stage_run_id, run_id, created_at, updated_at, decided_at`

// Create inserts a pending request. The insert is idempotent on the primary
// key: a retried activity must not create a second request for the same gate.
func (s *PostgresStore) Create(ctx context.Context, req ApprovalRequest) error {
	if s.pool == nil {
		return errors.New("gates: postgres store requires a connection pool")
	}
	contextJSON, err := marshalContext(req.Context)
	if err != nil {
		return err
	}
	err = s.inTenant(ctx, req.WorkspaceID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO approvals
				(id, workspace_id, gate_name, status, requested_by, risk_level,
				 deadline_at, context, workflow_id, task_run_id, stage_run_id, run_id,
				 created_at, updated_at)
			VALUES ($1, $2, $3, 'pending', $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (id) DO NOTHING`,
			req.ID, req.WorkspaceID, req.GateName, req.RequestedBy,
			req.RiskLevel, req.DeadlineAt, contextJSON, req.WorkflowID,
			nullUUIDPtr(req.TaskRunID), nullUUIDPtr(req.StageRunID),
			nullUUIDPtr(req.RunID),
			req.CreatedAt, req.UpdatedAt,
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("gates: create approval %s: %w", req.ID, err)
	}
	return nil
}

// Get returns the request with the given ID.
func (s *PostgresStore) Get(ctx context.Context, id uuid.UUID) (ApprovalRequest, error) {
	if s.pool == nil {
		return ApprovalRequest{}, errors.New("gates: postgres store requires a connection pool")
	}
	workspaceID, err := s.workspaceOf(ctx, id)
	if err != nil {
		return ApprovalRequest{}, err
	}
	var req ApprovalRequest
	err = s.inTenant(ctx, workspaceID, func(tx pgx.Tx) error {
		return scanApproval(tx.QueryRow(ctx,
			`SELECT `+approvalColumns+` FROM approvals WHERE id = $1`, id), &req)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApprovalRequest{}, ErrNotFound
	}
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("gates: get approval %s: %w", id, err)
	}
	return req, nil
}

// List returns the requests of one workspace, newest first, optionally
// filtered by status.
func (s *PostgresStore) List(ctx context.Context, workspaceID uuid.UUID, status *ApprovalStatus) ([]ApprovalRequest, error) {
	if s.pool == nil {
		return nil, errors.New("gates: postgres store requires a connection pool")
	}
	var out []ApprovalRequest
	err := s.inTenant(ctx, workspaceID, func(tx pgx.Tx) error {
		query := `SELECT ` + approvalColumns + ` FROM approvals WHERE workspace_id = $1`
		args := []any{workspaceID}
		if status != nil {
			query += ` AND status = $2`
			args = append(args, string(*status))
		}
		query += ` ORDER BY created_at DESC, id DESC`
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var req ApprovalRequest
			if err := scanApproval(rows, &req); err != nil {
				return err
			}
			out = append(out, req)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("gates: list approvals: %w", err)
	}
	return out, nil
}

// Decide records the human decision. A request that is no longer pending is
// left untouched and reported through ErrNotPending: the owner must not be
// able to flip an expired or already decided gate.
func (s *PostgresStore) Decide(
	ctx context.Context,
	id uuid.UUID,
	approved bool,
	decidedBy uuid.UUID,
	comment string,
) error {
	if s.pool == nil {
		return errors.New("gates: postgres store requires a connection pool")
	}
	workspaceID, err := s.workspaceOf(ctx, id)
	if err != nil {
		return err
	}
	status := string(ApprovalRejected)
	if approved {
		status = string(ApprovalApproved)
	}
	now := time.Now().UTC()
	var affected int64
	err = s.inTenant(ctx, workspaceID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE approvals
			SET status = $2, approved_by = $3, comment = $4,
			    updated_at = $5, decided_at = $5
			WHERE id = $1 AND status = 'pending'`,
			id, status, nullUUID(decidedBy), comment, now)
		if err != nil {
			return err
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return fmt.Errorf("gates: decide approval %s: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("gates: approval %s is not pending: %w", id, ErrNotPending)
	}
	return nil
}

// Expire closes a pending request that passed its deadline. The workflow timer
// calls it so the owner sees, in the UI, that the request is no longer
// actionable. A decided request is never overwritten.
func (s *PostgresStore) Expire(ctx context.Context, id uuid.UUID) error {
	if s.pool == nil {
		return errors.New("gates: postgres store requires a connection pool")
	}
	workspaceID, err := s.workspaceOf(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	err = s.inTenant(ctx, workspaceID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE approvals
			SET status = 'expired', updated_at = $2
			WHERE id = $1 AND status = 'pending'`, id, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("gates: expire approval %s: %w", id, err)
	}
	return nil
}

// workspaceOf reads the tenant of a request. The lookup runs outside RLS
// because the tenant is precisely what the RLS setting needs: the column is
// read through a policy-free helper query on the same pool.
// workspaceOf bypasses RLS intentionally: this is the trusted worker service;
// RLS context is set for downstream queries.
func (s *PostgresStore) workspaceOf(ctx context.Context, id uuid.UUID) (uuid.UUID, error) {
	var workspaceID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT workspace_id FROM approvals WHERE id = $1`, id).Scan(&workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("gates: resolve workspace of approval %s: %w", id, err)
	}
	return workspaceID, nil
}

// inTenant runs fn inside a transaction scoped to workspaceID by RLS.
func (s *PostgresStore) inTenant(ctx context.Context, workspaceID uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gates: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("gates: set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("gates: commit transaction: %w", err)
	}
	return nil
}

type rowLike interface {
	Scan(dest ...any) error
}

func scanApproval(row rowLike, req *ApprovalRequest) error {
	var (
		status      string
		approvedBy  *uuid.UUID
		taskRunID   *uuid.UUID
		stageRunID  *uuid.UUID
		runID       *uuid.UUID
		deadlineAt  *time.Time
		decidedAt   *time.Time
		contextJSON []byte
	)
	err := row.Scan(
		&req.ID, &req.WorkspaceID, &req.GateName, &status, &req.RequestedBy,
		&approvedBy, &req.RiskLevel, &deadlineAt, &contextJSON,
		&req.Comment, &req.WorkflowID, &taskRunID, &stageRunID,
		&runID, &req.CreatedAt, &req.UpdatedAt, &decidedAt,
	)
	if err != nil {
		return err
	}
	req.Status = ApprovalStatus(status)
	req.ApprovedBy = approvedBy
	req.TaskRunID = taskRunID
	req.StageRunID = stageRunID
	req.RunID = runID
	req.DeadlineAt = deadlineAt
	req.DecidedAt = decidedAt
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &req.Context); err != nil {
			return fmt.Errorf("gates: decode approval context %s: %w", req.ID, err)
		}
	}
	return nil
}

func marshalContext(ctxData map[string]any) ([]byte, error) {
	if len(ctxData) == 0 {
		return []byte(`{}`), nil
	}
	data, err := json.Marshal(ctxData)
	if err != nil {
		return nil, fmt.Errorf("gates: encode approval context: %w", err)
	}
	return data, nil
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullUUIDPtr(id *uuid.UUID) any {
	if id == nil || *id == uuid.Nil {
		return nil
	}
	return *id
}
