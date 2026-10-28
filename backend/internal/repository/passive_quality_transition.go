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

func freshGroupTargetBreach(p service.PassiveQualityPolicy, e service.PassiveQualityEvidence, metric string) bool {
	switch metric {
	case "error_rate":
		return e.FreshErrorRate != nil && *e.FreshErrorRate >= p.ErrorRate.Pause
	case "output_tps":
		return e.FreshOutputTPS != nil && *e.FreshOutputTPS <= p.TPS.Pause
	case "ttft_p90":
		return e.FreshTTFT != nil && *e.FreshTTFT >= p.TTFT.Pause
	case "cache_rate":
		return e.FreshCacheRate != nil && *e.FreshCacheRate <= p.CacheRate.Pause
	}
	return false
}
