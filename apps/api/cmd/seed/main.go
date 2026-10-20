package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/identity"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/domain"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/registry/repository"
	"gopkg.in/yaml.v3"
)

type seedMetadata struct {
	Name     string          `yaml:"name"`
	Version  string          `yaml:"version"`
	Category domain.Category `yaml:"category"`
}

type seedRole struct {
	Metadata seedMetadata    `yaml:"metadata"`
	Spec     domain.RoleSpec `yaml:"spec"`
}

type seedTeam struct {
	Metadata seedMetadata    `yaml:"metadata"`
	Spec     domain.TeamSpec `yaml:"spec"`
}

type seedProcess struct {
	Metadata seedMetadata       `yaml:"metadata"`
	Spec     domain.ProcessSpec `yaml:"spec"`
}

type seedDocument struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Metadata   seedMetadata  `yaml:"metadata"`
	Roles      []seedRole    `yaml:"roles"`
	Teams      []seedTeam    `yaml:"teams"`
	Processes  []seedProcess `yaml:"processes"`
}

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("Registry seed failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	workspaceID, generatedWorkspace, err := seedID("SEED_WORKSPACE_ID")
	if err != nil {
		return err
	}
	authorID, generatedAuthor, err := seedID("SEED_AUTHOR_ID")
	if err != nil {
		return err
	}
	if generatedWorkspace || generatedAuthor {
		slog.Warn("generated seed identity UUIDs are only for local development; set SEED_WORKSPACE_ID and SEED_AUTHOR_ID for persistent environments",
			"workspace_id", workspaceID, "author_id", authorID)
	}

	slpPath, err := locateFile(os.Getenv("SLP_PATH"), filepath.Join("packages", "ontology", "slp", "slp-core.yaml"))
	if err != nil {
		return err
	}
	schemaDir, err := locateFile("", filepath.Join("packages", "contracts", "schemas", "role.schema.json"))
	if err != nil {
		return err
	}
	schemaDir = filepath.Dir(schemaDir)

	data, err := os.ReadFile(slpPath)
	if err != nil {
		return fmt.Errorf("read SLP file: %w", err)
	}
	var seed seedDocument
	if err := yaml.Unmarshal(data, &seed); err != nil {
		return fmt.Errorf("parse SLP YAML: %w", err)
	}
	if seed.APIVersion != "ai-sdlc-factory/v1" || seed.Kind != "SLP" {
		return fmt.Errorf("SLP must declare apiVersion ai-sdlc-factory/v1 and kind SLP")
	}

	roleSchema, err := jsonschema.Compile(filepath.Join(schemaDir, "role.schema.json"))
	if err != nil {
		return fmt.Errorf("compile role JSON-Schema: %w", err)
	}
	teamSchema, err := jsonschema.Compile(filepath.Join(schemaDir, "team.schema.json"))
	if err != nil {
		return fmt.Errorf("compile team JSON-Schema: %w", err)
	}
	processSchema, err := jsonschema.Compile(filepath.Join(schemaDir, "process.schema.json"))
	if err != nil {
		return fmt.Errorf("compile process JSON-Schema: %w", err)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	ctx = identity.WithTenant(ctx, workspaceID, authorID)
	if err := ensureWorkspace(ctx, pool, workspaceID, authorID); err != nil {
		return err
	}
	roles := repository.NewPostgresRoleRepository(pool)
	teams := repository.NewPostgresTeamRepository(pool)
	processes := repository.NewPostgresProcessRepository(pool)

	for _, item := range seed.Roles {
		if err := validate(roleSchema, item.Spec); err != nil {
			return fmt.Errorf("role %q has invalid spec: %w", item.Metadata.Name, err)
		}
		version := seedVersion(item.Metadata.Version)
		_, err := roles.GetByNameAndVersion(ctx, item.Metadata.Name, version)
		if err == nil {
			slog.Info("seed role already exists", "name", item.Metadata.Name, "version", version)
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("check seeded role %q: %w", item.Metadata.Name, err)
		}
		role := domain.NewRole(workspaceID, authorID, item.Metadata.Name, item.Metadata.Category, item.Spec)
		if err := roles.Create(ctx, role); err != nil {
			return fmt.Errorf("create seeded role %q: %w", item.Metadata.Name, err)
		}
	}
	for _, item := range seed.Teams {
		if err := validate(teamSchema, item.Spec); err != nil {
			return fmt.Errorf("team %q has invalid spec: %w", item.Metadata.Name, err)
		}
		version := seedVersion(item.Metadata.Version)
		_, err := teams.GetByNameAndVersion(ctx, item.Metadata.Name, version)
		if err == nil {
			slog.Info("seed team already exists", "name", item.Metadata.Name, "version", version)
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("check seeded team %q: %w", item.Metadata.Name, err)
		}
		team := domain.NewTeam(workspaceID, authorID, item.Metadata.Name, item.Spec)
		if err := teams.Create(ctx, team); err != nil {
			return fmt.Errorf("create seeded team %q: %w", item.Metadata.Name, err)
		}
	}
	for _, item := range seed.Processes {
		if err := validate(processSchema, item.Spec); err != nil {
			return fmt.Errorf("process %q has invalid spec: %w", item.Metadata.Name, err)
		}
		version := seedVersion(item.Metadata.Version)
		_, err := processes.GetByNameAndVersion(ctx, item.Metadata.Name, version)
		if err == nil {
			slog.Info("seed process already exists", "name", item.Metadata.Name, "version", version)
			continue
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("check seeded process %q: %w", item.Metadata.Name, err)
		}
		process := domain.NewProcess(workspaceID, authorID, item.Metadata.Name, item.Spec)
		if err := processes.Create(ctx, process); err != nil {
			return fmt.Errorf("create seeded process %q: %w", item.Metadata.Name, err)
		}
	}
	slog.Info("SLP v0 seed completed", "roles", len(seed.Roles), "teams", len(seed.Teams), "processes", len(seed.Processes))
	return nil
}

func seedID(env string) (uuid.UUID, bool, error) {
	value := os.Getenv(env)
	if value == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("generate %s: %w", env, err)
		}
		return id, true, nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false, fmt.Errorf("%s must be a non-zero UUID", env)
	}
	return id, false, nil
}

func locateFile(configured, relative string) (string, error) {
	if configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("open configured file %q: %w", configured, err)
		}
		return abs, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		candidate := filepath.Join(dir, relative)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not locate %q from working directory", relative)
}

func ensureWorkspace(ctx context.Context, pool *pgxpool.Pool, workspaceID, authorID uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin workspace seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_workspace_id', $1, true)`, workspaceID.String()); err != nil {
		return fmt.Errorf("set workspace seed context: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO workspaces (id, name, owner_id)
		VALUES ($1, 'ai-sdlc-factory-local', $2)
		ON CONFLICT (id) DO NOTHING`, workspaceID, authorID.String())
	if err != nil {
		return fmt.Errorf("ensure seed workspace: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit workspace seed transaction: %w", err)
	}
	return nil
}

func seedVersion(value string) string {
	if value == "" {
		return "1.0.0"
	}
	return value
}

func validate(schema *jsonschema.Schema, spec any) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return fmt.Errorf("marshal spec: %w", err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode spec: %w", err)
	}
	return schema.Validate(value)
}
