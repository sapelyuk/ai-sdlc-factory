// Seed users creates initial admin user for the system.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/config"
	"github.com/sapelyuk/ai-sdlc-factory/apps/api/internal/iam"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("seed-users failed", "error", err)
		os.Exit(1)
	}
	slog.Info("seed-users completed")
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	// Create admin user.
	email := os.Getenv("SEED_ADMIN_EMAIL")
	if email == "" {
		email = "admin@example.com"
	}
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if password == "" {
		password = "admin"
	}
	workspaceID := os.Getenv("SEED_WORKSPACE_ID")
	if workspaceID == "" {
		return fmt.Errorf("SEED_WORKSPACE_ID is required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("generate password hash: %w", err)
	}

	// Check if user already exists.
	var exists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email = $1)", email).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check existing user: %w", err)
	}
	if exists {
		slog.Info("admin user already exists", "email", email)
		return nil
	}

	// Insert admin user with owner role.
	user := &iam.User{
		WorkspaceID:  workspaceID,
		Email:        email,
		PasswordHash: string(hash),
		Role:         "owner",
	}
	if err := iam.NewPostgresUserRepository(pool).Create(ctx, user); err != nil {
		return fmt.Errorf("create admin user: %w", err)
	}

	slog.Info("admin user created", "email", email, "workspace", workspaceID)
	return nil
}
