package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Percentages use 0..100; missing metric observations remain nil.
type PassiveQualityMetric struct {
	Enabled bool    `json:"enabled"`
	Pause   float64 `json:"pause"`
	Recover float64 `json:"recover"`
}
type PassiveQualityConfig struct {
	SchemaVersion        int                        `json:"schema_version,omitempty"`
	TTFTAction           string                     `json:"ttft_action,omitempty"`
	TTFTConcurrencyLimit int                        `json:"ttft_concurrency_limit,omitempty"`
	TTFTPriorityPenalty  int                        `json:"ttft_priority_penalty,omitempty"`
	CacheProtection      *PassiveQualityCacheConfig `json:"cache_protection,omitempty"`
	Name                 string                     `json:"name"`
	Enabled              bool                       `json:"enabled"`
	Mode                 string                     `json:"mode"`
	GroupIDs             []int64                    `json:"group_ids"`
	ExcludedAccountIDs   []int64                    `json:"excluded_account_ids"`
	Model                string                     `json:"model"`
	Scope                string                     `json:"scope"`
	Action               string                     `json:"action"`
	ConcurrencyLimit     int                        `json:"concurrency_limit"`
	PriorityPenalty      int                        `json:"priority_penalty"`
	WindowSeconds        int                        `json:"window_seconds"`
	MaxSamples           int                        `json:"max_samples"`
	MinSamples           int                        `json:"min_samples"`
	Confirmations        int                        `json:"confirmations"`
	NewSamples           int                        `json:"new_samples"`
	ErrorRate            PassiveQualityMetric       `json:"error_rate"`
	TTFT                 PassiveQualityMetric       `json:"ttft"`
	CacheRate            PassiveQualityMetric       `json:"cache_rate"`
	ConsecutiveFailures  int                        `json:"consecutive_failures"`
	CooldownSeconds      int                        `json:"cooldown_seconds"`
	MaxCooldownSeconds   int                        `json:"max_cooldown_seconds"`
	TrialSeconds         int                        `json:"trial_seconds"`
	TrialRequests        int                        `json:"trial_requests"`
	TrialConcurrency     int                        `json:"trial_concurrency"`
	RecoverySamples      int                        `json:"recovery_samples"`
}
type PassiveQualityPolicy struct {
	ID        int64     `json:"id"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
	PassiveQualityConfig
}
type PassiveQualitySample struct {
	CacheReported       bool      `json:"cache_reported,omitempty"`
	CacheOpportunity    string    `json:"cache_opportunity,omitempty"`
	CacheSession        string    `json:"cache_session,omitempty"`
	CachePrefix         string    `json:"cache_prefix,omitempty"`
	CacheReason         string    `json:"cache_reason,omitempty"`
	CacheExpectedTokens int64     `json:"cache_expected_tokens,omitempty"`
	Key                 string    `json:"key"`
	Model               string    `json:"model"`
	At                  time.Time `json:"at"`
	Failed              bool      `json:"failed"`
	TTFT                *float64  `json:"ttft"`
	InputCost           *float64  `json:"input_cost,omitempty"`
	CacheRead           int64     `json:"cache_read"`
	CacheInput          int64     `json:"cache_input"`
}
type PassiveQualityEvidence struct {
	TTFTFresh           int       `json:"ttft_fresh"`
	TTFTLatest          time.Time `json:"ttft_latest"`
	CacheReadTokens     int64     `json:"cache_read_tokens"`
	CacheInputTokens    int64     `json:"cache_input_tokens"`
	ProbeModel          string    `json:"probe_model,omitempty"`
	Samples             int       `json:"samples"`
	Failures            int       `json:"failures"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	ErrorRate           *float64  `json:"error_rate"`
	TTFT                *float64  `json:"ttft"`
	TTFTSamples         int       `json:"ttft_samples"`
	CacheRate           *float64  `json:"cache_rate"`
	CacheSamples        int       `json:"cache_samples"`
	Latest              time.Time `json:"latest"`
	Fresh               int       `json:"fresh"`
	Complete            bool      `json:"complete"`
}
type PassiveQualityState struct {
	CacheObserveAfter time.Time                           `json:"cache_observe_after"`
	Latency           *PassiveQualityMetricState          `json:"latency,omitempty"`
	Cache             map[string]PassiveQualityCacheState `json:"cache,omitempty"`
	PolicyID          int64                               `json:"policy_id"`
	PolicyName        string                              `json:"policy_name"`
	AccountID         int64                               `json:"account_id"`
	AccountName       string                              `json:"account_name,omitempty"`
	Model             string                              `json:"model"`
	ProbeModel        string                              `json:"probe_model,omitempty"`
	Scope             string                              `json:"scope"`
	Mode              string                              `json:"mode"`
	Version           int64                               `json:"version"`
	Generation        int64                               `json:"generation"`
	Phase             string                              `json:"phase"`
	Reason            string                              `json:"reason"`
	Hits              int                                 `json:"hits"`
	Backoff           int                                 `json:"backoff"`
	Used              int                                 `json:"used"`
	Inflight          int                                 `json:"inflight"`
	LastEvidenceAt    time.Time                           `json:"last_evidence_at"`
	ObserveAfter      time.Time                           `json:"observe_after"`
	NextAt            time.Time                           `json:"next_at"`
	TrialStartedAt    time.Time                           `json:"trial_started_at"`
	TrialEndsAt       time.Time                           `json:"trial_ends_at"`
	UpdatedAt         time.Time                           `json:"updated_at"`
	Evidence          PassiveQualityEvidence              `json:"evidence"`
	OtherHold         bool                                `json:"other_hold"`
}
type PassiveQualityEvent struct {
	ID         int64               `json:"id"`
	PolicyID   int64               `json:"policy_id"`
	AccountID  int64               `json:"account_id"`
	PolicyName string              `json:"policy_name"`
	Kind       string              `json:"kind"`
	Detail     PassiveQualityState `json:"detail"`
	CreatedAt  time.Time           `json:"created_at"`
}
type PassiveQualityFailure struct {
	CacheMetadata     *PassiveQualityCacheMetadata
	CacheReported     bool
	AttemptKey        string
	AccountID         int64
	Model, RequestKey string
	StartedAt, At     time.Time
}

