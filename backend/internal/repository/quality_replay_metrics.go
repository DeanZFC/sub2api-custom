package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Recorded-traffic replay shares the real metric helpers and evidence queries.
// It cannot infer counterfactual routing, probe outcomes, or a historical pool.
func replayPassiveMetrics(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s *service.PassiveQualityState, at, now time.Time) (service.QualityReplayPoint, int, bool, error) {
	point := service.QualityReplayPoint{At: at, AccountID: s.AccountID, Model: s.Model, Verdict: "pass", Reason: "healthy"}
	events := []string{}
	triggers := 0
	missing := false
	merge := func(v, r string) {
		if v == "fail" || (v == "pending" && point.Verdict != "fail") {
			point.Verdict, point.Reason = v, r
		}
	}
	evidence, err := passiveSamples(ctx, tx, p, *s, at)
	if err != nil {
		return point, 0, false, err
	}
	if at.Add(-time.Duration(p.WindowSeconds) * time.Second).Before(now.Add(-48 * time.Hour)) {
		evidence.Complete = false
	}
	point.Evidence = evidence
	if p.ErrorRate.Enabled || p.ConsecutiveFailures > 0 || p.IsGroupQualityTarget() {
		availability := p
		availability.PassiveQualityConfig = service.PassiveQualityMetricConfig(p.PassiveQualityConfig, "availability")
		v, r := service.EvaluatePassiveQuality(availability.PassiveQualityConfig, evidence, s.Phase == "limited")
		merge(v, r)
		old := s.Phase
		if p.Enabled && s.Phase != "paused" {
			advancePassiveObserved(availability, s, evidence, at)
		}
		if s.Phase != old {
			switch s.Phase {
			case "paused":
				events = append(events, "would_pause")
				triggers++
			case "limited":
				events = append(events, "would_limit")
				triggers++
			case "observed":
				events = append(events, "would_observe")
				triggers++
			case "healthy":
				if old == "limited" {
					events = append(events, "would_resume")
				}
			}
		}
		if s.Phase == "paused" && len(events) == 0 {
			if p.IsActiveRecovery() {
				events = append(events, "recovery_requires_probe")
			} else if p.IsGroupQualityTarget() {
				events = append(events, "recovery_requires_real_traffic")
			} else {
				events = append(events, "recovery_requires_probe")
			}
		}
	}
	if p.TTFT.Enabled && !p.IsGroupQualityTarget() {
		if s.Latency == nil {
			s.Latency = &service.PassiveQualityMetricState{ObserveAfter: p.EvidenceSince()}
		}
		view := *s
		view.Phase = "healthy"
		view.LastEvidenceAt = s.Latency.LastEvidenceAt
		view.ObserveAfter = s.Latency.ObserveAfter
		if s.CacheObserveAfter.After(view.ObserveAfter) {
			view.ObserveAfter = s.CacheObserveAfter
		}
		e, err := passiveSamples(ctx, tx, p, view, at)
		if err != nil {
			return point, 0, false, err
		}
		if at.Add(-time.Duration(p.WindowSeconds) * time.Second).Before(now.Add(-48 * time.Hour)) {
			e.Complete = false
		}
		v, r := service.EvaluatePassiveQuality(service.PassiveQualityMetricConfig(p.PassiveQualityConfig, "ttft"), e, s.Latency.Phase == "limited")
		merge(v, r)
		if p.Enabled {
			if event := service.AdvancePassiveQualityLatency(p.PassiveQualityConfig, s.Latency, e, at); event != "" {
				events = append(events, "would_"+event)
				if event == "ttft_limited" || event == "ttft_watch" {
					triggers++
				}
			}
		}
		cp := *s.Latency
		point.Latency = &cp
	}
	if p.TPS.Enabled && !p.IsGroupQualityTarget() {
		if s.Speed == nil {
			s.Speed = &service.PassiveQualityMetricState{ObserveAfter: p.EvidenceSince()}
		}
		view := *s
		view.Phase = "healthy"
		view.LastEvidenceAt = s.Speed.LastEvidenceAt
		view.ObserveAfter = s.Speed.ObserveAfter
		if s.CacheObserveAfter.After(view.ObserveAfter) {
			view.ObserveAfter = s.CacheObserveAfter
		}
		e, err := passiveSamples(ctx, tx, p, view, at)
		if err != nil {
			return point, 0, false, err
		}
		if at.Add(-time.Duration(p.WindowSeconds) * time.Second).Before(now.Add(-48 * time.Hour)) {
			e.Complete = false
		}
		v, r := service.EvaluatePassiveQuality(service.PassiveQualityMetricConfig(p.PassiveQualityConfig, "tps"), e, s.Speed.Phase == "limited")
		merge(v, r)
		if p.Enabled {
			if event := service.AdvancePassiveQualityTPS(p.PassiveQualityConfig, s.Speed, e, at); event != "" {
				events = append(events, "would_"+event)
				if event == "tps_limited" || event == "tps_watch" {
					triggers++
				}
			}
		}
		cp := *s.Speed
		point.Speed = &cp
	}
	if p.CacheRate.Enabled && p.CacheProtection != nil && !p.IsGroupQualityTarget() {
		samples, complete, err := passiveCacheSamples(ctx, tx, p, *s, at)
		if err != nil {
			return point, 0, false, err
		}
		if at.Add(-time.Duration(p.CacheProtection.ActionWindow.WindowSeconds) * time.Second).Before(now.Add(-48 * time.Hour)) {
			complete = false
		}
		if s.Cache == nil {
			s.Cache = map[string]service.PassiveQualityCacheState{}
		}
		windows := service.PassiveQualityCacheWindows(p.PassiveQualityConfig, samples, s.Cache, at, complete)
		if len(windows) == 0 {
			merge("pending", "insufficient_reusable_samples")
		}
		point.Cache = map[string]service.PassiveQualityCacheState{}
		for model, e := range windows {
			cache := s.Cache[model]
			v, r := service.EvaluatePassiveQualityCache(p.PassiveQualityConfig, p.CacheProtection.ActionWindow, e.Action, cache.Phase == "reduced" || cache.Phase == "recovering")
			merge(v, r)
			if e.Action.Reasons["reuse_history_unavailable"] > 0 {
				missing = true
			}
			if p.Enabled {
				if event := service.AdvancePassiveQualityCache(p.PassiveQualityConfig, &cache, e, at, s.Phase != "paused" && s.Phase != "probing" && s.Phase != "trial"); event != "" {
					events = append(events, "would_"+event)
					if event == "cache_reduced" || event == "cache_watch" {
						triggers++
					}
				}
			} else {
				cache.Evidence = e
			}
			s.Cache[model] = cache
			point.Cache[model] = cache
		}
	}
	point.Action = strings.Join(events, ",")
	point.Hits = s.Hits
	return point, triggers, missing, nil
}
