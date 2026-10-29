package service

import (
	"github.com/stretchr/testify/require"
	"testing"
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
	}
	c := independentGroupTargetConfig(t, "group_quality_target", 80)
	c.RecoveryMode = "unknown"
	require.ErrorContains(t, c.Validate(), "unsupported recovery mode")
}

func TestActiveRecoveryRejectsImpossibleProbeBudgets(t *testing.T) {
	for _, edit := range []func(*PassiveQualityConfig){
		func(c *PassiveQualityConfig) { c.RecoverySamples = 20 },
		func(c *PassiveQualityConfig) { c.Confirmations = 4 },
		func(c *PassiveQualityConfig) {
			c.Stability = &GroupQualityStability{RecoverySeconds: 600, NewSessionPercent: 100}
		},
	} {
		c := independentGroupTargetConfig(t, "group_quality_target", 80)
		c.RecoveryMode = "active"
		edit(&c)
		require.ErrorContains(t, c.Validate(), "20-request and 10-minute budget")
	}
}
