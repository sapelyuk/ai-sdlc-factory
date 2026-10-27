// Package config loads Sandbox Manager configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

const (
	defaultPort            = 8060
	defaultDiskLimit int64 = 2 * 1024 * 1024 * 1024
)

// Config contains runtime settings for the sandbox manager.
type Config struct {
	Port                int
	DockerHost          string
	Runtime             string
	DatabaseURL         string
	SandboxNetwork      string
	SandboxImage        string
	DiskLimitBytes      int64
	MaxTimeoutSeconds   int
	MaxOutputLimitBytes int
}

// Load reads and validates configuration.
func Load() (*Config, error) {
	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost == "" {
		return nil, fmt.Errorf("DOCKER_HOST is required; configure docker-socket-proxy")
	}

	port, err := envInt("PORT", defaultPort, 1, 65535)
	if err != nil {
		return nil, err
	}
	diskLimit, err := envInt64("SANDBOX_DISK_LIMIT_BYTES", defaultDiskLimit, 1, 1<<62)
	if err != nil {
		return nil, err
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	runtime := os.Getenv("RUNTIME")
	if runtime == "" {
		runtime = "runc"
	}
	if runtime != "runsc" && runtime != "runc" {
		return nil, fmt.Errorf("RUNTIME must be runsc or runc, got %q", runtime)
	}

	return &Config{
		Port:                port,
		DockerHost:          dockerHost,
		Runtime:             runtime,
		DatabaseURL:         databaseURL,
		SandboxNetwork:      env("SANDBOX_NETWORK", "sandbox-net"),
		SandboxImage:        env("SANDBOX_IMAGE", "ai-sdlc-factory/sandbox-go-node:latest"),
		DiskLimitBytes:      diskLimit,
		MaxTimeoutSeconds:   3600,
		MaxOutputLimitBytes: 16 * 1024 * 1024,
	}, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback, min, max int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return value, nil
}

func envInt64(key string, fallback, min, max int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("%s must be between %d and %d", key, min, max)
	}
	return value, nil
}
