package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamstats"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type accountPerformanceRepository struct{ db *sql.DB }

func NewAccountPerformanceRepository(db *sql.DB) service.AccountPerformanceRepository {
	return &accountPerformanceRepository{db: db}
}

// This custom build exposes the HTTP protection requested by administrators.
// Detailed request/probe evidence belongs to a separate upstream subsystem;
// absent evidence stays NULL and is never inferred from billed usage rows.
const performanceMetricsSQL = `
WITH source AS (SELECT unnest($1::bigint[]) AS id)
SELECT ids.id,NULL::numeric,0::bigint,NULL::numeric,0::bigint,NULL::numeric,0::bigint,
 COALESCE(m.config,'{}'),COALESCE(m.revision,0),COALESCE(m.paused,false),COALESCE(m.pause_generation,0),m.paused_at,COALESCE(m.reason,'')
FROM source ids
JOIN accounts a ON a.id=ids.id AND a.deleted_at IS NULL
LEFT JOIN account_performance_monitors m ON m.account_id=ids.id
WHERE $2::timestamptz <= $3::timestamptz
`

func readAccountPerformanceWindowMinutes(ctx context.Context, db sqlExecutor) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.SettingKeyAccountPerformanceWindowMinutes)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var value string
	if rows.Next() {
		if err := rows.Scan(&value); err != nil {
			return 0, err
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return service.ParseAccountPerformanceWindowMinutes(value)
}

func readAccountPerformance(ctx context.Context, db sqlExecutor, ids []int64, now time.Time) (map[int64]service.AccountPerformance, error) {
	out := make(map[int64]service.AccountPerformance, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	minutes, err := readAccountPerformanceWindowMinutes(ctx, db)
	if err != nil {
		return nil, err
	}
	since := now.Add(-time.Duration(minutes) * time.Minute)
	rows, err := db.QueryContext(ctx, performanceMetricsSQL, pq.Array(ids), since, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m service.AccountPerformance
		var config []byte
		if err := rows.Scan(&m.AccountID, &m.AvgTPS, &m.TPSSamples, &m.AvgTTFTMs, &m.TTFTSamples, &m.DegradationPercent, &m.QualitySamples, &config, &m.Revision, &m.Paused, &m.PauseGeneration, &m.PausedAt, &m.Reason); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(config, &m.Config); err != nil {
			return nil, err
		}
		m.WindowStart, m.WindowEnd = since, now
		out[m.AccountID] = m
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Read and the transactional evaluator share the exact process-local
	// response store and configured window, including incomplete windows.
	evidence := upstreamstats.Default.Snapshot(ids, since, now)
	for id, m := range out {
		m.UpstreamError = evidence[id]
		out[id] = m
	}
	return out, nil
}
func (r *accountPerformanceRepository) Read(ctx context.Context, ids []int64, now time.Time) (map[int64]service.AccountPerformance, error) {
	return readAccountPerformance(ctx, r.db, ids, now)
}
func (r *accountPerformanceRepository) EnabledIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT m.account_id FROM account_performance_monitors m JOIN accounts a ON a.id=m.account_id WHERE m.config->>'enabled'='true' AND NOT m.paused AND a.deleted_at IS NULL ORDER BY m.account_id`)
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
	rows, err := db.QueryContext(ctx, `SELECT account_id FROM account_performance_monitors WHERE paused AND account_id=ANY($1)`, pq.Array(ids))
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
	err = tx.QueryRowContext(ctx, `UPDATE account_performance_monitors SET config=$2,revision=revision+1,updated_at=NOW() WHERE account_id=$1 AND revision=$3 RETURNING pause_generation`, id, string(raw), revision).Scan(&gen)
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
	res, err := tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=false,paused_at=NULL,reason='',config=jsonb_set(config,'{enabled}','false'),revision=revision+1,updated_at=NOW() WHERE account_id=$1 AND revision=$2 AND pause_generation=$3 AND paused`, id, revision, generation)
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
			return nil // Deleted after EnabledIDs was captured.
		}
		return err
	}
	var raw []byte
	var revision, gen int64
	var paused bool
	// Multiple instances skip claimed rows. Configuration and release use the same lock.
	err = tx.QueryRowContext(ctx, `SELECT config,revision,paused,pause_generation FROM account_performance_monitors WHERE account_id=$1 FOR UPDATE SKIP LOCKED`, id).Scan(&raw, &revision, &paused, &gen)
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
	if !cfg.Enabled || paused {
		return nil
	}
	if err = cfg.Validate(); err != nil {
		return err
	}
	metrics, err := readAccountPerformance(ctx, tx, []int64{id}, now)
	if err != nil {
		return err
	}
	m := metrics[id]
	reasons := m.Breaches()
	if len(reasons) == 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_performance_monitors SET paused=true,pause_generation=pause_generation+1,paused_at=$2,reason=$3,updated_at=NOW() WHERE account_id=$1`, id, now, strings.Join(reasons, ","))
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err = performanceAudit(ctx, tx, id, "pause", 0, revision, gen+1, evidence); err != nil {
		return err
	}
	return tx.Commit()
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
