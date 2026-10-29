package repository

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Shared by live sweeps and recorded-traffic replay. Probes remain external.
func advancePassiveObserved(p service.PassiveQualityPolicy, s *service.PassiveQualityState, e service.PassiveQualityEvidence, now time.Time) {
	s.Evidence = e
	if e.ProbeModel != "" {
		s.ProbeModel = e.ProbeModel
	}
	if p.IsGroupQualityTarget() {
		if p.IsActiveRecovery() {
			advanceActiveGroupTarget(p, s, e, now)
			return
		}
		advanceGroupTargetStable(p, s, e, now)
		return
	}
	trial := s.Phase == "trial"
	limited := s.Phase == "limited"
	verdict, reason := service.EvaluatePassiveQuality(p.PassiveQualityConfig, e, trial || limited)
	if p.IsGroupQualityTarget() && !trial && !limited {
		if !s.LastEvidenceAt.IsZero() && now.Sub(s.LastEvidenceAt) > time.Duration(p.WindowSeconds)*time.Second {
			s.Hits = 0
		}
		if verdict == "pending" {
			s.Phase, s.Reason, s.Hits = "insufficient", reason, 0
		}
	}
	fresh := e.Fresh
	if p.IsGroupQualityTarget() && verdict == "fail" {
		if strings.HasPrefix(reason, "cache_rate:") {
			fresh = e.CacheFresh
		}
		if strings.HasPrefix(reason, "output_tps:") {
			fresh = e.TPSFresh
		}
		if strings.HasPrefix(reason, "ttft_p90:") {
			fresh = e.TTFTFresh
		}
	}
	if limited {
		// Keep serving bounded traffic while checking the healthier recovery
		// thresholds. Re-reading the same window cannot advance recovery.
		if fresh >= p.NewSamples {
			s.LastEvidenceAt = e.Latest
			if verdict == "pass" {
				s.Hits++
			} else {
				s.Hits = 0
			}
			if s.Hits >= p.Confirmations {
				s.Phase, s.Reason = "healthy", "recovered"
				s.Hits = 0
				s.Generation++
			} else {
				s.Reason = "waiting_for_recovery: " + reason
			}
		}
	} else if trial {
		if verdict == "pass" && e.Latest.After(s.TrialStartedAt) {
			s.Phase = "healthy"
			s.Reason = "recovered"
			s.Backoff = 0
			s.Generation++
			s.Hits = 0
			// Preserve late completions from the successful trial. Resetting
			// to recovery time would hide failures still in flight.
			s.ObserveAfter = s.TrialStartedAt
			s.LastEvidenceAt = e.Latest
		} else if !now.Before(s.TrialEndsAt) {
			if p.IsGroupQualityTarget() && verdict == "pending" {
				waitPassiveGroupTargetTrial(p, s, now, reason)
			} else {
				service.PassiveQualityPause(p.PassiveQualityConfig, s, now, "trial_expired: "+reason)
			}
		} else if verdict == "fail" {
			c := p.PassiveQualityConfig
			c.MinSamples = c.RecoverySamples
			if bad, _ := service.EvaluatePassiveQuality(c, e, false); bad == "fail" {
				service.PassiveQualityPause(c, s, now, "trial_failed: "+reason)
			} else {
				s.Reason = "waiting_for_recovery: " + reason
			}
		}
	} else if fresh >= p.NewSamples {
		s.LastEvidenceAt = e.Latest
		if verdict == "fail" {
			if p.IsGroupQualityTarget() {
				metric, _, _ := strings.Cut(reason, ":")
				sameMetric := s.ConfirmationMetric == metric
				if !sameMetric {
					s.Hits = 0
				}
				// The initial full window supplies the first suspicion. Later
				// confirmations must contain a new breach of the same metric;
				// two healthy arrivals cannot reconfirm an old five-request spike.
				if sameMetric && !freshGroupTargetBreach(p, e, metric) {
					s.Hits = 0
					s.Phase, s.Reason = "suspect", "waiting_for_fresh_failure: "+reason
					return
				}
				s.ConfirmationMetric = metric
			}
			s.Hits++
			s.Phase = "suspect"
			s.Reason = reason
			if s.Hits >= p.Confirmations {
				if p.Mode == "enforce" {
					if p.Action == "limit" {
						s.Phase, s.Reason, s.Hits = "limited", reason, 0
						s.Generation++
					} else {
						service.PassiveQualityPause(p.PassiveQualityConfig, s, now, reason)
					}
				} else {
					s.Phase = "observed"
				}
			}
		} else {
			s.Hits = 0
			s.ConfirmationMetric = ""
			s.Reason = reason
			if verdict == "pass" {
				s.Phase = "healthy"
			} else {
				s.Phase = "insufficient"
			}
		}
	} else if e.Samples < p.MinSamples {
		s.Phase = "insufficient"
		s.Reason = "insufficient_samples"
		s.Hits = 0
	}
}

