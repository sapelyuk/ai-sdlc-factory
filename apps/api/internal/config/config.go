// Package config loads configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all application configuration.
type Config struct {
	DatabaseURL       string
	Port              int
	LogLevel          string
	JWTSecret         string
	JWTDuration       time.Duration
	AllowLegacyAuth   bool
	DeploymentProfile string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable"
	}

	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("PORT: %w", err)
		}
	}

	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	jwtSecret := os.Getenv("JWT_SECRET")

	jwtDuration := 24 * time.Hour
	if jd := os.Getenv("JWT_DURATION"); jd != "" {
		d, err := time.ParseDuration(jd)
		if err != nil {
			return nil, fmt.Errorf("JWT_DURATION: %w", err)
		}
		jwtDuration = d
	}

	deploymentProfile := os.Getenv("DEPLOYMENT_PROFILE")
	if deploymentProfile == "" {
		deploymentProfile = "prod"
	}
	allowLegacy := os.Getenv("ALLOW_LEGACY_AUTH") == "true"
	if allowLegacy && deploymentProfile != "local-dev" {
		return nil, fmt.Errorf("ALLOW_LEGACY_AUTH is only permitted in the local-dev deployment profile")
	}

	return &Config{
		DatabaseURL:       dbURL,
		Port:              port,
		LogLevel:          logLevel,
		JWTSecret:         jwtSecret,
		JWTDuration:       jwtDuration,
		AllowLegacyAuth:   allowLegacy,
		DeploymentProfile: deploymentProfile,
	}, nil
}

// ValidateAPIAuth verifies configuration required only by the HTTP API.
func (c *Config) ValidateAPIAuth() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET must be set")
	}
	return nil
}
