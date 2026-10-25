// Package providers defines the provider interface and error classification.
package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/sapelyuk/ai-sdlc-factory/apps/gateway-llm/internal/domain"
)

// Provider is the interface all LLM provider adapters implement.
type Provider interface {
	// Chat sends a chat completion request and returns a unified response.
	Chat(ctx context.Context, modelID string, req domain.ChatRequest) (domain.ChatResponse, error)
	// Name returns the provider name (anthropic, openai, ollama).
	Name() string
}

// ErrorClass classifies provider errors for routing decisions.
type ErrorClass int

const (
	ErrUnknown ErrorClass = iota
	Err404ModelNotFound
	Err401Unauthorized
	Err403Forbidden
	Err400BadRequest
	Err429RateLimit
	Err5xxServerError
	ErrTimeout
)

// String returns a human-readable name for the error class.
func (e ErrorClass) String() string {
	switch e {
	case Err404ModelNotFound:
		return "failed_404"
	case Err401Unauthorized:
		return "failed_auth"
	case Err403Forbidden:
		return "failed_auth"
	case Err400BadRequest:
		return "failed_bad_request"
	case Err429RateLimit:
		return "failed_429"
	case Err5xxServerError:
		return "failed_5xx"
	case ErrTimeout:
		return "failed_timeout"
	default:
		return "failed_other"
	}
}

// ShouldCooldown returns true if the error class should trigger a cooldown.
// 429 / 5xx / timeout → cooldown. 404 / 401 / 403 / 400 → no cooldown.
func ShouldCooldown(class ErrorClass) bool {
	return class == Err429RateLimit || class == Err5xxServerError || class == ErrTimeout
}

// Classify determines the ErrorClass from a provider error.
func Classify(err error) ErrorClass {
	if err == nil {
		return ErrUnknown
	}

	// Check for timeout errors.
	var netErr interface{ Timeout() bool }
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	if errors.Is(err, syscall.ETIMEDOUT) {
		return ErrTimeout
	}

	// Check for HTTP status errors.
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch {
		case httpErr.StatusCode == 404:
			return Err404ModelNotFound
		case httpErr.StatusCode == 401:
			return Err401Unauthorized
		case httpErr.StatusCode == 403:
			return Err403Forbidden
		case httpErr.StatusCode == 400:
			return Err400BadRequest
		case httpErr.StatusCode == 429:
			return Err429RateLimit
		case httpErr.StatusCode >= 500:
			return Err5xxServerError
		default:
			return ErrUnknown
		}
	}

	return ErrUnknown
}

// HTTPError wraps an HTTP response error with the status code.
type HTTPError struct {
	StatusCode int
	Body       string
	Provider   string
	ModelID    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: status %d for model %s: %s", e.Provider, e.StatusCode, e.ModelID, e.Body)
}

// BuildHTTPError reads the body from an HTTP response and returns an HTTPError.
func BuildHTTPError(resp *http.Response, provider, modelID string) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(body)),
		Provider:   provider,
		ModelID:    modelID,
	}
}

// UsageError wraps an error with partial token usage (e.g. provider returns
// usage alongside a non-200 response). This enables dual-cost accounting:
// total_cost_usd may exceed final_cost_usd when earlier attempts consume tokens.
type UsageError struct {
	Err       error
	TokensIn  int
	TokensOut int
}

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

// NewHTTPErrorWithUsage builds an HTTPError and attaches partial usage.
func NewHTTPErrorWithUsage(resp *http.Response, provider, modelID string, tokensIn, tokensOut int) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	base := &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(body)),
		Provider:   provider,
		ModelID:    modelID,
	}
	return &UsageError{Err: base, TokensIn: tokensIn, TokensOut: tokensOut}
}

// NewHTTPClient returns a configured HTTP client with timeout.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}