// Time gates complement fresh batches. A traffic gap breaks continuity, and an
// unchanged rolling window can never move either the pause or recovery clock.
func advanceGroupTargetStable(p service.PassiveQualityPolicy, s *service.PassiveQualityState, e service.PassiveQualityEvidence, now time.Time) {
	gap := passiveGroupTargetEvidenceGap(p.PassiveQualityConfig, s.Phase == "trial")
	softCache := s.CacheAllocationReduced || s.Phase == "suspect" && s.ConfirmationMetric == "cache_rate" && s.Hits > 0
	s.CacheAllocationReduced = softCache
	verdict, reason := evaluateGroupTargetPriority(p.PassiveQualityConfig, e)
	metric, _, _ := strings.Cut(reason, ":")
	evidenceAt := passiveGroupTargetMetricAt(e, metric)
	if softCache && verdict != "fail" {
		evidenceAt = passiveGroupTargetMetricAt(e, "cache_rate")
	}
	if s.Phase == "trial" {
		evidenceAt = passiveGroupTargetRecoveryAt(service.GroupTargetHardConfig(p.PassiveQualityConfig), e)
	}
	previous := s.StabilityEvidenceAt
	if previous.IsZero() {
		previous = s.LastEvidenceAt
	}
	latest := now
	if evidenceAt.After(previous) {
		// Compare actual evidence times before wall time: a delayed sweep must
		// not discard a fresh batch that arrived within the allowed gap.
		latest = evidenceAt
	}
	if !previous.IsZero() && latest.Sub(previous) > gap {
		s.SuspicionSince, s.RecoverySince, s.HealthySince = time.Time{}, time.Time{}, time.Time{}
		s.Hits, s.RecoveryHits = 0, 0
		if softCache {
			s.Hits = 1
		}
	}
	if s.Phase == "trial" {
		advanceGroupTargetStableTrial(p, s, e, now)
		return
	}
	if softCache && verdict != "fail" {
		if s.ConfirmationMetric != "cache_rate" && e.Latest.After(s.LastEvidenceAt) && freshGroupTargetMetricHealthy(p, e, s.ConfirmationMetric) {
			// Cache hysteresis is independent of the prior hard suspicion. Fresh
			// healthy speed/availability must break its failure clock even while
			// cache remains below the higher recovery threshold.
			s.Hits, s.SuspicionSince, s.ConfirmationMetric = 1, time.Time{}, "cache_rate"
			s.Phase = "suspect"
		}
		if !advanceGroupTargetCacheRecovery(p, s, e, now, gap) {
			return
		}
		verdict, reason = evaluateGroupTargetPriority(p.PassiveQualityConfig, e)
		if verdict == "pass" {
			reason = "cache_allocation_recovered"
		}
	}
	if verdict == "pending" {
		s.Phase, s.Reason, s.Hits = "insufficient", reason, 0
		s.SuspicionSince, s.HealthySince = time.Time{}, time.Time{}
		s.ConfirmationMetric = ""
		return
	}
	fresh := e.Fresh
	if verdict == "fail" {
		switch metric {
		case "cache_rate":
			fresh = e.CacheFresh
		case "ttft_p90":
			fresh = e.TTFTFresh
		case "output_tps":
			fresh = e.TPSFresh
		}
	}
	if fresh < p.NewSamples || !e.Latest.After(s.LastEvidenceAt) || now.Sub(evidenceAt) > gap {
		return
	}
	s.LastEvidenceAt, s.StabilityEvidenceAt = e.Latest, evidenceAt
	if verdict == "fail" {
		s.HealthySince = time.Time{}
		s.RecoverySince, s.RecoveryHits = time.Time{}, 0
		if metric == "cache_rate" {
			s.CacheAllocationReduced = true
		}
		sameMetric := s.ConfirmationMetric == metric
		if !sameMetric {
			s.Hits, s.SuspicionSince = 0, time.Time{}
		}
		s.ConfirmationMetric = metric
		if sameMetric && !freshGroupTargetBreach(p, e, metric) {
			s.Hits, s.SuspicionSince = 0, time.Time{}
			if metric == "cache_rate" && softCache {
				s.Hits = 1
			}
			s.Phase, s.Reason = "suspect", "waiting_for_fresh_failure: "+reason
			return
		}
		if s.SuspicionSince.IsZero() {
			s.SuspicionSince = evidenceAt
		}
		s.Hits++
		s.Phase, s.Reason = "suspect", reason
		if s.Hits >= p.Confirmations && evidenceAt.Sub(s.SuspicionSince) >= p.GroupTargetPauseDuration(metric) {
			if metric == "cache_rate" {
				// Cache-only degradation keeps the owner and reduces new
				// allocation; rotating cold sessions would worsen the signal.
				s.Reason = "cache_allocation_reduced: " + reason
			} else if p.Mode == "enforce" {
				service.PassiveQualityPause(p.PassiveQualityConfig, s, now, reason)
			} else {
				s.Phase = "observed"
			}
		}
		return
	}
	s.Phase, s.Reason, s.Hits = "healthy", reason, 0
	s.SuspicionSince, s.ConfirmationMetric = time.Time{}, ""
	// A short successful trial must not erase repeated-failure backoff. Reset
	// it only after ten minutes of fresh, healthy production evidence.
	hard := service.GroupTargetHardConfig(p.PassiveQualityConfig)
	full, _ := service.EvaluatePassiveQuality(hard, e, true)
	current, _ := service.EvaluateGroupTargetFreshRecovery(hard, e)
	if full != "pass" || current != "pass" {
		s.HealthySince = time.Time{}
		return
	}
	if s.HealthySince.IsZero() {
		s.HealthySince = passiveGroupTargetRecoveryAt(hard, e)
	}
	if passiveGroupTargetRecoveryAt(hard, e).Sub(s.HealthySince) >= 10*time.Minute {
		s.Backoff = 0
	}
}

