package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPassiveQualityPolicyChangeIgnoresOrderAndDecisionBookkeeping(t *testing.T) {
	old := newPolicyForChangeTest()
	next := old
	next.GroupIDs = []int64{9, 8}
	next.ExcludedAccountIDs = []int64{63, 62}
	now := time.Now()
	next.EvidenceStartedAt = &now
	next.EvaluationVersion = 99
	unchanged, metadata := passivePolicyChange(old, next)
	require.True(t, unchanged)
	require.True(t, metadata)
	next.Name = "renamed"
	unchanged, metadata = passivePolicyChange(old, next)
	require.False(t, unchanged)
	require.True(t, metadata)
	next.ErrorRate.Pause++
	unchanged, metadata = passivePolicyChange(old, next)
	require.False(t, unchanged)
	require.False(t, metadata)
}

func TestPassiveQualityPolicyChangeTreatsOmittedGroupTrafficModeAsStrict(t *testing.T) {
	old := service.PassiveQualityConfig{
		Purpose: "group_quality_target", Name: "target", GroupIDs: []int64{8},
		CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80, Recover: 85},
	}
	next := old
	next.TrafficMode = "strict"
	unchanged, metadata := passivePolicyChange(old, next)
	require.True(t, unchanged)
	require.True(t, metadata)
}

func newPolicyForChangeTest() service.PassiveQualityConfig {
	return service.PassiveQualityConfig{Name: "old", GroupIDs: []int64{8, 9}, ExcludedAccountIDs: []int64{62, 63}, ErrorRate: service.PassiveQualityMetric{Enabled: true, Pause: 20}}
}
