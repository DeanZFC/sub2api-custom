package service

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Pass-rate governance is independent of legacy outcome actions and passive scores.
type ScheduledTestPassRateConfig struct {
	Enabled              bool    `json:"enabled"`
	WindowSize           int     `json:"window_size"`
	WindowSeconds        int     `json:"window_seconds"`
	PauseBelow           float64 `json:"pause_below"`
	RecoverAtLeast       float64 `json:"recover_at_least"`
	RecoverConsecutive   int     `json:"recover_consecutive"`
	PauseOnSingleFailure bool    `json:"pause_on_single_failure"`
}

type ScheduledTestPassRateSummary struct {
	Samples            int        `json:"samples"`
	Passed             int        `json:"passed"`
	PassRate           *float64   `json:"pass_rate"`
	WindowSize         int        `json:"window_size"`
	WindowSeconds      int        `json:"window_seconds"`
	PauseBelow         float64    `json:"pause_below"`
	RecoverAtLeast     float64    `json:"recover_at_least"`
	RecoverConsecutive int        `json:"recover_consecutive"`
	ConsecutivePasses  int        `json:"consecutive_passes"`
	Blocked            bool       `json:"blocked"`
	Ready              bool       `json:"ready"`
	Stale              bool       `json:"stale"`
	Reason             string     `json:"reason"`
	LatestAt           *time.Time `json:"latest_at,omitempty"`
	LatestVerdict      string     `json:"latest_verdict"`
	RuleVersion        string     `json:"rule_version"`
	Model              string     `json:"model"`
}

type ScheduledTestPassRateObservation struct {
	Verdict   string
	StartedAt time.Time
}

type scheduledTestPassRateSnapshotKey struct{}

// Carry the actual execution inputs across the persistence boundary so an
// edited definition cannot label an older prompt as evidence for a new rule.
type ScheduledTestPassRateSnapshot struct {
	Prompt          string
	Kind            string
	MaxOutputTokens int
}

func WithScheduledTestPassRateSnapshot(ctx context.Context, prompt, kind string, maxOutputTokens int) context.Context {
	return context.WithValue(ctx, scheduledTestPassRateSnapshotKey{}, ScheduledTestPassRateSnapshot{prompt, kind, maxOutputTokens})
}

func ScheduledTestPassRateSnapshotFromContext(ctx context.Context) (ScheduledTestPassRateSnapshot, bool) {
	v, ok := ctx.Value(scheduledTestPassRateSnapshotKey{}).(ScheduledTestPassRateSnapshot)
	return v, ok
}

// Execution/transport failure and latency are not intelligence observations.
// Their existing operational protection actions remain independent.
func EvaluateScheduledTestPassRateVerdict(rule ScheduledTestProtectionRule, result *ScheduledTestResult) (string, string) {
	if result != nil && result.Status == "failed" && result.ExecutionInfo != nil && result.ExecutionInfo.OutputContractFailed && (result.OutputKind == "number" || result.OutputKind == "html") {
		return "fail", "检测回答格式不符合要求"
	}
	if result == nil || result.Status != "success" && result.Status != "passed" || result.OutputKind == "statistics" {
		return "pending", "检测未完成"
	}
	if result.OutputKind == "model_check" {
		copy := *result
		normalizeScheduledTestModelCheck(&copy)
		result = &copy
	}
	rule.PauseOnFailure = false
	rule.OnFail = nil
	thresholds := make([]ScheduledTestThreshold, 0, len(rule.Thresholds))
	for _, threshold := range rule.Thresholds {
		if threshold.Metric != "latency_ms" {
			thresholds = append(thresholds, threshold)
		}
	}
	rule.Thresholds = thresholds
	return evaluateScheduledTestProtection(rule, result)
}

func (c *ScheduledTestPassRateConfig) Normalize() {
	if c.WindowSize == 0 {
		c.WindowSize = 10
	}
	if c.WindowSeconds == 0 {
		c.WindowSeconds = 7 * 24 * 3600
	}
	if c.RecoverConsecutive == 0 {
		c.RecoverConsecutive = 2
	}
}

