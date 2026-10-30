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

	// OpenAI-compatible providers. Each carries its own API key and base URL;
	// the base URL defaults to the provider's documented endpoint and can be
	// overridden for tests or when a provider moves its API surface.
	GoogleAPIKey    string
	GoogleBaseURL   string
	DeepSeekAPIKey  string
	DeepSeekBaseURL string
	GroqAPIKey      string
	GroqBaseURL     string
}

// Default base URLs for the OpenAI-compatible providers, taken from the
// official documentation on 2026-10-29:
//   - Gemini:  https://ai.google.dev/gemini-api/docs/openai
//   - DeepSeek: https://api-docs.deepseek.com/ (base_url, OpenAI format)
//   - Groq:    https://console.groq.com/docs/models
const (
	DefaultGoogleBaseURL   = "https://generativelanguage.googleapis.com/v1beta/openai"
	DefaultDeepSeekBaseURL = "https://api.deepseek.com"
	DefaultGroqBaseURL     = "https://api.groq.com/openai/v1"
)

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
		GoogleAPIKey:    os.Getenv("AI_GOOGLE_API_KEY"),
		GoogleBaseURL:   envOr("AI_GOOGLE_BASE_URL", DefaultGoogleBaseURL),
		DeepSeekAPIKey:  os.Getenv("AI_DEEPSEEK_API_KEY"),
		DeepSeekBaseURL: envOr("AI_DEEPSEEK_BASE_URL", DefaultDeepSeekBaseURL),
		GroqAPIKey:      os.Getenv("AI_GROQ_API_KEY"),
		GroqBaseURL:     envOr("AI_GROQ_BASE_URL", DefaultGroqBaseURL),
	}, nil
}

// envOr returns the value of the environment variable named key, or def when
// the variable is unset or empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
