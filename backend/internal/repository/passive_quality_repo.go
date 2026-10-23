package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type passiveQualityRepository struct{ db *sql.DB }

func NewPassiveQualityRepository(db *sql.DB) service.PassiveQualityRepository {
	return &passiveQualityRepository{db: db}
}

func scanPassivePolicy(row interface{ Scan(...any) error }) (service.PassiveQualityPolicy, error) {
	var p service.PassiveQualityPolicy
	var raw []byte
	err := row.Scan(&p.ID, &p.Version, &p.UpdatedAt, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &p.PassiveQualityConfig)
	}
	return p, err
}
func (r *passiveQualityRepository) ListPolicies(ctx context.Context) ([]service.PassiveQualityPolicy, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,version,updated_at,config FROM passive_quality_policies ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.PassiveQualityPolicy{}
	for rows.Next() {
		p, err := scanPassivePolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *passiveQualityRepository) SavePolicy(ctx context.Context, p *service.PassiveQualityPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize configuration writes without blocking ordinary admissions.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006961,1)`); err != nil {
		return err
	}
	var count, platforms int
	err = tx.QueryRowContext(ctx, `SELECT count(*),count(DISTINCT platform) FROM groups WHERE id=ANY($1) AND deleted_at IS NULL AND status='active' AND platform<>'composite'`, pq.Array(p.GroupIDs)).Scan(&count, &platforms)
	if err != nil {
		return err
	}
	if count != len(p.GroupIDs) || platforms != 1 {
		return fmt.Errorf("groups must be active and belong to one non-composite platform")
	}
	raw, err := json.Marshal(p.PassiveQualityConfig)
	if err != nil {
		return err
	}
	if p.ID == 0 {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM passive_quality_policies`).Scan(&count); err != nil {
			return err
		}
		if count >= 100 {
			return fmt.Errorf("at most 100 passive quality policies")
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO passive_quality_policies(config) VALUES($1) RETURNING id,version,updated_at`, raw).Scan(&p.ID, &p.Version, &p.UpdatedAt)
	} else {
		var version int64
		if err = tx.QueryRowContext(ctx, `SELECT version FROM passive_quality_policies WHERE id=$1 FOR UPDATE`, p.ID).Scan(&version); err != nil {
			return err
		}
		if p.Version != version {
			return service.ErrPassiveQualityConflict
		}
		if err = r.clearPolicy(ctx, tx, p.ID, "policy_changed"); err != nil {
			return err
		}
		err = tx.QueryRowContext(ctx, `UPDATE passive_quality_policies SET config=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1 RETURNING version,updated_at`, p.ID, raw).Scan(&p.Version, &p.UpdatedAt)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *passiveQualityRepository) DeletePolicy(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM passive_quality_policies WHERE id=$1 FOR UPDATE`, id).Scan(&exists); err != nil {
		return err
	}
	if err = r.clearPolicy(ctx, tx, id, "policy_deleted"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM passive_quality_policies WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *passiveQualityRepository) clearPolicy(ctx context.Context, tx *sql.Tx, id int64, reason string) error {
	rows, err := tx.QueryContext(ctx, `SELECT account_id,model_key FROM passive_quality_states WHERE policy_id=$1 ORDER BY account_id,model_key`, id)
	if err != nil {
		return err
	}
	type stateID struct {
		account int64
		model   string
	}
	var ids []stateID
	for rows.Next() {
		var id stateID
		if err = rows.Scan(&id.account, &id.model); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, target := range ids {
		aid := target.account
		if _, err = lockPassiveAccount(ctx, tx, aid); err != nil {
			return err
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT data FROM passive_quality_states WHERE policy_id=$1 AND account_id=$2 AND model_key=$3 FOR UPDATE`, id, aid, target.model).Scan(&raw); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		var s service.PassiveQualityState
		if err = json.Unmarshal(raw, &s); err != nil {
			return err
		}
		s.Reason = reason
		if err = passiveEvent(ctx, tx, s, reason); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM passive_quality_states WHERE policy_id=$1 AND account_id=$2 AND model_key=$3`, id, aid, target.model); err != nil {
			return err
		}
		if err = reconcilePassiveAccount(ctx, tx, aid); err != nil {
			return err
		}
	}
	return nil
}
func lockPassiveAccount(ctx context.Context, tx *sql.Tx, aid int64) (bool, error) {
	var other bool
	err := tx.QueryRowContext(ctx, `SELECT status<>'active' OR NOT schedulable OR deleted_at IS NOT NULL
 OR COALESCE(temp_unschedulable_until>now(),false) OR COALESCE(rate_limit_reset_at>now(),false)
 FROM accounts WHERE id=$1 FOR NO KEY UPDATE`, aid).Scan(&other)
	return other, err
}
func passiveEvent(ctx context.Context, tx *sql.Tx, s service.PassiveQualityState, kind string) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail) VALUES($1,$2,$3,$4,$5)`, s.PolicyID, s.AccountID, s.PolicyName, kind, raw)
	return err
}
func savePassiveState(ctx context.Context, tx *sql.Tx, s service.PassiveQualityState) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO passive_quality_states(policy_id,account_id,model_key,data) SELECT $1,$2,CASE WHEN config->>'schema_version'='2' AND config->>'scope'='model' THEN $4 ELSE '' END,$3 FROM passive_quality_policies WHERE id=$1 ON CONFLICT(policy_id,account_id,model_key) DO UPDATE SET data=excluded.data`, s.PolicyID, s.AccountID, raw, s.Model)
	return err
}

