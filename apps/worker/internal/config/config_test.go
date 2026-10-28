package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad_DefaultBudgetMatchesRoleLimits guards the defaults against drifting
// from Role.limits in SYSTEM_DESIGN.md §7.2 (backend-developer: $3 / 60 steps).
func TestLoad_DefaultBudgetMatchesRoleLimits(t *testing.T) {
	t.Setenv("DEFAULT_MAX_STEPS", "")
	t.Setenv("DEFAULT_MAX_COST_USD", "")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 60, cfg.DefaultMaxSteps)
	require.InDelta(t, 3.0, cfg.DefaultMaxCostUSD, 1e-9)
}

func TestLoad_EnvOverridesBudget(t *testing.T) {
	t.Setenv("DEFAULT_MAX_STEPS", "12")
	t.Setenv("DEFAULT_MAX_COST_USD", "1.5")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, 12, cfg.DefaultMaxSteps)
	require.InDelta(t, 1.5, cfg.DefaultMaxCostUSD, 1e-9)
}

func TestLoad_InvalidBudgetEnvReturnsError(t *testing.T) {
	t.Setenv("DEFAULT_MAX_STEPS", "not-a-number")

	_, err := Load()
	require.Error(t, err)
}
