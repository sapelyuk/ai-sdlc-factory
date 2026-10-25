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

	// Provider API keys.
	AnthropicAPIKey string
	OpenAIAPIKey    string
	OllamaBaseURL   string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/ai_sdlc_factory?sslmode=disable"
	}

	port := 8040
	if p := os.Getenv("PORT"); p != "" {
		var err error
		port, err = strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("PORT: %w", err)
		}
	}

	return &Config{
		DatabaseURL:     dbURL,
		Port:            port,
		AnthropicAPIKey: os.Getenv("AI_ANTHROPIC_API_KEY"),
		OpenAIAPIKey:    os.Getenv("AI_OPENAI_API_KEY"),
		OllamaBaseURL:   os.Getenv("AI_OLLAMA_BASE_URL"),
	}, nil
}
