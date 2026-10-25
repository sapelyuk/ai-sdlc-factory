package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const anthropicModel = "claude-sonnet-4-20250101"

// anthropicSuccessBody is a canned Messages API answer: one text block that
// must be returned and one non-text block that must be ignored.
const anthropicSuccessBody = `{
	"content": [
		{"type": "text", "text": "pong"},
		{"type": "tool_use", "name": "run_tests", "text": "ignored"}
	],
	"usage": {"input_tokens": 11, "output_tokens": 7}
}`

func TestAnthropicNameAndDefaultBaseURL(t *testing.T) {
	if got := NewAnthropic("key").baseURL; got != anthropicBaseURL {
		t.Errorf("default baseURL = %q, want %q", got, anthropicBaseURL)
	}
	if got := NewAnthropic("key").Name(); got != "anthropic" {
		t.Errorf("Name() = %q, want anthropic", got)
	}
}

// TestAnthropicChatSuccess verifies the unified mapping and the wire format:
// system messages are lifted out of `messages`, max_tokens is pinned and the
// Anthropic auth/version headers are set.
func TestAnthropicChatSuccess(t *testing.T) {
	rec := &recordedRequest{}
	srv := stubServer(t, rec.handler(http.StatusOK, anthropicSuccessBody))

	p := NewAnthropicWithBaseURL("secret-key", srv.URL+"/v1")
	got, err := p.Chat(context.Background(), anthropicModel, chatReqWithTool())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got.Content != "pong" {
		t.Errorf("Content = %q, want %q", got.Content, "pong")
	}
	if got.TokensIn != 11 || got.TokensOut != 7 {
		t.Errorf("tokens = %d/%d, want 11/7", got.TokensIn, got.TokensOut)
	}

	if rec.path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", rec.path)
	}
	if rec.headerOf("x-api-key") != "secret-key" {
		t.Errorf("x-api-key = %q, want secret-key", rec.headerOf("x-api-key"))
	}
	if rec.headerOf("anthropic-version") != anthropicVersion {
		t.Errorf("anthropic-version = %q, want %s", rec.headerOf("anthropic-version"), anthropicVersion)
	}
	if rec.headerOf("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", rec.headerOf("Content-Type"))
	}

	if rec.body["model"] != anthropicModel {
		t.Errorf("payload model = %v, want %s", rec.body["model"], anthropicModel)
	}
	if rec.body["system"] != "be brief" {
		t.Errorf("payload system = %v, want %q", rec.body["system"], "be brief")
	}
	if rec.body["max_tokens"] != float64(8192) {
		t.Errorf("payload max_tokens = %v, want 8192", rec.body["max_tokens"])
	}
	// The system message must not be duplicated inside `messages`.
	msgs, ok := rec.body["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("payload messages = %v, want exactly the user turn", rec.body["messages"])
	}
	if role := msgs[0].(map[string]any)["role"]; role != "user" {
		t.Errorf("messages[0].role = %v, want user", role)
	}
	tools, ok := rec.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("payload tools = %v, want one tool", rec.body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "run_tests" || tool["description"] != "Run the test suite" {
		t.Errorf("payload tool = %v, want run_tests/Run the test suite", tool)
	}
}

func TestAnthropicErrorClasses(t *testing.T) {
	const body = `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`
	for _, tt := range errorClasses {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := stubServer(t, staticHandler(tt.status, body))
			p := NewAnthropicWithBaseURL("key", srv.URL+"/v1")

			_, err := p.Chat(context.Background(), anthropicModel, chatReq())
			if err == nil {
				t.Fatal("Chat returned nil error for an error status")
			}
			errorStatus(t, err, tt.want, tt.status, "anthropic", anthropicModel)
			if !strings.Contains(err.Error(), "rate_limit_error") {
				t.Errorf("error %q does not carry the provider message", err)
			}
		})
	}
}

func TestAnthropicTimeout(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	p := NewAnthropicWithBaseURL("key", srv.URL+"/v1")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Chat(ctx, anthropicModel, chatReq())
	if err == nil {
		t.Fatal("Chat returned nil error after the deadline")
	}
	assertClassify(t, err, ErrTimeout)
}

func TestAnthropicInvalidJSON(t *testing.T) {
	srv := stubServer(t, staticHandler(http.StatusOK, `{"content": [`))
	p := NewAnthropicWithBaseURL("key", srv.URL+"/v1")

	_, err := p.Chat(context.Background(), anthropicModel, chatReq())
	if err == nil {
		t.Fatal("Chat accepted a truncated JSON body")
	}
	if !strings.Contains(err.Error(), "decode anthropic response") {
		t.Errorf("error = %v, want a decode failure", err)
	}
}

// TestAnthropicCanceledContext covers the client-disconnect path: the request
// must abort with the context error rather than a provider error.
func TestAnthropicCanceledContext(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	})
	p := NewAnthropicWithBaseURL("key", srv.URL+"/v1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Chat(ctx, anthropicModel, chatReq()); err == nil {
		t.Fatal("Chat succeeded with a canceled context")
	}
}
