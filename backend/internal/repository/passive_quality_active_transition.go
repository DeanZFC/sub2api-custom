package repository

import (
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// All four targets own hard protection under active recovery. Confirmation
// clocks are independent so alternating cache/speed failures cannot hide each
// other. The displayed hour remains intact after recovery, while fresh traffic
// must establish a new failure episode.
func advanceActiveGroupTarget(p service.PassiveQualityPolicy, s *service.PassiveQualityState, e service.PassiveQualityEvidence, now time.Time) {
	// Recorded customer completions never authorize recovery, including a state
	// written by a retired instance before the migration fenced its leases.
	switch s.Phase {
	case "paused", "probing":
		return
	case "trial", "limited":
		s.Phase, s.Reason, s.NextAt = "paused", "active_recovery_pending: customer_recovery_retired", now
		s.Generation++
		return
	}

	s.CacheAllocationReduced = false
	if s.ActiveConfirmations == nil {
		s.ActiveConfirmations = map[string]service.PassiveQualityConfirmation{}
	}
	decision := e
	if !s.RecoveredAt.IsZero() {
		if e.SinceRecovery == nil {
			s.Phase, s.Reason = "insufficient", "waiting_for_post_recovery_samples"
			return
		}
		decision = *e.SinceRecovery
	}
	if !e.Complete || !decision.Complete {
		s.Phase, s.Reason = "insufficient", "telemetry_incomplete"
		return
	}
	// A burst of confirmed upstream failures must not be diluted by an hour
	// of older successes. No expired or pre-recovery request can trigger it.
	if p.ErrorRate.Enabled && decision.RecentConsecutiveFailures >= 3 && decision.Latest.After(s.LastEvidenceAt) && now.Sub(decision.Latest) <= time.Minute {
		reason := fmt.Sprintf("consecutive_failures: %d within 60s", decision.RecentConsecutiveFailures)
		if p.Mode == "enforce" {
			service.PassiveQualityPause(p.PassiveQualityConfig, s, now, reason)
		} else {
			s.Phase, s.Reason = "observed", reason
		}
		s.LastEvidenceAt = decision.Latest
		return
	}
	gap := time.Duration(min(p.WindowSeconds, 120)) * time.Second
	var breaches, confirmed []string
	pending := false
	updated := false
	for _, m := range []struct {
		name   string
		metric service.PassiveQualityMetric
		fresh  int
		latest time.Time
	}{
		{"error_rate", p.ErrorRate, decision.Fresh, decision.Latest},
		{"ttft_p90", p.TTFT, decision.TTFTFresh, decision.TTFTLatest},
		{"output_tps", p.TPS, decision.TPSFresh, decision.TPSLatest},
		{"cache_rate", p.CacheRate, decision.CacheFresh, decision.CacheLatest},
	} {
		if !m.metric.Enabled {
			delete(s.ActiveConfirmations, m.name)
			continue
		}
		c := activeSingleMetricConfig(p.PassiveQualityConfig, m.name)
		full, reason := service.EvaluatePassiveQuality(c, e, false)
		current, _ := service.EvaluatePassiveQuality(c, decision, false)
		state, exists := s.ActiveConfirmations[m.name]
		if !exists {
			state.Latest = s.LastEvidenceAt
			s.ActiveConfirmations[m.name] = state
		}
		if full == "pending" || current == "pending" {
			pending = true
			continue
		}
		if full != "fail" || current != "fail" {
			state.Since, state.Hits = time.Time{}, 0
			if m.latest.After(state.Latest) {
				state.Latest = m.latest
			}
			s.ActiveConfirmations[m.name] = state
			continue
		}
		breaches = append(breaches, reason)
		if m.fresh < max(1, p.NewSamples) || !m.latest.After(state.Latest) || now.Sub(m.latest) > gap {
			continue
		}
		updated = true
		if !state.Latest.IsZero() && m.latest.Sub(state.Latest) > gap {
			state.Since, state.Hits = time.Time{}, 0
		}
		state.Latest = m.latest
		if !freshGroupTargetBreach(p, decision, m.name) {
			state.Since, state.Hits = time.Time{}, 0
			s.ActiveConfirmations[m.name] = state
			continue
		}
		if state.Since.IsZero() {
			state.Since = m.latest
		}
		state.Hits++
		s.ActiveConfirmations[m.name] = state
		if state.Hits >= max(2, p.Confirmations) && m.latest.Sub(state.Since) >= p.GroupTargetPauseDuration(m.name) {
			confirmed = append(confirmed, reason)
		}
	}
	if updated {
		s.LastEvidenceAt = decision.Latest
	}
	if len(confirmed) > 0 {
		reason := strings.Join(confirmed, "; ")
		if p.Mode == "enforce" {
			service.PassiveQualityPause(p.PassiveQualityConfig, s, now, reason)
		} else {
			s.Phase, s.Reason = "observed", reason
		}
		return
	}
	if len(breaches) > 0 {
		s.Phase, s.Reason = "suspect", strings.Join(breaches, "; ")
	} else if pending {
		s.Phase, s.Reason = "insufficient", "insufficient_metric_samples"
	} else {
		s.Phase, s.Reason = "healthy", "healthy"
		if decision.Latest.After(s.LastEvidenceAt) {
			if s.HealthySince.IsZero() {
				s.HealthySince = decision.Latest
			}
			if decision.Latest.Sub(s.HealthySince) >= 10*time.Minute {
				s.Backoff = 0
			}
			s.LastEvidenceAt = decision.Latest
		}
	}
	s.Hits = 0
	s.SuspicionSince = time.Time{}
	for metric, c := range s.ActiveConfirmations {
		if c.Hits > s.Hits {
			s.Hits, s.SuspicionSince, s.ConfirmationMetric = c.Hits, c.Since, metric
		}
	}
	if len(breaches) > 0 || pending {
		s.HealthySince = time.Time{}
	}
}

func activeSingleMetricConfig(c service.PassiveQualityConfig, metric string) service.PassiveQualityConfig {
	c.ErrorRate.Enabled = c.ErrorRate.Enabled && metric == "error_rate"
	c.CacheRate.Enabled = c.CacheRate.Enabled && metric == "cache_rate"
	c.TTFT.Enabled = c.TTFT.Enabled && metric == "ttft_p90"
	c.TPS.Enabled = c.TPS.Enabled && metric == "output_tps"
	return c
}