func advanceGroupTargetStableTrial(p service.PassiveQualityPolicy, s *service.PassiveQualityState, e service.PassiveQualityEvidence, now time.Time) {
	batch := p.GroupTargetRecoveryBatchSize()
	if e.Fresh >= batch && e.Latest.After(s.LastTrialObservedAt) {
		// Admission batches need an observation acknowledgement, independently
		// of whether their metrics qualify for a healthy confirmation.
		s.LastTrialObservedAt = e.Latest
	}
	hard := service.GroupTargetHardConfig(p.PassiveQualityConfig)
	hard.NewSamples = batch
	verdict, reason := service.EvaluatePassiveQuality(hard, e, true)
	fresh, freshReason := service.EvaluateGroupTargetFreshRecovery(hard, e)
	gap := passiveGroupTargetEvidenceGap(p.PassiveQualityConfig, true)
	evidenceAt := passiveGroupTargetRecoveryAt(hard, e)
	newBatch := e.Fresh >= batch && e.Latest.After(s.LastEvidenceAt) && evidenceAt.After(s.TrialStartedAt) && now.Sub(evidenceAt) <= gap
	// The new batch can start the clock while the full minimum is still
	// filling; the full minimum remains mandatory before the hold is released.
	if verdict != "fail" && fresh == "pass" && newBatch {
		if s.RecoverySince.IsZero() {
			s.RecoverySince = evidenceAt
		}
		s.LastEvidenceAt, s.StabilityEvidenceAt = e.Latest, evidenceAt
		s.Hits++
		if verdict == "pass" && s.Hits >= p.GroupTargetRecoveryConfirmations() && evidenceAt.Sub(s.RecoverySince) >= time.Duration(p.GroupTargetStability().RecoverySeconds)*time.Second {
			s.Phase, s.Reason = "healthy", "recovered"
			s.Generation++
			s.Hits = 0
			s.ObserveAfter = s.TrialStartedAt
			s.HealthySince = evidenceAt
			s.RecoverySince, s.SuspicionSince = time.Time{}, time.Time{}
			s.ConfirmationMetric = ""
			current, currentReason := evaluateGroupTargetPriority(p.PassiveQualityConfig, e)
			if p.CacheRate.Enabled && current != "fail" {
				cache := p.PassiveQualityConfig
				cache.ErrorRate.Enabled, cache.TTFT.Enabled, cache.TPS.Enabled = false, false, false
				if cacheVerdict, cacheReason := service.EvaluatePassiveQuality(cache, e, true); cacheVerdict == "fail" {
					current, currentReason = cacheVerdict, cacheReason
				}
			}
			switch current {
			case "pending":
				s.Phase, s.Reason = "insufficient", currentReason
			case "fail":
				s.Phase, s.Reason = "suspect", "cache_allocation_reduced: "+currentReason
				s.Hits, s.ConfirmationMetric = p.Confirmations, "cache_rate"
				s.CacheAllocationReduced = true
				s.SuspicionSince = e.Latest
			}
			return
		}
		s.Reason = "waiting_for_recovery: stability_confirmation"
	} else if verdict == "fail" || !e.Complete || (newBatch && fresh != "pass") {
		s.RecoverySince, s.Hits = time.Time{}, 0
		if newBatch {
			s.LastEvidenceAt, s.StabilityEvidenceAt = e.Latest, evidenceAt
		}
		if verdict == "pass" {
			s.Reason = "waiting_for_recovery: fresh_" + freshReason
		} else {
			s.Reason = "waiting_for_recovery: " + reason
		}
	}
	if !now.Before(s.TrialEndsAt) {
		switch verdict {
		case "pending":
			waitPassiveGroupTargetTrial(p, s, now, reason)
		case "pass":
			waitPassiveGroupTargetTrial(p, s, now, "stability_confirmation")
		default:
			service.PassiveQualityPause(p.PassiveQualityConfig, s, now, "trial_expired: "+reason)
		}
	} else if verdict == "fail" {
		c := hard
		c.MinSamples = c.RecoverySamples
		if bad, _ := service.EvaluatePassiveQuality(c, e, false); bad == "fail" {
			service.PassiveQualityPause(c, s, now, "trial_failed: "+reason)
		}
	}
}

