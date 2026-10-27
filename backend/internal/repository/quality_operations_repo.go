package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
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
		return service.ErrPassiveQualityConflict
	}
	return err
}
func scanQualitySupplier(row interface{ Scan(...any) error }) (service.QualitySupplier, error) {
	var s service.QualitySupplier
	var raw []byte
	var version int64
	err := row.Scan(&s.ID, &version, &raw, &s.NextProbeAt)
	if err != nil {
		return s, err
	}
	id, next := s.ID, s.NextProbeAt
	err = json.Unmarshal(raw, &s)
	s.ID, s.Version, s.NextProbeAt = id, version, next
	s.AccountIDs = []int64{}
	s.Members = []service.QualitySupplierMember{}
	return s, err
}
func fillQualitySupplier(ctx context.Context, q qualityQuery, s *service.QualitySupplier, now time.Time) error {
	rows, err := q.QueryContext(ctx, `SELECT a.id,a.name,COALESCE(o.kind,''),o.observed_at FROM quality_supplier_accounts m JOIN accounts a ON a.id=m.account_id JOIN quality_suppliers s ON s.id=m.supplier_id
 LEFT JOIN quality_probe_outcomes o ON o.account_id=a.id AND o.supplier_id=s.id AND o.observed_at>=s.updated_at AND o.observed_at>=$2
 WHERE m.supplier_id=$1 AND a.deleted_at IS NULL ORDER BY a.id`, s.ID, now.Add(-time.Duration(s.WindowSeconds)*time.Second))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m service.QualitySupplierMember
		if err = rows.Scan(&m.AccountID, &m.Name, &m.Kind, &m.ObservedAt); err != nil {
			return err
		}
		s.AccountIDs = append(s.AccountIDs, m.AccountID)
		s.Members = append(s.Members, m)
	}
	s.Summarize()
	return rows.Err()
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
	out := &service.QualityOperationsSnapshot{Config: c, Day: day, ResetsAt: now.Truncate(24 * time.Hour).Add(24 * time.Hour), Usage: []service.QualityBudgetUsage{}, Suppliers: []service.QualitySupplier{}}
	rows, err := tx.QueryContext(ctx, `SELECT account_id,requests,tokens,unknown_requests,recovery_requests,recovery_tokens FROM quality_budget_days WHERE day=$1 ORDER BY account_id`, day)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u service.QualityBudgetUsage
		if err = rows.Scan(&u.AccountID, &u.Requests, &u.Tokens, &u.UnknownRequests, &u.RecoveryRequests, &u.RecoveryTokens); err != nil {
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
	rows, err = tx.QueryContext(ctx, `SELECT id,version,config,next_probe_at FROM quality_suppliers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		s, e := scanQualitySupplier(rows)
		if e != nil {
			_ = rows.Close()
			return nil, e
		}
		out.Suppliers = append(out.Suppliers, s)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out.Suppliers {
		if err = fillQualitySupplier(ctx, tx, &out.Suppliers[i], now); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}
func (r *qualityOperationsRepository) SaveSupplier(ctx context.Context, s *service.QualitySupplier) error {
	if err := s.Validate(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize membership edits, including moving a key between suppliers.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006961,9)`); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(s.AccountIDs)).Scan(&count); err != nil {
		return err
	}
	if count != len(s.AccountIDs) {
		return fmt.Errorf("one or more accounts no longer exist")
	}
	var overlap bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quality_supplier_accounts WHERE account_id=ANY($1) AND supplier_id<>$2)`, pq.Array(s.AccountIDs), s.ID).Scan(&overlap); err != nil {
		return err
	}
	if overlap {
		return fmt.Errorf("an account already belongs to another supplier; remove it there first")
	}
	copy := *s
	copy.Members = nil
	copy.AccountIDs = nil
	copy.NextProbeAt = nil
	copy.Incident = false
	copy.Observed = 0
	copy.Affected = 0
	raw, _ := json.Marshal(copy)
	if s.ID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO quality_suppliers(config,updated_at) VALUES($1,clock_timestamp()) RETURNING id,version`, raw).Scan(&s.ID, &s.Version)
	} else {
		// A probe can hold this row while its budget admission is pending. Take
		// the row lock before evaluating the evidence fence: UPDATE expressions
		// can otherwise be evaluated before waiting for a pure row lock.
		var lockedID int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM quality_suppliers WHERE id=$1 AND version=$2 FOR UPDATE`, s.ID, s.Version).Scan(&lockedID)
		if err == nil {
			err = tx.QueryRowContext(ctx, `UPDATE quality_suppliers SET config=$1,version=version+1,updated_at=clock_timestamp(),next_probe_at=NULL,probe_lease='' WHERE id=$2 AND version=$3 RETURNING version`, raw, s.ID, s.Version).Scan(&s.Version)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrPassiveQualityConflict
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM quality_supplier_accounts WHERE supplier_id=$1`, s.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO quality_supplier_accounts(account_id,supplier_id) SELECT unnest($1::bigint[]),$2`, pq.Array(s.AccountIDs), s.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *qualityOperationsRepository) DeleteSupplier(ctx context.Context, id, version int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM quality_suppliers WHERE id=$1 AND version=$2`, id, version)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return service.ErrPassiveQualityConflict
	}
	return nil
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
	// Supplier row locks coordinate sentinel rounds across scheduler instances.
	purpose := service.QualityProbePurpose(ctx)
	recovery := purpose == "recovery"
	var supplierID *int64
	supplier, err := scanQualitySupplier(tx.QueryRowContext(ctx, `SELECT s.id,s.version,s.config,s.next_probe_at FROM quality_suppliers s JOIN quality_supplier_accounts m ON m.supplier_id=s.id WHERE m.account_id=$1 FOR UPDATE OF s`, account))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		supplierID = &supplier.ID
		if automatic && supplier.Enabled {
			if err = fillQualitySupplier(ctx, tx, &supplier, now); err != nil {
				return nil, err
			}
			if supplier.Incident {
				var lease string
				if err = tx.QueryRowContext(ctx, `SELECT probe_lease FROM quality_suppliers WHERE id=$1`, supplier.ID).Scan(&lease); err != nil {
					return nil, err
				}
				if supplier.NextProbeAt != nil && supplier.NextProbeAt.After(now) && (session == "" || session != lease) {
					return nil, &service.QualityDeferredError{Reason: "供应商故障聚合复测等待", Until: *supplier.NextProbeAt}
				}
				if lease != session || session == "" || supplier.NextProbeAt == nil || !supplier.NextProbeAt.After(now) {
					if _, err = tx.ExecContext(ctx, `UPDATE quality_suppliers SET next_probe_at=$2,probe_lease=$3 WHERE id=$1`, supplier.ID, now.Add(time.Duration(supplier.ProbeIntervalSeconds)*time.Second), session); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	if tokens < c.TokenReservation {
		tokens = c.TokenReservation
	}
	for _, id := range []int64{0, account} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO quality_budget_days(day,account_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, day, id); err != nil {
			return nil, err
		}
		var requests, spent, recoveryRequests, recoveryTokens int64
		if err = tx.QueryRowContext(ctx, `SELECT requests,tokens,recovery_requests,recovery_tokens FROM quality_budget_days WHERE day=$1 AND account_id=$2 FOR UPDATE`, day, id).Scan(&requests, &spent, &recoveryRequests, &recoveryTokens); err != nil {
			return nil, err
		}
		maxRequests, maxTokens := c.DailyRequests, c.DailyTokens
		if id != 0 {
			maxRequests, maxTokens = c.AccountDailyRequests, c.AccountDailyTokens
		}
		if c.BudgetEnabled && ((maxRequests > 0 && requests >= maxRequests) || (maxTokens > 0 && (tokens > maxTokens || spent > maxTokens-tokens))) {
			return nil, &service.QualityDeferredError{Reason: "今日检测预算不足", Until: now.Truncate(24 * time.Hour).Add(24 * time.Hour)}
		}
		requestLimit := service.QualityBudgetAdmissionLimit(maxRequests, recoveryRequests, c.RecoveryReservePercent, purpose)
		tokenLimit := service.QualityBudgetAdmissionLimit(maxTokens, recoveryTokens, c.RecoveryReservePercent, purpose)
		if c.BudgetEnabled && ((maxRequests > 0 && requests >= requestLimit) || (maxTokens > 0 && (tokens > tokenLimit || spent > tokenLimit-tokens))) {
			return nil, &service.QualityDeferredError{Reason: "剩余额度已为恢复探测预留", Until: now.Truncate(24 * time.Hour).Add(24 * time.Hour)}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE quality_budget_days SET requests=requests+1,tokens=tokens+$3,unknown_requests=unknown_requests+1,recovery_requests=recovery_requests+CASE WHEN $4 THEN 1 ELSE 0 END,recovery_tokens=recovery_tokens+CASE WHEN $4 THEN $3 ELSE 0 END WHERE day=$1 AND account_id=$2`, day, id, tokens, recovery); err != nil {
			return nil, err
		}
	}
	res := &service.QualityProbeReservation{AccountID: account}
	// Use the same database clock as the supplier edit fence, after obtaining
	// the supplier row lock, so old-config probes cannot become fresh evidence.
	if err = tx.QueryRowContext(ctx, `INSERT INTO quality_probe_attempts(account_id,day,supplier_id,reserved_tokens,started_at,purpose) VALUES($1,$2,$3,$4,clock_timestamp(),$5) RETURNING id`, account, day, supplierID, tokens, purpose).Scan(&res.ID); err != nil {
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
	var supplier *int64
	var started time.Time
	var purpose string
	err = tx.QueryRowContext(ctx, `SELECT day,reserved_tokens,settled,account_id,supplier_id,started_at,purpose FROM quality_probe_attempts WHERE id=$1 FOR UPDATE`, res.ID).Scan(&day, &reserved, &settled, &account, &supplier, &started, &purpose)
	if err != nil {
		return err
	}
	if settled {
		return nil
	}
	if o.Tokens != nil && *o.Tokens >= 0 {
		for _, id := range []int64{0, account} {
			if _, err = tx.ExecContext(ctx, `UPDATE quality_budget_days SET tokens=tokens+$3,unknown_requests=unknown_requests-1,recovery_tokens=recovery_tokens+CASE WHEN $4 THEN $3 ELSE 0 END WHERE day=$1 AND account_id=$2`, day, id, *o.Tokens-reserved, purpose == "recovery"); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE quality_probe_attempts SET settled=true,actual_tokens=$2,kind=$3 WHERE id=$1`, res.ID, o.Tokens, o.Kind); err != nil {
		return err
	}
	// A slow old request cannot overwrite a newer request's health observation.
	if _, err = tx.ExecContext(ctx, `INSERT INTO quality_probe_outcomes(account_id,supplier_id,attempt_id,kind,observed_at) SELECT $1,$2,$3,$4,$5 WHERE EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND deleted_at IS NULL)
 ON CONFLICT(account_id) DO UPDATE SET supplier_id=excluded.supplier_id,attempt_id=excluded.attempt_id,kind=excluded.kind,observed_at=excluded.observed_at WHERE quality_probe_outcomes.attempt_id<excluded.attempt_id`, account, supplier, res.ID, o.Kind, started); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *qualityOperationsRepository) QualityFrequencyDue(ctx context.Context, plan, account int64, key string) (*time.Time, error) {
	var next time.Time
	err := r.db.QueryRowContext(ctx, `SELECT f.next_at FROM quality_healthy_frequency f JOIN quality_operations_config c ON c.id=1 AND c.version=f.config_version AND c.config->>'dynamic_enabled'='true'
 WHERE f.plan_id=$1 AND f.account_id=$2 AND f.policy_key=$3 AND f.streak>=(c.config->>'healthy_rounds')::integer AND f.next_at>now()
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=$2 AND s.blocked)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE s.account_id=$2 AND p.config->>'enabled'='true' AND (s.data->>'updated_at')::timestamptz>f.observed_at AND (s.data->>'phase' IN ('suspect','observed','limited','paused','probing','trial') OR (s.data->>'phase'<>'exempt' AND (s.data->'latency'->>'phase' IN ('watching','limited') OR EXISTS(SELECT 1 FROM jsonb_each(COALESCE(NULLIF(s.data->'cache','null'::jsonb),'{}'::jsonb)) c WHERE c.value->>'phase' IN ('watching','reduced','recovering'))))))`, plan, account, key).Scan(&next)
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
	// Outstanding operational or capability holds prevent healthy active tests
	// from slowing the fresh probes needed to recover a degraded account.
	var anomaly bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=$1 AND s.blocked) OR EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE s.account_id=$1 AND p.config->>'enabled'='true' AND (s.data->>'phase' IN ('suspect','observed','limited','paused','probing','trial') OR (s.data->>'phase'<>'exempt' AND (s.data->'latency'->>'phase' IN ('watching','limited') OR EXISTS(SELECT 1 FROM jsonb_each(COALESCE(NULLIF(s.data->'cache','null'::jsonb),'{}'::jsonb)) c WHERE c.value->>'phase' IN ('watching','reduced','recovering'))))))`, *result.AccountID).Scan(&anomaly); err != nil {
		return err
	}
	if anomaly {
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
