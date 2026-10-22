// Package config loads configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration.
type Config struct {
	DatabaseURL string
	Port        int
	SeedWorkspaceID string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable"
	}

	port := 8010
	if p := os.Getenv("PORT"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("PORT: %w", err)
		}
	}

	seedWorkspaceID := os.Getenv("SEED_WORKSPACE_ID")

	return &Config{
		DatabaseURL:     dbURL,
		Port:            port,
		SeedWorkspaceID: seedWorkspaceID,
	}, nil
}
