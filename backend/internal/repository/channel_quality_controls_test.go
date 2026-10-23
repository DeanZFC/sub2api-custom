package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityOverviewIndependentControls(t *testing.T) {
	now := time.Now()
	state := service.PassiveQualityState{PolicyID: 5, PolicyName: "Protection", Scope: "account", Mode: "enforce", Phase: "healthy", Latency: &service.PassiveQualityMetricState{Phase: "limited", Reason: "slow", HoldUntil: now}, Cache: map[string]service.PassiveQualityCacheState{
		"model-b": {Phase: "insufficient", AllocationPercent: 100},
		"model-a": {Phase: "recovering", AllocationPercent: 50, Reason: "cache_allocation_recovery_step", Evidence: service.PassiveQualityCacheWindowsEvidence{Model: "model-a"}},
	}}
	controls := passiveQualityOverviewControls(state, "")
	require.Len(t, controls, 4)
	require.Equal(t, "availability", controls[0].Metric)
	require.Equal(t, "healthy", controls[0].Phase)
	require.Equal(t, "latency", controls[1].Metric)
	require.Equal(t, "limited", controls[1].Phase)
	require.Equal(t, "production_samples", controls[1].Recovery)
	require.Equal(t, "model-a", controls[2].Model)
	require.Equal(t, "model", controls[2].Scope)
	require.Equal(t, "cache_limited", controls[2].Phase)
	require.Equal(t, 50, controls[2].AllocationPercent)
	require.Equal(t, "cache_reuse_samples", controls[2].Recovery)
	require.Equal(t, "model-a", controls[2].Cache.Model)
	require.False(t, controls[2].Blocking, "cache reduction must never imply account unavailability")
	require.Equal(t, "model-b", controls[3].Model)
	require.Equal(t, "insufficient", controls[3].Phase)
	filtered := passiveQualityOverviewControls(state, "model-b")
	require.Len(t, filtered, 3)
	require.Equal(t, "model-b", filtered[2].Model)
	state.Phase = "paused"
	require.True(t, passiveQualityOverviewControls(state, "")[0].Blocking)
	state.Phase = "exempt"
	exempt := passiveQualityOverviewControls(state, "")
	require.Len(t, exempt, 1, "exemption suppresses all this policy's metric controls")
	require.Equal(t, "exemption_expiry", exempt[0].Recovery)
	require.False(t, exempt[0].Blocking)
}
