package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPassiveQualityPoolOpenAIQuotaReserve(t *testing.T) {
	now := time.Now()
	a := &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"auto_pause_5h_threshold": 0.8, "auto_pause_7d_disabled": true, "codex_5h_used_percent": 90.0, "codex_usage_updated_at": now.Format(time.RFC3339), "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
	}}
	require.False(t, PassiveQualityPoolAccountEligible(context.Background(), a, "gpt-5.6-sol"), "account beyond explicit upstream quota threshold is not serving reserve")
	a.Extra["codex_5h_reset_at"] = now.Add(-time.Second).Format(time.RFC3339)
	require.True(t, PassiveQualityPoolAccountEligible(context.Background(), a, "gpt-5.6-sol"), "an expired upstream quota window must stop blocking")
	a.Extra["codex_5h_reset_at"] = now.Add(time.Hour).Format(time.RFC3339)
	a.Extra[OpenAIAutoResetCreditEnabledExtraKey] = true
	a.Extra[OpenAIAutoResetCredit5hThresholdExtraKey] = 1.0
	a.Extra[OpenAIAutoResetCredit7dThresholdExtraKey] = 1.0
	require.False(t, PassiveQualityPoolAccountEligible(context.Background(), a, "gpt-5.6-sol"), "unknown reset credit capacity is not reserve")
	a.Extra[OpenAIAutoResetCreditStateExtraKey] = OpenAIAutoResetCreditState{Status: OpenAIAutoResetStatusAvailable, AvailableCount: 1, CheckedAt: now.Format(time.RFC3339)}
	require.True(t, PassiveQualityPoolAccountEligible(context.Background(), a, "gpt-5.6-sol"))
	a.Extra["codex_5h_used_percent"] = 100.0
	require.False(t, PassiveQualityPoolAccountEligible(context.Background(), a, "gpt-5.6-sol"), "pending credit reset cannot count as currently serving")
}

func isolatedPassiveTestConfig() PassiveQualityConfig {
	c := passiveTestConfig()
	c.SchemaVersion = 2
	c.CacheRate = PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 50}
	p := DefaultPassiveQualityCacheConfig()
	p.Quick = PassiveQualityCacheWindow{WindowSeconds: 300, MaxSamples: 50, MinSamples: 6, Confirmations: 2, NewSamples: 2, MinTokens: 600, MinSessions: 3, MaxSessionSharePercent: 60}
	p.ActionWindow = PassiveQualityCacheWindow{WindowSeconds: 900, MaxSamples: 200, MinSamples: 6, Confirmations: 3, NewSamples: 2, MinTokens: 600, MinSessions: 3, MaxSessionSharePercent: 60}
	p.MinHoldSeconds = 30
	p.RecoveryHoldSeconds = 30
	c.CacheProtection = &p
	return c
}

func reusableCacheSamples(now time.Time, n int, read int64) []PassiveQualitySample {
	samples := make([]PassiveQualitySample, 0, n)
	for i := 0; i < n; i++ {
		samples = append(samples, PassiveQualitySample{Key: fmt.Sprintf("%d/%d", now.UnixNano(), i), Model: "model", At: now.Add(-time.Duration(i) * time.Second), CacheReported: true, CacheOpportunity: "reusable", CacheSession: fmt.Sprintf("session-%d", i%3), CachePrefix: "verified-prefix", CacheExpectedTokens: 100, CacheInput: 200, CacheRead: read})
	}
	return samples
}

func cacheWindowsForTest(c PassiveQualityConfig, s PassiveQualityCacheState, samples []PassiveQualitySample, now time.Time) PassiveQualityCacheWindowsEvidence {
	return PassiveQualityCacheWindows(c, samples, map[string]PassiveQualityCacheState{"model": s}, now, true)["model"]
}

