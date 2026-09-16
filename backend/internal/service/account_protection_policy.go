package service

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// AccountProtectionPolicyKey is stored inside account.extra.  It deliberately
// contains policy and ownership metadata only; credentials and fingerprint
// seeds remain in their existing fields.
const AccountProtectionPolicyKey = "account_protection_policy"

type AccountProtectionPolicy struct {
	Enabled                bool   `json:"enabled"`
	Mode                   string `json:"mode"`              // observe or enforce
	RequestIntegrity       string `json:"request_integrity"` // off, observe, enforce
	AdaptiveConcurrency    bool   `json:"adaptive_concurrency"`
	AdaptiveMode           string `json:"adaptive_mode"` // observe or automatic
	AdaptiveMinConcurrency int    `json:"adaptive_min_concurrency"`
	FailureThreshold       int    `json:"failure_threshold"`
	FailureWindowSeconds   int    `json:"failure_window_seconds"`
	RecoverySeconds        int    `json:"recovery_seconds"`
}

func DefaultAccountProtectionPolicy() AccountProtectionPolicy {
	return AccountProtectionPolicy{
		Mode: "observe", RequestIntegrity: "observe", AdaptiveMode: "observe",
		AdaptiveMinConcurrency: 1, FailureThreshold: 3,
		FailureWindowSeconds: 60, RecoverySeconds: 60,
	}
}

func NormalizeAccountProtectionPolicy(in AccountProtectionPolicy) AccountProtectionPolicy {
	d := DefaultAccountProtectionPolicy()
	if in.Mode != "observe" && in.Mode != "enforce" {
		in.Mode = d.Mode
	}
	if in.RequestIntegrity != "off" && in.RequestIntegrity != "observe" && in.RequestIntegrity != "enforce" {
		in.RequestIntegrity = d.RequestIntegrity
	}
	if in.AdaptiveMode != "observe" && in.AdaptiveMode != "automatic" {
		in.AdaptiveMode = d.AdaptiveMode
	}
	if in.AdaptiveMinConcurrency < 1 {
		in.AdaptiveMinConcurrency = d.AdaptiveMinConcurrency
	}
	if in.AdaptiveMinConcurrency > 10000 {
		in.AdaptiveMinConcurrency = 10000
	}
	if in.FailureThreshold < 1 {
		in.FailureThreshold = d.FailureThreshold
	}
	if in.FailureWindowSeconds < 10 {
		in.FailureWindowSeconds = d.FailureWindowSeconds
	}
	if in.RecoverySeconds < 10 {
		in.RecoverySeconds = d.RecoverySeconds
	}
	return in
}

func AccountProtectionPolicyFromExtra(extra map[string]any) AccountProtectionPolicy {
	p := DefaultAccountProtectionPolicy()
	if extra == nil || extra[AccountProtectionPolicyKey] == nil {
		return p
	}
	raw, ok := extra[AccountProtectionPolicyKey].(map[string]any)
	if !ok {
		return p
	}
	if v, ok := raw["enabled"].(bool); ok {
		p.Enabled = v
	}
	if v, ok := raw["mode"].(string); ok {
		p.Mode = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := raw["request_integrity"].(string); ok {
		p.RequestIntegrity = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := raw["adaptive_concurrency"].(bool); ok {
		p.AdaptiveConcurrency = v
	}
	if v, ok := raw["adaptive_mode"].(string); ok {
		p.AdaptiveMode = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := extraInt(raw["adaptive_min_concurrency"]); ok {
		p.AdaptiveMinConcurrency = v
	}
	if v, ok := extraInt(raw["failure_threshold"]); ok {
		p.FailureThreshold = v
	}
	if v, ok := extraInt(raw["failure_window_seconds"]); ok {
		p.FailureWindowSeconds = v
	}
	if v, ok := extraInt(raw["recovery_seconds"]); ok {
		p.RecoverySeconds = v
	}
	return NormalizeAccountProtectionPolicy(p)
}

func extraInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), n == float64(int(n))
	}
	return 0, false
}

type adaptiveAccountState struct {
	mu           sync.Mutex
	limit        int
	adminLimit   int
	minimum      int
	threshold    int
	window       time.Duration
	recovery     time.Duration
	failures     []time.Time
	lastFailure  time.Time
	lastSuccess  time.Time
	lastAdjusted time.Time
}

