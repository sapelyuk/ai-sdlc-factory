// Package providers tests error classification and provider interface.
package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"nil error", nil, ErrUnknown},
		{"404", &HTTPError{StatusCode: 404, Provider: "test", ModelID: "x"}, Err404ModelNotFound},
		{"401", &HTTPError{StatusCode: 401, Provider: "test", ModelID: "x"}, Err401Unauthorized},
		{"403", &HTTPError{StatusCode: 403, Provider: "test", ModelID: "x"}, Err403Forbidden},
		{"400", &HTTPError{StatusCode: 400, Provider: "test", ModelID: "x"}, Err400BadRequest},
		{"429", &HTTPError{StatusCode: 429, Provider: "test", ModelID: "x"}, Err429RateLimit},
		{"500", &HTTPError{StatusCode: 500, Provider: "test", ModelID: "x"}, Err5xxServerError},
		{"502", &HTTPError{StatusCode: 502, Provider: "test", ModelID: "x"}, Err5xxServerError},
		{"503", &HTTPError{StatusCode: 503, Provider: "test", ModelID: "x"}, Err5xxServerError},
		{"timeout", &HTTPError{StatusCode: 0, Provider: "x", ModelID: "x"}, ErrUnknown}, // not HTTP
		{"os deadline exceeded", os.ErrDeadlineExceeded, ErrTimeout},
		{"context deadline exceeded", context.DeadlineExceeded, ErrTimeout},
		{"net timeout", netError{}, ErrTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			if got != tt.want {
				t.Errorf("Classify(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// Helpers for timeout tests.
type netError struct{}

func (netError) Error() string   { return "timeout" }
func (netError) Timeout() bool   { return true }
func (netError) Temporary() bool { return false }

func TestShouldCooldown(t *testing.T) {
	tests := []struct {
		class ErrorClass
		want  bool
	}{
		{Err404ModelNotFound, false},
		{Err401Unauthorized, false},
		{Err403Forbidden, false},
		{Err400BadRequest, false},
		{Err429RateLimit, true},
		{Err5xxServerError, true},
		{ErrTimeout, true},
		{ErrUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.class.String(), func(t *testing.T) {
			got := ShouldCooldown(tt.class)
			if got != tt.want {
				t.Errorf("ShouldCooldown(%v) = %v, want %v", tt.class, got, tt.want)
			}
		})
	}
}

func TestHTTPError_Error(t *testing.T) {
	e := &HTTPError{StatusCode: 404, Body: "not found", Provider: "anthropic", ModelID: "x"}
	got := e.Error()
	if !strings.Contains(got, "404") || !strings.Contains(got, "anthropic") {
		t.Errorf("HTTPError.Error() = %q, expected to contain 404 and 'anthropic'", got)
	}
}

func TestUsageError(t *testing.T) {
	base := &HTTPError{StatusCode: 429, Provider: "test", ModelID: "x", Body: "rate limited"}
	ue := &UsageError{Err: base, TokensIn: 100, TokensOut: 0}
	if ue.Error() != base.Error() {
		t.Errorf("UsageError.Error() = %q, want base error", ue.Error())
	}
	var unwrapped *HTTPError
	if !errors.As(ue, &unwrapped) {
		t.Error("UsageError.Unwrap() should return base HTTPError")
	}
}

func TestBuildHTTPError(t *testing.T) {
	body := "too many requests"
	resp := &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(body))}
	err := BuildHTTPError(resp, "openai", "gpt-4")
	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if httpErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", httpErr.StatusCode)
	}
	if httpErr.Provider != "openai" {
		t.Errorf("Provider = %q, want 'openai'", httpErr.Provider)
	}
}