func TestPassiveQualityV2ExplicitOptInAndValidation(t *testing.T) {
	legacy := passiveTestConfig()
	require.NoError(t, legacy.Validate())
	require.Nil(t, legacy.CacheProtection)
	require.False(t, legacy.IsolatedMetrics())
	c := passiveTestConfig()
	c.SchemaVersion = 2
	require.NoError(t, c.Validate())
	require.Equal(t, DefaultPassiveQualityCacheConfig(), *c.CacheProtection)
	require.Equal(t, "limit", c.TTFTAction)
	require.Equal(t, 2, c.TTFTConcurrencyLimit)
	for _, edit := range []func(*PassiveQualityConfig){
		func(c *PassiveQualityConfig) { c.SchemaVersion = 1 },
		func(c *PassiveQualityConfig) { c.TTFTAction = "pause" },
		func(c *PassiveQualityConfig) { c.CacheProtection.Action = "pause" },
		func(c *PassiveQualityConfig) { c.CacheProtection.AllocationPercent = 0 },
		func(c *PassiveQualityConfig) { c.CacheProtection.MaxReducedPercent = 100 },
		func(c *PassiveQualityConfig) { c.CacheProtection.ActionWindow.MinSamples = 5 },
		func(c *PassiveQualityConfig) { c.CacheProtection.ActionWindow.MinSessions = 1 },
		func(c *PassiveQualityConfig) { c.CacheProtection.ActionWindow.MinTokens = 0 },
	} {
		x := isolatedPassiveTestConfig()
		edit(&x)
		require.Error(t, x.Validate())
	}
}

func TestPassiveQualityReusableCacheIncludesZeroWithoutPriorHit(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	samples := reusableCacheSamples(now, 6, 0)
	e := cacheWindowsForTest(c, PassiveQualityCacheState{}, samples, now).Action
	require.Equal(t, 6, e.ReusableSamples)
	require.Equal(t, 3, e.Sessions)
	require.Equal(t, 3, e.BreachedSessions)
	require.Equal(t, 0.0, *e.CacheRate)
	require.Equal(t, 0.0, *e.ProtectionRate)
	verdict, reason := EvaluatePassiveQualityCache(c, c.CacheProtection.ActionWindow, e, false)
	require.Equal(t, "fail", verdict)
	require.Equal(t, "cache_reuse_below_threshold", reason)
}

func TestPassiveQualityCacheActualAndReusableDenominators(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	samples := reusableCacheSamples(now, 6, 80)
	cold := reusableCacheSamples(now.Add(time.Second), 1, 0)[0]
	cold.CacheOpportunity = "cold"
	cold.CacheReason = "first_account_placement"
	unknown := cold
	unknown.Key = "unknown"
	unknown.CacheOpportunity = "unknown"
	unknown.CacheReason = "legacy_telemetry"
	missing := cold
	missing.Key = "missing"
	missing.CacheReported = false
	failed := cold
	failed.Key = "failed"
	failed.Failed = true
	samples = append(samples, cold, unknown, missing, failed)
	e := cacheWindowsForTest(c, PassiveQualityCacheState{}, samples, now.Add(time.Second)).Action
	require.Equal(t, 10, e.TotalSamples)
	require.Equal(t, 1, e.FailedSamples)
	require.Equal(t, 8, e.ReportedSamples)
	require.Equal(t, 6, e.ReusableSamples)
	require.Equal(t, 1, e.ColdSamples)
	require.Equal(t, 1, e.UnknownSamples)
	require.Equal(t, 1, e.MissingTelemetrySamples)
	require.Equal(t, int64(1600), e.InputTokens)
	require.Equal(t, int64(600), e.EligibleInputTokens)
	require.Equal(t, 30.0, *e.CacheRate)
	require.Equal(t, 80.0, *e.ProtectionRate)
	require.Equal(t, 6, e.Fresh, "cold, unknown and missing-telemetry samples cannot advance confirmations")
}

func TestPassiveQualityCacheNewSessionsAndUnknownStayPending(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	for _, kind := range []string{"cold", "unknown", ""} {
		samples := reusableCacheSamples(now, 30, 0)
		for i := range samples {
			samples[i].CacheOpportunity = kind
		}
		s := PassiveQualityCacheState{}
		e := cacheWindowsForTest(c, s, samples, now)
		verdict, _ := EvaluatePassiveQualityCache(c, c.CacheProtection.ActionWindow, e.Action, false)
		require.Equal(t, "pending", verdict)
		require.Equal(t, "", AdvancePassiveQualityCache(c, &s, e, now, true))
		require.Equal(t, 100, s.AllocationPercent)
		require.Zero(t, s.Hits)
		require.Equal(t, "insufficient", s.Phase)
	}
}

