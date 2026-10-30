package providers

import (
	"context"
	"net/http"
	"testing"

	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/config"
)

// TestOpenAICompatNameAndDefaultBaseURLs pins the documented endpoints the
// gateway dials for each OpenAI-compatible provider (issue #112):
//   - Gemini:   https://ai.google.dev/gemini-api/docs/openai
//   - DeepSeek: https://api-docs.deepseek.com/ (base_url, OpenAI format)
//   - Groq:     https://console.groq.com/docs/models
func TestOpenAICompatDefaultBaseURLs(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{"google", config.DefaultGoogleBaseURL},
		{"deepseek", config.DefaultDeepSeekBaseURL},
		{"groq", config.DefaultGroqBaseURL},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			p := NewOpenAICompat(tc.provider, "key", tc.want)
			if p.Name() != tc.provider {
				t.Errorf("Name() = %q, want %q (errors and audit rows attribute to it)", p.Name(), tc.provider)
			}
			if p.baseURL != tc.want {
				t.Errorf("baseURL = %q, want %q", p.baseURL, tc.want)
			}
		})
	}
}

// TestOpenAICompatRequestRouting verifies a compat provider adapter sends the
// OpenAI Chat Completions wire format to <base_url>/chat/completions with a
// Bearer token, and that the provider name is reported on errors.
func TestOpenAICompatRequestRouting(t *testing.T) {
	cases := []struct {
		provider string
		basePath string
		wantPath string
	}{
		{"google", "/v1beta/openai", "/v1beta/openai/chat/completions"},
		{"deepseek", "", "/chat/completions"},
		{"groq", "/openai/v1", "/openai/v1/chat/completions"},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			rec := &recordedRequest{}
			srv := stubServer(t, rec.handler(http.StatusOK, openAISuccessBody))

			p := NewOpenAICompat(tc.provider, "secret", srv.URL+tc.basePath)
			got, err := p.Chat(context.Background(), "model-x", chatReq())
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if got.Content != "pong" {
				t.Errorf("Content = %q, want pong", got.Content)
			}
			if rec.path != tc.wantPath {
				t.Errorf("path = %q, want %q", rec.path, tc.wantPath)
			}
			if h := rec.headerOf("Authorization"); h != "Bearer secret" {
				t.Errorf("Authorization = %q, want 'Bearer secret'", h)
			}

			// Error attribution uses the compat provider name, not "openai".
			errSrv := stubServer(t, staticHandler(http.StatusNotFound, `{"error":{"message":"no model"}}`))
			ep := NewOpenAICompat(tc.provider, "secret", errSrv.URL+tc.basePath)
			_, err = ep.Chat(context.Background(), "model-x", chatReq())
			errorStatus(t, err, Err404ModelNotFound, http.StatusNotFound, tc.provider, "model-x")
		})
	}
}