const passiveMembership = `NOT EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(p.config->'excluded_account_ids','null'::jsonb),'[]')) x WHERE x::bigint=s.account_id)
 AND EXISTS (SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
 WHERE ag.account_id=s.account_id AND g.deleted_at IS NULL AND g.status='active'
 AND ag.group_id IN (SELECT x::bigint FROM jsonb_array_elements_text(p.config->'group_ids') x))`

// Projection is only an optimization. Live admissions always recheck durable holds.
// This never writes status or schedulable, which are owned by other controls.
func reconcilePassiveAccount(ctx context.Context, tx sqlExecutor, aid int64) error {
	result, err := tx.ExecContext(ctx, `WITH active AS (
 SELECT p.id,p.config,s.data
 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE s.account_id=$1 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND s.data->>'phase'<>'exempt' AND `+passiveMembership+`
 ), entries AS (
 SELECT id,jsonb_build_object('policy_id',id,'policy_name',config->>'name',
 'scope',config->>'scope','model',COALESCE(data->>'model',config->>'model'),'phase',data->>'phase','reason',data->>'reason',
 'trial_ends_at',data->'trial_ends_at','used',data->'used','max_requests',config->'trial_requests',
 'concurrency_limit',config->'concurrency_limit','priority_penalty',config->'priority_penalty') AS value
 FROM active WHERE data->>'phase' IN ('paused','probing','trial','limited')
 UNION ALL
 SELECT id,jsonb_build_object('policy_id',id,'policy_name',config->>'name','metric','ttft',
 'scope',config->>'scope','model',COALESCE(data->>'model',config->>'model'),'phase','limited','reason',data->'latency'->>'reason',
 'concurrency_limit',config->'ttft_concurrency_limit','priority_penalty',config->'ttft_priority_penalty')
 FROM active WHERE config->>'schema_version'='2' AND data->'latency'->>'phase'='limited'
 UNION ALL
 SELECT id,jsonb_build_object('policy_id',id,'policy_name',config->>'name','metric','cache',
 'scope','model','model',entry.key,'phase','cache_limited','reason',entry.value->>'reason',
 'allocation_percent',entry.value->'allocation_percent')
 FROM active CROSS JOIN LATERAL jsonb_each(COALESCE(NULLIF(data->'cache','null'::jsonb),'{}'::jsonb)) entry
 WHERE config->>'schema_version'='2' AND entry.value->>'phase' IN ('reduced','recovering')
 ), holds AS (SELECT COALESCE(jsonb_agg(value ORDER BY id,value->>'metric',value->>'model'),'[]'::jsonb) AS value FROM entries)
 UPDATE accounts a SET extra=CASE WHEN holds.value='[]'::jsonb THEN COALESCE(extra,'{}')-'passive_quality'
 ELSE jsonb_set(COALESCE(extra,'{}'),'{passive_quality}',holds.value) END,updated_at=clock_timestamp()
 FROM holds WHERE a.id=$1 AND COALESCE(a.extra->'passive_quality','[]') IS DISTINCT FROM holds.value`, aid)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &aid, nil, nil)
}
func (r *passiveQualityRepository) ListStates(ctx context.Context, aid int64) ([]service.PassiveQualityState, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT s.data,a.name,(SELECT count(*) FROM passive_quality_leases l WHERE l.policy_id=s.policy_id AND l.account_id=s.account_id AND l.model_key=s.model_key AND l.generation=(s.data->>'generation')::bigint),(a.status<>'active' OR NOT a.schedulable OR COALESCE(a.temp_unschedulable_until>now(),false) OR COALESCE(a.rate_limit_reset_at>now(),false))
 FROM passive_quality_states s JOIN accounts a ON a.id=s.account_id WHERE ($1=0 OR s.account_id=$1) AND a.deleted_at IS NULL ORDER BY s.policy_id,s.account_id,s.model_key LIMIT 10000`, aid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.PassiveQualityState{}
	for rows.Next() {
		var raw []byte
		var s service.PassiveQualityState
		var name string
		var inflight int
		var other bool
		if err = rows.Scan(&raw, &name, &inflight, &other); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		s.AccountName = name
		s.Inflight = inflight
		s.OtherHold = other
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *passiveQualityRepository) ListEvents(ctx context.Context, aid, before int64) ([]service.PassiveQualityEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,policy_id,account_id,policy_name,kind,detail,created_at FROM passive_quality_events WHERE ($1=0 OR account_id=$1) AND ($2=0 OR id<$2) ORDER BY id DESC LIMIT 100`, aid, before)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.PassiveQualityEvent{}
	for rows.Next() {
		var e service.PassiveQualityEvent
		var raw []byte
		if err = rows.Scan(&e.ID, &e.PolicyID, &e.AccountID, &e.PolicyName, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (r *passiveQualityRepository) lockedState(ctx context.Context, tx *sql.Tx, pid, aid int64, models ...string) (service.PassiveQualityPolicy, service.PassiveQualityState, error) {
	p, err := scanPassivePolicy(tx.QueryRowContext(ctx, `SELECT id,version,updated_at,config FROM passive_quality_policies WHERE id=$1 FOR NO KEY UPDATE`, pid))
	if err != nil {
		return p, service.PassiveQualityState{}, err
	}
	other, err := lockPassiveAccount(ctx, tx, aid)
	if err != nil {
		return p, service.PassiveQualityState{}, err
	}
	modelKey := ""
	if p.PerModel() {
		modelKey = p.Model
		if len(models) > 0 && strings.TrimSpace(models[0]) != "" {
			modelKey = strings.TrimSpace(models[0])
		}
		if modelKey == "" {
			return p, service.PassiveQualityState{}, service.ErrPassiveQualityModelRequired
		}
		if p.Model != "" && modelKey != p.Model {
			return p, service.PassiveQualityState{}, service.ErrPassiveQualityConflict
		}
		p.Model = modelKey
	}
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: aid, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Phase: "insufficient", ObserveAfter: time.Now()}
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT data FROM passive_quality_states WHERE policy_id=$1 AND account_id=$2 AND model_key=$3 FOR UPDATE`, pid, aid, modelKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err == nil {
		err = json.Unmarshal(raw, &s)
	}
	s.OtherHold = other
	return p, s, err
}
func passiveTarget(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, aid int64) (bool, error) {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN account_groups ag ON ag.account_id=a.id JOIN groups g ON g.id=ag.group_id WHERE a.id=$1 AND a.deleted_at IS NULL AND g.deleted_at IS NULL AND g.status='active' AND ag.group_id=ANY($2) AND NOT (a.id=ANY($3)))`, aid, pq.Array(p.GroupIDs), pq.Array(p.ExcludedAccountIDs)).Scan(&ok)
	return ok, err
}
func (r *passiveQualityRepository) Act(ctx context.Context, pid, aid int64, action string, seconds int, models ...string) error {
	if action != "verify" && action != "exempt" && action != "cancel_exemption" {
		return fmt.Errorf("unsupported action")
	}
	if action == "exempt" && (seconds < 60 || seconds > 86400) {
		return fmt.Errorf("exemption must last 60..86400 seconds")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	p, s, err := r.lockedState(ctx, tx, pid, aid, models...)
	if err != nil {
		return err
	}
	valid, err := passiveTarget(ctx, tx, p, aid)
	if err != nil {
		return err
	}
	if !p.Enabled || !valid {
		return service.ErrPassiveQualityConflict
	}
	now := time.Now()
	s.Generation++
	s.UpdatedAt = now
	switch action {
	case "verify":
		if s.Phase != "paused" {
			return service.ErrPassiveQualityConflict
		}
		s.NextAt = now
		s.Reason = "verification_requested"
	case "exempt":
		s.Latency = nil
		s.Cache = nil
		s.CacheObserveAfter = now
		s.Phase = "exempt"
		s.NextAt = now.Add(time.Duration(seconds) * time.Second)
		s.Reason = "temporary_exemption"
	case "cancel_exemption":
		if s.Phase != "exempt" {
			return service.ErrPassiveQualityConflict
		}
		s.Phase = "insufficient"
		s.ObserveAfter = now
		s.CacheObserveAfter = now
		s.Latency = nil
		s.Cache = nil
		s.Hits = 0
		s.Reason = "exemption_ended"
	}
	if err = savePassiveState(ctx, tx, s); err != nil {
		return err
	}
	if err = passiveEvent(ctx, tx, s, action); err != nil {
		return err
	}
	if err = reconcilePassiveAccount(ctx, tx, aid); err != nil {
		return err
	}
	return tx.Commit()
}