func TestPassiveQualityCacheRejectsUnprovenOrMalformedReuse(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	for _, edit := range []func(*PassiveQualitySample){
		func(s *PassiveQualitySample) { s.CacheSession = "" },
		func(s *PassiveQualitySample) { s.CachePrefix = "" },
		func(s *PassiveQualitySample) { s.CacheExpectedTokens = 0 },
		func(s *PassiveQualitySample) { s.CacheExpectedTokens = s.CacheInput + 1 },
		func(s *PassiveQualitySample) { s.CacheReported = false },
		func(s *PassiveQualitySample) { s.CacheRead = s.CacheInput + 1 },
	} {
		samples := reusableCacheSamples(now, 6, 0)
		for i := range samples {
			edit(&samples[i])
		}
		e := cacheWindowsForTest(c, PassiveQualityCacheState{}, samples, now).Action
		require.Zero(t, e.ReusableSamples)
		require.Nil(t, e.ProtectionRate)
	}
}

func TestPassiveQualityCacheRejectsSingleDominantConversation(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	samples := reusableCacheSamples(now, 6, 0)
	samples[0].CacheExpectedTokens = 10000
	samples[0].CacheInput = 10000
	e := cacheWindowsForTest(c, PassiveQualityCacheState{}, samples, now).Action
	require.Equal(t, 3, e.Sessions)
	verdict, reason := EvaluatePassiveQualityCache(c, c.CacheProtection.ActionWindow, e, false)
	require.Equal(t, "pending", verdict)
	require.Equal(t, "cache_session_concentration", reason)
	// A large healthy session cannot establish account recovery over bad sessions.
	samples[0].CacheRead = 10000
	e = cacheWindowsForTest(c, PassiveQualityCacheState{}, samples, now).Action
	verdict, _ = EvaluatePassiveQualityCache(c, c.CacheProtection.ActionWindow, e, true)
	require.Equal(t, "pending", verdict)
}

func TestPassiveQualityCacheModelsAndWindowsStaySeparate(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	c.Model = ""
	samples := reusableCacheSamples(now, 6, 0)
	other := reusableCacheSamples(now, 6, 100)
	for i := range other {
		other[i].Model = "other"
	}
	samples = append(samples, other...)
	windows := PassiveQualityCacheWindows(c, samples, nil, now, true)
	require.Len(t, windows, 2)
	require.Equal(t, 0.0, *windows["model"].Action.ProtectionRate)
	require.Equal(t, 100.0, *windows["other"].Action.ProtectionRate)
	old := reusableCacheSamples(now.Add(-400*time.Second), 6, 0)
	e := PassiveQualityCacheWindows(c, old, nil, now, true)["model"]
	require.Zero(t, e.Quick.ReusableSamples)
	require.Equal(t, 6, e.Action.ReusableSamples)
}

func TestPassiveQualityCacheWindowSortsCapsAndDeduplicates(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	c.CacheProtection.ActionWindow.MaxSamples = 6
	samples := reusableCacheSamples(now, 12, 0)
	samples = append(samples, samples[0], samples[0])
	for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
		samples[i], samples[j] = samples[j], samples[i]
	}
	e := cacheWindowsForTest(c, PassiveQualityCacheState{}, samples, now).Action
	require.Equal(t, 6, e.TotalSamples)
	require.Equal(t, 6, e.ReusableSamples)
	require.Equal(t, now, e.Latest)
}

