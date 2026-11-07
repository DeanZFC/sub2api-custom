package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountPerformanceRepository struct{ db *sql.DB }

func NewAccountPerformanceRepository(db *sql.DB) service.AccountPerformanceRepository {
	return &accountPerformanceRepository{db: db}
}

const performanceMetricsSQL = `
SELECT a.id,COALESCE(m.config,'{}'),COALESCE(m.revision,0),COALESCE(m.paused,false),
 COALESCE(m.pause_generation,0),m.paused_at,COALESCE(m.reason,''),m.trial_started_at,
 COALESCE(m.trial_requests,0),m.metrics_since
FROM accounts a LEFT JOIN account_performance_monitors m ON m.account_id=a.id
WHERE a.id=ANY($1) AND a.deleted_at IS NULL`

func readAccountPerformance(ctx context.Context, db sqlExecutor, ids []int64, now time.Time) (map[int64]service.AccountPerformance, error) {
	out := make(map[int64]service.AccountPerformance, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, performanceMetricsSQL, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		m := service.AccountPerformance{Config: service.DefaultPerformanceConfig(), MonitoringAvailable: true}
		var raw []byte
		if err = rows.Scan(&m.AccountID, &raw, &m.Revision, &m.Paused, &m.PauseGeneration, &m.PausedAt, &m.Reason, &m.TrialStartedAt, &m.TrialRequests, &m.MetricsSince); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &m.Config); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[m.AccountID] = m
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	// Most accounts share the rolling hour. Trial/recovered accounts use a fresh
	// baseline so old failures cannot immediately pause a recovered account again.
	windows := map[time.Time][]int64{}
	for id, m := range out {
		since := now.Add(-time.Hour)
		if m.MetricsSince != nil && m.MetricsSince.After(since) {
			since = *m.MetricsSince
		}
		windows[since] = append(windows[since], id)
	}
	for since, ids := range windows {
		stats, err := readOpsAccountRequestStats(ctx, db, ids, since, now)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			m := out[id]
			m.Metrics = service.OpsAccountRequestStats{WindowStart: since, WindowEnd: now}
			if entry := stats[strconv.FormatInt(id, 10)]; entry != nil {
				m.Metrics = *entry
			}
			out[id] = m
		}
	}
	return out, nil
}
func (r *accountPerformanceRepository) Read(ctx context.Context, ids []int64, now time.Time) (map[int64]service.AccountPerformance, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return readAccountPerformance(ctx, r.db, ids, now)
}
func (r *accountPerformanceRepository) EnabledIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT m.account_id FROM account_performance_monitors m JOIN accounts a ON a.id=m.account_id WHERE m.config->>'enabled'='true' AND a.deleted_at IS NULL ORDER BY m.account_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func performancePaused(ctx context.Context, db sqlExecutor, ids []int64) (map[int64]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT account_id FROM account_performance_monitors WHERE paused AND account_id=ANY($1) AND
 (trial_started_at IS NULL OR next_trial_at>NOW() OR trial_started_at+INTERVAL '10 minutes'<=NOW()
 OR trial_requests >= (config->>'recovery_samples')::int*3)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
func (r *accountPerformanceRepository) Paused(ctx context.Context, ids []int64) (map[int64]bool, error) {
	return performancePaused(ctx, r.db, ids)
}
func (r *accountRepository) PerformancePaused(ctx context.Context, ids []int64) (map[int64]bool, error) {
	return performancePaused(ctx, r.sql, ids)
}
func (r *accountPerformanceRepository) Save(ctx context.Context, id int64, c service.PerformanceConfig, revision, actor int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockPerformanceAccount(ctx, tx, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO account_performance_monitors(account_id) VALUES($1) ON CONFLICT DO NOTHING`, id)
	if err != nil {
		return err
	}
	var gen int64
	err = tx.QueryRowContext(ctx, `UPDATE account_performance_monitors SET config=$2,revision=revision+1,paused=false,paused_at=NULL,reason='',trial_started_at=NULL,trial_requests=0,next_trial_at=NULL,metrics_since=NULL,updated_at=NOW() WHERE account_id=$1 AND revision=$3 RETURNING pause_generation`, id, string(raw), revision).Scan(&gen)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPerformanceConflict
	}
	if err != nil {
		return err
	}
	if err = performanceAudit(ctx, tx, id, "configure", actor, revision+1, gen, raw); err != nil {
		return err
	}
	return tx.Commit()
}
func performanceAudit(ctx context.Context, tx *sql.Tx, id int64, action string, actor, revision, gen int64, evidence []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO account_performance_events(account_id,action,actor_id,revision,pause_generation,evidence) VALUES($1,$2,NULLIF($3::bigint,0),$4,$5,$6)`, id, action, actor, revision, gen, string(evidence))
	return err
}

// Soft deletion changes a non-key column: KEY SHARE would allow it through.
// All monitor mutations lock account -> monitor, holding the account active
// until the state change and audit commit together.
func lockPerformanceAccount(ctx context.Context, tx *sql.Tx, id int64) error {
	var existing int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPerformanceConflict
	}
	return err
}

