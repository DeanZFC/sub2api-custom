package service

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// Recovery is opt-in; existing protection rules retain their current behavior.
type ScheduledTestCacheRecoveryConfig struct {
	Enabled         bool    `json:"enabled"`
	CooldownSeconds int     `json:"cooldown_seconds"`
	TrialSeconds    int     `json:"trial_seconds"`
	MaxRequests     int     `json:"max_requests"`
	MinSamples      int     `json:"min_samples"`
	RecoverRate     float64 `json:"recover_rate"`
}

type ScheduledTestCacheRecoveryRepository interface {
	AdvanceCacheRecovery(context.Context, time.Time) error
	CacheRecoveryWindowStart(context.Context, int64, int64, int64) (*time.Time, error)
}

func validateScheduledTestCacheRecovery(rule *ScheduledTestProtectionRule) error {
	c := rule.Recovery
	if c == nil || !c.Enabled {
		return nil
	}
	if rule.OutcomeAction("fail").Scheduling != "pause" || rule.OutcomeAction("pass").Scheduling != "resume" || (rule.Vote != nil && rule.Vote.Enabled) {
		return fmt.Errorf("cache recovery requires pause on failure, resume on pass and no voting")
	}
	if action := rule.OutcomeAction("fail"); action.GroupMode == "assign" && len(action.GroupIDs) == 0 {
		return fmt.Errorf("cache recovery requires at least one failure group for trial traffic")
	}
	if c.CooldownSeconds < 60 || c.CooldownSeconds > 86400 || c.TrialSeconds < 60 || c.TrialSeconds > 3600 {
		return fmt.Errorf("cache recovery cooldown must be 60..86400 seconds and trial 60..3600 seconds")
	}
	if c.MaxRequests < 1 || c.MaxRequests > 1000 || c.MinSamples < 1 || c.MinSamples > c.MaxRequests {
		return fmt.Errorf("cache recovery max_requests must be 1..1000 and min_samples 1..max_requests")
	}
	if math.IsNaN(c.RecoverRate) || math.IsInf(c.RecoverRate, 0) || c.RecoverRate < 0 || c.RecoverRate > 100 {
		return fmt.Errorf("cache recovery recover_rate must be between 0 and 100")
	}
	found := false
	for _, threshold := range rule.Thresholds {
		if threshold.Metric != "cache_rate" {
			continue
		}
		if threshold.Operator != "lt" || c.RecoverRate < threshold.Value {
			return fmt.Errorf("cache recovery requires cache_rate lt thresholds and recover_rate at least the pause threshold")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("cache recovery requires a cache_rate lt threshold")
	}
	return nil
}

// EvaluateScheduledTestCacheRecovery consumes only a fresh trial snapshot. A
// missing cache observation is inconclusive even if other requests succeeded.
func EvaluateScheduledTestCacheRecovery(rule ScheduledTestProtectionRule, stats *ScheduledTestStatistics) (string, string) {
	c := rule.Recovery
	if c == nil || !c.Enabled || stats == nil || stats.CacheSamples < int64(c.MinSamples) {
		return "pending", "试运行有效缓存样本不足"
	}
	rule.Thresholds = append([]ScheduledTestThreshold(nil), rule.Thresholds...)
	for i := range rule.Thresholds {
		if rule.Thresholds[i].Metric == "cache_rate" {
			rule.Thresholds[i].Value = c.RecoverRate
		}
	}
	// Recovery has its own sample requirement; the normal hourly minimum must
	// not make a bounded trial impossible to complete.
	rule.MinSamples = int64(c.MinSamples)
	return evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputKind: "statistics", OutputStatistics: stats})
}

func (s *ScheduledTestRunnerService) advanceCacheRecovery(ctx context.Context, now time.Time) {
	if s.scheduledSvc == nil {
		return
	}
	repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestCacheRecoveryRepository)
	if !ok {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	defer cancel()
	if err := repo.AdvanceCacheRecovery(queryCtx, now); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] cache recovery sweep: %v", err)
	}
}
