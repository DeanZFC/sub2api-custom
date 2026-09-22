package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cacheRecoveryRunnerRepo struct {
	*protectionRepositoryStub
	windowStart *time.Time
	advancedAt  time.Time
}

func (r *cacheRecoveryRunnerRepo) AdvanceCacheRecovery(_ context.Context, now time.Time) error {
	r.advancedAt = now
	return nil
}

func (r *cacheRecoveryRunnerRepo) CacheRecoveryWindowStart(context.Context, int64, int64, int64) (*time.Time, error) {
	return r.windowStart, nil
}

func TestScheduledTestCacheRecoveryRunnerUsesFreshWindow(t *testing.T) {
	for _, age := range []time.Duration{5 * time.Minute, 2 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			start := time.Now().UTC().Add(-age)
			repo := &cacheRecoveryRunnerRepo{protectionRepositoryStub: &protectionRepositoryStub{eligible: true}, windowStart: &start}
			repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
				require.Equal(t, start, *filter.RequestStartedAfter)
				if age < time.Hour {
					require.Equal(t, start, filter.WindowStart)
				} else {
					require.Equal(t, time.Hour, filter.WindowEnd.Sub(filter.WindowStart))
				}
				return &ScheduledTestStatistics{WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd}, nil
			}
			runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, repo), nil, nil, nil, nil)
			runner.runStatisticsDefinition(context.Background(), protectionPlan())
			require.Contains(t, repo.events, "collect")
			require.NotNil(t, repo.completed.OutputStatistics)
			now := time.Now().UTC()
			runner.advanceCacheRecovery(context.Background(), now)
			require.Equal(t, now, repo.advancedAt, "recovery advances independently of due plan discovery")
		})
	}
}

func cacheRecoveryRule() ScheduledTestProtectionRule {
	return ScheduledTestProtectionRule{
		TestDefinitionID: 1, MinSamples: 100,
		OnPass:     &ScheduledTestOutcomeAction{Scheduling: "resume"},
		OnFail:     &ScheduledTestOutcomeAction{Scheduling: "pause"},
		Thresholds: []ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 80}},
		Recovery:   &ScheduledTestCacheRecoveryConfig{Enabled: true, CooldownSeconds: 300, TrialSeconds: 300, MaxRequests: 20, MinSamples: 10, RecoverRate: 85},
	}
}

func TestScheduledTestCacheRecoveryValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ScheduledTestProtectionRule)
		valid  bool
	}{
		{"valid", func(r *ScheduledTestProtectionRule) {}, true},
		{"disabled legacy", func(r *ScheduledTestProtectionRule) { r.Recovery = nil }, true},
		{"short cooldown", func(r *ScheduledTestProtectionRule) { r.Recovery.CooldownSeconds = 59 }, false},
		{"long trial", func(r *ScheduledTestProtectionRule) { r.Recovery.TrialSeconds = 3601 }, false},
		{"unreachable samples", func(r *ScheduledTestProtectionRule) { r.Recovery.MinSamples = 21 }, false},
		{"unbounded requests", func(r *ScheduledTestProtectionRule) { r.Recovery.MaxRequests = 1001 }, false},
		{"lower recovery", func(r *ScheduledTestProtectionRule) { r.Recovery.RecoverRate = 79 }, false},
		{"nonfinite recovery", func(r *ScheduledTestProtectionRule) { r.Recovery.RecoverRate = math.NaN() }, false},
		{"wrong direction", func(r *ScheduledTestProtectionRule) { r.Thresholds[0].Operator = "gt" }, false},
		{"no cache threshold", func(r *ScheduledTestProtectionRule) { r.Thresholds = nil }, false},
		{"no resume", func(r *ScheduledTestProtectionRule) { r.OnPass = &ScheduledTestOutcomeAction{Scheduling: "keep"} }, false},
		{"no pause", func(r *ScheduledTestProtectionRule) { r.OnFail = &ScheduledTestOutcomeAction{Scheduling: "keep"} }, false},
		{"no trial route", func(r *ScheduledTestProtectionRule) {
			r.OnFail = &ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign"}
		}, false},
		{"voting", func(r *ScheduledTestProtectionRule) { r.Vote = &ScheduledTestVoteConfig{Enabled: true} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := cacheRecoveryRule()
			tc.mutate(&rule)
			err := validateScheduledTestCacheRecovery(&rule)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	rule := cacheRecoveryRule()
	require.Error(t, validateProtectionOutputKind(&rule, "text"))
	require.NoError(t, validateProtectionOutputKind(&rule, "statistics"))
}

func TestScheduledTestCacheRecoveryEvaluation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples int64
		rate    float64
		want    string
	}{
		{"no fresh traffic", 0, .95, "pending"},
		{"insufficient streaming samples", 9, .95, "pending"},
		{"above stop below recovery", 10, .82, "fail"},
		{"recovery boundary", 10, .85, "pass"},
		{"healthy", 20, .95, "pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := cacheRecoveryRule()
			stats := &ScheduledTestStatistics{TotalRequests: 1000, CacheSamples: tc.samples, CacheInputTokens: 1000, CacheRate: &tc.rate}
			verdict, _ := EvaluateScheduledTestCacheRecovery(rule, stats)
			require.Equal(t, tc.want, verdict)
			require.Equal(t, float64(80), rule.Thresholds[0].Value, "evaluation must not mutate saved pause thresholds")
		})
	}
	rule := cacheRecoveryRule()
	rule.Thresholds = append(rule.Thresholds, ScheduledTestThreshold{Metric: "success_rate", Operator: "lt", Value: 90})
	stats := &ScheduledTestStatistics{TotalRequests: 20, CacheSamples: 20, CacheInputTokens: 1000, CacheRate: protectionFloat(.95), SuccessRate: protectionFloat(.5)}
	verdict, _ := EvaluateScheduledTestCacheRecovery(rule, stats)
	require.Equal(t, "fail", verdict, "cache recovery must still respect other rule thresholds")
}

func TestScheduledTestCacheSampleMinimumExcludesOtherRequests(t *testing.T) {
	rule := cacheRecoveryRule()
	rule.MinSamples = 10
	stats := &ScheduledTestStatistics{TotalRequests: 1000, CacheSamples: 1, CacheInputTokens: 100, CacheRate: protectionFloat(.5)}
	verdict, _ := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputKind: "statistics", OutputStatistics: stats})
	require.Equal(t, "pending", verdict, "synchronous or failed requests cannot satisfy the cache sample minimum")
	stats.CacheSamples = 10
	verdict, _ = evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputKind: "statistics", OutputStatistics: stats})
	require.Equal(t, "fail", verdict)
}
