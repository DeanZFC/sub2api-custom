package service

// Account health is deliberately a small, opt-in observer around the existing
// scheduler state.  It does not create a second admission path and it never
// changes Account.Concurrency.  Automatic isolation is only allowed when the
// global setting is enabled and the account protection policy explicitly uses
// mode=enforce; all other accounts remain on their existing scheduling path.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	SettingKeyAccountHealthSettings = "account_health_settings"
	accountHealthReasonPrefix       = "health:"
	accountHealthAutoReasonPrefix   = "health:auto"
)

// AccountHealthSettings controls the optional account-health observer.
// Enabled defaults to false so upgrading an existing installation cannot
// change account scheduling until an administrator opts in.
type AccountHealthSettings struct {
	Enabled         bool    `json:"enabled"`
	WindowMinutes   int     `json:"window_minutes"`
	MinSamples      int     `json:"min_samples"`
	IsolateErrRate  float64 `json:"isolate_err_rate"`
	RecoverErrRate  float64 `json:"recover_err_rate"`
	CooldownMinutes int     `json:"cooldown_minutes"`
	IntervalSeconds int     `json:"interval_seconds"`
}

func DefaultAccountHealthSettings() AccountHealthSettings {
	return AccountHealthSettings{
		Enabled:         false,
		WindowMinutes:   10,
		MinSamples:      10,
		IsolateErrRate:  0.5,
		RecoverErrRate:  0.2,
		CooldownMinutes: 30,
		IntervalSeconds: 60,
	}
}

func normalizeAccountHealthSettings(in AccountHealthSettings) AccountHealthSettings {
	d := DefaultAccountHealthSettings()
	if in.WindowMinutes <= 0 {
		in.WindowMinutes = d.WindowMinutes
	}
	if in.MinSamples <= 0 {
		in.MinSamples = d.MinSamples
	}
	if in.IsolateErrRate <= 0 || in.IsolateErrRate > 1 {
		in.IsolateErrRate = d.IsolateErrRate
	}
	if in.RecoverErrRate < 0 || in.RecoverErrRate >= in.IsolateErrRate {
		in.RecoverErrRate = d.RecoverErrRate
	}
	if in.CooldownMinutes <= 0 {
		in.CooldownMinutes = d.CooldownMinutes
	}
	if in.IntervalSeconds < 10 {
		in.IntervalSeconds = d.IntervalSeconds
	}
	return in
}

// AccountHealthSnapshot is safe to expose to an administrator.  It contains
// no credentials or proxy data.
type AccountHealthSnapshot struct {
	AccountID     int64      `json:"account_id"`
	Name          string     `json:"name"`
	Platform      string     `json:"platform"`
	Score         int        `json:"score"`
	ErrRate       float64    `json:"err_rate"`
	AvgLatencyMs  *int       `json:"avg_latency_ms,omitempty"`
	Total         int64      `json:"total"`
	Errors        int64      `json:"errors"`
	State         string     `json:"state"` // healthy / degraded / isolated
	Isolated      bool       `json:"isolated"`
	IsolateReason string     `json:"isolate_reason,omitempty"`
	IsolatedUntil *time.Time `json:"isolated_until,omitempty"`
	EvaluatedAt   time.Time  `json:"evaluated_at"`
}

// AccountHealthAccountStore rechecks ownership and policy in the same database
// statement that changes scheduling state and enqueues its cache invalidation.
type AccountHealthAccountStore interface {
	TrySetAccountHealthIsolation(context.Context, int64, time.Time, string, bool) (bool, error)
	ClearAccountHealthIsolation(context.Context, int64, bool) (bool, error)
}

type AccountHealthService struct {
	db          *sql.DB
	accountRepo AccountHealthAccountStore
	settingRepo SettingRepository

	mu              sync.RWMutex
	snapshot        map[int64]AccountHealthSnapshot
	updated         time.Time
	stopCh          chan struct{}
	stopOnce        sync.Once
	startOnce       sync.Once
	settingsChanged chan struct{}
	wg              sync.WaitGroup
}

func NewAccountHealthService(db *sql.DB, accountRepo AccountHealthAccountStore, settingRepo SettingRepository) *AccountHealthService {
	return &AccountHealthService{
		db: db, accountRepo: accountRepo, settingRepo: settingRepo,
		snapshot: make(map[int64]AccountHealthSnapshot), stopCh: make(chan struct{}), settingsChanged: make(chan struct{}, 1),
	}
}

// ProvideAccountHealthService is intentionally not required for correctness;
// hosts may construct this service only when the feature is enabled.  Start is
// safe with nil dependencies and remains a no-op in that case.
func ProvideAccountHealthService(db *sql.DB, accountRepo AccountRepository, settingRepo SettingRepository) *AccountHealthService {
	healthRepo, _ := accountRepo.(AccountHealthAccountStore)
	svc := NewAccountHealthService(db, healthRepo, settingRepo)
	svc.Start()
	return svc
}

