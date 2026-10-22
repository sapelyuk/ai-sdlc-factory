// Package main is a standalone CLI to seed SLP v0 ontology.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/semantic/seed"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable"
	}

	workspaceEnv := os.Getenv("SEED_WORKSPACE_ID")
	if workspaceEnv == "" {
		slog.Error("SEED_WORKSPACE_ID must be set")
		os.Exit(1)
	}
	workspaceID, err := uuid.Parse(workspaceEnv)
	if err != nil {
		slog.Error("invalid SEED_WORKSPACE_ID", "error", err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	ctx := context.Background()
	if err := pool.Ping(ctx); err != nil {
		slog.Error("failed to ping database", "error", err)
		os.Exit(1)
	}

	slog.Info("seeding SLP v0 ontology", "workspace", workspaceID)
	count, err := seed.LoadSLPv0(ctx, pool, workspaceID)
	if err != nil {
		slog.Error("SLP seed failed", "error", err)
		os.Exit(1)
	}
	slog.Info("SLP v0 seed completed", "nodes", count)
}
