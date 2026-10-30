package catalog

import (
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/config"
)

// fullConfig returns a config where every provider has credentials.
func fullConfig() *config.Config {
	return &config.Config{
		AnthropicAPIKey: "sk-ant",
		OpenAIAPIKey:    "sk-oai",
		GoogleAPIKey:    "sk-google",
		DeepSeekAPIKey:  "sk-ds",
		GroqAPIKey:      "sk-groq",
		OllamaBaseURL:   "http://ollama:11434",
	}
}

// TestProviderConfiguredAllProviders pins the matrix: with full credentials
// every known provider is configured.
func TestProviderConfiguredAllProviders(t *testing.T) {
	c := &Catalog{cfg: fullConfig()}
	for _, provider := range []string{"anthropic", "openai", "google", "deepseek", "groq", "ollama"} {
		if !c.providerConfigured(provider) {
			t.Errorf("providerConfigured(%q) = false, want true with full credentials", provider)
		}
	}
}

// TestProviderConfiguredMissingCredential is the core guarantee of issue #112:
// a provider without credentials is reported as not configured, so its rows
// never reach the catalog and the router never calls it.
func TestProviderConfiguredMissingCredential(t *testing.T) {
	cases := []struct {
		provider string
		clear    func(*config.Config)
	}{
		{"anthropic", func(c *config.Config) { c.AnthropicAPIKey = "" }},
		{"openai", func(c *config.Config) { c.OpenAIAPIKey = "" }},
		{"google", func(c *config.Config) { c.GoogleAPIKey = "" }},
		{"deepseek", func(c *config.Config) { c.DeepSeekAPIKey = "" }},
		{"groq", func(c *config.Config) { c.GroqAPIKey = "" }},
		// Ollama has no API key: its local endpoint URL is the credential.
		{"ollama", func(c *config.Config) { c.OllamaBaseURL = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			cfg := fullConfig()
			tc.clear(cfg)
			c := &Catalog{cfg: cfg}

			if c.providerConfigured(tc.provider) {
				t.Errorf("providerConfigured(%q) = true, want false when its credential is empty", tc.provider)
			}
			// Clearing one provider must not hide the others.
			for _, other := range []string{"anthropic", "openai", "google", "deepseek", "groq", "ollama"} {
				if other == tc.provider {
					continue
				}
				if !c.providerConfigured(other) {
					t.Errorf("providerConfigured(%q) regressed after clearing %q", other, tc.provider)
				}
			}
		})
	}
}

// TestProviderConfiguredUnknownProvider fails closed: a provider that the
// gateway does not know how to authenticate is never loaded.
func TestProviderConfiguredUnknownProvider(t *testing.T) {
	c := &Catalog{cfg: fullConfig()}
	if c.providerConfigured("mistral") {
		t.Error("providerConfigured(mistral) = true, want false for an unknown provider")
	}
	if c.providerConfigured("") {
		t.Error("providerConfigured(empty) = true, want false")
	}
}

// TestProviderConfiguredNilConfigIsOpen documents the test-only mode of the
// routing integration suite: without a config nothing is filtered.
func TestProviderConfiguredNilConfigIsOpen(t *testing.T) {
	c := &Catalog{}
	for _, provider := range []string{"anthropic", "openai", "google", "deepseek", "groq", "ollama", "custom-fake"} {
		if !c.providerConfigured(provider) {
			t.Errorf("providerConfigured(%q) = false, want true when no config is set", provider)
		}
	}
}
