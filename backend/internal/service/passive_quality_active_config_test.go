package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActiveRecoveryMinimalDefaults(t *testing.T) {
	for _, purpose := range []string{"group_quality_target", "cache_target"} {
		c := independentGroupTargetConfig(t, purpose, 80)
		c.RecoveryMode = ""
		c.MaxSamples, c.WindowSeconds = 100, 600
		require.NoError(t, c.Validate())
		require.True(t, c.IsActiveRecovery())
		require.Equal(t, 3600, c.WindowSeconds)
		require.Zero(t, c.MaxSamples, "the hour has no request-count ceiling")
		require.Equal(t, 20, c.MinSamples)
		require.Equal(t, 5, c.NewSamples)
		require.Equal(t, 2, c.Confirmations)
		require.Equal(t, 5, c.RecoverySamples)
		require.Equal(t, 3600, c.MaxCooldownSeconds)
		require.Zero(t, c.TrialRequests)
		require.Zero(t, c.TrialSeconds)
		require.Zero(t, c.TrialConcurrency)
	}
	c := independentGroupTargetConfig(t, "group_quality_target", 80)
	c.RecoveryMode = "unknown"
	require.ErrorContains(t, c.Validate(), "unsupported recovery mode")
}

func TestActiveRecoveryCannotFallBackToRetiredMode(t *testing.T) {
	for _, purpose := range []string{"group_quality_target", "cache_target"} {
		for _, mode := range []string{"", "active", "traffic", "unknown"} {
			c := PassiveQualityConfig{Purpose: purpose, RecoveryMode: mode}
			require.True(t, c.IsActiveRecovery(), "persisted legacy data must never select customer trials")
		}
	}
	require.False(t, (PassiveQualityConfig{RecoveryMode: "active"}).IsActiveRecovery())
}

func TestActiveRecoveryRejectsImpossibleProbeBudgets(t *testing.T) {
	for _, edit := range []func(*PassiveQualityConfig){
		func(c *PassiveQualityConfig) { c.RecoverySamples = 20 },
		func(c *PassiveQualityConfig) { c.Confirmations = 4 },
		func(c *PassiveQualityConfig) {
			c.Stability = &GroupQualityStability{RecoverySeconds: 600}
		},
	} {
		c := independentGroupTargetConfig(t, "group_quality_target", 80)
		c.RecoveryMode = "active"
		edit(&c)
		require.ErrorContains(t, c.Validate(), "20-request and 10-minute budget")
	}
}
