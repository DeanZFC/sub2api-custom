package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func independentObservedGroupTarget(t *testing.T) service.PassiveQualityPolicy {
	t.Helper()
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Purpose: "group_quality_target", Name: "independent target", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80, Recover: 85}}}
	require.NoError(t, p.Validate())
	return p
}

func TestGroupQualityTargetIndependentConfirmationUsesBreachedMetricFreshness(t *testing.T) {
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for _, metric := range []string{"cache", "ttft"} {
		t.Run(metric, func(t *testing.T) {
			p := independentObservedGroupTarget(t)
			p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 10000, Recover: 8000}
			cache, latency := 50.0, 1000.0
			if metric == "ttft" {
				cache, latency = 95, 11000
			}
			e := service.PassiveQualityEvidence{Complete: true, Samples: 10, CacheSamples: 5, TTFTSamples: 5, CacheRate: &cache, TTFT: &latency, Fresh: 5, Latest: now.Add(time.Second)}
			s := service.PassiveQualityState{Phase: "suspect", Hits: 1, LastEvidenceAt: now}
			advancePassiveObserved(p, &s, e, now.Add(2*time.Second))
			require.Equal(t, "suspect", s.Phase)
			require.Equal(t, 1, s.Hits, "unrelated new observations cannot confirm old bad metric values")
			require.Equal(t, now, s.LastEvidenceAt)
			if metric == "cache" {
				e.CacheFresh = p.NewSamples
			} else {
				e.TTFTFresh = p.NewSamples
			}
			advancePassiveObserved(p, &s, e, now.Add(3*time.Second))
			require.Equal(t, "paused", s.Phase)
		})
	}
}

func TestGroupQualityTargetIndependentGapCannotRetainHealthyLabel(t *testing.T) {
	p := independentObservedGroupTarget(t)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	cache := 95.0
	s := service.PassiveQualityState{Phase: "healthy", Reason: "healthy", LastEvidenceAt: now}
	e := service.PassiveQualityEvidence{Complete: false, Samples: 5, CacheSamples: 5, CacheRate: &cache, Latest: now}
	advancePassiveObserved(p, &s, e, now.Add(time.Second))
	require.Equal(t, "insufficient", s.Phase)
	require.Equal(t, "telemetry_incomplete", s.Reason)
	require.Zero(t, s.Hits)
}

func TestGroupQualityTargetIndependentTrialRequiresPostStartEvidence(t *testing.T) {
	p := independentObservedGroupTarget(t)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	cache := 95.0
	s := service.PassiveQualityState{AccountID: 62, Phase: "trial", TrialStartedAt: now, TrialEndsAt: now.Add(time.Minute), Generation: 4}
	e := service.PassiveQualityEvidence{Complete: true, Samples: 5, CacheSamples: 5, CacheRate: &cache, Latest: now.Add(-time.Second)}
	advancePassiveObserved(p, &s, e, now.Add(time.Second))
	require.Equal(t, "trial", s.Phase, "a healthy pre-trial aggregate cannot release a hold")
	require.EqualValues(t, 4, s.Generation)
	advancePassiveObserved(p, &s, e, now.Add(time.Minute))
	require.Equal(t, "paused", s.Phase)
	require.Contains(t, s.Reason, "trial_expired:")
	require.Greater(t, s.Generation, int64(4))
}

func TestGroupQualityTargetIndependentRecoveryNeedsEveryEnabledMetric(t *testing.T) {
	p := independentObservedGroupTarget(t)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 15}
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 10000, Recover: 8000}
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{Phase: "trial", TrialStartedAt: now, TrialEndsAt: now.Add(time.Minute)}
	cache, errors, latency := 85.0, 0.0, 7000.0
	e := service.PassiveQualityEvidence{Complete: true, Samples: 10, CacheSamples: 4, TTFTSamples: 10, CacheRate: &cache, ErrorRate: &errors, TTFT: &latency, Latest: now.Add(time.Second)}
	advancePassiveObserved(p, &s, e, now.Add(2*time.Second))
	require.Equal(t, "trial", s.Phase, "successful fast requests cannot replace missing cache samples")
	e.CacheSamples = 5
	advancePassiveObserved(p, &s, e, now.Add(3*time.Second))
	require.Equal(t, "healthy", s.Phase)
	require.Equal(t, "recovered", s.Reason)
	require.Equal(t, s.TrialStartedAt, s.ObserveAfter, "late trial completions must remain visible after recovery")
}
