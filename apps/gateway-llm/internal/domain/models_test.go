// Package domain tests domain model validation.
package domain

import (
	"testing"
)

func TestModelProfile_ValidatePinned(t *testing.T) {
	tests := []struct {
		name    string
		modelID string
		wantErr bool
		errMsg  string
	}{
		{"pinned version", "claude-sonnet-4-20250101", false, ""},
		{"pinned version gpt", "gpt-4o-2024-11-20", false, ""},
		{"pinned ollama", "llama3.1:8b-instruct-q4_K_M", false, ""},
		{"-latest alias", "claude-sonnet-4-latest", true, ErrFloatingAlias.Error()},
		{"-latest in middle", "claude-latest-sonnet", true, ErrFloatingAlias.Error()},
		{"empty", "", false, ""}, // empty model_id is allowed
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := ModelProfile{ModelID: tt.modelID}
			err := p.ValidatePinned()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tt.errMsg)
				}
				if err.Error() != tt.errMsg {
					t.Errorf("error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
