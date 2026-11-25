package service

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"
)

var ErrPerformanceConflict = errors.New("performance monitor changed; reload before saving")

const (
	PerformanceTrialDuration         = 10 * time.Minute
	PerformanceTrialBudgetMultiplier = 3
	PerformanceDisabled              = "disabled"
	PerformanceMonitoring            = "monitoring"
	PerformancePaused                = "paused"
	PerformanceTrial                 = "trial"
)

type PerformanceBounds struct {
	Min *float64 `json:"min"`
	Max *float64 `json:"max"`
}

func (b PerformanceBounds) Validate(percent bool) error {
	for _, v := range []*float64{b.Min, b.Max} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || *v > 1e9 || (percent && *v > 100)) {
			return fmt.Errorf("invalid performance threshold")
		}
	}
	if b.Min != nil && b.Max != nil && *b.Min > *b.Max {
		return fmt.Errorf("minimum exceeds maximum")
	}
	return nil
}
func (b PerformanceBounds) Breached(v *float64) bool {
	return v != nil && ((b.Min != nil && *v < *b.Min) || (b.Max != nil && *v > *b.Max))
}

// Empty bounds disable a metric. Percentages use 0..100; latency uses milliseconds.
type PerformanceRule struct {
	Pause    PerformanceBounds `json:"pause"`
	Recovery PerformanceBounds `json:"recovery"`
}

func (r PerformanceRule) Enabled() bool { return r.Pause.Min != nil || r.Pause.Max != nil }
func (r PerformanceRule) RecoveryBounds() PerformanceBounds {
	b := r.Recovery
	if b.Min == nil {
		b.Min = r.Pause.Min
	}
	if b.Max == nil {
		b.Max = r.Pause.Max
	}
	return b
}

type PerformanceConfig struct {
	Version         int             `json:"version"`
	Enabled         bool            `json:"enabled"`
	SuccessRate     PerformanceRule `json:"success_rate"`
	CacheRate       PerformanceRule `json:"cache_rate"`
	TTFT            PerformanceRule `json:"ttft"`
	Duration        PerformanceRule `json:"duration"`
	MinSamples      int64           `json:"min_samples"`
	AutoRecover     bool            `json:"auto_recover"`
	CooldownMinutes int             `json:"cooldown_minutes"`
	RecoverySamples int64           `json:"recovery_samples"`
}

func DefaultPerformanceConfig() PerformanceConfig {
	return PerformanceConfig{Version: 2, MinSamples: 20, AutoRecover: true, CooldownMinutes: 5, RecoverySamples: 3}
}
func (c PerformanceConfig) Validate() error {
	if c.Version != 2 {
		return fmt.Errorf("reload the metric configuration before saving")
	}
	if c.MinSamples < 1 || c.MinSamples > 10000 || c.RecoverySamples < 1 || c.RecoverySamples > 100 || c.CooldownMinutes < 1 || c.CooldownMinutes > 1440 {
		return fmt.Errorf("invalid sample count or cooldown")
	}
	enabled := false
	for i, r := range []PerformanceRule{c.SuccessRate, c.CacheRate, c.TTFT, c.Duration} {
		if err := r.Pause.Validate(i < 2); err != nil {
			return err
		}
		if err := r.Recovery.Validate(i < 2); err != nil {
			return err
		}
		if !r.Enabled() && (r.Recovery.Min != nil || r.Recovery.Max != nil) {
			return fmt.Errorf("recovery requires a pause threshold")
		}
		b := r.RecoveryBounds()
		if err := b.Validate(i < 2); err != nil {
			return err
		}
		if (r.Pause.Min != nil && b.Min != nil && *b.Min < *r.Pause.Min) || (r.Pause.Max != nil && b.Max != nil && *b.Max > *r.Pause.Max) {
			return fmt.Errorf("recovery thresholds must be within the normal range")
		}
		enabled = enabled || r.Enabled()
	}
	if c.Enabled && !enabled {
		return fmt.Errorf("select at least one metric")
	}
	return nil
}

type AccountPerformance struct {
	MonitoringAvailable bool                   `json:"monitoring_available"`
	AccountID           int64                  `json:"account_id"`
	Metrics             OpsAccountRequestStats `json:"metrics,omitzero"`
	Config              PerformanceConfig      `json:"config"`
	Revision            int64                  `json:"revision"`
	Paused              bool                   `json:"paused"`
	PauseGeneration     int64                  `json:"pause_generation"`
	PausedAt            *time.Time             `json:"paused_at"`
	TrialStartedAt      *time.Time             `json:"trial_started_at"`
	TrialRequests       int64                  `json:"trial_requests"`
	MetricsSince        *time.Time             `json:"metrics_since"`
	Reason              string                 `json:"reason"`
	Phase               string                 `json:"phase"`
	RecoverAfter        *time.Time             `json:"recover_after,omitempty"`
	TrialEndsAt         *time.Time             `json:"trial_ends_at,omitempty"`
	TrialLimit          int64                  `json:"trial_limit,omitempty"`
}