func (r *accountPerformanceRepository) Release(ctx context.Context, id, revision, generation, actor int64) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockPerformanceAccount(ctx, tx, id); err != nil {
		return err
	}
	// Explicit release disables evaluation atomically; no old evaluator can re-pause.
	res, err := tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=false,paused_at=NULL,reason='',trial_started_at=NULL,trial_requests=0,next_trial_at=NULL,metrics_since=NULL,config=jsonb_set(config,'{enabled}','false'),revision=revision+1,updated_at=NOW() WHERE account_id=$1 AND revision=$2 AND pause_generation=$3 AND paused`, id, revision, generation)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrPerformanceConflict
	}
	if err = performanceAudit(ctx, tx, id, "release", actor, revision+1, generation, []byte(`{"enabled":false}`)); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *accountPerformanceRepository) Evaluate(ctx context.Context, id int64, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockPerformanceAccount(ctx, tx, id); err != nil {
		if errors.Is(err, service.ErrPerformanceConflict) {
			return nil
		}
		return err
	}
	var raw []byte
	var revision, gen int64
	var paused bool
	var pausedAt, trial *time.Time
	err = tx.QueryRowContext(ctx, `SELECT config,revision,paused,pause_generation,paused_at,trial_started_at FROM account_performance_monitors WHERE account_id=$1 FOR UPDATE SKIP LOCKED`, id).Scan(&raw, &revision, &paused, &gen, &pausedAt, &trial)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var cfg service.PerformanceConfig
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	if err = cfg.Validate(); err != nil {
		return err
	}
	if paused && trial == nil {
		if !cfg.AutoRecover || pausedAt == nil || now.Before(pausedAt.Add(time.Duration(cfg.CooldownMinutes)*time.Minute)) {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET trial_started_at=$2,metrics_since=$2,trial_requests=0,next_trial_at=$2,updated_at=NOW() WHERE account_id=$1`, id, now)
		if err != nil {
			return err
		}
		if err = performanceAudit(ctx, tx, id, "trial", 0, revision, gen, []byte(`{}`)); err != nil {
			return err
		}
		return tx.Commit()
	}
	metrics, err := readAccountPerformance(ctx, tx, []int64{id}, now)
	if err != nil {
		return err
	}
	m, ok := metrics[id]
	if !ok {
		return nil
	}
	reasons := m.Breaches()
	action := "pause"
	if trial != nil {
		if len(reasons) == 0 && now.Before(trial.Add(10*time.Minute)) && m.ReadyForRecovery() {
			_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=false,paused_at=NULL,reason='',trial_started_at=NULL,trial_requests=0,next_trial_at=NULL,updated_at=NOW() WHERE account_id=$1`, id)
			action = "recover"
		} else if len(reasons) == 0 && now.Before(trial.Add(10*time.Minute)) {
			return nil
		} else if len(reasons) == 0 {
			reasons = []string{"trial_timeout"}
		}
	} else if len(reasons) == 0 {
		return nil
	}
	if action == "pause" {
		gen++
		_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=true,pause_generation=pause_generation+1,paused_at=$2,reason=$3,trial_started_at=NULL,trial_requests=0,next_trial_at=NULL,updated_at=NOW() WHERE account_id=$1`, id, now, strings.Join(reasons, ","))
	}
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err = performanceAudit(ctx, tx, id, action, 0, revision, gen, evidence); err != nil {
		return err
	}
	return tx.Commit()
}

// Row locking and the persisted rate/budget gate apply to every gateway instance.
// At most one trial request is admitted every five seconds, with a finite budget.
const performanceTrialSQL = `WITH candidate AS MATERIALIZED (
 SELECT * FROM account_performance_monitors WHERE account_id=$1 AND paused FOR UPDATE
), admitted AS (
 UPDATE account_performance_monitors m SET trial_requests=m.trial_requests+1,next_trial_at=NOW()+INTERVAL '5 seconds'
 FROM candidate c WHERE m.account_id=c.account_id AND c.paused AND c.trial_started_at IS NOT NULL
 AND c.trial_started_at+INTERVAL '10 minutes'>NOW() AND c.next_trial_at<=NOW()
 AND c.trial_requests < (c.config->>'recovery_samples')::int*3
 RETURNING m.pause_generation,m.revision
)
SELECT COALESCE((SELECT NOT paused FROM candidate),true) OR EXISTS(SELECT 1 FROM admitted),
 COALESCE((SELECT pause_generation FROM admitted),-1),COALESCE((SELECT revision FROM admitted),-1)`

func (r *accountRepository) AcquirePerformanceTrial(ctx context.Context, id int64) (bool, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := r.sql.QueryContext(ctx, performanceTrialSQL, id)
	if err != nil {
		return false, nil, err
	}
	var allowed bool
	var gen, revision int64
	if !rows.Next() {
		err = rows.Err()
		_ = rows.Close()
		if err == nil {
			err = sql.ErrNoRows
		}
		return false, nil, err
	}
	err = rows.Scan(&allowed, &gen, &revision)
	closeErr := rows.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return false, nil, err
	}
	if !allowed || gen < 0 {
		return allowed, nil, nil
	}
	var once sync.Once
	refund := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			// Revision + generation prevent a late refund from modifying a newer trial.
			_, _ = r.sql.ExecContext(ctx, `UPDATE account_performance_monitors SET trial_requests=GREATEST(trial_requests-1,0) WHERE account_id=$1 AND revision=$2 AND pause_generation=$3 AND trial_started_at IS NOT NULL`, id, revision, gen)
		})
	}
	return true, refund, nil
}

func (r *accountPerformanceRepository) Events(ctx context.Context, id int64) ([]service.PerformanceEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,action,actor_id,revision,pause_generation,evidence,created_at FROM account_performance_events WHERE account_id=$1 ORDER BY id DESC LIMIT 50`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]service.PerformanceEvent, 0)
	for rows.Next() {
		var e service.PerformanceEvent
		if err := rows.Scan(&e.ID, &e.Action, &e.ActorID, &e.Revision, &e.PauseGeneration, &e.Evidence, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