func (s *AccountHealthService) currentSettings() AccountHealthSettings {
	d := DefaultAccountHealthSettings()
	if s == nil || s.settingRepo == nil {
		return d
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAccountHealthSettings)
	if err != nil || strings.TrimSpace(raw) == "" {
		return d
	}
	var parsed AccountHealthSettings
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return d
	}
	return normalizeAccountHealthSettings(parsed)
}

func (s *AccountHealthService) GetSettings(context.Context) AccountHealthSettings {
	if s == nil {
		return DefaultAccountHealthSettings()
	}
	return s.currentSettings()
}

func (s *AccountHealthService) UpdateSettings(ctx context.Context, in AccountHealthSettings) (AccountHealthSettings, error) {
	norm := normalizeAccountHealthSettings(in)
	if s == nil || s.settingRepo == nil {
		return norm, errors.New("account health settings repository is unavailable")
	}
	raw, err := json.Marshal(norm)
	if err != nil {
		return norm, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyAccountHealthSettings, string(raw)); err != nil {
		return norm, err
	}
	select {
	case s.settingsChanged <- struct{}{}:
	default:
	}
	return norm, nil
}

func (s *AccountHealthService) Snapshot() []AccountHealthSnapshot {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]AccountHealthSnapshot, 0, len(s.snapshot))
	for _, value := range s.snapshot {
		out = append(out, value)
	}
	return out
}

func (s *AccountHealthService) Start() {
	if s == nil || s.db == nil || s.accountRepo == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			timer := time.NewTimer(time.Duration(s.currentSettings().IntervalSeconds) * time.Second)
			defer timer.Stop()
			// Do not query or mutate anything while disabled.
			s.runOnce()
			for {
				select {
				case <-timer.C:
					s.runOnce()
					timer.Reset(time.Duration(s.currentSettings().IntervalSeconds) * time.Second)
				case <-s.settingsChanged:
					s.runOnce()
					timer.Reset(time.Duration(s.currentSettings().IntervalSeconds) * time.Second)
				case <-s.stopCh:
					return
				}
			}
		}()
	})
}

func (s *AccountHealthService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
}

type accountHealthRow struct {
	id, ok, err    int64
	name, platform string
	avgMs          sql.NullFloat64
	until          sql.NullTime
	reason         sql.NullString
	canIsolate     bool
}

func (s *AccountHealthService) runOnce() {
	cfg := s.currentSettings()
	if !cfg.Enabled || s == nil || s.db == nil || s.accountRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := s.queryWindowStats(ctx, cfg.WindowMinutes)
	if err != nil {
		return
	}
	now := time.Now()
	next := make(map[int64]AccountHealthSnapshot, len(rows))
	for _, row := range rows {
		snap := decideAccountHealth(row, cfg, now)
		if row.healthIsolated(now) {
			// Manual isolation is an administrator decision and never expires
			// early merely because the observation window becomes healthy.
			snap.State, snap.Isolated = "isolated", true
			if strings.HasPrefix(row.reason.String, accountHealthAutoReasonPrefix) && row.ok+row.err >= int64(cfg.MinSamples) && snap.ErrRate <= cfg.RecoverErrRate {
				if changed, err := s.accountRepo.ClearAccountHealthIsolation(ctx, row.id, true); err == nil && changed {
					snap.State, snap.Isolated, snap.IsolateReason, snap.IsolatedUntil = "healthy", false, "", nil
				}
			}
		} else if snap.State == "isolated" {
			// A recommendation is not an actual scheduler isolation. The
			// conditional write may lose to a newer cooldown or disabled policy.
			snap.State, snap.Isolated = "degraded", false
			if row.canIsolate && (!row.until.Valid || !row.until.Time.After(now)) {
				until := now.Add(time.Duration(cfg.CooldownMinutes) * time.Minute)
				reason := accountHealthAutoReasonPrefix + " err_rate=" + formatAccountHealthRate(snap.ErrRate)
				if changed, err := s.accountRepo.TrySetAccountHealthIsolation(ctx, row.id, until, reason, true); err == nil && changed {
					snap.State, snap.Isolated, snap.IsolateReason, snap.IsolatedUntil = "isolated", true, reason, &until
				}
			}
		}
		next[row.id] = snap
	}
	s.mu.Lock()
	s.snapshot, s.updated = next, now
	s.mu.Unlock()
}