// DescribeState derives API fields and evaluator deadlines from the persisted state.
func (m *AccountPerformance) DescribeState() {
	m.TrialEndsAt, m.RecoverAfter, m.TrialLimit = nil, nil, 0
	switch {
	case !m.Config.Enabled:
		m.Phase = PerformanceDisabled
	case !m.Paused:
		m.Phase = PerformanceMonitoring
	case m.TrialStartedAt != nil:
		m.Phase = PerformanceTrial
		deadline := m.TrialStartedAt.Add(PerformanceTrialDuration)
		m.TrialEndsAt = &deadline
		m.TrialLimit = m.Config.RecoverySamples * PerformanceTrialBudgetMultiplier
	default:
		m.Phase = PerformancePaused
		if m.Config.AutoRecover && m.PausedAt != nil {
			due := m.PausedAt.Add(time.Duration(m.Config.CooldownMinutes) * time.Minute)
			m.RecoverAfter = &due
		}
	}
}

func (m AccountPerformance) MetricWindow(now time.Time) (time.Time, time.Time) {
	if m.TrialEndsAt != nil && m.TrialStartedAt != nil {
		end := now
		if m.TrialEndsAt.Before(end) {
			end = *m.TrialEndsAt
		}
		return *m.TrialStartedAt, end
	}
	since := now.Add(-time.Hour)
	if m.MetricsSince != nil && m.MetricsSince.After(since) {
		since = *m.MetricsSince
	}
	return since, now
}

type performanceMeasurement struct {
	name    string
	rule    PerformanceRule
	value   *float64
	samples int64
}

func percent(v *float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v * 100
	return &n
}
func (m AccountPerformance) measurements() []performanceMeasurement {
	return []performanceMeasurement{
		{"success_rate", m.Config.SuccessRate, percent(m.Metrics.SuccessRate), m.Metrics.RequestSamples},
		{"cache_rate", m.Config.CacheRate, percent(m.Metrics.CacheRate), m.Metrics.CacheSamples},
		{"ttft", m.Config.TTFT, m.Metrics.AvgFirstTokenMs, m.Metrics.FirstTokenSamples},
		{"duration", m.Config.Duration, m.Metrics.AvgDurationMs, m.Metrics.DurationSamples},
	}
}
func validPerformanceValue(v *float64) bool {
	return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0
}
func (m AccountPerformance) Breaches() []string {
	var reasons []string
	minimum := m.Config.MinSamples
	if m.TrialStartedAt != nil {
		minimum = m.Config.RecoverySamples
	}
	for _, v := range m.measurements() {
		if v.rule.Enabled() && v.samples >= minimum && validPerformanceValue(v.value) && v.rule.Pause.Breached(v.value) {
			reasons = append(reasons, v.name)
		}
	}
	return reasons
}
func (m AccountPerformance) recoveryFailure() string {
	failure := ""
	for _, v := range m.measurements() {
		if !v.rule.Enabled() {
			continue
		}
		if v.samples < m.Config.RecoverySamples || !validPerformanceValue(v.value) {
			return "trial_insufficient_samples"
		}
		if v.rule.RecoveryBounds().Breached(v.value) {
			failure = "trial_recovery_threshold"
		}
	}
	return failure
}

// MetricDecision operates on enabled, validated rules and metrics from MetricWindow.
// A late evaluator can still recover from samples recorded before the deadline.
func (m AccountPerformance) MetricDecision(now time.Time) (action, reason string) {
	if breaches := m.Breaches(); len(breaches) > 0 {
		return "pause", strings.Join(breaches, ",")
	}
	if m.TrialEndsAt != nil {
		failure := m.recoveryFailure()
		if failure == "" {
			return "recover", ""
		}
		if !now.Before(*m.TrialEndsAt) {
			return "pause", failure
		}
	}
	return "", ""
}

// Trial admission is persistent and atomic across instances.
type PerformanceAdmission interface {
	AdmitPerformanceRequest(context.Context, int64) (bool, error)
}

type PerformanceEvent struct {
	ID              int64           `json:"id"`
	Action          string          `json:"action"`
	Reason          string          `json:"reason,omitempty"`
	ActorID         *int64          `json:"actor_id"`
	Revision        int64           `json:"revision"`
	PauseGeneration int64           `json:"pause_generation"`
	Evidence        json.RawMessage `json:"evidence"`
	CreatedAt       time.Time       `json:"created_at"`
}

