package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
)

const openAIBaseURL = "https://api.openai.com/v1"

// OpenAIProvider implements Provider for the OpenAI Chat Completions API.
type OpenAIProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenAI creates an OpenAI provider adapter.
func NewOpenAI(apiKey string) *OpenAIProvider {
	return NewOpenAIWithBaseURL(apiKey, openAIBaseURL)
}

// NewOpenAIWithBaseURL creates an OpenAI adapter with a custom base URL
// (used in tests against stub HTTP servers).
func NewOpenAIWithBaseURL(apiKey, baseURL string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  NewHTTPClient(120 * time.Second),
	}
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string { return "openai" }

// openaiRequest is the wire format for OpenAI Chat Completions API.
type openaiRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Tools    []openaiTool    `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Chat sends a chat completion request to OpenAI.
func (p *OpenAIProvider) Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
	messages := make([]openaiMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		messages = append(messages, openaiMessage{Role: m.Role, Content: m.Content})
	}

	tools := make([]openaiTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		var tool openaiTool
		tool.Type = "function"
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = t.InputSchema
		tools = append(tools, tool)
	}

	payload := openaiRequest{Model: modelID, Messages: messages, Tools: tools}

	body, err := json.Marshal(payload)
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("marshal openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("build openai request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return domain.ChatResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.ChatResponse{}, BuildHTTPError(resp, p.Name(), modelID)
	}

	var parsed openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return domain.ChatResponse{}, fmt.Errorf("decode openai response: %w", err)
	}

	content := ""
	if len(parsed.Choices) > 0 {
		content = parsed.Choices[0].Message.Content
	}

	return domain.ChatResponse{
		Content:   content,
		TokensIn:  parsed.Usage.PromptTokens,
		TokensOut: parsed.Usage.CompletionTokens,
	}, nil
}
