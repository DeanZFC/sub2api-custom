package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// PassiveQualityPoolAccountEligible reuses durable scheduling gates when
// counting the pool reserve. Bounded quality trials and credential-sharing
// shadows are not independent healthy reserve capacity. Runtime load and
// request-only capability checks remain the actual scheduler responsibility.
func PassiveQualityPoolAccountEligible(ctx context.Context, a *Account, model string) bool {
	if a == nil || a.ParentAccountID != nil || !a.IsModelSupported(model) || !a.IsSchedulableForModelWithContext(ctx, model) {
		return false
	}
	if trial, _ := a.Extra["quality_protection_trial"].(bool); trial {
		return false
	}
	if a.IsOpenAI() {
		now := time.Now()
		limit5h, limit7d := resolveOpenAIQuotaAutoPauseThresholds(ctx, a)
		config := ResolveOpenAIAutoResetCreditConfig(a)
		for _, window := range []struct {
			name           string
			threshold      float64
			disabled       bool
			resetThreshold float64
		}{
			{"5h", limit5h, resolveAccountExtraBool(a.Extra, "auto_pause_5h_disabled"), config.Threshold5h},
			{"7d", limit7d, resolveAccountExtraBool(a.Extra, "auto_pause_7d_disabled"), config.Threshold7d},
		} {
			utilization, known := resolveOpenAIQuotaUtilization(a.Extra, window.name, now)
			if !known {
				continue
			}
			if config.Enabled && window.resetThreshold > 0 && utilization >= window.resetThreshold {
				return false
			}
			if !window.disabled && window.threshold > 0 && utilization >= window.threshold {
				if config.Enabled {
					state := openAIAutoResetStateFromExtra(a.Extra)
					if state != nil && state.Status == OpenAIAutoResetStatusAvailable && state.AvailableCount > 0 && !openAIAutoResetStateStale(state, now) {
						continue
					}
				}
				return false
			}
		}
	}
	return true
}

// Cache protection is opt-in. Policies without schema_version=2 retain their
// original shared metric/action semantics until explicitly upgraded.
type PassiveQualityCacheWindow struct {
	WindowSeconds          int   `json:"window_seconds"`
	MaxSamples             int   `json:"max_samples"`
	MinSamples             int   `json:"min_samples"`
	Confirmations          int   `json:"confirmations"`
	NewSamples             int   `json:"new_samples"`
	MinTokens              int64 `json:"min_tokens"`
	MinSessions            int   `json:"min_sessions"`
	MaxSessionSharePercent int   `json:"max_session_share_percent"`
}

type PassiveQualityCacheConfig struct {
	Action                string                    `json:"action"`
	Quick                 PassiveQualityCacheWindow `json:"quick"`
	ActionWindow          PassiveQualityCacheWindow `json:"action_window"`
	AllocationPercent     int                       `json:"allocation_percent"`
	RecoveryStepPercent   int                       `json:"recovery_step_percent"`
	RecoveryConfirmations int                       `json:"recovery_confirmations"`
	MinHoldSeconds        int                       `json:"min_hold_seconds"`
	RecoveryHoldSeconds   int                       `json:"recovery_hold_seconds"`
	MaxReducedPercent     int                       `json:"max_reduced_percent"`
	MinServingAccounts    int                       `json:"min_serving_accounts"`
	ReuseWindowSeconds    int                       `json:"reuse_window_seconds"`
	WarmupRequests        int                       `json:"warmup_requests"`
}

