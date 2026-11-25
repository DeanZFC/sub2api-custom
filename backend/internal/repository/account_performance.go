package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountPerformanceRepository struct{ db *sql.DB }

func NewAccountPerformanceRepository(db *sql.DB) service.AccountPerformanceRepository {
	return &accountPerformanceRepository{db: db}
}

const performanceStateColumns = `a.id,COALESCE(m.config,'{}'),COALESCE(m.revision,0),COALESCE(m.paused,false),
 COALESCE(m.pause_generation,0),m.paused_at,COALESCE(m.reason,''),m.trial_started_at,
 COALESCE(m.trial_requests,0),m.metrics_since`

const performanceMetricsSQL = `SELECT ` + performanceStateColumns + `
FROM accounts a LEFT JOIN account_performance_monitors m ON a.id=m.account_id
WHERE a.id=ANY($1) AND a.deleted_at IS NULL`

func scanPerformanceState(row interface{ Scan(...any) error }) (service.AccountPerformance, error) {
	m := service.AccountPerformance{Config: service.DefaultPerformanceConfig(), MonitoringAvailable: true}
	var raw []byte
	if err := row.Scan(&m.AccountID, &raw, &m.Revision, &m.Paused, &m.PauseGeneration, &m.PausedAt, &m.Reason, &m.TrialStartedAt, &m.TrialRequests, &m.MetricsSince); err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m.Config); err != nil {
		return m, err
	}
	m.DescribeState()
	return m, nil
}

func (r *accountPerformanceRepository) Read(ctx context.Context, ids []int64, now time.Time, includeMetrics bool) (map[int64]service.AccountPerformance, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := make(map[int64]service.AccountPerformance, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, performanceMetricsSQL, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		m, err := scanPerformanceState(rows)
		if err != nil {
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
	if !includeMetrics {
		return out, nil
	}
	// Most accounts share the rolling hour. Trial/recovered accounts use a fresh
	// baseline so old failures cannot immediately pause a recovered account again.
	type window struct{ start, end time.Time }
	windows := map[window][]int64{}
	for id, m := range out {
		start, end := m.MetricWindow(now)
		w := window{start, end}
		windows[w] = append(windows[w], id)
	}
	for w, ids := range windows {
		stats, err := readOpsAccountRequestStats(ctx, r.db, ids, w.start, w.end)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			m := out[id]
			m.Metrics = service.OpsAccountRequestStats{WindowStart: w.start, WindowEnd: w.end}
			if entry := stats[strconv.FormatInt(id, 10)]; entry != nil {
				m.Metrics = *entry
			}
			out[id] = m
		}
	}
	return out, nil
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
func performanceRoutingStates(ctx context.Context, db sqlExecutor, ids []int64) (map[int64]service.PerformanceRoutingState, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := map[int64]service.PerformanceRoutingState{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `SELECT account_id, CASE WHEN `+trialEligibleSQL+`
 THEN COALESCE(next_trial_at,trial_started_at) END FROM account_performance_monitors WHERE paused AND account_id=ANY($1)`,
		pq.Array(ids), service.PerformanceTrialDuration.Seconds(), service.PerformanceTrialBudgetMultiplier)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var due *time.Time
		if err := rows.Scan(&id, &due); err != nil {
			return nil, err
		}
		out[id] = service.PerformanceRoutingState{Blocked: due == nil, TrialDueAt: due}
	}
	return out, rows.Err()
}
func (r *accountRepository) PerformanceRoutingStates(ctx context.Context, ids []int64) (map[int64]service.PerformanceRoutingState, error) {
	return performanceRoutingStates(ctx, r.sql, ids)
}
func performancePaused(ctx context.Context, db sqlExecutor, ids []int64) (map[int64]bool, error) {
	states, err := performanceRoutingStates(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	paused := make(map[int64]bool, len(states))
	for id, state := range states {
		if state.Blocked {
			paused[id] = true
		}
	}
	return paused, nil
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
	m, err := scanPerformanceState(tx.QueryRowContext(ctx, `SELECT `+performanceStateColumns+`
 FROM accounts a JOIN account_performance_monitors m ON a.id=m.account_id WHERE m.account_id=$1 FOR UPDATE OF m SKIP LOCKED`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.Phase == service.PerformanceDisabled {
		return nil
	}
	if err = m.Config.Validate(); err != nil {
		return err
	}
	if m.Phase == service.PerformancePaused {
		if m.RecoverAfter == nil || now.Before(*m.RecoverAfter) {
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET trial_started_at=$2,metrics_since=$2,trial_requests=0,next_trial_at=$2,updated_at=NOW() WHERE account_id=$1`, id, now)
		if err != nil {
			return err
		}
		if err = performanceAudit(ctx, tx, id, "trial", 0, m.Revision, m.PauseGeneration, []byte(`{}`)); err != nil {
			return err
		}
		return tx.Commit()
	}
	start, end := m.MetricWindow(now)
	metrics, err := readOpsAccountRequestStats(ctx, tx, []int64{id}, start, end)
	if err != nil {
		return err
	}
	m.Metrics = service.OpsAccountRequestStats{WindowStart: start, WindowEnd: end}
	if entry := metrics[strconv.FormatInt(id, 10)]; entry != nil {
		m.Metrics = *entry
	}
	action, reason := m.MetricDecision(now)
	gen := m.PauseGeneration
	switch action {
	case "pause":
		gen++
		_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=true,pause_generation=pause_generation+1,paused_at=$2,reason=$3,trial_started_at=NULL,trial_requests=0,next_trial_at=NULL,updated_at=NOW() WHERE account_id=$1`, id, now, reason)
	case "recover":
		_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=false,paused_at=NULL,reason='',trial_started_at=NULL,trial_requests=0,next_trial_at=NULL,updated_at=NOW() WHERE account_id=$1`, id)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(struct {
		service.AccountPerformance
		DecisionReason string `json:"decision_reason,omitempty"`
	}{m, reason})
	if err != nil {
		return err
	}
	if err = performanceAudit(ctx, tx, id, action, 0, m.Revision, gen, evidence); err != nil {
		return err
	}
	return tx.Commit()
}

const trialEligibleSQL = `trial_started_at IS NOT NULL
 AND trial_started_at + make_interval(secs => $2) > NOW()
 AND COALESCE(next_trial_at,trial_started_at) <= NOW()
 AND trial_requests < (config->>'recovery_samples')::int * $3`

// The UPDATE rechecks its predicate under the row lock when gateways race.
// Healthy accounts, including accounts with no rules, never take a write lock.
const performanceTrialSQL = `WITH admitted AS (
 UPDATE account_performance_monitors SET trial_requests=trial_requests+1,next_trial_at=NOW()+INTERVAL '5 seconds'
 WHERE account_id=$1 AND paused AND ` + trialEligibleSQL + ` RETURNING 1
) SELECT EXISTS (SELECT 1 FROM admitted) OR NOT EXISTS (
 SELECT 1 FROM account_performance_monitors WHERE account_id=$1 AND paused)`

func (r *accountRepository) AdmitPerformanceRequest(ctx context.Context, id int64) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := r.sql.QueryContext(ctx, performanceTrialSQL, id, service.PerformanceTrialDuration.Seconds(), service.PerformanceTrialBudgetMultiplier)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	var allowed bool
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, sql.ErrNoRows
	}
	if err := rows.Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, rows.Close()
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
		e.ReadPauseReason()
		out = append(out, e)
	}
	return out, rows.Err()
}