func TestPassiveQualityCacheConfirmationsNeedNewReusableEvidence(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{}
	samples := reusableCacheSamples(now, 6, 0)
	e := cacheWindowsForTest(c, s, samples, now)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now, true))
	require.Equal(t, 1, s.Hits)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now.Add(time.Second), true), "replaying the same evidence cannot advance")
	require.Equal(t, 1, s.Hits)
	cold := reusableCacheSamples(now.Add(10*time.Second), 6, 0)
	for i := range cold {
		cold[i].CacheOpportunity = "cold"
	}
	samples = append(samples, cold...)
	e = cacheWindowsForTest(c, s, samples, now.Add(10*time.Second))
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now.Add(10*time.Second), true))
	require.Equal(t, 1, s.Hits)
	for batch := 1; batch <= 2; batch++ {
		at := now.Add(time.Duration(20*batch) * time.Second)
		samples = append(samples, reusableCacheSamples(at, 6, 0)...)
		e = cacheWindowsForTest(c, s, samples, at)
		event := AdvancePassiveQualityCache(c, &s, e, at, true)
		if batch == 1 {
			require.Equal(t, "cache_watch", event)
			require.Equal(t, 100, s.AllocationPercent)
		} else {
			require.Equal(t, "cache_reduced", event)
		}
	}
	require.Equal(t, "reduced", s.Phase)
	require.Equal(t, 25, s.AllocationPercent)
	require.True(t, s.ObserveAfter.Equal(now.Add(40*time.Second)))
}

func TestPassiveQualityCachePoolGuardAndObserveNeverPause(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	c.CacheProtection.ActionWindow.Confirmations = 1
	for _, mode := range []string{"observe", "pool_guard"} {
		s := PassiveQualityCacheState{}
		cfg := c
		if mode == "observe" {
			cfg.Mode = "observe"
		}
		e := cacheWindowsForTest(cfg, s, reusableCacheSamples(now, 6, 0), now)
		event := AdvancePassiveQualityCache(cfg, &s, e, now, false)
		require.Equal(t, "watching", s.Phase)
		require.Equal(t, 100, s.AllocationPercent)
		if mode == "pool_guard" {
			require.Equal(t, "cache_pool_guard", event)
		}
	}
}

func TestPassiveQualityCacheRecoveryUsesFreshStagesAndHold(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{Phase: "reduced", AllocationPercent: 25, ObserveAfter: now, LastEvidenceAt: now, HoldUntil: now.Add(30 * time.Second)}
	samples := reusableCacheSamples(now.Add(-time.Second), 6, 100)
	e := cacheWindowsForTest(c, s, samples, now.Add(time.Second))
	require.Equal(t, 0, e.Action.ReusableSamples, "pre-restriction good requests cannot recover a hold")
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now.Add(time.Second), true))
	require.Equal(t, "reduced", s.Phase)
	// Bounded cold traffic must not falsely heal or escalate the restriction.
	cold := reusableCacheSamples(now.Add(40*time.Second), 6, 0)
	for i := range cold {
		cold[i].CacheOpportunity = "cold"
	}
	e = cacheWindowsForTest(c, s, cold, now.Add(40*time.Second))
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now.Add(40*time.Second), true))
	require.Equal(t, 25, s.AllocationPercent)
	for stage := 0; stage < 3; stage++ {
		at := now.Add(time.Duration(50+stage*60) * time.Second)
		first := reusableCacheSamples(at, 6, 100)
		e = cacheWindowsForTest(c, s, first, at)
		require.Empty(t, AdvancePassiveQualityCache(c, &s, e, at, true))
		require.Equal(t, 1, s.RecoveryHits)
		require.Empty(t, AdvancePassiveQualityCache(c, &s, e, at.Add(time.Second), true))
		require.Equal(t, 1, s.RecoveryHits)
		second := reusableCacheSamples(at.Add(10*time.Second), 6, 100)
		e = cacheWindowsForTest(c, s, append(first, second...), at.Add(10*time.Second))
		event := AdvancePassiveQualityCache(c, &s, e, at.Add(10*time.Second), true)
		require.Equal(t, 50+stage*25, s.AllocationPercent)
		if stage == 2 {
			require.Equal(t, "cache_recovered", event)
			require.Equal(t, "healthy", s.Phase)
		} else {
			require.Equal(t, "cache_recovery_step", event)
			require.Equal(t, "recovering", s.Phase)
		}
	}
}

func TestPassiveQualityCacheRecoveryRelapseDoesNotPause(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{Phase: "recovering", AllocationPercent: 75, ObserveAfter: now, LastEvidenceAt: now, HoldUntil: now.Add(30 * time.Second)}
	var samples []PassiveQualitySample
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(40+i*10) * time.Second)
		samples = append(samples, reusableCacheSamples(at, 6, 0)...)
		event := AdvancePassiveQualityCache(c, &s, cacheWindowsForTest(c, s, samples, at), at, true)
		if i == 2 {
			require.Equal(t, "cache_recovery_relapsed", event)
		}
	}
	require.Equal(t, "reduced", s.Phase)
	require.Equal(t, 25, s.AllocationPercent)
}