var adaptiveAccountStates sync.Map // account id -> *adaptiveAccountState

func RegisterAccountProtectionPolicy(account *Account) {
	if account == nil || account.ID <= 0 {
		return
	}
	p := AccountProtectionPolicyFromExtra(account.Extra)
	if !p.Enabled || !p.AdaptiveConcurrency || p.AdaptiveMode != "automatic" {
		// A policy can be disabled or switched to observe mode after a previous
		// automatic run. Drop stale runtime state so the saved admin concurrency
		// immediately becomes effective again.
		adaptiveAccountStates.Delete(account.ID)
		return
	}
	limit := account.Concurrency
	if limit < 1 {
		return
	}
	value, _ := adaptiveAccountStates.LoadOrStore(account.ID, &adaptiveAccountState{
		limit: limit, adminLimit: limit, minimum: protectionMinInt(p.AdaptiveMinConcurrency, limit),
		threshold: p.FailureThreshold, window: time.Duration(p.FailureWindowSeconds) * time.Second,
		recovery: time.Duration(p.RecoverySeconds) * time.Second,
	})
	s := value.(*adaptiveAccountState)
	s.mu.Lock()
	s.adminLimit = limit
	s.minimum = protectionMinInt(protectionMaxInt(1, p.AdaptiveMinConcurrency), limit)
	s.threshold = p.FailureThreshold
	s.window = time.Duration(p.FailureWindowSeconds) * time.Second
	s.recovery = time.Duration(p.RecoverySeconds) * time.Second
	if s.threshold < 1 {
		s.threshold = 3
	}
	if s.window <= 0 {
		s.window = time.Minute
	}
	if s.recovery <= 0 {
		s.recovery = time.Minute
	}
	if s.limit < s.minimum || s.limit > limit {
		s.limit = limit
	}
	s.mu.Unlock()
}

// EffectiveAccountConcurrency is the single runtime limit used by account
// admission. It never changes Account.Concurrency or the saved configuration.
func EffectiveAccountConcurrency(account *Account, configured int) int {
	if account == nil || configured <= 0 {
		return configured
	}
	RegisterAccountProtectionPolicy(account)
	value, ok := adaptiveAccountStates.Load(account.ID)
	if !ok {
		return configured
	}
	s := value.(*adaptiveAccountState)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit <= 0 || s.limit > configured {
		return configured
	}
	return s.limit
}

// ObserveAccountProtectionOutcome adjusts only the runtime limit. Callers must
// pass the final upstream outcome for a request; local queue rejections should
// not be reported here.
func ObserveAccountProtectionOutcome(accountID int64, statusCode int, networkError bool) {
	value, ok := adaptiveAccountStates.Load(accountID)
	if !ok {
		return
	}
	s := value.(*adaptiveAccountState)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	isFailure := networkError || statusCode == 429 || statusCode >= 500
	if isFailure {
		s.failures = append(s.failures, now)
		s.lastFailure = now
		window := s.window
		if window <= 0 {
			window = time.Minute
		}
		cutoff := now.Add(-window)
		kept := s.failures[:0]
		for _, at := range s.failures {
			if at.After(cutoff) {
				kept = append(kept, at)
			}
		}
		s.failures = kept
		if len(s.failures) >= s.threshold && now.Sub(s.lastAdjusted) >= 10*time.Second && s.limit > s.minimum {
			previous := s.limit
			s.limit = protectionMaxInt(s.minimum, s.limit/2)
			s.lastAdjusted = now
			slog.Info("account_protection.adaptive_concurrency_reduced", "account_id", accountID, "previous", previous, "current", s.limit, "failures", len(s.failures))
		}
		return
	}
	if statusCode >= 200 && statusCode < 400 {
		s.lastSuccess = now
		recovery := s.recovery
		if recovery <= 0 {
			recovery = time.Minute
		}
		if s.limit < s.adminLimit && now.Sub(s.lastFailure) >= recovery && now.Sub(s.lastAdjusted) >= recovery {
			s.limit++
			s.lastAdjusted = now
			slog.Info("account_protection.adaptive_concurrency_recovered", "account_id", accountID, "current", s.limit, "configured", s.adminLimit)
		}
	}
}

func protectionMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func protectionMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
