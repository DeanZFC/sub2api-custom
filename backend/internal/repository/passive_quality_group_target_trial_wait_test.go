package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityTargetTrialWaitDoesNotTreatMissingEvidenceAsFailure(t *testing.T) {
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	cache := 95.0
	for _, tc := range []struct {
		name     string
		evidence service.PassiveQualityEvidence
		reason   string
	}{
		{"no_samples", service.PassiveQualityEvidence{Complete: true}, "insufficient_samples"},
		{"few_samples", service.PassiveQualityEvidence{Complete: true, Samples: 4, CacheSamples: 4, CacheRate: &cache}, "insufficient_samples"},
		{"few_metric_samples", service.PassiveQualityEvidence{Complete: true, Samples: 8, CacheSamples: 4, CacheRate: &cache}, "stability_confirmation"},
		{"telemetry_gap", service.PassiveQualityEvidence{Samples: 8, CacheSamples: 8, CacheRate: &cache}, "telemetry_incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := independentObservedGroupTarget(t)
			s := service.PassiveQualityState{AccountID: 62, Phase: "trial", Generation: 7, Backoff: 3, Used: p.TrialRequests, TrialStartedAt: now.Add(-time.Minute), TrialEndsAt: now, LastEvidenceAt: now.Add(-time.Minute)}
			for _, at := range []time.Time{now, now.Add(time.Second), now.Add(time.Duration(p.CooldownSeconds-1) * time.Second)} {
				advancePassiveObserved(p, &s, tc.evidence, at)
				require.Equal(t, "trial", s.Phase)
				require.Equal(t, "waiting_for_recovery: "+tc.reason, s.Reason)
				require.Equal(t, 3, s.Backoff)
				require.EqualValues(t, 7, s.Generation)
				require.Equal(t, p.TrialRequests, s.Used, "waiting must not refund an exhausted admission budget")
				require.Equal(t, now, s.TrialEndsAt)
				require.Equal(t, now.Add(time.Duration(p.CooldownSeconds)*time.Second), s.NextAt, "sweeps must not postpone the next window")
			}
		})
	}
}

func TestGroupQualityTargetTrialWaitReopensBoundedWindowsWithoutErasingEvidence(t *testing.T) {
	p := independentObservedGroupTarget(t)
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	start := now.Add(-time.Duration(p.TrialSeconds) * time.Second)
	s := service.PassiveQualityState{AccountID: 62, Phase: "trial", Generation: 7, Backoff: 3, Used: p.TrialRequests, Inflight: 1, TrialStartedAt: start, TrialEndsAt: now, LastEvidenceAt: start}
	cache := 90.0
	e := service.PassiveQualityEvidence{Complete: true, Samples: 2, CacheSamples: 2, CacheRate: &cache, Latest: now.Add(-time.Second)}
	for round := 1; round <= 2; round++ {
		reopen := s.TrialEndsAt.Add(time.Duration(p.CooldownSeconds) * time.Second)
		advancePassiveObserved(p, &s, e, reopen)
		require.Equal(t, "trial", s.Phase)
		require.Equal(t, 3, s.Backoff)
		require.EqualValues(t, 7+round, s.Generation)
		require.Zero(t, s.Used)
		require.Zero(t, s.Inflight)
		require.Equal(t, start, s.TrialStartedAt)
		require.Equal(t, start, s.LastEvidenceAt)
		require.Equal(t, e, s.Evidence)
		require.Equal(t, reopen.Add(time.Duration(p.TrialSeconds)*time.Second), s.TrialEndsAt)
		require.True(t, s.NextAt.IsZero())
		s.Used = p.TrialRequests
	}
}

func TestGroupQualityTargetTrialWaitCannotRenewOverOtherHolds(t *testing.T) {
	p := independentObservedGroupTarget(t)
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{Phase: "trial", Generation: 7, Backoff: 3, Used: 2, TrialStartedAt: now.Add(-time.Hour), TrialEndsAt: now, OtherHold: true}
	advancePassiveObserved(p, &s, service.PassiveQualityEvidence{Complete: true}, now.Add(time.Hour))
	require.Equal(t, "trial", s.Phase)
	require.EqualValues(t, 7, s.Generation)
	require.Equal(t, now, s.TrialEndsAt)
	require.Equal(t, 2, s.Used)
	require.Equal(t, 3, s.Backoff)
}

func TestGroupQualityTargetTrialWaitStillResolvesRealEvidence(t *testing.T) {
	p := independentObservedGroupTarget(t)
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	for _, cache := range []float64{50, 82, 95} {
		s := service.PassiveQualityState{AccountID: 62, Phase: "trial", Generation: 7, Backoff: 3, TrialStartedAt: now.Add(-time.Minute), TrialEndsAt: now}
		e := service.PassiveQualityEvidence{Complete: true, Samples: 5, Fresh: 2, CacheFresh: 2, CacheSamples: 5, CacheRate: &cache, FreshCacheRate: &cache, Latest: now.Add(-time.Second)}
		advancePassiveObserved(p, &s, e, now.Add(-time.Second))
		require.Equal(t, "trial", s.Phase)
		e.Latest = now.Add(time.Second)
		advancePassiveObserved(p, &s, e, now.Add(2*time.Second))
		if cache >= p.CacheRate.Recover {
			require.Equal(t, "healthy", s.Phase)
			require.Equal(t, s.TrialStartedAt, s.ObserveAfter)
		} else {
			require.Equal(t, "suspect", s.Phase, "cache alone remains soft after hard availability recovery")
			require.Equal(t, "cache_rate", s.ConfirmationMetric)
			require.Contains(t, s.Reason, "cache_allocation_reduced:")
		}
		require.Equal(t, 3, s.Backoff, "short successful trials retain repeated-failure backoff")
		require.EqualValues(t, 8, s.Generation)
	}
}

func TestOrdinaryPassiveTrialExpirySemanticsRemainUnchanged(t *testing.T) {
	p := independentObservedGroupTarget(t)
	p.Purpose = ""
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{AccountID: 62, Phase: "trial", Generation: 7, Backoff: 3, TrialStartedAt: now.Add(-time.Minute), TrialEndsAt: now}
	advancePassiveObserved(p, &s, service.PassiveQualityEvidence{Complete: true}, now)
	require.Equal(t, "paused", s.Phase)
	require.Equal(t, "trial_expired: insufficient_samples", s.Reason)
	require.Equal(t, 4, s.Backoff)
}