// Before decision_reason was recorded, evidence.reason described the preceding
// pause. Reconstruct only reasons supported by the historical evaluator's inputs.
func (e *PerformanceEvent) ReadPauseReason() {
	if e.Action != "pause" {
		return
	}
	var snapshot struct {
		AccountPerformance
		DecisionReason string `json:"decision_reason"`
	}
	if json.Unmarshal(e.Evidence, &snapshot) != nil {
		return
	}
	if snapshot.DecisionReason != "" {
		e.Reason = snapshot.DecisionReason
		return
	}
	if !snapshot.Config.Enabled || snapshot.Config.Validate() != nil {
		return
	}
	if breaches := snapshot.Breaches(); len(breaches) > 0 {
		e.Reason = strings.Join(breaches, ",")
	} else if snapshot.TrialStartedAt != nil && !snapshot.Metrics.WindowEnd.Before(snapshot.TrialStartedAt.Add(PerformanceTrialDuration)) {
		// Legacy trials aggregated beyond the deadline and reported a timeout
		// even when enough good samples were present by evaluation time.
		e.Reason = "trial_timeout"
	}
}

type AccountPerformanceRepository interface {
	Events(context.Context, int64) ([]PerformanceEvent, error)
	Read(context.Context, []int64, time.Time, bool) (map[int64]AccountPerformance, error)
	Save(context.Context, int64, PerformanceConfig, int64, int64) error
	Release(context.Context, int64, int64, int64, int64) error
	Evaluate(context.Context, int64, time.Time) error
	EnabledIDs(context.Context) ([]int64, error)
	Paused(context.Context, []int64) (map[int64]bool, error)
}

type AccountPerformanceService struct {
	Repo   AccountPerformanceRepository
	ops    *OpsService
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// Only the evaluator goroutine owns this cursor. Advance on attempts,
	// including timeouts, so slow accounts cannot starve the remaining IDs.
	evaluationCursor int64
}

func NewAccountPerformanceService(repo AccountPerformanceRepository) *AccountPerformanceService {
	return &AccountPerformanceService{Repo: repo}
}
func (s *AccountPerformanceService) MonitoringAvailable(ctx context.Context) bool {
	return s.ops == nil || s.ops.IsMonitoringEnabled(ctx)
}
func (s *AccountPerformanceService) Read(ctx context.Context, ids []int64, now time.Time, includeMetrics bool) (map[int64]AccountPerformance, error) {
	available := s.MonitoringAvailable(ctx)
	rows, err := s.Repo.Read(ctx, ids, now, available && includeMetrics)
	if err != nil {
		return nil, err
	}
	for id, m := range rows {
		m.MonitoringAvailable = available
		if includeMetrics && !available {
			start, end := m.MetricWindow(now)
			m.Metrics = OpsAccountRequestStats{WindowStart: start, WindowEnd: end}
		}
		rows[id] = m
	}
	return rows, nil
}
func (s *AccountPerformanceService) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.evaluate(ctx)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.evaluate(ctx)
			}
		}
	}()
}
func (s *AccountPerformanceService) Stop() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
func (s *AccountPerformanceService) evaluate(ctx context.Context) {
	if !s.MonitoringAvailable(ctx) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	ids, err := s.Repo.EnabledIDs(ctx)
	if err != nil {
		slog.Error("account performance list", "error", err)
		return
	}
	start := 0
	for i, id := range ids {
		if id > s.evaluationCursor {
			start = i
			break
		}
	}
	for offset := range ids {
		if ctx.Err() != nil {
			return
		}
		id := ids[(start+offset)%len(ids)]
		s.evaluationCursor = id
		if err := s.Repo.Evaluate(ctx, id, time.Now()); err != nil {
			slog.Error("account performance evaluate", "account_id", id, "error", err)
		}
	}
}

// Live authority check is intentionally outside all Redis/regional snapshots.
// A database error fails closed; cached permission cannot override an auto pause.
type PerformancePauseReader interface {
	PerformancePaused(context.Context, []int64) (map[int64]bool, error)
}

type PerformanceRoutingState struct {
	Blocked    bool
	TrialDueAt *time.Time
}

type PerformanceRoutingReader interface {
	PerformanceRoutingStates(context.Context, []int64) (map[int64]PerformanceRoutingState, error)
}

type unmeasuredPerformanceRequest struct{}

// Token counts and model catalogs produce no generation samples. Keep recovery
// capacity for requests whose outcome the evaluator can observe.
func withoutPerformanceTrials(ctx context.Context) context.Context {
	return context.WithValue(ctx, unmeasuredPerformanceRequest{}, true)
}

