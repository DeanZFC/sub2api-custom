package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func activeTargetPolicy(t *testing.T) service.PassiveQualityPolicy {
	t.Helper()
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Purpose: "group_quality_target", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, Model: "A", CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80, Recover: 85}}}
	require.NoError(t, p.Validate())
	return p
}
func activeTargetEvidence(at time.Time) service.PassiveQualityEvidence {
	cache, errors, latency, tps := 90.0, 0.0, 1000.0, 50.0
	return service.PassiveQualityEvidence{Complete: true, Samples: 20, CacheSamples: 20, TTFTSamples: 20, TPSSamples: 20, CacheRate: &cache, ErrorRate: &errors, TTFT: &latency, OutputTPS: &tps, Fresh: 5, CacheFresh: 5, TTFTFresh: 5, TPSFresh: 5, FreshCacheRate: &cache, FreshErrorRate: &errors, FreshTTFT: &latency, FreshOutputTPS: &tps, Latest: at, CacheLatest: at, TTFTLatest: at, TPSLatest: at}
}
func TestActiveTargetEveryMetricHardPausesOnlyAfterSustainedEvidence(t *testing.T) {
	base := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	for _, metric := range []string{"cache", "ttft", "tps", "error"} {
		t.Run(metric, func(t *testing.T) {
			p := activeTargetPolicy(t)
			p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 5000, Recover: 4000}
			p.TPS = service.PassiveQualityMetric{Enabled: true, Pause: 30, Recover: 36}
			p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 5}
			s := service.PassiveQualityState{Phase: "healthy"}
			duration := 300
			if metric == "error" {
				duration = 60
			}
			for elapsed := 0; elapsed <= duration; elapsed += 30 {
				at := base.Add(time.Duration(elapsed) * time.Second)
				e := activeTargetEvidence(at)
				switch metric {
				case "cache":
					*e.CacheRate = 50
				case "ttft":
					*e.TTFT = 6000
				case "tps":
					*e.OutputTPS = 20
				case "error":
					*e.ErrorRate = 20
				}
				advancePassiveObserved(p, &s, e, at)
				if elapsed < duration {
					require.Equal(t, "suspect", s.Phase)
				} else {
					require.Equal(t, "paused", s.Phase)
				}
			}
			require.False(t, s.CacheAllocationReduced, "active mode is a hard pause, never soft cache placement")
		})
	}
}
func TestActiveTargetOldWindowCannotConfirmFreshHealthyBatch(t *testing.T) {
	p := activeTargetPolicy(t)
	base := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{Phase: "healthy"}
	for elapsed := 0; elapsed <= 600; elapsed += 60 {
		at := base.Add(time.Duration(elapsed) * time.Second)
		e := activeTargetEvidence(at)
		cold, warm := 10.0, 95.0
		e.CacheRate, e.FreshCacheRate = &cold, &warm
		advancePassiveObserved(p, &s, e, at)
		require.Equal(t, "suspect", s.Phase)
		require.Zero(t, s.Hits, "old cold traffic must not become another confirmation")
	}
}
func TestActiveTargetPostRecoveryEvidenceGatesAnotherPause(t *testing.T) {
	p := activeTargetPolicy(t)
	base := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{Phase: "healthy", RecoveredAt: base, LastEvidenceAt: base}
	e := activeTargetEvidence(base.Add(time.Minute))
	cold := 10.0
	e.CacheRate = &cold
	fresh := activeTargetEvidence(e.Latest)
	fresh.Samples, fresh.CacheSamples = 4, 4
	e.SinceRecovery = &fresh
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, "insufficient", s.Phase)
	fresh.Samples, fresh.CacheSamples = 20, 20
	advancePassiveObserved(p, &s, e, e.Latest)
	require.Equal(t, "healthy", s.Phase, "historical low hour alone cannot repause a recovered account")
	for elapsed := 120; elapsed <= 420; elapsed += 60 {
		at := base.Add(time.Duration(elapsed) * time.Second)
		e = activeTargetEvidence(at)
		*e.CacheRate = 10
		after := e
		e.SinceRecovery = &after
		advancePassiveObserved(p, &s, e, at)
	}
	require.Equal(t, "paused", s.Phase, "a complete new post-recovery failure episode still pauses")
}
func TestActiveTargetRapidUpstreamFailureCircuit(t *testing.T) {
	p := activeTargetPolicy(t)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 5}
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	e := activeTargetEvidence(now)
	e.Samples = 3
	e.RecentConsecutiveFailures = 3
	s := service.PassiveQualityState{Phase: "healthy"}
	advancePassiveObserved(p, &s, e, now)
	require.Equal(t, "paused", s.Phase)
	require.Contains(t, s.Reason, "within 60s")
	for _, tc := range []struct {
		name string
		e    service.PassiveQualityEvidence
		last time.Time
	}{
		{"replayed", e, now},
		{"stale", func() service.PassiveQualityEvidence { x := e; x.Latest = now.Add(-61 * time.Second); return x }(), time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := service.PassiveQualityState{Phase: "healthy", LastEvidenceAt: tc.last}
			advancePassiveObserved(p, &s, tc.e, now)
			require.NotEqual(t, "paused", s.Phase)
		})
	}
}
func activeRecoveryPassingResult(now time.Time) service.PassiveQualityActiveRecoveryResult {
	first, last := activeTargetEvidence(now.Add(time.Second)), activeTargetEvidence(now.Add(121*time.Second))
	first.Samples, first.CacheSamples, first.TTFTSamples, first.TPSSamples = 5, 5, 5, 5
	last.Samples, last.CacheSamples, last.TTFTSamples, last.TPSSamples = 5, 5, 5, 5
	return service.PassiveQualityActiveRecoveryResult{Verdict: "pass", Passed: true, Requests: 12, WarmupRequests: 2, Batches: 2, StartedAt: now, FinishedAt: now.Add(122 * time.Second), Evidence: activeTargetEvidence(last.Latest), EvidenceBatches: []service.PassiveQualityEvidence{first, last}}
}
func TestActiveRecoverySettlementRequiresCurrentCompleteSeparatedBatches(t *testing.T) {
	p := activeTargetPolicy(t)
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 5000, Recover: 4000}
	p.TPS = service.PassiveQualityMetric{Enabled: true, Pause: 30, Recover: 36}
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{ProbeStartedAt: now, NextAt: now.Add(12 * time.Minute)}
	for _, tc := range []struct {
		name, want string
		edit       func(*service.PassiveQualityActiveRecoveryResult)
	}{
		{"valid", "pass", func(*service.PassiveQualityActiveRecoveryResult) {}},
		{"missing cache", "pending", func(r *service.PassiveQualityActiveRecoveryResult) { r.EvidenceBatches[1].CacheRate = nil }},
		{"missing tps", "pending", func(r *service.PassiveQualityActiveRecoveryResult) { r.EvidenceBatches[1].OutputTPS = nil }},
		{"low tps", "fail", func(r *service.PassiveQualityActiveRecoveryResult) { v := 1.0; r.EvidenceBatches[1].OutputTPS = &v }},
		{"prejob batch", "pending", func(r *service.PassiveQualityActiveRecoveryResult) {
			r.EvidenceBatches[0].Latest = now.Add(-time.Second)
		}},
		{"future metric", "pending", func(r *service.PassiveQualityActiveRecoveryResult) {
			r.EvidenceBatches[1].CacheLatest = now.Add(time.Hour)
		}},
		{"runtime only", "pending", func(r *service.PassiveQualityActiveRecoveryResult) { r.EvidenceBatches[1] = r.EvidenceBatches[0] }},
		{"single batch", "pending", func(r *service.PassiveQualityActiveRecoveryResult) { r.Batches = 1 }},
		{"prejob start", "pending", func(r *service.PassiveQualityActiveRecoveryResult) { r.StartedAt = now.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := activeRecoveryPassingResult(now)
			tc.edit(&r)
			got, _ := validateActiveRecoveryResult(p.PassiveQualityConfig, s, r, now.Add(123*time.Second))
			require.Equal(t, tc.want, got)
		})
	}
}