func TestPassiveQualityAvailabilityRecoveryIgnoresCacheAndLatency(t *testing.T) {
	c := isolatedPassiveTestConfig()
	c.TTFT = PassiveQualityMetric{Enabled: true, Pause: 1000, Recover: 500}
	e := PassiveQualityEvidence{Samples: 6, Complete: true, ErrorRate: new(float64(0))}
	verdict, _ := EvaluatePassiveQuality(PassiveQualityMetricConfig(c, "availability"), e, true)
	require.Equal(t, "pass", verdict)
	verdict, _ = EvaluatePassiveQuality(c, e, true)
	require.Equal(t, "pending", verdict, "legacy evaluator keeps its compatibility contract")
}

func TestPassiveQualityLatencyNeedsNewLatencyEvidence(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	c.TTFT = PassiveQualityMetric{Enabled: true, Pause: 1000, Recover: 500}
	s := PassiveQualityMetricState{}
	samples := reusableCacheSamples(now, 6, 0)
	for i := range samples {
		samples[i].TTFT = new(float64(2000))
	}
	e := PassiveQualitySummarize(samples, s.LastEvidenceAt, true)
	require.Empty(t, AdvancePassiveQualityLatency(c, &s, e, now))
	require.Equal(t, 1, s.Hits)
	for i := range samples {
		samples[i].At = samples[i].At.Add(10 * time.Second)
		samples[i].TTFT = nil
	}
	e = PassiveQualitySummarize(samples, s.LastEvidenceAt, true)
	require.Empty(t, AdvancePassiveQualityLatency(c, &s, e, now.Add(10*time.Second)))
	require.Zero(t, s.Hits, "missing metric evidence invalidates the confirmation streak")
	for i := range samples {
		samples[i].TTFT = new(float64(2000))
	}
	e = PassiveQualitySummarize(samples, s.LastEvidenceAt, true)
	require.Empty(t, AdvancePassiveQualityLatency(c, &s, e, now.Add(10*time.Second)))
	for i := range samples {
		samples[i].At = samples[i].At.Add(10 * time.Second)
	}
	e = PassiveQualitySummarize(samples, s.LastEvidenceAt, true)
	require.Equal(t, "ttft_limited", AdvancePassiveQualityLatency(c, &s, e, now.Add(20*time.Second)))
	require.Equal(t, "limited", s.Phase)
	require.True(t, s.ObserveAfter.Equal(now.Add(20*time.Second)))
}

func TestPassiveQualityCacheSnapshotsPreserveExistingSessions(t *testing.T) {
	a := &Account{Extra: map[string]any{"passive_quality": []any{map[string]any{"scope": "model", "model": "model", "phase": "cache_limited", "allocation_percent": float64(25)}}}}
	require.False(t, a.PassiveQualityBlocked("model"))
	require.False(t, a.PassiveQualityBlocked("other"))
	states, ok := a.Extra["passive_quality"].([]any)
	require.True(t, ok)
	a.Extra["passive_quality"] = append(states, map[string]any{"scope": "model", "model": "model", "phase": "paused"})
	require.True(t, a.PassiveQualityBlocked("model"), "real availability pause still takes precedence")
}

func TestPassiveQualityCacheEmptyEvidenceDoesNotBecomeHealthy(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{}
	e := cacheWindowsForTest(c, s, nil, now)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now, true))
	require.Equal(t, "insufficient", s.Phase)
	require.Equal(t, "insufficient_reusable_samples", s.Reason)
	require.Equal(t, 100, s.AllocationPercent)
	// A hold is not healed by the passage of its warmup/minimum-hold time.
	s = PassiveQualityCacheState{Phase: "reduced", AllocationPercent: 25, ObserveAfter: now, HoldUntil: now.Add(time.Second)}
	later := now.Add(time.Hour)
	e = cacheWindowsForTest(c, s, nil, later)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, later, true))
	require.Equal(t, "reduced", s.Phase)
	require.Equal(t, 25, s.AllocationPercent)
	require.Equal(t, "insufficient_reusable_samples", s.Reason)
}

