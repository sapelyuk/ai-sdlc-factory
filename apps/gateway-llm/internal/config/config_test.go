package config

import "testing"

// setEnv sets an environment variable for the duration of the test.
func setEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
}

// TestLoadReadsAllProviderKeys verifies every provider credential is read
// from the environment (no hardcoded secrets, issue #112).
func TestLoadReadsAllProviderKeys(t *testing.T) {
	setEnv(t, "AI_ANTHROPIC_API_KEY", "sk-ant")
	setEnv(t, "AI_OPENAI_API_KEY", "sk-oai")
	setEnv(t, "AI_GOOGLE_API_KEY", "sk-google")
	setEnv(t, "AI_DEEPSEEK_API_KEY", "sk-ds")
	setEnv(t, "AI_GROQ_API_KEY", "sk-groq")
	setEnv(t, "AI_OLLAMA_BASE_URL", "http://ollama:11434")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"AnthropicAPIKey", cfg.AnthropicAPIKey, "sk-ant"},
		{"OpenAIAPIKey", cfg.OpenAIAPIKey, "sk-oai"},
		{"GoogleAPIKey", cfg.GoogleAPIKey, "sk-google"},
		{"DeepSeekAPIKey", cfg.DeepSeekAPIKey, "sk-ds"},
		{"GroqAPIKey", cfg.GroqAPIKey, "sk-groq"},
		{"OllamaBaseURL", cfg.OllamaBaseURL, "http://ollama:11434"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestLoadProviderBaseURLDefaults checks that the OpenAI-compatible providers
// fall back to the endpoints documented by each vendor.
func TestLoadProviderBaseURLDefaults(t *testing.T) {
	for _, tt := range []struct {
		envKey string
		got    func(*Config) string
		want   string
	}{
		{"AI_GOOGLE_BASE_URL", func(c *Config) string { return c.GoogleBaseURL }, DefaultGoogleBaseURL},
		{"AI_DEEPSEEK_BASE_URL", func(c *Config) string { return c.DeepSeekBaseURL }, DefaultDeepSeekBaseURL},
		{"AI_GROQ_BASE_URL", func(c *Config) string { return c.GroqBaseURL }, DefaultGroqBaseURL},
	} {
		t.Setenv(tt.envKey, "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got := tt.got(cfg); got != tt.want {
			t.Errorf("%s default = %q, want %q", tt.envKey, got, tt.want)
		}
	}
}

// TestLoadProviderBaseURLOverride checks that an explicit base URL wins,
// which is what tests and private gateways rely on.
func TestLoadProviderBaseURLOverride(t *testing.T) {
	setEnv(t, "AI_GOOGLE_BASE_URL", "https://google.test/v1/openai")
	setEnv(t, "AI_DEEPSEEK_BASE_URL", "https://ds.test")
	setEnv(t, "AI_GROQ_BASE_URL", "https://groq.test/openai/v1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GoogleBaseURL != "https://google.test/v1/openai" {
		t.Errorf("GoogleBaseURL = %q", cfg.GoogleBaseURL)
	}
	if cfg.DeepSeekBaseURL != "https://ds.test" {
		t.Errorf("DeepSeekBaseURL = %q", cfg.DeepSeekBaseURL)
	}
	if cfg.GroqBaseURL != "https://groq.test/openai/v1" {
		t.Errorf("GroqBaseURL = %q", cfg.GroqBaseURL)
	}
}

// TestLoadMissingKeysIsNotFatal documents the contract the catalog filter
// depends on: absent credentials yield an empty string instead of an error.
func TestLoadMissingKeysIsNotFatal(t *testing.T) {
	for _, key := range []string{
		"AI_ANTHROPIC_API_KEY", "AI_OPENAI_API_KEY", "AI_GOOGLE_API_KEY",
		"AI_DEEPSEEK_API_KEY", "AI_GROQ_API_KEY", "AI_OLLAMA_BASE_URL",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no credentials: %v", err)
	}
	if cfg.GoogleAPIKey != "" || cfg.DeepSeekAPIKey != "" || cfg.GroqAPIKey != "" {
		t.Errorf("expected empty keys, got google=%q deepseek=%q groq=%q",
			cfg.GoogleAPIKey, cfg.DeepSeekAPIKey, cfg.GroqAPIKey)
	}
}

// TestLoadInvalidPortStillErrors guards the pre-existing behavior.
func TestLoadInvalidPortStillErrors(t *testing.T) {
	t.Setenv("PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a non-numeric PORT")
	}
}
