package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const ollamaModel = "llama3.1:8b-instruct-q4_K_M"

// ollamaSuccessBody is a canned /api/chat answer. Ollama reports usage as
// prompt_eval_count / eval_count rather than a nested usage object.
const ollamaSuccessBody = `{
	"message": {"role": "assistant", "content": "pong"},
	"prompt_eval_count": 33,
	"eval_count": 12
}`

func TestOllamaNameAndBaseURL(t *testing.T) {
	if got := NewOllama("").Name(); got != "ollama" {
		t.Errorf("Name() = %q, want ollama", got)
	}
	if got := NewOllama("").baseURL; got != defaultOllamaBaseURL {
		t.Errorf("empty baseURL = %q, want the default %q", got, defaultOllamaBaseURL)
	}
	// A trailing slash must not produce a doubled path segment.
	if got := NewOllama("http://localhost:11434/").baseURL; got != "http://localhost:11434" {
		t.Errorf("trimmed baseURL = %q, want http://localhost:11434", got)
	}
}

// TestOllamaChatSuccess verifies the unified mapping and that the request is
// non-streaming: the gateway accounts cost per completed call.
func TestOllamaChatSuccess(t *testing.T) {
	rec := &recordedRequest{}
	srv := stubServer(t, rec.handler(http.StatusOK, ollamaSuccessBody))

	p := NewOllama(srv.URL)
	got, err := p.Chat(context.Background(), ollamaModel, chatReq())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got.Content != "pong" {
		t.Errorf("Content = %q, want %q", got.Content, "pong")
	}
	if got.TokensIn != 33 || got.TokensOut != 12 {
		t.Errorf("tokens = %d/%d, want 33/12", got.TokensIn, got.TokensOut)
	}

	if rec.path != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", rec.path)
	}
	if rec.headerOf("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", rec.headerOf("Content-Type"))
	}
	if rec.body["model"] != ollamaModel {
		t.Errorf("payload model = %v, want %s", rec.body["model"], ollamaModel)
	}
	if rec.body["stream"] != false {
		t.Errorf("payload stream = %v, want false", rec.body["stream"])
	}
	msgs, ok := rec.body["messages"].([]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("payload messages = %v, want both turns", rec.body["messages"])
	}
}

func TestOllamaErrorClasses(t *testing.T) {
	const body = `{"error": "model not found"}`
	for _, tt := range errorClasses {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			srv := stubServer(t, staticHandler(tt.status, body))
			p := NewOllama(srv.URL)

			_, err := p.Chat(context.Background(), ollamaModel, chatReq())
			if err == nil {
				t.Fatal("Chat returned nil error for an error status")
			}
			errorStatus(t, err, tt.want, tt.status, "ollama", ollamaModel)
			if !strings.Contains(err.Error(), "model not found") {
				t.Errorf("error %q does not carry the provider message", err)
			}
		})
	}
}

func TestOllamaTimeout(t *testing.T) {
	srv := stubServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	p := NewOllama(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := p.Chat(ctx, ollamaModel, chatReq())
	if err == nil {
		t.Fatal("Chat returned nil error after the deadline")
	}
	assertClassify(t, err, ErrTimeout)
}

func TestOllamaInvalidJSON(t *testing.T) {
	srv := stubServer(t, staticHandler(http.StatusOK, `{"message": `))
	p := NewOllama(srv.URL)

	_, err := p.Chat(context.Background(), ollamaModel, chatReq())
	if err == nil {
		t.Fatal("Chat accepted a truncated JSON body")
	}
	if !strings.Contains(err.Error(), "decode ollama response") {
		t.Errorf("error = %v, want a decode failure", err)
	}
}

// TestOllamaConnectionRefused covers an unreachable local Ollama: the error
// must surface as a network failure, not a provider error envelope.
func TestOllamaConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(staticHandler(http.StatusOK, ollamaSuccessBody))
	srv.Close() // free the port so the dial fails

	p := NewOllama(srv.URL)
	_, err := p.Chat(context.Background(), ollamaModel, chatReq())
	if err == nil {
		t.Fatal("Chat succeeded against a closed server")
	}
	if _, ok := err.(*HTTPError); ok {
		t.Errorf("error %v is an HTTPError, want a transport failure", err)
	}
}
