package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateNodeInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   CreateNodeInput
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid SLP node",
			input:   CreateNodeInput{Layer: "SLP", Kind: "Test", Label: "Test Node"},
			wantErr: false,
		},
		{
			name:    "valid SLPr node",
			input:   CreateNodeInput{Layer: "SLPr", Kind: "Order", Label: "Order entity"},
			wantErr: false,
		},
		{
			name:    "valid AM node",
			input:   CreateNodeInput{Layer: "AM", Kind: "Container", Label: "API service"},
			wantErr: false,
		},
		{
			name:    "missing layer",
			input:   CreateNodeInput{Kind: "Test", Label: "Test Node"},
			wantErr: true,
			errMsg:  "layer is required",
		},
		{
			name:    "invalid layer",
			input:   CreateNodeInput{Layer: "INVALID", Kind: "Test", Label: "Test Node"},
			wantErr: true,
			errMsg:  "layer must be one of: SLP, SLPr, AM",
		},
		{
			name:    "missing kind",
			input:   CreateNodeInput{Layer: "SLP", Label: "Test Node"},
			wantErr: true,
			errMsg:  "kind is required",
		},
		{
			name:    "missing label",
			input:   CreateNodeInput{Layer: "SLP", Kind: "Test"},
			wantErr: true,
			errMsg:  "label is required",
		},
		{
			name:    "invalid status",
			input:   CreateNodeInput{Layer: "SLP", Kind: "Test", Label: "Test", Status: "unknown"},
			wantErr: true,
			errMsg:  "status must be one of",
		},
		{
			name:    "valid status draft",
			input:   CreateNodeInput{Layer: "SLP", Kind: "Test", Label: "Test", Status: "draft"},
			wantErr: false,
		},
		{
			name:    "valid status active",
			input:   CreateNodeInput{Layer: "SLP", Kind: "Test", Label: "Test", Status: "active"},
			wantErr: false,
		},
		{
			name:    "invalid status approved",
			input:   CreateNodeInput{Layer: "SLP", Kind: "Test", Label: "Test", Status: "approved"},
			wantErr: true,
			errMsg:  "status must be one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateNodeInput_DefaultStatus(t *testing.T) {
	input := CreateNodeInput{Layer: "SLP", Kind: "Test", Label: "Test"}
	assert.Equal(t, "draft", input.DefaultStatus())

	input.Status = "active"
	assert.Equal(t, "active", input.DefaultStatus())
}

func TestUpdateNodeInput_Validate(t *testing.T) {
	input := UpdateNodeInput{Kind: "NewKind", Label: "NewLabel"}
	assert.NoError(t, input.Validate())

	longKind := make([]byte, 129)
	for i := range longKind {
		longKind[i] = 'x'
	}
	input.Kind = string(longKind)
	assert.Error(t, input.Validate())
}
