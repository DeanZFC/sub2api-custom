package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGroupQualityTargetFreshHealthySamplesCannotConfirmOldSpike(t *testing.T) {
	for _, badFresh := range []bool{false, true} {
		name := "healthy"
		if badFresh {
			name = "bad"
		}
		t.Run(name, func(t *testing.T) {
			p := independentObservedGroupTarget(t)
			p.CacheRate.Enabled = false
			p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}
			base := time.Now()
			samples := []service.PassiveQualitySample{}
			for i := 0; i < 5; i++ {
				samples = append(samples, service.PassiveQualitySample{At: base.Add(time.Duration(i) * time.Second), Failed: true})
			}
			state := service.PassiveQualityState{Phase: "insufficient"}
			e := service.PassiveQualitySummarize(samples, state.LastEvidenceAt, true)
			advancePassiveObserved(p, &state, e, base.Add(5*time.Second))
			require.Equal(t, "suspect", state.Phase)
			require.Equal(t, 1, state.Hits)
			for i := 5; i < 7; i++ {
				samples = append(samples, service.PassiveQualitySample{At: base.Add(time.Duration(i) * time.Second), Failed: badFresh})
			}
			e = service.PassiveQualitySummarize(samples, state.LastEvidenceAt, true)
			advancePassiveObserved(p, &state, e, base.Add(7*time.Second))
			if badFresh {
				require.Equal(t, "paused", state.Phase)
			} else {
				require.Equal(t, "suspect", state.Phase)
				require.Zero(t, state.Hits)
				for i := 7; i < 9; i++ {
					samples = append(samples, service.PassiveQualitySample{At: base.Add(time.Duration(i) * time.Second)})
				}
				e = service.PassiveQualitySummarize(samples, state.LastEvidenceAt, true)
				advancePassiveObserved(p, &state, e, base.Add(9*time.Second))
				require.Equal(t, "suspect", state.Phase)
				require.Zero(t, state.Hits)
			}
			require.Equal(t, 5, p.MinSamples)
		})
	}
}

func TestGroupQualityTargetConfirmationCannotCombineMetrics(t *testing.T) {
	p := independentObservedGroupTarget(t)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}
	now := time.Now()
	rate := 50.0
	cache := 50.0
	state := service.PassiveQualityState{Phase: "suspect", Hits: 1, ConfirmationMetric: "cache_rate", LastEvidenceAt: now}
	e := service.PassiveQualityEvidence{Complete: true, Samples: 5, Fresh: 2, CacheSamples: 5, CacheFresh: 2, ErrorRate: &rate, FreshErrorRate: &rate, CacheRate: &cache, FreshCacheRate: &cache, Latest: now.Add(time.Second)}
	advancePassiveObserved(p, &state, e, now.Add(2*time.Second))
	require.Equal(t, "suspect", state.Phase)
	require.Equal(t, 1, state.Hits)
	require.Equal(t, "error_rate", state.ConfirmationMetric)
}

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
func newPolicyForChangeTest() service.PassiveQualityConfig {
	return service.PassiveQualityConfig{Name: "old", GroupIDs: []int64{8, 9}, ExcludedAccountIDs: []int64{62, 63}, ErrorRate: service.PassiveQualityMetric{Enabled: true, Pause: 20}}
}