// Once cache has reduced new placement, returning merely above its pause
// threshold cannot reopen allocation. Require the higher recovery threshold
// across fresh batches and time, while keeping existing owners eligible.
func advanceGroupTargetCacheRecovery(p service.PassiveQualityPolicy, s *service.PassiveQualityState, e service.PassiveQualityEvidence, now time.Time, gap time.Duration) bool {
	c := p.PassiveQualityConfig
	c.ErrorRate.Enabled, c.TTFT.Enabled, c.TPS.Enabled = false, false, false
	full, reason := service.EvaluatePassiveQuality(c, e, true)
	fresh, freshReason := service.EvaluateGroupTargetFreshRecovery(c, e)
	evidenceAt := passiveGroupTargetMetricAt(e, "cache_rate")
	newBatch := e.CacheFresh >= p.NewSamples && e.Latest.After(s.LastEvidenceAt) && now.Sub(evidenceAt) <= gap
	if full != "pass" || (newBatch && fresh != "pass") {
		s.RecoverySince, s.RecoveryHits = time.Time{}, 0
		if full == "pass" {
			reason = "fresh_" + freshReason
		}
		s.Reason = "waiting_for_recovery: " + reason
	} else if newBatch {
		if s.RecoverySince.IsZero() {
			s.RecoverySince = evidenceAt
		}
		s.RecoveryHits++
		s.Reason = "waiting_for_recovery: stability_confirmation"
		if s.RecoveryHits >= max(2, p.Confirmations) && evidenceAt.Sub(s.RecoverySince) >= time.Duration(p.GroupTargetStability().RecoverySeconds)*time.Second {
			s.RecoverySince, s.RecoveryHits = time.Time{}, 0
			s.CacheAllocationReduced = false
			return true
		}
	}
	if newBatch {
		s.LastEvidenceAt, s.StabilityEvidenceAt = e.Latest, evidenceAt
	}
	return false
}

