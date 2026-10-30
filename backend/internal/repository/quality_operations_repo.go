package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type qualityOperationsRepository struct{ db *sql.DB }

func NewQualityOperationsRepository(db *sql.DB) service.QualityOperationsRepository {
	return &qualityOperationsRepository{db: db}
}

type qualityQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func qualityConfig(ctx context.Context, q qualityQuery, lock bool) (service.QualityOperationsConfig, error) {
	var c service.QualityOperationsConfig
	var raw []byte
	query := `SELECT version,config FROM quality_operations_config WHERE id=1`
	if lock {
		query += ` FOR SHARE`
	}
	err := q.QueryRowContext(ctx, query).Scan(&c.Version, &raw)
	if err != nil {
		return c, err
	}
	version := c.Version
	err = json.Unmarshal(raw, &c)
	c.Version = version
	return c, err
}
func (r *qualityOperationsRepository) SaveOperationsConfig(ctx context.Context, c *service.QualityOperationsConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	err = r.db.QueryRowContext(ctx, `UPDATE quality_operations_config SET config=$1,version=version+1,updated_at=now() WHERE id=1 AND version=$2 RETURNING version`, raw, c.Version).Scan(&c.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrQualityOperationsConflict
	}
	return err
}
func (r *qualityOperationsRepository) OperationsSnapshot(ctx context.Context) (*service.QualityOperationsSnapshot, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := qualityConfig(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	out := &service.QualityOperationsSnapshot{Config: c, Day: day, ResetsAt: now.Truncate(24 * time.Hour).Add(24 * time.Hour), Usage: []service.QualityBudgetUsage{}}
	rows, err := tx.QueryContext(ctx, `SELECT account_id,requests,tokens,unknown_requests FROM quality_budget_days WHERE day=$1 ORDER BY account_id`, day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u service.QualityBudgetUsage
		if err = rows.Scan(&u.AccountID, &u.Requests, &u.Tokens, &u.UnknownRequests); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out.Usage = append(out.Usage, u)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
func (r *qualityOperationsRepository) ReserveQualityProbe(ctx context.Context, account, tokens int64, automatic bool, session string) (*service.QualityProbeReservation, error) {
	if account <= 0 || tokens < 0 {
		return nil, fmt.Errorf("invalid quality probe admission")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := qualityConfig(ctx, tx, true)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	day := now.Format("2006-01-02")
	if tokens < c.TokenReservation {
		tokens = c.TokenReservation
	}
	for _, id := range []int64{0, account} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO quality_budget_days(day,account_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, day, id); err != nil {
			return nil, err
		}
		var requests, spent int64
		if err = tx.QueryRowContext(ctx, `SELECT requests,tokens FROM quality_budget_days WHERE day=$1 AND account_id=$2 FOR UPDATE`, day, id).Scan(&requests, &spent); err != nil {
			return nil, err
		}
		maxRequests, maxTokens := c.DailyRequests, c.DailyTokens
		if id != 0 {
			maxRequests, maxTokens = c.AccountDailyRequests, c.AccountDailyTokens
		}
		if c.BudgetEnabled && ((maxRequests > 0 && requests >= maxRequests) || (maxTokens > 0 && (tokens > maxTokens || spent > maxTokens-tokens))) {
			return nil, &service.QualityDeferredError{Reason: "今日检测预算不足", Until: now.Truncate(24 * time.Hour).Add(24 * time.Hour)}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE quality_budget_days SET requests=requests+1,tokens=tokens+$3,unknown_requests=unknown_requests+1 WHERE day=$1 AND account_id=$2`, day, id, tokens); err != nil {
			return nil, err
		}
	}
	res := &service.QualityProbeReservation{AccountID: account}
	// Persist each reservation before an upstream request starts.
	if err = tx.QueryRowContext(ctx, `INSERT INTO quality_probe_attempts(account_id,day,reserved_tokens,started_at) VALUES($1,$2,$3,clock_timestamp()) RETURNING id`, account, day, tokens).Scan(&res.ID); err != nil {
		return nil, err
	}
	return res, tx.Commit()
}
func (r *qualityOperationsRepository) SettleQualityProbe(ctx context.Context, res *service.QualityProbeReservation, o service.QualityProbeOutcome) error {
	if res == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var day time.Time
	var reserved, account int64
	var settled bool
	err = tx.QueryRowContext(ctx, `SELECT day,reserved_tokens,settled,account_id FROM quality_probe_attempts WHERE id=$1 FOR UPDATE`, res.ID).Scan(&day, &reserved, &settled, &account)
	if err != nil {
		return err
	}
	if settled {
		return nil
	}
	if o.Tokens != nil && *o.Tokens >= 0 {
		for _, id := range []int64{0, account} {
			if _, err = tx.ExecContext(ctx, `UPDATE quality_budget_days SET tokens=tokens+$3,unknown_requests=unknown_requests-1 WHERE day=$1 AND account_id=$2`, day, id, *o.Tokens-reserved); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE quality_probe_attempts SET settled=true,actual_tokens=$2,kind=$3 WHERE id=$1`, res.ID, o.Tokens, o.Kind); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *qualityOperationsRepository) QualityFrequencyDue(ctx context.Context, plan, account int64, key string) (*time.Time, error) {
	needed, err := scheduledTestAllPassNeedsFreshRound(ctx, r.db, plan, account)
	if err != nil {
		return nil, err
	}
	if needed {
		return nil, nil
	}
	var next time.Time
	err = r.db.QueryRowContext(ctx, `SELECT f.next_at FROM quality_healthy_frequency f JOIN quality_operations_config c ON c.id=1 AND c.version=f.config_version AND c.config->>'dynamic_enabled'='true'
 WHERE f.plan_id=$1 AND f.account_id=$2 AND f.policy_key=$3 AND f.streak>=(c.config->>'healthy_rounds')::integer AND f.next_at>now()
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=$2 AND s.blocked)`, plan, account, key).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &next, nil
}
func (r *qualityOperationsRepository) RecordQualityFrequency(ctx context.Context, plan *service.ScheduledTestPlan, result *service.ScheduledTestResult, key string, healthy bool) error {
	if result.AccountID == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := qualityConfig(ctx, tx, true)
	if err != nil {
		return err
	}
	if !c.DynamicEnabled {
		return nil
	}
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT latest_run_id=$2 AND updated_at=$3 FROM scheduled_test_plans WHERE id=$1 FOR UPDATE`, plan.ID, result.RunID, plan.UpdatedAt).Scan(&current); err != nil {
		return err
	}
	if !current {
		return nil
	}
	var streak int
	var oldKey string
	var version int64
	var observed time.Time
	err = tx.QueryRowContext(ctx, `SELECT policy_key,config_version,streak,observed_at FROM quality_healthy_frequency WHERE plan_id=$1 AND account_id=$2 FOR UPDATE`, plan.ID, *result.AccountID).Scan(&oldKey, &version, &streak, &observed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !observed.IsZero() && !observed.Before(result.StartedAt) {
		return nil
	}
	if oldKey != key || version != c.Version {
		streak = 0
	}
	if healthy {
		streak++
	} else {
		streak = 0
	}
	// Outstanding capability holds prevent healthy active tests
	// from slowing the fresh probes needed to recover a degraded account.
	var anomaly bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=$1 AND s.blocked)`, *result.AccountID).Scan(&anomaly); err != nil {
		return err
	}
	if anomaly {
		streak = 0
	}
	needed, err := scheduledTestAllPassNeedsFreshRound(ctx, tx, plan.ID, *result.AccountID)
	if err != nil {
		return err
	}
	if needed {
		streak = 0
	}
	next := time.Now().UTC().Add(c.HealthyDelay(streak))
	if _, err = tx.ExecContext(ctx, `INSERT INTO quality_healthy_frequency(plan_id,account_id,policy_key,config_version,streak,next_at,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7)
 ON CONFLICT(plan_id,account_id) DO UPDATE SET policy_key=excluded.policy_key,config_version=excluded.config_version,streak=excluded.streak,next_at=excluded.next_at,observed_at=excluded.observed_at`, plan.ID, *result.AccountID, key, c.Version, streak, next, result.StartedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// Keep detailed admissions for 30 days and daily aggregates for 90 days. The
// batches are bounded so maintenance cannot monopolize the quality worker.
func (r *qualityOperationsRepository) PruneQualityOperations(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM quality_probe_attempts WHERE id IN (SELECT id FROM quality_probe_attempts WHERE started_at<now()-INTERVAL '30 days' ORDER BY started_at LIMIT 5000)`); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM quality_budget_days WHERE (day,account_id) IN (SELECT day,account_id FROM quality_budget_days WHERE day<(now() AT TIME ZONE 'UTC')::date-90 ORDER BY day,account_id LIMIT 1000)`)
	return err
}
