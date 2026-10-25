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

const anthropicBaseURL = "https://api.anthropic.com/v1"
const anthropicVersion = "2023-06-01"

// AnthropicProvider implements Provider for the Anthropic Messages API.
type AnthropicProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewAnthropic creates an Anthropic provider adapter.
func NewAnthropic(apiKey string) *AnthropicProvider {
	return NewAnthropicWithBaseURL(apiKey, anthropicBaseURL)
}

// NewAnthropicWithBaseURL creates an Anthropic adapter with a custom base URL
// (used in tests against stub HTTP servers).
func NewAnthropicWithBaseURL(apiKey, baseURL string) *AnthropicProvider {
	return &AnthropicProvider{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  NewHTTPClient(120 * time.Second),
	}
}

// Name returns the provider name.
func (p *AnthropicProvider) Name() string { return "anthropic" }

// anthropicRequest is the wire format for Anthropic Messages API.
type anthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Chat sends a chat completion request to Anthropic.
func (p *AnthropicProvider) Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error) {
	// Anthropic separates system messages from the conversation.
	var messages []anthropicMessage
	var system string
	for _, m := range req.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, anthropicMessage{Role: m.Role, Content: m.Content})
	}

	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, t := range req.Tools {
		tools = append(tools, anthropicTool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}

	payload := anthropicRequest{
		Model:     modelID,
		Messages:  messages,
		MaxTokens: 8192,
		System:    system,
		Tools:     tools,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("marshal anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("build anthropic request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return domain.ChatResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.ChatResponse{}, BuildHTTPError(resp, p.Name(), modelID)
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return domain.ChatResponse{}, fmt.Errorf("decode anthropic response: %w", err)
	}

	var content string
	for _, block := range parsed.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return domain.ChatResponse{
		Content:   content,
		TokensIn:  parsed.Usage.InputTokens,
		TokensOut: parsed.Usage.OutputTokens,
	}, nil
}
