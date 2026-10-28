package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func stableTargetPolicy(t *testing.T) service.PassiveQualityPolicy {
	t.Helper()
	p := independentObservedGroupTarget(t)
	p.Stability = nil
	return p
}

func cacheTargetEvidence(at time.Time, rate float64) service.PassiveQualityEvidence {
	return service.PassiveQualityEvidence{Complete: true, Samples: 10, Fresh: 2, CacheSamples: 10, CacheFresh: 2,
		CacheRate: &rate, FreshCacheRate: &rate, Latest: at, CacheLatest: at}
}

func TestGroupTargetAvailabilityNeedsSustainedFreshFailures(t *testing.T) {
	p := stableTargetPolicy(t)
	p.CacheRate.Enabled = false
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	rate := 50.
	s := service.PassiveQualityState{}
	e := service.PassiveQualityEvidence{Complete: true, Samples: 5, Fresh: 2, ErrorRate: &rate, FreshErrorRate: &rate, Latest: base}
	advancePassiveObserved(p, &s, e, base)
	require.Equal(t, "suspect", s.Phase)
	e.Latest = base.Add(30 * time.Second)
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, "suspect", s.Phase, "enough confirmations alone must not bypass the duration gate")
	e.Latest = base.Add(60 * time.Second)
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, "paused", s.Phase)
}

func TestGroupTargetCacheOnlyFailureKeepsOwnersThroughSustainedDegradation(t *testing.T) {
	p := stableTargetPolicy(t)
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{}
	for _, seconds := range []int{0, 120, 240, 360} {
		e := cacheTargetEvidence(base.Add(time.Duration(seconds)*time.Second), 50)
		advancePassiveObserved(p, &s, e, e.Latest)
		require.Equal(t, "suspect", s.Phase, "cache degradation must never hard-pause a warm owner")
		require.True(t, s.CacheAllocationReduced)
	}
	require.Contains(t, s.Reason, "cache_allocation_reduced:")
}

func TestGroupTargetCacheAllocationRecoveryUsesHysteresisAndFreshTime(t *testing.T) {
	p := stableTargetPolicy(t)
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{}
	for _, step := range []struct {
		seconds int
		rate    float64
		limited bool
	}{
		{0, 79, true}, {5, 81, true}, {10, 85, true}, {70, 86, true},
		{100, 84, true}, {130, 85, true}, {250, 86, false},
	} {
		e := cacheTargetEvidence(base.Add(time.Duration(step.seconds)*time.Second), step.rate)
		advancePassiveObserved(p, &s, e, e.Latest)
		require.Equal(t, step.limited, s.CacheAllocationReduced, "at %ds with cache %v", step.seconds, step.rate)
	}
	require.Equal(t, "healthy", s.Phase)
	require.Equal(t, "cache_allocation_recovered", s.Reason)
}

func TestGroupTargetCacheLatchSurvivesAnotherSuspectMetric(t *testing.T) {
	p := stableTargetPolicy(t)
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 10000, Recover: 8000}
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{}
	for i, step := range []struct{ cache, ttft float64 }{{79, 5000}, {81, 11000}, {81, 9000}} {
		at := base.Add(time.Duration(i) * 10 * time.Second)
		e := cacheTargetEvidence(at, step.cache)
		e.TTFT, e.FreshTTFT, e.TTFTSamples, e.TTFTFresh, e.TTFTLatest = &step.ttft, &step.ttft, 10, 2, at
		advancePassiveObserved(p, &s, e, at)
		require.True(t, s.CacheAllocationReduced, "a speed fluctuation cannot bypass cache recovery hysteresis")
	}
}

func TestGroupTargetCacheRecoveryDoesNotRetainOldHardFailureClock(t *testing.T) {
	p := stableTargetPolicy(t)
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 10000, Recover: 8000}
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{}
	for _, step := range []struct {
		seconds     int
		cache, ttft float64
	}{
		{0, 79, 5000}, {10, 81, 11000}, {20, 81, 9000}, {60, 81, 9000},
		{120, 81, 9000}, {180, 81, 9000}, {240, 81, 9000}, {300, 81, 9000},
		{310, 81, 11000},
	} {
		at := base.Add(time.Duration(step.seconds) * time.Second)
		e := cacheTargetEvidence(at, step.cache)
		e.TTFT, e.FreshTTFT, e.TTFTSamples, e.TTFTFresh, e.TTFTLatest = &step.ttft, &step.ttft, 10, 2, at
		advancePassiveObserved(p, &s, e, at)
		require.True(t, s.CacheAllocationReduced)
		require.NotEqual(t, "paused", s.Phase, "healthy hard batches must break the old hard failure clock")
	}
	require.Equal(t, 1, s.Hits)
	require.Equal(t, "ttft_p90", s.ConfirmationMetric)
	require.Equal(t, base.Add(310*time.Second), s.SuspicionSince)
}

