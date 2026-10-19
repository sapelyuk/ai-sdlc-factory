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

type postgresRoleRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRoleRepository(pool *pgxpool.Pool) RoleRepository {
	return &postgresRoleRepository{pool: pool}
}

func scanRole(row rowScanner) (*domain.Role, error) {
	var role domain.Role
	var version, category, status string
	var spec []byte
	var approverID uuid.NullUUID
	if err := row.Scan(
		&role.ID, &role.WorkspaceID, &role.Name, &version, &category, &spec,
		&status, &role.AuthorID, &approverID, &role.CreatedAt, &role.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan role: %w", err)
	}
	parsedVersion, err := semver.NewVersion(version)
	if err != nil {
		return nil, fmt.Errorf("parse stored role version %q: %w", version, err)
	}
	role.Version = parsedVersion
	role.Category = domain.Category(category)
	role.Status = domain.Status(status)
	if approverID.Valid {
		role.ApproverID = &approverID.UUID
	}
	if err := unmarshalSpec(spec, &role.Spec); err != nil {
		return nil, err
	}
	return &role, nil
}

const roleColumns = `id, workspace_id, name, version, category, spec, status, author_id, approver_id, created_at, updated_at`

func (r *postgresRoleRepository) Create(ctx context.Context, role *domain.Role) error {
	spec, err := marshalSpec(role.Spec)
	if err != nil {
		return err
	}
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		if role.WorkspaceID != workspaceID {
			return fmt.Errorf("role workspace does not match request tenant")
		}
		authorID, err := identity.AuthorID(ctx)
		if err != nil {
			return err
		}
		role.AuthorID = authorID
		_, err = tx.Exec(ctx, `
			INSERT INTO roles (id, workspace_id, name, version, category, spec, status, author_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			role.ID, workspaceID, role.Name, role.VersionString(), string(role.Category),
			spec, string(role.Status), authorID)
		if err != nil {
			return fmt.Errorf("create role: %w", err)
		}
		return nil
	})
}

func (r *postgresRoleRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Role, error) {
	var result *domain.Role
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		var err error
		result, err = scanRole(tx.QueryRow(ctx,
			`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND workspace_id = $2`, id, workspaceID))
		return err
	})
	return result, err
}

func (r *postgresRoleRepository) GetByNameAndVersion(ctx context.Context, name, version string) (*domain.Role, error) {
	var result *domain.Role
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		var err error
		result, err = scanRole(tx.QueryRow(ctx,
			`SELECT `+roleColumns+` FROM roles WHERE workspace_id = $1 AND name = $2 AND version = $3`,
			workspaceID, name, version))
		return err
	})
	return result, err
}

func (r *postgresRoleRepository) List(ctx context.Context, category, status string) ([]*domain.Role, error) {
	var result []*domain.Role
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		query := `SELECT ` + roleColumns + ` FROM roles WHERE workspace_id = $1`
		args := []any{workspaceID}
		if category != "" {
			args = append(args, category)
			query += fmt.Sprintf(" AND category = $%d", len(args))
		}
		if status != "" {
			args = append(args, status)
			query += fmt.Sprintf(" AND status = $%d", len(args))
		}
		query += ` ORDER BY name, version`
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list roles: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			role, err := scanRole(rows)
			if err != nil {
				return err
			}
			result = append(result, role)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate roles: %w", err)
		}
		return nil
	})
	return result, err
}

func (r *postgresRoleRepository) Update(ctx context.Context, id uuid.UUID, newSpec domain.RoleSpec) error {
	spec, err := marshalSpec(newSpec)
	if err != nil {
		return err
	}
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		current, err := scanRole(tx.QueryRow(ctx,
			`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if current.Status != domain.StatusDraft {
			return domain.ErrImmutable
		}
		if _, err := tx.Exec(ctx,
			`UPDATE roles SET spec = $1 WHERE id = $2 AND workspace_id = $3`, spec, id, workspaceID); err != nil {
			return fmt.Errorf("update role spec: %w", err)
		}
		return nil
	})
}