func TestPassiveQualityCacheIncompleteOrExpiredEvidenceResetsConfirmations(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	for _, kind := range []string{"gap", "expired", "missing"} {
		t.Run(kind, func(t *testing.T) {
			s := PassiveQualityCacheState{}
			samples := reusableCacheSamples(now, 6, 0)
			require.Empty(t, AdvancePassiveQualityCache(c, &s, cacheWindowsForTest(c, s, samples, now), now, true))
			require.Equal(t, 1, s.Hits)
			next := now.Add(time.Second)
			e := cacheWindowsForTest(c, s, samples, next)
			switch kind {
			case "gap":
				e.Action.Complete = false
				e.Quick.Complete = false
			case "expired":
				next = now.Add(901 * time.Second)
				e = cacheWindowsForTest(c, s, samples, next)
			case "missing":
				fresh := reusableCacheSamples(next, 6, 0)
				for i := range fresh {
					fresh[i].CacheReported = false
				}
				e = cacheWindowsForTest(c, s, fresh, next)
			}
			require.Empty(t, AdvancePassiveQualityCache(c, &s, e, next, true))
			require.Zero(t, s.Hits)
			require.Zero(t, s.QuickHits)
			require.Equal(t, "insufficient", s.Phase)
		})
	}
}

func TestPassiveQualityCacheEqualTimestampArrivalsCountExactlyOnce(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{}
	samples := reusableCacheSamples(now, 6, 0)
	for i := range samples {
		samples[i].At = now
	}
	first := cacheWindowsForTest(c, s, samples, now)
	require.Equal(t, 6, first.Action.Fresh)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, first, now, true))
	require.Equal(t, 1, s.Hits)
	require.Len(t, s.LastEvidenceKeys, 6)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, first, now.Add(time.Second), true))
	require.Equal(t, 1, s.Hits)
	for batch := 1; batch <= 2; batch++ {
		more := reusableCacheSamples(now, 6, 0)
		for i := range more {
			more[i].Key = fmt.Sprintf("late-%d-%d", batch, i)
			more[i].At = now
		}
		samples = append(samples, more...)
		e := cacheWindowsForTest(c, s, samples, now.Add(time.Duration(batch)*time.Second))
		require.Equal(t, 6, e.Action.Fresh)
		event := AdvancePassiveQualityCache(c, &s, e, now.Add(time.Duration(batch)*time.Second), true)
		if batch == 1 {
			require.Equal(t, "cache_watch", event)
			require.Equal(t, 2, s.Hits)
		} else {
			require.Equal(t, "cache_reduced", event)
		}
	}
	require.Equal(t, "reduced", s.Phase)
	require.Equal(t, 25, s.AllocationPercent)
}

func TestPassiveQualityCacheRecoveryGapResetsHealthyStreak(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{Phase: "reduced", AllocationPercent: 25, ObserveAfter: now, LastEvidenceAt: now, HoldUntil: now}
	at := now.Add(time.Minute)
	good := reusableCacheSamples(at, 6, 100)
	e := cacheWindowsForTest(c, s, good, at)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, at, true))
	require.Equal(t, 1, s.RecoveryHits)
	e.Action.Complete = false
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, at.Add(time.Second), true))
	require.Zero(t, s.RecoveryHits)
	require.Equal(t, 25, s.AllocationPercent)
}

func TestPassiveQualityCacheMinimumHoldRequiresNewEvidenceAfterHold(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	s := PassiveQualityCacheState{Phase: "reduced", AllocationPercent: 25, ObserveAfter: now, LastEvidenceAt: now, HoldUntil: now.Add(time.Minute)}
	at := now.Add(20 * time.Second)
	good := reusableCacheSamples(at, 6, 100)
	e := cacheWindowsForTest(c, s, good, at)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, at, true))
	require.Zero(t, s.RecoveryHits)
	require.Equal(t, "cache_minimum_hold", s.Reason)
	require.Empty(t, AdvancePassiveQualityCache(c, &s, e, now.Add(2*time.Minute), true))
	require.Zero(t, s.RecoveryHits, "the passage of time cannot replay pre-hold evidence")
	require.Equal(t, 25, s.AllocationPercent)
}
