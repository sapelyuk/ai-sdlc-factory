package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBudgetSpec_RoundTrip verifies that the three optional budget fields
// added to BudgetSpec (run_max_cost_usd, run_max_steps, day_max_cost_usd)
// survive JSON marshalling and unmarshalling.
//
// The registry handler decodes TeamSpec with json.Decoder.DisallowUnknownFields
// (registry.go:145). Without these struct fields, a valid schema payload
// containing run_max_cost_usd would be rejected at decode time. With the
// fields present, a round-trip must preserve them (marshal → unmarshal → marshal).
//
// Schema reference: packages/contracts/schemas/team.schema.json,
// "budget" object properties: run_max_cost_usd, run_max_steps, day_max_cost_usd.
func TestBudgetSpec_RoundTrip(t *testing.T) {
	raw := `{
		"max_cost_usd": 5.0,
		"max_steps": 100,
		"run_max_cost_usd": 50.0,
		"run_max_steps": 500,
		"day_max_cost_usd": 200.0
	}`
	var spec BudgetSpec
	require.NoError(t, json.Unmarshal([]byte(raw), &spec))
	require.Equal(t, 5.0, spec.MaxCostUSD)
	require.Equal(t, 100, spec.MaxSteps)
	require.Equal(t, 50.0, spec.RunMaxCostUSD)
	require.Equal(t, 500, spec.RunMaxSteps)
	require.Equal(t, 200.0, spec.DayMaxCostUSD)

	// Round-trip: marshal back and verify all fields are present.
	out, err := json.Marshal(spec)
	require.NoError(t, err)
	require.Contains(t, string(out), `"max_cost_usd":5`)
	require.Contains(t, string(out), `"max_steps":100`)
	require.Contains(t, string(out), `"run_max_cost_usd":50`)
	require.Contains(t, string(out), `"run_max_steps":500`)
	require.Contains(t, string(out), `"day_max_cost_usd":200`)
}

// TestBudgetSpec_OmitEmpty verifies that zero-value optional fields are
// omitted on marshal (omitempty tag).
func TestBudgetSpec_OmitEmpty(t *testing.T) {
	spec := BudgetSpec{
		MaxCostUSD: 5.0,
		MaxSteps:   100,
	}
	out, err := json.Marshal(spec)
	require.NoError(t, err)
	require.Contains(t, string(out), `"max_cost_usd":5`)
	require.Contains(t, string(out), `"max_steps":100`)
	require.NotContains(t, string(out), `"run_max_cost_usd"`)
	require.NotContains(t, string(out), `"run_max_steps"`)
	require.NotContains(t, string(out), `"day_max_cost_usd"`)
}
