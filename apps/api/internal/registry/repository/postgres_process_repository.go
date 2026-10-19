package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

type postgresProcessRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresProcessRepository(pool *pgxpool.Pool) ProcessRepository {
	return &postgresProcessRepository{pool: pool}
}

const processColumns = `id, workspace_id, name, version, spec, status, author_id, approver_id, created_at, updated_at`

func scanProcess(row rowScanner) (*domain.Process, error) {
	var process domain.Process
	var version, status string
	var spec []byte
	var approverID uuid.NullUUID
	if err := row.Scan(
		&process.ID, &process.WorkspaceID, &process.Name, &version, &spec, &status,
		&process.AuthorID, &approverID, &process.CreatedAt, &process.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan process: %w", err)
	}
	parsedVersion, err := semver.NewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("parse stored process version %q: %w", version, err)
	}
	process.Version = parsedVersion
	process.Status = domain.Status(status)
	if approverID.Valid {
		process.ApproverID = &approverID.UUID
	}
	if err := unmarshalSpec(spec, &process.Spec); err != nil {
		return nil, err
	}
	return &process, nil
}

func (r *postgresProcessRepository) Create(ctx context.Context, process *domain.Process) error {
	spec, err := marshalSpec(process.Spec)
	if err != nil {
		return err
	}
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		if process.WorkspaceID != workspaceID {
			return fmt.Errorf("process workspace does not match request tenant")
		}
		authorID, err := identity.AuthorID(ctx)
		if err != nil {
			return err
		}
		process.AuthorID = authorID
		_, err = tx.Exec(ctx, `
			INSERT INTO processes (id, workspace_id, name, version, spec, status, author_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			process.ID, workspaceID, process.Name, process.VersionString(), spec, string(process.Status), authorID)
		if err != nil {
			return fmt.Errorf("create process: %w", err)
		}
		return nil
	})
}

func (r *postgresProcessRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
	var result *domain.Process
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		var err error
		result, err = scanProcess(tx.QueryRow(ctx,
			`SELECT `+processColumns+` FROM processes WHERE id = $1 AND workspace_id = $2`, id, workspaceID))
		return err
	})
	return result, err
}

func (r *postgresProcessRepository) GetByNameAndVersion(ctx context.Context, name, version string) (*domain.Process, error) {
	var result *domain.Process
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		var err error
		result, err = scanProcess(tx.QueryRow(ctx,
			`SELECT `+processColumns+` FROM processes WHERE workspace_id = $1 AND name = $2 AND version = $3`,
			workspaceID, name, version))
		return err
	})
	return result, err
}

func (r *postgresProcessRepository) List(ctx context.Context, status string) ([]*domain.Process, error) {
	var result []*domain.Process
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		query := `SELECT ` + processColumns + ` FROM processes WHERE workspace_id = $1`
		args := []any{workspaceID}
		if status != "" {
			args = append(args, status)
			query += fmt.Sprintf(" AND status = $%d", len(args))
		}
		query += ` ORDER BY name, version`
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list processes: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			process, err := scanProcess(rows)
			if err != nil {
				return err
			}
			result = append(result, process)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate processes: %w", err)
		}
		return nil
	})
	return result, err
}

func (r *postgresProcessRepository) Update(ctx context.Context, id uuid.UUID, newSpec domain.ProcessSpec) error {
	spec, err := marshalSpec(newSpec)
	if err != nil {
		return err
	}
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		current, err := scanProcess(tx.QueryRow(ctx,
			`SELECT `+processColumns+` FROM processes WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if current.Status != domain.StatusDraft {
			return domain.ErrImmutable
		}
		if _, err := tx.Exec(ctx,
			`UPDATE processes SET spec = $1 WHERE id = $2 AND workspace_id = $3`, spec, id, workspaceID); err != nil {
			return fmt.Errorf("update process spec: %w", err)
		}
		return nil
	})
}

func (r *postgresProcessRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status, approverID uuid.UUID) error {
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		current, err := scanProcess(tx.QueryRow(ctx,
			`SELECT `+processColumns+` FROM processes WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if err := statusTransition(current.Status, status, current.AuthorID, approverID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE processes SET status = $1, approver_id = $2 WHERE id = $3 AND workspace_id = $4`,
			string(status), approverID, id, workspaceID)
		if err != nil {
			return fmt.Errorf("update process status: %w", err)
		}
		return nil
	})
}

func (r *postgresProcessRepository) Clone(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
	var result *domain.Process
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		source, err := scanProcess(tx.QueryRow(ctx,
			`SELECT `+processColumns+` FROM processes WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			workspaceID.String()+":"+source.Name); err != nil {
			return fmt.Errorf("lock process version sequence: %w", err)
		}
		version, err := latestVersion(ctx, tx, "processes", workspaceID, source.Name)
		if err != nil {
			return err
		}
		result = source.Clone()
		result.Version, err = semver.NewVersion(version)
		if err != nil {
			return fmt.Errorf("parse next process version: %w", err)
		}
		result.AuthorID, err = identity.AuthorID(ctx)
		if err != nil {
			return err
		}
		return insertProcess(ctx, tx, result)
	})
	return result, err
}

func (r *postgresProcessRepository) Revert(ctx context.Context, id uuid.UUID) (*domain.Process, error) {
	var result *domain.Process
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		source, err := scanProcess(tx.QueryRow(ctx,
			`SELECT `+processColumns+` FROM processes WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			workspaceID.String()+":"+source.Name); err != nil {
			return fmt.Errorf("lock process version sequence: %w", err)
		}
		previous, err := previousProcess(ctx, tx, workspaceID, source.Name, source.Version)
		if err != nil {
			return err
		}
		version, err := latestVersion(ctx, tx, "processes", workspaceID, source.Name)
		if err != nil {
			return err
		}
		result = source.Revert(previous.Spec)
		result.Version, err = semver.NewVersion(version)
		if err != nil {
			return fmt.Errorf("parse next process version: %w", err)
		}
		result.AuthorID, err = identity.AuthorID(ctx)
		if err != nil {
			return err
		}
		return insertProcess(ctx, tx, result)
	})
	return result, err
}

func insertProcess(ctx context.Context, tx pgx.Tx, process *domain.Process) error {
	spec, err := marshalSpec(process.Spec)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO processes (id, workspace_id, name, version, spec, status, author_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		process.ID, process.WorkspaceID, process.Name, process.VersionString(),
		spec, string(process.Status), process.AuthorID)
	if err != nil {
		return fmt.Errorf("insert cloned process: %w", err)
	}
	return nil
}

func previousProcess(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, name string, before *semver.Version) (*domain.Process, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+processColumns+` FROM processes WHERE workspace_id = $1 AND name = $2`,
		workspaceID, name)
	if err != nil {
		return nil, fmt.Errorf("find previous process version: %w", err)
	}
	defer rows.Close()
	var previous *domain.Process
	for rows.Next() {
		process, err := scanProcess(rows)
		if err != nil {
			return nil, err
		}
		if process.Version.LessThan(before) && (previous == nil || process.Version.GreaterThan(previous.Version)) {
			previous = process
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate process versions: %w", err)
	}
	if previous == nil {
		return nil, domain.ErrNotFound
	}
	return previous, nil
}
