// Package config loads Agent Runtime worker configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all worker configuration.
type Config struct {
	// TemporalHost is the Temporal server gRPC address.
	TemporalHost string

	// TaskQueue is the Temporal task queue the worker polls.
	TaskQueue string

	// LLMGatewayURL is the base URL of the LLM Gateway service.
	LLMGatewayURL string

	// ToolGatewayURL is the base URL of the Tool Gateway service.
	ToolGatewayURL string

	// SemanticURL is the base URL of the Semantic Service.
	SemanticURL string

	// KnowledgeURL is the base URL of the Knowledge Service.
	KnowledgeURL string

	// DefaultMaxCostUSD is the per-task budget cost limit.
	DefaultMaxCostUSD float64

	// DefaultMaxSteps is the per-task budget steps limit.
	DefaultMaxSteps int
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	maxSteps, err := envInt("DEFAULT_MAX_STEPS", 50)
	if err != nil {
		return nil, err
	}

	maxCost, err := envFloat("DEFAULT_MAX_COST_USD", 10.0)
	if err != nil {
		return nil, err
	}

	return &Config{
		TemporalHost:      env("TEMPORAL_HOST", "temporal:7233"),
		TaskQueue:         env("AI_TEMPORAL_TASK_QUEUE", "ai-sdlc-factory"),
		LLMGatewayURL:     env("LLM_GATEWAY_URL", "http://gateway-llm:8040"),
		ToolGatewayURL:    env("TOOL_GATEWAY_URL", "http://tool-gateway:8030"),
		SemanticURL:       env("SEMANTIC_URL", "http://semantic:8010"),
		KnowledgeURL:      env("KNOWLEDGE_URL", "http://knowledge:8020"),
		DefaultMaxCostUSD: maxCost,
		DefaultMaxSteps:   maxSteps,
	}, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}

func envFloat(key string, fallback float64) (float64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return v, nil
}
