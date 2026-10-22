// Package seed provides the SLP v0 ontology loader.
package seed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/internal/identity"
	"gopkg.in/yaml.v3"
)

// SLPCoreClass represents a single SLP class definition.
type SLPCoreClass struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// SLPCoreDoc represents the slp-core.yaml document.
type SLPCoreDoc struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
	Classes []SLPCoreClass `yaml:"classes"`
}

// LoadSLPv0 reads the SLP core YAML and inserts nodes into sem_nodes.
// Idempotent: uses INSERT ... WHERE NOT EXISTS + ON CONFLICT DO NOTHING.
func LoadSLPv0(ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID) (int, error) {
	slpPath, err := findSLPFile()
	if err != nil {
		return 0, err
	}

	data, err := os.ReadFile(slpPath)
	if err != nil {
		return 0, fmt.Errorf("read SLP file: %w", err)
	}

	var doc SLPCoreDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return 0, fmt.Errorf("parse SLP YAML: %w", err)
	}

	if doc.APIVersion != "ai-sdlc-factory/v1" || doc.Kind != "SLP" {
		return 0, fmt.Errorf("SLP must declare apiVersion ai-sdlc-factory/v1 and kind SLP")
	}

	tenantCtx := identity.WithTenant(ctx, workspaceID, workspaceID)

	count := 0
	for _, class := range doc.Classes {
		if class.Name == "" {
			continue
		}
		props := map[string]any{
			"description": class.Description,
			"slp_version": doc.Metadata.Version,
		}
		var inserted bool
		err := inSeedTx(tenantCtx, pool, workspaceID, func(tx pgx.Tx) error {
			result, err := tx.Exec(ctx, `
				INSERT INTO sem_nodes (workspace_id, layer, kind, label, props, version, status)
				SELECT $1, 'SLP', $2, $3, $4::jsonb, 1, 'active'
				WHERE NOT EXISTS (
					SELECT 1 FROM sem_nodes
					WHERE workspace_id = $1 AND layer = 'SLP' AND kind = $2
				)
				ON CONFLICT DO NOTHING`,
				workspaceID, class.Name, class.Name, props)
			if err != nil {
				return fmt.Errorf("insert SLP class %q: %w", class.Name, err)
			}
			affected := result.RowsAffected()
			inserted = affected > 0
			return nil
		})
		if err != nil {
			return count, err
		}
		if inserted {
			count++
		}
	}

	return count, nil
}

// findSLPFile locates the slp-core.yaml file.
func findSLPFile() (string, error) {
	if configured := os.Getenv("SLP_PATH"); configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
		return "", fmt.Errorf("open configured SLP file %q: not found", configured)
	}

	// Try relative to runtime caller.
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		candidate := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "..", "packages", "ontology", "slp", "slp-core.yaml")
		abs, err := filepath.Abs(candidate)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs, nil
			}
		}
	}

	// Walk up from working directory.
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		candidate := filepath.Join(dir, "packages", "ontology", "slp", "slp-core.yaml")
		if _, err := os.Stat(candidate); err == nil {
			abs, _ := filepath.Abs(candidate)
			return abs, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("could not locate slp-core.yaml; set SLP_PATH")
}

// inSeedTx runs a function in a transaction with RLS context set.
func inSeedTx(ctx context.Context, pool *pgxpool.Pool, workspaceID uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
