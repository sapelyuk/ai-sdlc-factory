package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Masterminds/semver/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
)

type rowScanner interface {
	Scan(...any) error
}

func inTenantTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx, uuid.UUID) error) error {
	workspaceID, err := identity.WorkspaceID(ctx)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin registry transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	if err := fn(tx, workspaceID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit registry transaction: %w", err)
	}
	return nil
}

func marshalSpec(spec any) ([]byte, error) {
	data, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal registry spec: %w", err)
	}
	return data, nil
}

func unmarshalSpec(data []byte, spec any) error {
	if err := json.Unmarshal(data, spec); err != nil {
		return fmt.Errorf("decode registry spec: %w", err)
	}
	return nil
}

func statusTransition(current, next domain.Status, authorID, approverID uuid.UUID) error {
	if !current.CanTransition(next) {
		return domain.ErrInvalidStatusTransition
	}
	if authorID == approverID {
		return domain.ErrApproverIsAuthor
	}
	return nil
}

func nextMinorVersion(version string) (string, error) {
	current, err := semver.NewVersion(version)
	if err != nil {
		return "", fmt.Errorf("parse stored semantic version %q: %w", version, err)
	}
	return current.IncMinor().String(), nil
}

func latestVersion(ctx context.Context, tx pgx.Tx, table string, workspaceID uuid.UUID, name string) (string, error) {
	rows, err := tx.Query(ctx, `SELECT version FROM `+table+` WHERE workspace_id = $1 AND name = $2`, workspaceID, name)
	if err != nil {
		return "", fmt.Errorf("list registry versions: %w", err)
	}
	defer rows.Close()

	var latest *semver.Version
	for rows.Next() {
		var versionText string
		if err := rows.Scan(&versionText); err != nil {
			return "", fmt.Errorf("scan registry version: %w", err)
		}
		version, err := semver.NewVersion(versionText)
		if err != nil {
			return "", fmt.Errorf("parse stored semantic version %q: %w", versionText, err)
		}
		if latest == nil || version.GreaterThan(latest) {
			latest = version
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate registry versions: %w", err)
	}
	if latest == nil {
		return "", domain.ErrNotFound
	}
	return latest.IncMinor().String(), nil
}