func (s *AccountHealthService) queryWindowStats(ctx context.Context, windowMinutes int) ([]accountHealthRow, error) {
	// The request-path/error-phase filters keep scheduled tests, probes and
	// monitoring calls from turning into account-health failures. #>> avoids a
	// cast on malformed legacy JSON in accounts.extra.
	const query = `
SELECT a.id, COALESCE(a.name, ''), COALESCE(a.platform, ''),
       COALESCE(u.ok_count, 0), u.avg_ms, COALESCE(e.err_count, 0),
       a.temp_unschedulable_until, a.temp_unschedulable_reason,
       COALESCE(
         (a.extra #> '{account_protection_policy,enabled}') = 'true'::jsonb
         AND lower(btrim(a.extra #>> '{account_protection_policy,mode}')) = 'enforce',
         FALSE
       )
FROM accounts a
LEFT JOIN (
  SELECT account_id, COUNT(*) AS ok_count, AVG(duration_ms) AS avg_ms
  FROM usage_logs
  WHERE created_at >= NOW() - make_interval(mins => $1)
    AND account_id IS NOT NULL AND account_id > 0
  GROUP BY account_id
) u ON u.account_id = a.id
LEFT JOIN (
  SELECT account_id, COUNT(*) AS err_count
  FROM ops_error_logs
  WHERE created_at >= NOW() - make_interval(mins => $1)
    AND COALESCE(status_code, upstream_status_code, 0) >= 400
    AND NOT COALESCE(is_business_limited, FALSE)
    AND COALESCE(error_owner, 'provider') <> 'client'
    AND account_id IS NOT NULL AND account_id > 0
    AND COALESCE(error_phase, '') NOT IN ('test', 'scheduled_test', 'probe', 'monitor')
    AND COALESCE(request_path, '') NOT ILIKE '%/test%'
    AND COALESCE(request_path, '') NOT ILIKE '%probe%'
    AND COALESCE(request_path, '') NOT ILIKE '%monitor%'
  GROUP BY account_id
) e ON e.account_id = a.id
WHERE a.deleted_at IS NULL
  AND (u.account_id IS NOT NULL OR e.account_id IS NOT NULL
       OR COALESCE(a.temp_unschedulable_reason, '') LIKE 'health:%')`
	rows, err := s.db.QueryContext(ctx, query, windowMinutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []accountHealthRow
	for rows.Next() {
		var row accountHealthRow
		if err := rows.Scan(&row.id, &row.name, &row.platform, &row.ok, &row.avgMs, &row.err, &row.until, &row.reason, &row.canIsolate); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r accountHealthRow) healthIsolated(now time.Time) bool {
	return r.until.Valid && r.until.Time.After(now) && r.reason.Valid && strings.HasPrefix(r.reason.String, accountHealthReasonPrefix)
}

func decideAccountHealth(r accountHealthRow, cfg AccountHealthSettings, now time.Time) AccountHealthSnapshot {
	total := r.ok + r.err
	snap := AccountHealthSnapshot{AccountID: r.id, Name: r.name, Platform: r.platform, Total: total, Errors: r.err, Score: 100, State: "healthy", EvaluatedAt: now}
	if total > 0 {
		snap.ErrRate = float64(r.err) / float64(total)
		snap.Score = int(math.Round(100 * (1 - snap.ErrRate)))
		if total >= int64(cfg.MinSamples) {
			switch {
			case snap.ErrRate >= cfg.IsolateErrRate:
				snap.State = "isolated"
			case snap.ErrRate > cfg.RecoverErrRate:
				snap.State = "degraded"
			}
		}
	}
	if r.avgMs.Valid {
		value := int(math.Round(r.avgMs.Float64))
		snap.AvgLatencyMs = &value
	}
	if r.until.Valid {
		u := r.until.Time
		snap.IsolatedUntil = &u
	}
	if r.reason.Valid {
		snap.IsolateReason = r.reason.String
	}
	snap.Isolated = r.healthIsolated(now) || snap.State == "isolated"
	return snap
}

func formatAccountHealthRate(value float64) string {
	return strconv.FormatFloat(math.Round(value*1000)/10, 'f', 1, 64) + "%"
}

// Isolate and Resume never replace or clear another subsystem's cooldown.
func (s *AccountHealthService) Isolate(ctx context.Context, id int64) error {
	if s == nil || s.accountRepo == nil || id <= 0 {
		return errors.New("account health service is unavailable")
	}
	changed, err := s.accountRepo.TrySetAccountHealthIsolation(ctx, id, time.Now().Add(24*time.Hour), accountHealthReasonPrefix+"manual", false)
	if err != nil {
		return err
	}
	if !changed {
		return infraerrors.Conflict("ACCOUNT_HEALTH_ISOLATION_CONFLICT", "账号状态已变化或存在其他功能的冷却，请刷新后重试")
	}
	return nil
}

func (s *AccountHealthService) Resume(ctx context.Context, id int64) error {
	if s == nil || s.accountRepo == nil || id <= 0 {
		return errors.New("account health service is unavailable")
	}
	if _, err := s.accountRepo.ClearAccountHealthIsolation(ctx, id, false); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.snapshot, id)
	s.mu.Unlock()
	return nil
}
