package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const openAIModel = "gpt-4o-2024-11-20"

// openAISuccessBody is a canned Chat Completions answer with two choices, of
// which only the first one must be returned.
const openAISuccessBody = `{
	"choices": [
		{"message": {"role": "assistant", "content": "pong"}},
		{"message": {"role": "assistant", "content": "ignored"}}
	],
	"usage": {"prompt_tokens": 21, "completion_tokens": 9}
}`

func TestOpenAINameAndDefaultBaseURL(t *testing.T) {
	if got := NewOpenAI("key").baseURL; got != openAIBaseURL {
		t.Errorf("default baseURL = %q, want %q", got, openAIBaseURL)
	}
	if got := NewOpenAI("key").Name(); got != "openai" {
		t.Errorf("Name() = %q, want openai", got)
	}
}

// TestOpenAIChatSuccess verifies the unified mapping and the wire format,
// including the Bearer authorization header and the function-tool envelope.
func TestOpenAIChatSuccess(t *testing.T) {
	rec := &recordedRequest{}
	srv := stubServer(t, rec.handler(http.StatusOK, openAISuccessBody))

	p := NewOpenAIWithBaseURL("secret-key", srv.URL+"/v1")
	got, err := p.Chat(context.Background(), openAIModel, chatReqWithTool())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got.Content != "pong" {
		t.Errorf("Content = %q, want %q", got.Content, "pong")
	}
	if got.TokensIn != 21 || got.TokensOut != 9 {
		t.Errorf("tokens = %d/%d, want 21/9", got.TokensIn, got.TokensOut)
	}

	if rec.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", rec.path)
	}
	if got := rec.headerOf("Authorization"); got != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want 'Bearer secret-key'", got)
	}
	if rec.body["model"] != openAIModel {
		t.Errorf("payload model = %v, want %s", rec.body["model"], openAIModel)
	}
	// OpenAI keeps the system turn inside `messages`.
	msgs, ok := rec.body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("payload messages = %v, want both turns", rec.body["messages"])
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Errorf("messages[0].role = %v, want system", msgs[0].(map[string]any)["role"])
	}
	tools, ok := rec.body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("payload tools = %v, want one tool", rec.body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}
	fn, ok := tool["function"].(map[string]any)
	if !ok || fn["name"] != "run_tests" {
		t.Fatalf("tool.function = %v, want run_tests", tool["function"])
	}
	if _, ok := fn["parameters"].(map[string]any); !ok {
		t.Errorf("tool.function.parameters = %v, want the input schema object", fn["parameters"])
	}
}

// TestOpenAIChatWithoutTools checks that the tools key is omitted when the
// request carries none, which some gateways reject as an empty array.
func TestOpenAIChatWithoutTools(t *testing.T) {
	rec := &recordedRequest{}
	srv := stubServer(t, rec.handler(http.StatusOK, openAISuccessBody))

	p := NewOpenAIWithBaseURL("key", srv.URL+"/v1")
	if _, err := p.Chat(context.Background(), openAIModel, chatReq()); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := rec.body["tools"]; ok {
		t.Errorf("payload %s must omit the tools key", rec.raw)
	}
}

// TestOpenAIChatNoChoices covers a 200 answer without choices: the adapter
// must return empty content instead of panicking on the missing element.
func TestOpenAIChatNoChoices(t *testing.T) {
	srv := stubServer(t, staticHandler(http.StatusOK, `{"choices": [], "usage": {"prompt_tokens": 1}}`))
	p := NewOpenAIWithBaseURL("key", srv.URL+"/v1")

	got, err := p.Chat(context.Background(), openAIModel, chatReq())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got.Content != "" {
		t.Errorf("Content = %q, want empty", got.Content)
	}
	if got.TokensIn != 1 {
		t.Errorf("TokensIn = %d, want 1", got.TokensIn)
	}
}

func TestOpenAIErrorClasses(t *testing.T) {
	const body = `{"error": {"message": "Rate limit reached", "type": "rate_limit_error"}}`
	for _, tt := range errorClasses {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := stubServer(t, staticHandler(tt.status, body))
			p := NewOpenAIWithBaseURL("key", srv.URL+"/v1")

			_, err := p.Chat(context.Background(), openAIModel, chatReq())
			if err == nil {
				t.Fatal("Chat returned nil error for an error status")
			}
			errorStatus(t, err, tt.want, tt.status, "openai", openAIModel)
			if !strings.Contains(err.Error(), "Rate limit reached") {
				t.Errorf("error %q does not carry the provider message", err)
			}
		})
	}
}

func TestOpenAITimeout(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	p := NewOpenAIWithBaseURL("key", srv.URL+"/v1")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Chat(ctx, openAIModel, chatReq())
	if err == nil {
		t.Fatal("Chat returned nil error after the deadline")
	}
	assertClassify(t, err, ErrTimeout)
}

func TestOpenAIInvalidJSON(t *testing.T) {
	srv := stubServer(t, staticHandler(http.StatusOK, `{"choices": "not-an-array"}`))
	p := NewOpenAIWithBaseURL("key", srv.URL+"/v1")

	_, err := p.Chat(context.Background(), openAIModel, chatReq())
	if err == nil {
		t.Fatal("Chat accepted a response with the wrong shape")
	}
	if !strings.Contains(err.Error(), "decode openai response") {
		t.Errorf("error = %v, want a decode failure", err)
	}
}