func (r *postgresRoleRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.Status, approverID uuid.UUID) error {
	return inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		current, err := scanRole(tx.QueryRow(ctx,
			`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if err := statusTransition(current.Status, status, current.AuthorID, approverID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`UPDATE roles SET status = $1, approver_id = $2 WHERE id = $3 AND workspace_id = $4`,
			string(status), approverID, id, workspaceID)
		if err != nil {
			return fmt.Errorf("update role status: %w", err)
		}
		return nil
	})
}

func (r *postgresRoleRepository) Clone(ctx context.Context, id uuid.UUID) (*domain.Role, error) {
	var result *domain.Role
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		source, err := scanRole(tx.QueryRow(ctx,
			`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			workspaceID.String()+":"+source.Name); err != nil {
			return fmt.Errorf("lock role version sequence: %w", err)
		}
		version, err := latestVersion(ctx, tx, "roles", workspaceID, source.Name)
		if err != nil {
			return err
		}
		parsedVersion, err := semver.NewVersion(version)
		if err != nil {
			return fmt.Errorf("parse next role version: %w", err)
		}
		result = source.Clone()
		result.Version = parsedVersion
		result.AuthorID, err = identity.AuthorID(ctx)
		if err != nil {
			return err
		}
		return insertRole(ctx, tx, result)
	})
	return result, err
}

func (r *postgresRoleRepository) Revert(ctx context.Context, id uuid.UUID) (*domain.Role, error) {
	var result *domain.Role
	err := inTenantTx(ctx, r.pool, func(tx pgx.Tx, workspaceID uuid.UUID) error {
		source, err := scanRole(tx.QueryRow(ctx,
			`SELECT `+roleColumns+` FROM roles WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
			id, workspaceID))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			workspaceID.String()+":"+source.Name); err != nil {
			return fmt.Errorf("lock role version sequence: %w", err)
		}
		previous, err := previousRole(ctx, tx, workspaceID, source.Name, source.Version)
		if err != nil {
			return err
		}
		version, err := latestVersion(ctx, tx, "roles", workspaceID, source.Name)
		if err != nil {
			return err
		}
		parsedVersion, err := semver.NewVersion(version)
		if err != nil {
			return fmt.Errorf("parse next role version: %w", err)
		}
		result = source.Revert(previous.Spec)
		result.Version = parsedVersion
		result.AuthorID, err = identity.AuthorID(ctx)
		if err != nil {
			return err
		}
		return insertRole(ctx, tx, result)
	})
	return result, err
}

func insertRole(ctx context.Context, tx pgx.Tx, role *domain.Role) error {
	spec, err := marshalSpec(role.Spec)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO roles (id, workspace_id, name, version, category, spec, status, author_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		role.ID, role.WorkspaceID, role.Name, role.VersionString(), string(role.Category),
		spec, string(role.Status), role.AuthorID)
	if err != nil {
		return fmt.Errorf("insert cloned role: %w", err)
	}
	return nil
}

func previousRole(ctx context.Context, tx pgx.Tx, workspaceID uuid.UUID, name string, before *semver.Version) (*domain.Role, error) {
	rows, err := tx.Query(ctx,
		`SELECT `+roleColumns+` FROM roles WHERE workspace_id = $1 AND name = $2`,
		workspaceID, name)
	if err != nil {
		return nil, fmt.Errorf("find previous role version: %w", err)
	}
	defer rows.Close()
	var previous *domain.Role
	for rows.Next() {
		role, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		if role.Version.LessThan(before) && (previous == nil || role.Version.GreaterThan(previous.Version)) {
			previous = role
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role versions: %w", err)
	}
	if previous == nil {
		return nil, domain.ErrNotFound
	}
	return previous, nil
}