func passiveGroupTargetMetricAt(e service.PassiveQualityEvidence, metric string) time.Time {
	at := e.Latest
	switch metric {
	case "cache_rate":
		at = e.CacheLatest
	case "ttft_p90":
		at = e.TTFTLatest
	case "output_tps":
		at = e.TPSLatest
	}
	// Older serialized evidence and direct callers may not carry per-metric
	// timestamps yet; freshly summarized production evidence always does.
	if at.IsZero() {
		return e.Latest
	}
	return at
}

func passiveGroupTargetRecoveryAt(c service.PassiveQualityConfig, e service.PassiveQualityEvidence) time.Time {
	at := e.Latest
	for _, metric := range []struct {
		enabled bool
		name    string
	}{{c.ErrorRate.Enabled, "error_rate"}, {c.TTFT.Enabled, "ttft_p90"}, {c.TPS.Enabled, "output_tps"}, {c.CacheRate.Enabled, "cache_rate"}} {
		if current := passiveGroupTargetMetricAt(e, metric.name); metric.enabled && current.Before(at) {
			at = current
		}
	}
	return at
}

func evaluateGroupTargetPriority(c service.PassiveQualityConfig, e service.PassiveQualityEvidence) (string, string) {
	if verdict, reason := service.EvaluatePassiveQuality(service.GroupTargetHardConfig(c), e, false); verdict == "fail" {
		return verdict, reason
	}
	return service.EvaluatePassiveQuality(c, e, false)
}

func freshGroupTargetBreach(p service.PassiveQualityPolicy, e service.PassiveQualityEvidence, metric string) bool {
	switch metric {
	case "error_rate":
		return e.FreshErrorRate != nil && *e.FreshErrorRate > p.ErrorRate.Pause
	case "output_tps":
		return e.FreshOutputTPS != nil && *e.FreshOutputTPS < p.TPS.Pause
	case "ttft_p90":
		return e.FreshTTFT != nil && *e.FreshTTFT > p.TTFT.Pause
	case "cache_rate":
		return e.FreshCacheRate != nil && *e.FreshCacheRate < p.CacheRate.Pause
	}
	return false
}

func freshGroupTargetMetricHealthy(p service.PassiveQualityPolicy, e service.PassiveQualityEvidence, metric string) bool {
	if !e.Complete {
		return false
	}
	switch metric {
	case "error_rate":
		return e.Fresh >= p.NewSamples && e.FreshErrorRate != nil && *e.FreshErrorRate <= p.ErrorRate.Pause
	case "ttft_p90":
		return e.TTFTFresh >= p.NewSamples && e.FreshTTFT != nil && *e.FreshTTFT <= p.TTFT.Pause
	case "output_tps":
		return e.TPSFresh >= p.NewSamples && e.FreshOutputTPS != nil && *e.FreshOutputTPS >= p.TPS.Pause
	}
	return false
}
