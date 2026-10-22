package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateEdgeInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		input   CreateEdgeInput
		wantErr bool
		errMsg  string
	}{
		{
			name:  "valid edge",
			input: CreateEdgeInput{SrcID: "0190b4c0-0000-7000-8000-000000000001", DstID: "0190b4c0-0000-7000-8000-000000000002", Kind: "uses"},
		},
		{
			name:  "valid edge with props",
			input: CreateEdgeInput{SrcID: "src", DstID: "dst", Kind: "depends_on", Props: map[string]any{"weight": 1}},
		},
		{
			name:    "missing src_id",
			input:   CreateEdgeInput{DstID: "dst", Kind: "uses"},
			wantErr: true,
			errMsg:  "src_id is required",
		},
		{
			name:    "missing dst_id",
			input:   CreateEdgeInput{SrcID: "src", Kind: "uses"},
			wantErr: true,
			errMsg:  "dst_id is required",
		},
		{
			name:    "missing kind",
			input:   CreateEdgeInput{SrcID: "src", DstID: "dst"},
			wantErr: true,
			errMsg:  "kind is required",
		},
		{
			name:    "empty input",
			input:   CreateEdgeInput{},
			wantErr: true,
			errMsg:  "src_id is required",
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

func TestImmutableStatuses_BlocksOnlyApprovedState(t *testing.T) {
	// An approved node is persisted as "active" and must not be updated.
	assert.True(t, ImmutableStatuses["active"], "approved nodes are stored as 'active' and must be immutable")

	for _, status := range []string{"draft", "deprecated", "deleted"} {
		assert.False(t, ImmutableStatuses[status], "status %q must remain mutable", status)
	}
}