func (c *ScheduledTestPassRateConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	c.Normalize()
	if c.WindowSize < 1 || c.WindowSize > 1000 {
		return fmt.Errorf("pass_rate window_size must be between 1 and 1000")
	}
	if c.WindowSeconds < 3600 || c.WindowSeconds > 30*24*3600 {
		return fmt.Errorf("pass_rate window_seconds must be between 3600 and 2592000")
	}
	if math.IsNaN(c.PauseBelow) || math.IsInf(c.PauseBelow, 0) || c.PauseBelow < 0 || c.PauseBelow > 100 || math.IsNaN(c.RecoverAtLeast) || math.IsInf(c.RecoverAtLeast, 0) || c.RecoverAtLeast < c.PauseBelow || c.RecoverAtLeast > 100 {
		return fmt.Errorf("pass_rate thresholds must satisfy 0 <= pause_below <= recover_at_least <= 100")
	}
	if c.RecoverConsecutive < 1 || c.RecoverConsecutive > c.WindowSize {
		return fmt.Errorf("pass_rate recover_consecutive must be between 1 and window_size")
	}
	return nil
}

// Observations must be newest first. Expiry or insufficient samples never clears a hold.
func SummarizeScheduledTestPassRate(c ScheduledTestPassRateConfig, observations []ScheduledTestPassRateObservation, wasBlocked bool, now time.Time) ScheduledTestPassRateSummary {
	c.Normalize()
	s := ScheduledTestPassRateSummary{WindowSize: c.WindowSize, WindowSeconds: c.WindowSeconds, PauseBelow: c.PauseBelow, RecoverAtLeast: c.RecoverAtLeast, RecoverConsecutive: c.RecoverConsecutive, Blocked: wasBlocked}
	cutoff := now.Add(-time.Duration(c.WindowSeconds) * time.Second)
	consecutive := true
	for _, o := range observations {
		if s.Samples >= c.WindowSize {
			break
		}
		if o.StartedAt.Before(cutoff) || o.StartedAt.After(now) || (o.Verdict != "pass" && o.Verdict != "fail") {
			continue
		}
		if s.Samples == 0 {
			at := o.StartedAt
			s.LatestAt = &at
			s.LatestVerdict = o.Verdict
		}
		s.Samples++
		if o.Verdict == "pass" {
			s.Passed++
			if consecutive {
				s.ConsecutivePasses++
			}
		} else {
			consecutive = false
		}
	}
	s.Ready = s.Samples == c.WindowSize
	s.Stale = s.Samples == 0
	if s.Samples > 0 {
		rate := float64(s.Passed) * 100 / float64(s.Samples)
		s.PassRate = &rate
	}
	if s.Ready && float64(s.Passed)*100 < c.PauseBelow*float64(s.Samples) || c.PauseOnSingleFailure && s.LatestVerdict == "fail" {
		s.Blocked = true
	}
	if wasBlocked && s.Ready && float64(s.Passed)*100 >= c.RecoverAtLeast*float64(s.Samples) && s.ConsecutivePasses >= c.RecoverConsecutive {
		s.Blocked = false
	}
	switch {
	case s.Stale:
		s.Reason = "stale_evidence"
	case !wasBlocked && s.Blocked && c.PauseOnSingleFailure && s.LatestVerdict == "fail":
		s.Reason = "single_failure"
	case s.Blocked && s.Ready && float64(s.Passed)*100 < c.PauseBelow*float64(s.Samples):
		s.Reason = "pass_rate_below_threshold"
	case s.Blocked:
		s.Reason = "recovery_pending"
	case !s.Ready:
		s.Reason = "insufficient_samples"
	case wasBlocked:
		s.Reason = "recovered"
	default:
		s.Reason = "healthy"
	}
	return s
}