func TestGroupTargetTrialPacingCanRecoverAcrossConfiguredDuration(t *testing.T) {
	for _, tc := range []struct {
		name                                                           string
		recovery, requests, confirmations, recoverySamples, newSamples int
	}{
		{"zero_duration", 0, 20, 2, 5, 2},
		{"small_budget_and_short_window", 120, 5, 2, 5, 2},
		{"long_duration", 3600, 20, 2, 5, 2},
		{"many_confirmations", 120, 20, 10, 5, 2},
		{"all_budget_samples", 120, 20, 10, 20, 2},
		{"long_duration_all_budget_samples", 3600, 20, 10, 20, 2},
		{"one_request_batches_large_minimum", 0, 20, 2, 20, 1},
		{"legacy_oversized_batch", 120, 20, 2, 5, 20},
		{"legacy_oversized_confirmations", 120, 5, 10, 5, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := stableTargetPolicy(t)
			p.CacheRate.Enabled = false
			p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}
			settings := p.GroupTargetStability()
			settings.RecoverySeconds = tc.recovery
			p.Stability = &settings
			p.TrialRequests, p.Confirmations, p.WindowSeconds, p.TrialSeconds = tc.requests, tc.confirmations, 60, 60
			p.RecoverySamples = tc.recoverySamples
			p.NewSamples = tc.newSamples
			require.NoError(t, p.Validate(), "bounded trial recovery normalizes legacy batches within the existing request cap")
			base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
			s := service.PassiveQualityState{}
			beginPassiveTrial(p, &s, base, "group_target_trial")
			var samples []service.PassiveQualitySample
			for at := base.Add(time.Second); at.Before(s.TrialEndsAt) && s.Phase == "trial"; at = at.Add(time.Second) {
				if s.Used < p.TrialRequests && passiveTrialAdmissionReady(p.PassiveQualityConfig, s, at) {
					s.Used++
					s.LastTrialAdmissionAt = at
					samples = append(samples, service.PassiveQualitySample{At: at})
				}
				if at.Sub(base)%(30*time.Second) == 0 {
					window := max(time.Duration(p.WindowSeconds)*time.Second, p.GroupTargetTrialDuration())
					var retained []service.PassiveQualitySample
					for _, sample := range samples {
						if !sample.At.Before(at.Add(-window)) {
							retained = append(retained, sample)
						}
					}
					e := service.PassiveQualitySummarize(retained, s.LastEvidenceAt, true)
					advancePassiveObserved(p, &s, e, at)
				}
			}
			require.Equal(t, "healthy", s.Phase, "%+v", s)
			require.LessOrEqual(t, s.Used, p.TrialRequests)
			require.GreaterOrEqual(t, s.LastEvidenceAt.Sub(samples[p.GroupTargetRecoveryBatchSize()-1].At), time.Duration(tc.recovery)*time.Second)
		})
	}
}

func TestGroupTargetTrialUsesMetricTimesInsteadOfUnrelatedRequests(t *testing.T) {
	p := stableTargetPolicy(t)
	p.CacheRate.Enabled = false
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 10000, Recover: 8000}
	base := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{}
	beginPassiveTrial(p, &s, base, "group_target_trial")
	latency := 7000.
	e := service.PassiveQualityEvidence{Complete: true, Samples: 5, Fresh: 3, TTFTSamples: 5, TTFTFresh: 2, TTFT: &latency, FreshTTFT: &latency,
		Latest: base.Add(60 * time.Second), TTFTLatest: base.Add(2 * time.Second)}
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, base.Add(2*time.Second), s.RecoverySince)
	e.Latest, e.TTFTLatest = base.Add(180*time.Second), base.Add(62*time.Second)
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, "trial", s.Phase, "unrelated requests cannot manufacture 120 seconds of healthy latency evidence")
	e.Latest, e.TTFTLatest = base.Add(190*time.Second), base.Add(182*time.Second)
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, "healthy", s.Phase)
}