func isUnmeasuredPerformanceRequest(ctx context.Context) bool {
	return ctx.Value(unmeasuredPerformanceRequest{}) == true
}

func performanceRoutingStates(ctx context.Context, repo any, ids []int64) (map[int64]PerformanceRoutingState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if reader, ok := repo.(PerformanceRoutingReader); ok {
		states, err := reader.PerformanceRoutingStates(ctx, ids)
		if err == nil && isUnmeasuredPerformanceRequest(ctx) {
			nonTrial := make(map[int64]PerformanceRoutingState, len(states))
			for id, state := range states {
				if state.TrialDueAt != nil {
					state = PerformanceRoutingState{Blocked: true}
				}
				nonTrial[id] = state
			}
			return nonTrial, nil
		}
		return states, err
	}
	reader, ok := repo.(PerformancePauseReader)
	if !ok {
		return nil, nil
	}
	paused, err := reader.PerformancePaused(ctx, ids)
	if err != nil {
		return nil, err
	}
	states := make(map[int64]PerformanceRoutingState, len(paused))
	for id, blocked := range paused {
		states[id] = PerformanceRoutingState{Blocked: blocked}
	}
	return states, nil
}

// Direct sticky lookups need the same live state as batched candidates.
func getPerformanceRoutingAccount(ctx context.Context, repo AccountRepository, id int64) (*Account, error) {
	a, err := repo.GetByID(ctx, id)
	if err != nil || a == nil {
		return a, err
	}
	states, err := performanceRoutingStates(ctx, repo, []int64{id})
	if err != nil {
		return nil, err
	}
	if state, ok := states[id]; ok {
		copy := *a
		copy.PerformancePaused, copy.PerformanceTrialDueAt = state.Blocked, state.TrialDueAt
		return &copy, nil
	}
	return a, nil
}

// Older due times take the next eligible request first. Break trial ties consistently across instances.
func comparePerformanceTrialDue(a, b *Account) int {
	aDue := a != nil && a.PerformanceTrialDueAt != nil
	bDue := b != nil && b.PerformanceTrialDueAt != nil
	switch {
	case aDue && bDue:
		return cmp.Or(a.PerformanceTrialDueAt.Compare(*b.PerformanceTrialDueAt), cmp.Compare(a.ID, b.ID))
	case aDue:
		return -1
	case bDue:
		return 1
	default:
		return 0
	}
}

func filterPerformanceCandidates(ctx context.Context, repo any, accounts []Account) ([]Account, error) {
	if len(accounts) == 0 {
		return accounts, nil
	}
	ids := make([]int64, 0, len(accounts))
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	states, err := performanceRoutingStates(ctx, repo, ids)
	if err != nil {
		return nil, err
	}
	if states == nil {
		return accounts, nil
	}
	out := make([]Account, 0, len(accounts))
	for _, a := range accounts {
		state, hasState := states[a.ID]
		if hasState {
			a.PerformancePaused = state.Blocked
			a.PerformanceTrialDueAt = state.TrialDueAt
		}
		if !a.PerformancePaused {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *SchedulerSnapshotService) filterPerformanceAccounts(ctx context.Context, accounts []Account) ([]Account, error) {
	return filterPerformanceCandidates(ctx, s.accountRepo, accounts)
}
func (s *SchedulerSnapshotService) markPerformanceAccounts(ctx context.Context, accounts map[int64]*Account) (map[int64]*Account, error) {
	if len(accounts) == 0 {
		return accounts, nil
	}
	ids := make([]int64, 0, len(accounts))
	for id := range accounts {
		ids = append(ids, id)
	}
	states, err := performanceRoutingStates(ctx, s.accountRepo, ids)
	if err != nil {
		return nil, err
	}
	if states == nil {
		return accounts, nil
	}
	out := make(map[int64]*Account, len(accounts))
	for id, a := range accounts {
		if a != nil {
			copy := *a
			state, hasState := states[id]
			if hasState {
				copy.PerformancePaused = state.Blocked
				copy.PerformanceTrialDueAt = state.TrialDueAt
			}
			out[id] = &copy
		}
	}
	return out, nil
}

func admitPerformanceRequest(ctx context.Context, repo any, accountID int64) (bool, error) {
	if admission, ok := repo.(PerformanceAdmission); ok {
		return admission.AdmitPerformanceRequest(ctx, accountID)
	}
	if reader, ok := repo.(PerformancePauseReader); ok {
		paused, err := reader.PerformancePaused(ctx, []int64{accountID})
		return !paused[accountID], err
	}
	return true, nil
}
