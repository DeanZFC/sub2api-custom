package service

import (
	"fmt"
	"time"
)

// GroupQualityStability separates short availability failures from slower
// performance changes. A non-nil config preserves explicitly selected zeroes.
type GroupQualityStability struct {
	AvailabilityPauseSeconds int `json:"availability_pause_seconds"`
	PerformancePauseSeconds  int `json:"performance_pause_seconds"`
	RecoverySeconds          int `json:"recovery_seconds"`
	NewSessionPercent        int `json:"new_session_percent"`
}

func (c PassiveQualityConfig) GroupTargetStability() GroupQualityStability {
	if c.Stability != nil {
		return *c.Stability
	}
	return GroupQualityStability{AvailabilityPauseSeconds: 60, PerformancePauseSeconds: 300, RecoverySeconds: 120, NewSessionPercent: 20}
}

func (c PassiveQualityConfig) validateGroupTargetStability() error {
	if c.Stability == nil {
		return nil
	}
	if !c.IsGroupQualityTarget() {
		return fmt.Errorf("stability is only supported for group quality targets")
	}
	s := *c.Stability
	if s.AvailabilityPauseSeconds < 0 || s.AvailabilityPauseSeconds > 3600 || s.PerformancePauseSeconds < 0 || s.PerformancePauseSeconds > 3600 || s.RecoverySeconds < 0 || s.RecoverySeconds > 3600 || s.NewSessionPercent < 1 || s.NewSessionPercent > 100 {
		return fmt.Errorf("stability durations must be 0..3600 seconds and new session percent 1..100")
	}
	return nil
}

func (c PassiveQualityConfig) GroupTargetPauseDuration(metric string) time.Duration {
	s := c.GroupTargetStability()
	seconds := s.PerformancePauseSeconds
	if metric == "error_rate" {
		seconds = s.AvailabilityPauseSeconds
	}
	return time.Duration(seconds) * time.Second
}

// Old policies may have a shorter trial than the new recovery gate. Extend
// that bounded window, preserving the same request and concurrency budgets.
func (c PassiveQualityConfig) GroupTargetTrialDuration() time.Duration {
	seconds := c.TrialSeconds
	if c.IsGroupQualityTarget() {
		seconds = max(seconds, max(c.GroupTargetStability().RecoverySeconds, c.GroupTargetRecoveryConfirmations()*30)+120)
	}
	return time.Duration(seconds) * time.Second
}

// Policies can have a fresh batch larger than their finite trial budget. Keep
// the configured values intact and divide the bounded trial into effective
// recovery confirmations and batches at runtime. Ordinary failure and cache
// recovery continue to use the configured confirmation settings.
func (c PassiveQualityConfig) GroupTargetRecoveryConfirmations() int {
	return min(max(2, c.Confirmations), max(2, c.TrialRequests))
}

func (c PassiveQualityConfig) GroupTargetRecoveryBatchSize() int {
	return min(max(1, c.NewSamples), max(1, c.TrialRequests/c.GroupTargetRecoveryConfirmations()))
}

// Cache use depends on workload and warmup. It can reduce new placement, but
// only availability and speed metrics can interrupt an established account.
func GroupTargetHardConfig(c PassiveQualityConfig) PassiveQualityConfig {
	c.CacheRate.Enabled = false
	return c
}

// Fresh recovery needs every enabled metric in the new batch, in addition to
// the full recovery window. Missing counters and repeated windows cannot pass.
func EvaluateGroupTargetFreshRecovery(c PassiveQualityConfig, e PassiveQualityEvidence) (string, string) {
	c.RecoverySamples = max(1, c.NewSamples)
	fresh := PassiveQualityEvidence{
		Complete: e.Complete, Samples: e.Fresh, ErrorRate: e.FreshErrorRate,
		CacheSamples: e.CacheFresh, CacheRate: e.FreshCacheRate,
		TTFTSamples: e.TTFTFresh, TTFT: e.FreshTTFT,
		TPSSamples: e.TPSFresh, OutputTPS: e.FreshOutputTPS,
	}
	return EvaluatePassiveQuality(c, fresh, true)
}
