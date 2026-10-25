package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
)

const defaultOllamaBaseURL = "http://localhost:11434"

// OllamaProvider implements Provider for a local Ollama server.
type OllamaProvider struct {
	baseURL string
	client  *http.Client
}

// NewOllama creates an Ollama provider adapter.
func NewOllama(baseURL string) *OllamaProvider {
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}
	return &OllamaProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  NewHTTPClient(300 * time.Second),
	}
}

// Name returns the provider name.
func (p *OllamaProvider) Name() string { return "ollama" }

// ollamaChatRequest is the wire format for the Ollama chat API.
type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

// Chat sends a chat completion request to Ollama.
func (p *OllamaProvider) Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
	messages := make([]ollamaMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, ollamaMessage{Role: m.Role, Content: m.Content})
	}

	payload := ollamaChatRequest{Model: modelID, Messages: messages, Stream: false}

	body, err := json.Marshal(payload)
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("marshal ollama request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("build ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return domain.ChatResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return domain.ChatResponse{}, BuildHTTPError(resp, p.Name(), modelID)
	}

	var parsed ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return domain.ChatResponse{}, fmt.Errorf("decode ollama response: %w", err)
	}

	return domain.ChatResponse{
		Content:   parsed.Message.Content,
		TokensIn:  parsed.PromptEvalCount,
		TokensOut: parsed.EvalCount,
	}, nil
}