// The repository serializes writes in policy -> account -> state order.
// Work returns fenced probes; the remote call never holds a SQL lock.
type PassiveQualityRepository interface {
	ListPolicies(context.Context) ([]PassiveQualityPolicy, error)
	SavePolicy(context.Context, *PassiveQualityPolicy) error
	DeletePolicy(context.Context, int64) error
	ListStates(context.Context, int64) ([]PassiveQualityState, error)
	ListEvents(context.Context, int64, int64) ([]PassiveQualityEvent, error)
	Act(context.Context, int64, int64, string, int, ...string) error
	Sweep(context.Context, time.Time, int) ([]PassiveQualityState, error)
	FinishProbe(context.Context, PassiveQualityState, bool, time.Time) error
	RecordFailures(context.Context, []PassiveQualityFailure, *time.Time) error
	AcquirePassiveQuality(context.Context, int64, string, string) (bool, bool, error)
	ReleasePassiveQuality(context.Context, string, bool) error
}

type PassiveQualityConcurrencyGate interface {
	PassiveQualityConcurrencyLimit(context.Context, int64, string) (int, error)
}

func (c *PassiveQualityConfig) Validate() error {
	if err := c.validateIsolatedMetrics(); err != nil {
		return err
	}
	if c.Action == "" {
		c.Action = "pause"
	}
	if c.Action != "pause" && c.Action != "limit" {
		return fmt.Errorf("action must be pause or limit")
	}
	if c.Action == "limit" && (c.ConcurrencyLimit < 1 || c.ConcurrencyLimit > 1000 || c.PriorityPenalty < 0 || c.PriorityPenalty > 1000) {
		return fmt.Errorf("limited concurrency must be 1..1000 and priority penalty 0..1000")
	}
	if c.ExcludedAccountIDs == nil {
		c.ExcludedAccountIDs = []int64{}
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Model = strings.TrimSpace(c.Model)
	if c.Name == "" || len(c.Name) > 160 || len(c.Model) > 200 {
		return fmt.Errorf("name is required (max 160 characters); optional model must be at most 200 characters")
	}
	if c.Mode != "observe" && c.Mode != "enforce" {
		return fmt.Errorf("mode must be observe or enforce")
	}
	if c.Scope != "" && c.Scope != "model" && c.Scope != "account" {
		return fmt.Errorf("scope must be model or account")
	}
	// V2 can discover request models dynamically without mixing their evidence
	// or holds. Preserve explicit account-wide and legacy policy semantics.
	if c.IsolatedMetrics() {
		if c.Scope == "" {
			c.Scope = "model"
		}
	} else if c.Model == "" || c.Scope == "" {
		c.Scope = "account"
	}
	validIDs := func(ids []int64, max int) bool {
		seen := map[int64]bool{}
		for _, id := range ids {
			if id <= 0 || seen[id] {
				return false
			}
			seen[id] = true
		}
		return len(ids) <= max
	}
	if len(c.GroupIDs) == 0 || !validIDs(c.GroupIDs, 100) || !validIDs(c.ExcludedAccountIDs, 10000) {
		return fmt.Errorf("select 1..100 groups; IDs must be positive and unique")
	}
	if c.WindowSeconds < 60 || c.WindowSeconds > 86400 || c.MaxSamples < 5 || c.MaxSamples > 2000 || c.MinSamples < 5 || c.MinSamples > c.MaxSamples {
		return fmt.Errorf("window must be 60..86400 seconds; samples 5..2000; minimum cannot exceed window size")
	}
	if c.Confirmations < 1 || c.Confirmations > 10 || c.NewSamples < 1 || c.NewSamples > c.MaxSamples {
		return fmt.Errorf("confirmations must be 1..10 and new samples 1..max_samples")
	}
	if !c.ErrorRate.Enabled && !c.TTFT.Enabled && !c.CacheRate.Enabled && c.ConsecutiveFailures == 0 {
		return fmt.Errorf("enable at least one metric")
	}
	if c.ConsecutiveFailures < 0 || c.ConsecutiveFailures > c.MaxSamples {
		return fmt.Errorf("consecutive failures must be 0..max_samples")
	}
	for _, m := range []struct {
		name string
		v    PassiveQualityMetric
		max  float64
		low  bool
	}{{"error_rate", c.ErrorRate, 100, false}, {"ttft", c.TTFT, 600000, false}, {"cache_rate", c.CacheRate, 100, true}} {
		if !m.v.Enabled {
			continue
		}
		if math.IsNaN(m.v.Pause) || math.IsNaN(m.v.Recover) || math.IsInf(m.v.Pause, 0) || math.IsInf(m.v.Recover, 0) || m.v.Pause < 0 || m.v.Pause > m.max || m.v.Recover < 0 || m.v.Recover > m.max {
			return fmt.Errorf("invalid %s threshold", m.name)
		}
		if (m.low && m.v.Recover <= m.v.Pause) || (!m.low && m.v.Recover >= m.v.Pause) {
			return fmt.Errorf("%s recovery must be strictly healthier than pause threshold", m.name)
		}
	}
	if c.RecoverySamples < 5 || c.RecoverySamples > c.MaxSamples {
		return fmt.Errorf("recovery samples must be 5..max_samples")
	}
	if c.Action != "limit" && (c.CooldownSeconds < 30 || c.MaxCooldownSeconds < c.CooldownSeconds || c.MaxCooldownSeconds > 86400 || c.TrialSeconds < 60 || c.TrialSeconds > 3600 || c.TrialRequests < 5 || c.TrialRequests > 2000 || c.TrialConcurrency < 1 || c.TrialConcurrency > 20 || c.TrialConcurrency > c.TrialRequests || c.RecoverySamples < 5 || c.RecoverySamples > c.TrialRequests || c.RecoverySamples > c.MaxSamples) {
		return fmt.Errorf("invalid recovery limits: cooldown 30..86400s, trial 60..3600s, requests 5..2000, concurrency 1..20, minimum 5..min(requests,max_samples)")
	}
	return nil
}

func PassiveQualitySummarize(samples []PassiveQualitySample, after time.Time, complete bool) PassiveQualityEvidence {
	e := PassiveQualityEvidence{Samples: len(samples), Complete: complete}
	var ttft []float64
	var read, input int64
	consecutive := true
	failureProbe := false
	// Repository supplies newest first, with a deterministic tie break.
	for _, s := range samples {
		// Prefer the newest failed request's model; otherwise use the newest
		// observed model. The caller retains it across cooldown and empty trials.
		if model := strings.TrimSpace(s.Model); model != "" && (e.ProbeModel == "" || (s.Failed && !failureProbe)) {
			e.ProbeModel = model
			failureProbe = s.Failed
		}
		if s.At.After(e.Latest) {
			e.Latest = s.At
		}
		if s.At.After(after) {
			e.Fresh++
		}
		if s.Failed {
			e.Failures++
			if consecutive {
				e.ConsecutiveFailures++
			}
			continue
		}
		consecutive = false
		if s.TTFT != nil && *s.TTFT >= 0 {
			ttft = append(ttft, *s.TTFT)
			if s.At.After(after) {
				e.TTFTFresh++
			}
			if s.At.After(e.TTFTLatest) {
				e.TTFTLatest = s.At
			}
		}
		if s.CacheInput > 0 {
			e.CacheSamples++
			input += s.CacheInput
			read += s.CacheRead
		}
	}
	if e.Samples > 0 {
		v := float64(e.Failures) * 100 / float64(e.Samples)
		e.ErrorRate = &v
	}
	if len(ttft) > 0 {
		sort.Float64s(ttft)
		v := ttft[int(math.Ceil(float64(len(ttft))*.9))-1]
		e.TTFT = &v
		e.TTFTSamples = len(ttft)
	}
	e.CacheReadTokens, e.CacheInputTokens = read, input
	if input > 0 {
		v := float64(read) * 100 / float64(input)
		e.CacheRate = &v
	}
	return e
}

// Return fail if any configured metric fails; pass requires every metric's own
// minimum. A probe cannot call this a pass without fresh production evidence.
func EvaluatePassiveQuality(c PassiveQualityConfig, e PassiveQualityEvidence, recovery bool) (string, string) {
	if !e.Complete {
		return "pending", "telemetry_incomplete"
	}
	minimum := c.MinSamples
	if recovery {
		minimum = c.RecoverySamples
	}
	if e.Samples < minimum {
		return "pending", "insufficient_samples"
	}
	pending := false
	for _, m := range []struct {
		name    string
		cfg     PassiveQualityMetric
		value   *float64
		samples int
		low     bool
	}{{"error_rate", c.ErrorRate, e.ErrorRate, e.Samples, false}, {"ttft_p90", c.TTFT, e.TTFT, e.TTFTSamples, false}, {"cache_rate", c.CacheRate, e.CacheRate, e.CacheSamples, true}} {
		if !m.cfg.Enabled {
			continue
		}
		if m.value == nil || m.samples < minimum {
			pending = true
			continue
		}
		threshold := m.cfg.Pause
		if recovery {
			threshold = m.cfg.Recover
		}
		bad := *m.value >= threshold
		if m.low {
			bad = *m.value <= threshold
		}
		if recovery {
			bad = *m.value > threshold
			if m.low {
				bad = *m.value < threshold
			}
		}
		if bad {
			return "fail", fmt.Sprintf("%s: %.2f / %.2f (%d)", m.name, *m.value, threshold, m.samples)
		}
	}
	if c.ConsecutiveFailures > 0 && e.ConsecutiveFailures >= c.ConsecutiveFailures {
		return "fail", fmt.Sprintf("consecutive_failures: %d", e.ConsecutiveFailures)
	}
	if recovery && c.ConsecutiveFailures > 0 && e.ConsecutiveFailures > 0 {
		return "pending", "waiting_for_success"
	}
	if pending {
		return "pending", "insufficient_metric_samples"
	}
	return "pass", "healthy"
}

func PassiveQualityPause(c PassiveQualityConfig, s *PassiveQualityState, now time.Time, reason string) {
	s.Phase = "paused"
	s.Reason = reason
	s.Generation++
	s.Used = 0
	s.Inflight = 0
	s.Hits = 0
	seconds := math.Min(float64(c.MaxCooldownSeconds), float64(c.CooldownSeconds)*math.Pow(2, float64(min(s.Backoff, 16))))
	// Deterministic per-account jitter avoids synchronized probes after an outage.
	seconds = math.Min(float64(c.MaxCooldownSeconds), seconds*(1+float64((s.AccountID+s.Generation)%11)/100))
	s.NextAt = now.Add(time.Duration(seconds) * time.Second)
	s.Backoff++
	s.UpdatedAt = now
}

func (a *Account) PassiveQualityBlocked(model string) bool {
	if a == nil {
		return false
	}
	holds, ok := a.Extra["passive_quality"].([]any)
	if !ok {
		return false
	}
	accountTrial, matchingTrial := false, false
	for _, h := range holds {
		v, ok := h.(map[string]any)
		if !ok {
			continue
		}
		scope, _ := v["scope"].(string)
		m, _ := v["model"].(string)
		if scope != "account" && (model == "" || model != m) {
			continue
		}
		phase, _ := v["phase"].(string)
		if phase == "limited" || phase == "cache_limited" {
			continue
		}
		if phase != "trial" {
			return true
		}
		ends, _ := v["trial_ends_at"].(string)
		end, err := time.Parse(time.RFC3339Nano, ends)
		if err != nil || !time.Now().Before(end) {
			return true
		}
		if scope == "account" && m != "" {
			accountTrial = true
		}
		// Generic account eligibility cannot consume or apply a model's budget.
		// With overlapping account-wide holds, each model trials its own policy.
		if model == "" || (m != "" && model != m) {
			continue
		}
		matchingTrial = matchingTrial || m != ""
		used, _ := v["used"].(float64)
		budget, _ := v["max_requests"].(float64)
		if budget > 0 && used >= budget {
			return true
		}
	}
	return model != "" && accountTrial && !matchingTrial
}