type PassiveQualityMetricState struct {
	Phase          string                 `json:"phase"`
	Reason         string                 `json:"reason"`
	Hits           int                    `json:"hits"`
	RecoveryHits   int                    `json:"recovery_hits"`
	LastEvidenceAt time.Time              `json:"last_evidence_at"`
	ObserveAfter   time.Time              `json:"observe_after"`
	HoldUntil      time.Time              `json:"hold_until"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Evidence       PassiveQualityEvidence `json:"evidence"`
}

type PassiveQualityCacheEvidence struct {
	TotalSamples            int            `json:"total_samples"`
	FailedSamples           int            `json:"failed_samples"`
	ReportedSamples         int            `json:"reported_samples"`
	ReusableSamples         int            `json:"reusable_samples"`
	ColdSamples             int            `json:"cold_samples"`
	UnknownSamples          int            `json:"unknown_samples"`
	MissingTelemetrySamples int            `json:"missing_telemetry_samples"`
	AccountSwitches         int            `json:"account_switches"`
	TTFTP90                 *float64       `json:"ttft_p90"`
	TTFTSamples             int            `json:"ttft_samples"`
	InputCost               *float64       `json:"input_cost"`
	InputCostSamples        int            `json:"input_cost_samples"`
	CacheRate               *float64       `json:"cache_rate"`
	ProtectionRate          *float64       `json:"protection_rate"`
	ReadTokens              int64          `json:"read_tokens"`
	InputTokens             int64          `json:"input_tokens"`
	EligibleReadTokens      int64          `json:"eligible_read_tokens"`
	EligibleInputTokens     int64          `json:"eligible_input_tokens"`
	Sessions                int            `json:"sessions"`
	MaxSessionSharePercent  float64        `json:"max_session_share_percent"`
	BreachedSessions        int            `json:"breached_sessions"`
	HealthySessions         int            `json:"healthy_sessions"`
	Fresh                   int            `json:"fresh"`
	Latest                  time.Time      `json:"latest"`
	LatestKeys              []string       `json:"latest_keys,omitempty"`
	Complete                bool           `json:"complete"`
	WindowSeconds           int            `json:"window_seconds"`
	MaxSamples              int            `json:"max_samples"`
	Reasons                 map[string]int `json:"reasons,omitempty"`
}

type PassiveQualityCacheWindowsEvidence struct {
	Model  string                      `json:"model"`
	Quick  PassiveQualityCacheEvidence `json:"quick"`
	Action PassiveQualityCacheEvidence `json:"action"`
}

type PassiveQualityCacheState struct {
	Phase             string                             `json:"phase"`
	Reason            string                             `json:"reason"`
	Hits              int                                `json:"hits"`
	QuickHits         int                                `json:"quick_hits"`
	RecoveryHits      int                                `json:"recovery_hits"`
	AllocationPercent int                                `json:"allocation_percent"`
	LastEvidenceAt    time.Time                          `json:"last_evidence_at"`
	QuickEvidenceAt   time.Time                          `json:"quick_evidence_at"`
	LastEvidenceKeys  []string                           `json:"last_evidence_keys,omitempty"`
	QuickEvidenceKeys []string                           `json:"quick_evidence_keys,omitempty"`
	ObserveAfter      time.Time                          `json:"observe_after"`
	HoldUntil         time.Time                          `json:"hold_until"`
	UpdatedAt         time.Time                          `json:"updated_at"`
	Evidence          PassiveQualityCacheWindowsEvidence `json:"evidence"`
}

func DefaultPassiveQualityCacheConfig() PassiveQualityCacheConfig {
	return PassiveQualityCacheConfig{
		Action:            "reduce",
		Quick:             PassiveQualityCacheWindow{300, 50, 15, 2, 3, 32768, 3, 60},
		ActionWindow:      PassiveQualityCacheWindow{900, 200, 30, 3, 5, 65536, 3, 60},
		AllocationPercent: 25, RecoveryStepPercent: 25, RecoveryConfirmations: 2,
		MinHoldSeconds: 300, RecoveryHoldSeconds: 300, MaxReducedPercent: 50,
		MinServingAccounts: 1, ReuseWindowSeconds: 300, WarmupRequests: 1,
	}
}

func (c PassiveQualityConfig) IsolatedMetrics() bool { return c.SchemaVersion == 2 }

// PerModel includes automatic discovery when the optional model filter is empty.
func (c PassiveQualityConfig) PerModel() bool { return c.IsolatedMetrics() && c.Scope == "model" }

func (c *PassiveQualityConfig) validateIsolatedMetrics() error {
	if c.SchemaVersion < 0 || c.SchemaVersion > 2 {
		return fmt.Errorf("schema_version must be 0, 1 or 2")
	}
	if !c.IsolatedMetrics() {
		if c.CacheProtection != nil || c.TTFTAction != "" {
			return fmt.Errorf("independent metric configuration requires schema_version 2")
		}
		return nil
	}
	if c.TTFTAction == "" {
		c.TTFTAction = "limit"
		if c.TTFTPriorityPenalty == 0 {
			c.TTFTPriorityPenalty = 50
		}
	}
	if c.TTFTConcurrencyLimit == 0 {
		c.TTFTConcurrencyLimit = 2
	}
	if c.TTFTAction != "observe" && c.TTFTAction != "limit" {
		return fmt.Errorf("ttft_action must be observe or limit")
	}
	if c.TTFTConcurrencyLimit < 1 || c.TTFTConcurrencyLimit > 1000 || c.TTFTPriorityPenalty < 0 || c.TTFTPriorityPenalty > 1000 {
		return fmt.Errorf("invalid TTFT concurrency limit or priority penalty")
	}
	if c.CacheProtection == nil {
		d := DefaultPassiveQualityCacheConfig()
		c.CacheProtection = &d
	}
	p := c.CacheProtection
	if p.Action != "observe" && p.Action != "reduce" {
		return fmt.Errorf("cache action must be observe or reduce")
	}
	for _, w := range []PassiveQualityCacheWindow{p.Quick, p.ActionWindow} {
		if w.WindowSeconds < 60 || w.WindowSeconds > 86400 || w.MaxSamples < 5 || w.MaxSamples > 2000 || w.MinSamples < 5 || w.MinSamples > w.MaxSamples || w.Confirmations < 1 || w.Confirmations > 10 || w.NewSamples < 1 || w.NewSamples > w.MaxSamples || w.MinTokens < 1 || w.MinTokens > 1000000000 || w.MinSessions < 2 || w.MinSessions*2 > w.MinSamples || w.MaxSessionSharePercent < 10 || w.MaxSessionSharePercent > 90 || w.MaxSessionSharePercent*w.MinSessions < 100 {
			return fmt.Errorf("invalid cache window, token floor or independent-session evidence limits")
		}
	}
	if p.ActionWindow.WindowSeconds < p.Quick.WindowSeconds || p.ActionWindow.MaxSamples < p.Quick.MaxSamples || p.ActionWindow.MinSamples < p.Quick.MinSamples || p.ActionWindow.Confirmations < p.Quick.Confirmations || p.ActionWindow.NewSamples < p.Quick.NewSamples || p.ActionWindow.MinTokens < p.Quick.MinTokens || p.ActionWindow.MinSessions < p.Quick.MinSessions {
		return fmt.Errorf("cache action window must be at least as conservative as quick observation")
	}
	if p.AllocationPercent < 1 || p.AllocationPercent > 99 || p.RecoveryStepPercent < 1 || p.RecoveryStepPercent > 99 || p.RecoveryConfirmations < 1 || p.RecoveryConfirmations > 10 || p.MinHoldSeconds < 30 || p.MinHoldSeconds > 86400 || p.RecoveryHoldSeconds < 30 || p.RecoveryHoldSeconds > 86400 || p.MaxReducedPercent < 1 || p.MaxReducedPercent > 90 || p.MinServingAccounts < 1 || p.MinServingAccounts > 10000 || p.ReuseWindowSeconds < 30 || p.ReuseWindowSeconds > 3600 || p.WarmupRequests < 1 || p.WarmupRequests > 20 {
		return fmt.Errorf("invalid cache allocation, recovery, pool guard or warmup limits")
	}
	return nil
}

// PassiveQualityMetricConfig isolates one metric while preserving the legacy
// availability probe/trial settings. Never pass a combined v2 policy to the
// legacy evaluator: a cache evidence gap must not veto availability recovery.
func PassiveQualityMetricConfig(c PassiveQualityConfig, metric string) PassiveQualityConfig {
	switch metric {
	case "availability":
		c.TTFT.Enabled, c.CacheRate.Enabled = false, false
	case "ttft":
		c.ErrorRate.Enabled, c.CacheRate.Enabled, c.ConsecutiveFailures = false, false, 0
		c.Action, c.ConcurrencyLimit, c.PriorityPenalty = "limit", c.TTFTConcurrencyLimit, c.TTFTPriorityPenalty
	}
	return c
}

// CacheWindowEvidence requires pre-response reuse classification and explicit
// telemetry. In particular an explicit zero hit is still valid bad evidence.
// The expected-token denominator is the known reusable prefix, while the
// actual utilization denominator remains all reported input tokens.
func PassiveQualityCacheWindowEvidence(c PassiveQualityConfig, w PassiveQualityCacheWindow, samples []PassiveQualitySample, after, observeAfter, now time.Time, complete bool) PassiveQualityCacheEvidence {
	return passiveQualityCacheWindowEvidence(c, w, samples, after, nil, observeAfter, now, complete)
}

func passiveQualityCacheWindowEvidence(c PassiveQualityConfig, w PassiveQualityCacheWindow, samples []PassiveQualitySample, after time.Time, afterKeys []string, observeAfter, now time.Time, complete bool) PassiveQualityCacheEvidence {
	e := PassiveQualityCacheEvidence{Complete: complete, WindowSeconds: w.WindowSeconds, MaxSamples: w.MaxSamples, Reasons: map[string]int{}}
	previousKeys := make(map[string]bool, len(afterKeys))
	for _, key := range afterKeys {
		previousKeys[key] = true
	}
	cutoff := now.Add(-time.Duration(w.WindowSeconds) * time.Second)
	if observeAfter.After(cutoff) {
		cutoff = observeAfter
	}
	sorted := append([]PassiveQualitySample(nil), samples...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].At.Equal(sorted[j].At) {
			return sorted[i].Key > sorted[j].Key
		}
		return sorted[i].At.After(sorted[j].At)
	})
	type sessionEvidence struct {
		read, input int64
		count       int
	}
	sessions := map[string]*sessionEvidence{}
	seen := map[string]bool{}
	latencies := []float64{}
	inputCost := 0.0
	for _, s := range sorted {
		if s.At.Before(cutoff) || s.At.After(now) || (!observeAfter.IsZero() && !s.At.After(observeAfter)) {
			continue
		}
		if s.Key != "" {
			if seen[s.Key] {
				continue
			}
			seen[s.Key] = true
		}
		if e.TotalSamples >= w.MaxSamples {
			break
		}
		e.TotalSamples++
		if s.Failed {
			e.FailedSamples++
			continue
		}
		// Impact metrics include all successful relevant requests in this
		// window, including cold/unknown cache opportunities. Recorded fees
		// are configured-rate amounts, not upstream invoices or user charges.
		if s.CacheReason == "account_switched" {
			e.AccountSwitches++
		}
		if s.TTFT != nil && *s.TTFT >= 0 && !math.IsNaN(*s.TTFT) && !math.IsInf(*s.TTFT, 0) {
			latencies = append(latencies, *s.TTFT)
		}
		if s.InputCost != nil && *s.InputCost >= 0 && !math.IsNaN(*s.InputCost) && !math.IsInf(*s.InputCost, 0) {
			inputCost += *s.InputCost
			e.InputCostSamples++
		}
		if s.CacheReason != "" {
			e.Reasons[s.CacheReason]++
		}
		if !s.CacheReported || s.CacheInput <= 0 || s.CacheRead < 0 || s.CacheRead > s.CacheInput {
			e.MissingTelemetrySamples++
			continue
		}
		e.ReportedSamples++
		e.ReadTokens += s.CacheRead
		e.InputTokens += s.CacheInput
		switch s.CacheOpportunity {
		case "cold":
			e.ColdSamples++
			continue
		case "reusable":
			if strings.TrimSpace(s.CacheSession) == "" || strings.TrimSpace(s.CachePrefix) == "" || s.CacheExpectedTokens <= 0 || s.CacheExpectedTokens > s.CacheInput {
				e.UnknownSamples++
				continue
			}
		default:
			e.UnknownSamples++
			continue
		}
		e.ReusableSamples++
		read := min(s.CacheRead, s.CacheExpectedTokens)
		e.EligibleReadTokens += read
		e.EligibleInputTokens += s.CacheExpectedTokens
		if s.At.After(e.Latest) {
			e.Latest = s.At
			e.LatestKeys = nil
		}
		if s.At.Equal(e.Latest) && s.Key != "" {
			e.LatestKeys = append(e.LatestKeys, s.Key)
		}
		if s.At.After(after) || (len(afterKeys) > 0 && s.Key != "" && s.At.Equal(after) && !previousKeys[s.Key]) {
			e.Fresh++
		}
		if sessions[s.CacheSession] == nil {
			sessions[s.CacheSession] = &sessionEvidence{}
		}
		item := sessions[s.CacheSession]
		item.count++
		item.read += read
		item.input += s.CacheExpectedTokens
	}
	if e.InputCostSamples > 0 {
		e.InputCost = &inputCost
	}
	if len(latencies) > 0 {
		sort.Float64s(latencies)
		v := latencies[int(math.Ceil(float64(len(latencies))*.9))-1]
		e.TTFTP90, e.TTFTSamples = &v, len(latencies)
	}
	if e.InputTokens > 0 {
		v := float64(e.ReadTokens) * 100 / float64(e.InputTokens)
		e.CacheRate = &v
	}
	if e.EligibleInputTokens > 0 {
		v := float64(e.EligibleReadTokens) * 100 / float64(e.EligibleInputTokens)
		e.ProtectionRate = &v
		for _, item := range sessions {
			e.MaxSessionSharePercent = math.Max(e.MaxSessionSharePercent, float64(item.input)*100/float64(e.EligibleInputTokens))
			// A single incidental request cannot manufacture independent corroboration.
			if item.count < 2 {
				continue
			}
			e.Sessions++
			rate := float64(item.read) * 100 / float64(item.input)
			if rate <= c.CacheRate.Pause {
				e.BreachedSessions++
			}
			if rate >= c.CacheRate.Recover {
				e.HealthySessions++
			}
		}
	}
	return e
}

// Separate windows are computed per model. Existing states are returned even
// with no recent traffic so their lack of recovery evidence stays visible.
func PassiveQualityCacheWindows(c PassiveQualityConfig, samples []PassiveQualitySample, states map[string]PassiveQualityCacheState, now time.Time, complete bool) map[string]PassiveQualityCacheWindowsEvidence {
	out := map[string]PassiveQualityCacheWindowsEvidence{}
	if !c.IsolatedMetrics() || !c.CacheRate.Enabled || c.CacheProtection == nil {
		return out
	}
	grouped := map[string][]PassiveQualitySample{}
	if c.Model != "" {
		grouped[c.Model] = nil
	}
	for model := range states {
		grouped[model] = nil
	}
	for _, s := range samples {
		model := strings.TrimSpace(s.Model)
		if c.Model != "" && model != c.Model {
			continue
		}
		grouped[model] = append(grouped[model], s)
	}
	for model, items := range grouped {
		state := states[model]
		e := PassiveQualityCacheWindowsEvidence{Model: model}
		e.Quick = passiveQualityCacheWindowEvidence(c, c.CacheProtection.Quick, items, state.QuickEvidenceAt, state.QuickEvidenceKeys, state.ObserveAfter, now, complete)
		e.Action = passiveQualityCacheWindowEvidence(c, c.CacheProtection.ActionWindow, items, state.LastEvidenceAt, state.LastEvidenceKeys, state.ObserveAfter, now, complete)
		if model == "" {
			e.Quick.Complete = false
			e.Action.Complete = false
		}
		out[model] = e
	}
	return out
}

func EvaluatePassiveQualityCache(c PassiveQualityConfig, w PassiveQualityCacheWindow, e PassiveQualityCacheEvidence, recovery bool) (string, string) {
	if !e.Complete {
		return "pending", "cache_telemetry_incomplete"
	}
	if e.ReusableSamples < w.MinSamples {
		return "pending", "insufficient_reusable_samples"
	}
	if e.EligibleInputTokens < w.MinTokens {
		return "pending", "insufficient_reusable_tokens"
	}
	if e.Sessions < w.MinSessions {
		return "pending", "insufficient_independent_sessions"
	}
	if e.MaxSessionSharePercent > float64(w.MaxSessionSharePercent) {
		return "pending", "cache_session_concentration"
	}
	if e.ProtectionRate == nil {
		return "pending", "cache_reuse_unknown"
	}
	if recovery {
		if *e.ProtectionRate >= c.CacheRate.Recover && e.HealthySessions >= w.MinSessions && e.BreachedSessions == 0 {
			return "pass", "cache_reuse_recovered"
		}
		return "fail", "cache_recovery_threshold_not_met"
	}
	if *e.ProtectionRate <= c.CacheRate.Pause && e.BreachedSessions >= 2 {
		return "fail", "cache_reuse_below_threshold"
	}
	// Mixed sessions are evidence of a narrower problem, not account-wide health.
	if e.BreachedSessions > 0 {
		return "pending", "cache_session_results_mixed"
	}
	return "pass", "cache_reuse_healthy"
}

func passiveCacheCursorAdvanced(e PassiveQualityCacheEvidence, after time.Time, keys []string) bool {
	if e.Latest.After(after) {
		return true
	}
	if !e.Latest.Equal(after) || len(keys) == 0 {
		return false
	}
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		seen[key] = true
	}
	for _, key := range e.LatestKeys {
		if !seen[key] {
			return true
		}
	}
	return false
}

// Advances only on new reusable samples. Quick observations never restrict
// routing. Recovery requires fresh post-intervention evidence at every stage.
// Pool eligibility is provided by the transaction holding the shared pool lock.
func AdvancePassiveQualityCache(c PassiveQualityConfig, s *PassiveQualityCacheState, e PassiveQualityCacheWindowsEvidence, now time.Time, allowReduction bool) string {
	if !c.IsolatedMetrics() || !c.CacheRate.Enabled || c.CacheProtection == nil || s == nil {
		return ""
	}
	p := c.CacheProtection
	previousReason := s.Reason
	s.Evidence = e
	s.UpdatedAt = now
	// Confirmations from a window that has expired cannot be carried into a
	// later burst of unrelated traffic. Lack of data never removes a hold.
	if !s.LastEvidenceAt.IsZero() && now.Sub(s.LastEvidenceAt) > time.Duration(p.ActionWindow.WindowSeconds)*time.Second {
		s.Hits, s.RecoveryHits = 0, 0
	}
	if !s.QuickEvidenceAt.IsZero() && now.Sub(s.QuickEvidenceAt) > time.Duration(p.Quick.WindowSeconds)*time.Second {
		s.QuickHits = 0
	}
	if s.Phase == "" {
		s.Phase = "insufficient"
		s.AllocationPercent = 100
	}
	restricted := s.Phase == "reduced" || s.Phase == "recovering"
	if restricted {
		verdict, reason := EvaluatePassiveQualityCache(c, p.ActionWindow, e.Action, true)
		s.Reason = reason
		if verdict == "pending" {
			s.RecoveryHits, s.Hits = 0, 0
		}
		if e.Action.Fresh < p.ActionWindow.NewSamples || !passiveCacheCursorAdvanced(e.Action, s.LastEvidenceAt, s.LastEvidenceKeys) {
			return ""
		}
		s.LastEvidenceAt = e.Action.Latest
		s.LastEvidenceKeys = append([]string(nil), e.Action.LatestKeys...)
		if verdict != "pass" {
			s.RecoveryHits = 0
			if s.Phase == "recovering" && !now.Before(s.HoldUntil) {
				actionVerdict, _ := EvaluatePassiveQualityCache(c, p.ActionWindow, e.Action, false)
				if actionVerdict == "fail" {
					s.Hits++
					if s.Hits >= p.ActionWindow.Confirmations {
						s.Phase = "reduced"
						s.AllocationPercent = p.AllocationPercent
						s.Hits = 0
						s.Reason = "cache_recovery_relapsed"
						s.ObserveAfter = now
						s.LastEvidenceAt = now
						s.QuickEvidenceAt = now
						s.LastEvidenceKeys, s.QuickEvidenceKeys = nil, nil
						s.HoldUntil = now.Add(time.Duration(p.MinHoldSeconds) * time.Second)
						return "cache_recovery_relapsed"
					}
				} else {
					s.Hits = 0
				}
			}
			return ""
		}
		s.Hits = 0
		if now.Before(s.HoldUntil) {
			s.Reason = "cache_minimum_hold"
			return ""
		}
		s.RecoveryHits++
		if s.RecoveryHits < p.RecoveryConfirmations {
			s.Reason = "cache_recovery_confirming"
			return ""
		}
		s.AllocationPercent = min(100, s.AllocationPercent+p.RecoveryStepPercent)
		s.RecoveryHits, s.Hits, s.QuickHits = 0, 0, 0
		s.ObserveAfter = now
		s.LastEvidenceAt = now
		s.QuickEvidenceAt = now
		s.LastEvidenceKeys, s.QuickEvidenceKeys = nil, nil
		s.HoldUntil = now.Add(time.Duration(p.RecoveryHoldSeconds) * time.Second)
		if s.AllocationPercent == 100 {
			s.Phase = "healthy"
			s.Reason = "cache_allocation_recovered"
			return "cache_recovered"
		}
		s.Phase = "recovering"
		s.Reason = "cache_allocation_recovery_step"
		return "cache_recovery_step"
	}
	event := ""
	quickVerdict, _ := EvaluatePassiveQualityCache(c, p.Quick, e.Quick, false)
	if quickVerdict == "pending" {
		s.QuickHits = 0
	}
	if e.Quick.Fresh >= p.Quick.NewSamples && passiveCacheCursorAdvanced(e.Quick, s.QuickEvidenceAt, s.QuickEvidenceKeys) {
		verdict, reason := EvaluatePassiveQualityCache(c, p.Quick, e.Quick, false)
		if verdict != "pending" {
			s.QuickEvidenceAt = e.Quick.Latest
			s.QuickEvidenceKeys = append([]string(nil), e.Quick.LatestKeys...)
			if verdict == "fail" {
				s.QuickHits++
			} else {
				s.QuickHits = 0
			}
			if s.QuickHits >= p.Quick.Confirmations && s.Phase != "watching" {
				s.Phase = "watching"
				s.Reason = "cache_quick_observation"
				event = "cache_watch"
			}
		} else if s.Phase == "healthy" || s.Phase == "insufficient" {
			s.Reason = reason
		}
	}
	verdict, reason := EvaluatePassiveQualityCache(c, p.ActionWindow, e.Action, false)
	if verdict == "pending" {
		s.Hits = 0
		if s.QuickHits < p.Quick.Confirmations {
			s.Phase = "insufficient"
		}
		s.Reason = reason
		return event
	}
	if e.Action.Fresh < p.ActionWindow.NewSamples || !passiveCacheCursorAdvanced(e.Action, s.LastEvidenceAt, s.LastEvidenceKeys) {
		return event
	}
	s.LastEvidenceAt = e.Action.Latest
	s.LastEvidenceKeys = append([]string(nil), e.Action.LatestKeys...)
	if verdict == "pass" {
		s.Hits = 0
		if s.Phase == "watching" {
			s.Phase = "healthy"
			s.QuickHits = 0
			s.Reason = reason
			return "cache_watch_cleared"
		}
		s.Reason = reason
		s.Phase = "healthy"
		return event
	}
	s.Hits++
	s.Reason = reason
	if s.Hits < p.ActionWindow.Confirmations {
		return event
	}
	s.Hits = p.ActionWindow.Confirmations
	if c.Mode != "enforce" || p.Action == "observe" {
		s.Phase = "watching"
		s.Reason = "cache_observe_only"
		return event
	}
	if now.Before(s.HoldUntil) {
		s.Phase = "watching"
		s.Reason = "cache_minimum_hold"
		return event
	}
	if !allowReduction {
		changed := previousReason != "cache_pool_guard"
		s.Phase = "watching"
		s.Reason = "cache_pool_guard"
		if changed {
			return "cache_pool_guard"
		}
		return event
	}
	s.Phase = "reduced"
	s.AllocationPercent = p.AllocationPercent
	s.Reason = "cache_new_sessions_reduced"
	s.RecoveryHits = 0
	s.ObserveAfter = now
	s.LastEvidenceAt = now
	s.QuickEvidenceAt = now
	s.LastEvidenceKeys, s.QuickEvidenceKeys = nil, nil
	s.HoldUntil = now.Add(time.Duration(p.MinHoldSeconds) * time.Second)
	return "cache_reduced"
}

// TTFT is independent of both availability and cache evidence. A latency
// restriction never pauses an account or consumes availability trial budgets.
func AdvancePassiveQualityLatency(c PassiveQualityConfig, s *PassiveQualityMetricState, e PassiveQualityEvidence, now time.Time) string {
	if !c.IsolatedMetrics() || !c.TTFT.Enabled || s == nil {
		return ""
	}
	s.Evidence = e
	s.UpdatedAt = now
	if !s.LastEvidenceAt.IsZero() && now.Sub(s.LastEvidenceAt) > time.Duration(c.WindowSeconds)*time.Second {
		s.Hits, s.RecoveryHits = 0, 0
	}
	if s.Phase == "" {
		s.Phase = "healthy"
	}
	restricted := s.Phase == "limited"
	verdict, reason := EvaluatePassiveQuality(PassiveQualityMetricConfig(c, "ttft"), e, restricted)
	s.Reason = reason
	if verdict == "pending" {
		s.Hits, s.RecoveryHits = 0, 0
	}
	if e.TTFTFresh < c.NewSamples || !e.TTFTLatest.After(s.LastEvidenceAt) || verdict == "pending" {
		return ""
	}
	s.LastEvidenceAt = e.TTFTLatest
	if restricted {
		if verdict != "pass" {
			s.RecoveryHits = 0
			return ""
		}
		if now.Before(s.HoldUntil) {
			s.Reason = "ttft_minimum_hold"
			return ""
		}
		s.RecoveryHits++
		if s.RecoveryHits < c.Confirmations {
			return ""
		}
		s.Phase = "healthy"
		s.Reason = "ttft_recovered"
		s.Hits = 0
		s.RecoveryHits = 0
		s.ObserveAfter = now
		s.HoldUntil = now.Add(time.Duration(max(30, c.CooldownSeconds)) * time.Second)
		return "ttft_recovered"
	}
	if verdict == "pass" {
		s.Hits = 0
		if s.Phase == "watching" {
			s.Phase = "healthy"
			return "ttft_watch_cleared"
		}
		return ""
	}
	s.Hits++
	if s.Hits < c.Confirmations {
		return ""
	}
	s.Hits = c.Confirmations
	if c.Mode != "enforce" || c.TTFTAction == "observe" {
		if s.Phase == "watching" {
			return ""
		}
		s.Phase = "watching"
		return "ttft_watch"
	}
	if now.Before(s.HoldUntil) {
		s.Phase = "watching"
		s.Reason = "ttft_minimum_hold"
		return ""
	}
	s.Phase = "limited"
	s.ObserveAfter = now
	s.LastEvidenceAt = now
	s.HoldUntil = now.Add(time.Duration(max(30, c.CooldownSeconds)) * time.Second)
	return "ttft_limited"
}
