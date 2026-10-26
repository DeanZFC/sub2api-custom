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

func passiveSamples(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) (service.PassiveQualityEvidence, error) {
	if p.IsGroupQualityTarget() {
		return passiveGroupTargetSamples(ctx, tx, p, s, now)
	}
	from := now.Add(-time.Duration(p.WindowSeconds) * time.Second)
	if s.ObserveAfter.After(from) {
		from = s.ObserveAfter
	}
	started := s.ObserveAfter
	if s.Phase == "trial" {
		from = s.TrialStartedAt
		started = s.TrialStartedAt
	}
	// Limit both indexed input branches before the final merge. Failure wins for
	// the same account/request, even when a retry subsequently succeeds.
	rows, err := tx.QueryContext(ctx, `WITH failed AS MATERIALIZED (
 SELECT request_key,created_at,model FROM passive_quality_failures WHERE account_id=$1 AND ($2='' OR model=$2) AND created_at>=$3 AND created_at<=$4 AND started_at>=$5 ORDER BY created_at DESC,id DESC LIMIT $6
 ), succeeded AS (
 SELECT COALESCE(NULLIF(cr.attempt_key,''),ul.request_id) AS request_id,ul.id,ul.created_at,CASE WHEN ul.stream OR ul.openai_ws_mode THEN ul.first_token_ms END AS first_token_ms,
 CASE WHEN `+channelMonitorV2CacheEligibleUL+` AND (ul.cache_read_tokens>0 OR cr.cache_reported) AND NOT COALESCE(ul.native_compaction_v2,false)
 AND COALESCE(ul.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(ul.upstream_endpoint,'') NOT LIKE '%/compact%'
 THEN GREATEST(ul.input_tokens-ul.image_input_tokens,0)::bigint+GREATEST(ul.cache_creation_tokens,0)+GREATEST(ul.cache_read_tokens,0) ELSE 0 END AS cache_input,
 GREATEST(ul.cache_read_tokens,0)::bigint AS cache_read,COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model) AS model
 FROM usage_logs ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE ul.account_id=$1 AND ($2='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$2)
 AND ul.created_at>=$3 AND ul.created_at<=$4 AND ul.duration_ms IS NOT NULL
 AND ul.created_at-GREATEST(ul.duration_ms,0)*INTERVAL '1 millisecond'>=$5
 AND COALESCE(ul.request_type,0) NOT IN (4,6)
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_input_tokens>0 OR ul.image_output_tokens>0 OR ul.total_cost>0 OR ul.actual_cost>0 OR ul.image_count>0 OR ul.video_count>0)
 AND NOT EXISTS(SELECT 1 FROM failed f WHERE f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id))
 ORDER BY ul.created_at DESC,ul.id DESC LIMIT $6
 ), outcomes AS (
 SELECT DISTINCT ON (COALESCE(NULLIF(request_id,''),'usage:'||id)) COALESCE(NULLIF(request_id,''),'usage:'||id) AS key,created_at,false AS failed,first_token_ms::float8,cache_input,cache_read,model FROM succeeded ORDER BY COALESCE(NULLIF(request_id,''),'usage:'||id),created_at DESC
 ) SELECT key,created_at,failed,first_token_ms,cache_input,cache_read,COALESCE(model,'') FROM (
 SELECT * FROM outcomes UNION ALL SELECT request_key,created_at,true,NULL::float8,0,0,model FROM failed
 ) combined ORDER BY created_at DESC,key DESC LIMIT $6`, s.AccountID, p.Model, from, now, started, p.MaxSamples)
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	samples := []service.PassiveQualitySample{}
	for rows.Next() {
		var v service.PassiveQualitySample
		if err = rows.Scan(&v.Key, &v.At, &v.Failed, &v.TTFT, &v.CacheInput, &v.CacheRead, &v.Model); err != nil {
			_ = rows.Close()
			return service.PassiveQualityEvidence{}, err
		}
		samples = append(samples, v)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	complete, err := passiveTelemetryComplete(ctx, tx, from, now, s.AccountID, p.Model, 0, "all")
	return service.PassiveQualitySummarize(samples, s.LastEvidenceAt, complete), err
}

func (r *passiveQualityRepository) Sweep(ctx context.Context, now time.Time, probeLimit int) ([]service.PassiveQualityState, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	var leader bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(1937006961,2)`).Scan(&leader); err != nil || !leader {
		return nil, err
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, `SELECT pg_advisory_unlock(1937006961,2)`)
	}()
	policyRows, err := r.db.QueryContext(ctx, `SELECT id,version,updated_at,config FROM passive_quality_policies WHERE config->>'enabled'='true' ORDER BY last_scanned_at,id`)
	if err != nil {
		return nil, err
	}
	var policies []service.PassiveQualityPolicy
	for policyRows.Next() {
		p, e := scanPassivePolicy(policyRows)
		if e != nil {
			_ = policyRows.Close()
			return nil, e
		}
		policies = append(policies, p)
	}
	err = policyRows.Err()
	_ = policyRows.Close()
	if err != nil {
		return nil, err
	}
	var probing int
	if err = r.db.QueryRowContext(ctx, `SELECT count(*) FROM passive_quality_states WHERE data->>'phase'='probing' AND (data->>'next_at')::timestamptz>clock_timestamp()`).Scan(&probing); err != nil {
		return nil, err
	}
	probeLimit = max(0, min(probeLimit, 4-probing))
	probes := []service.PassiveQualityState{}
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		targets, err := r.modelTargets(ctx, p, now)
		if err != nil {
			return probes, err
		}
		// Durable round robin across policies and oldest states avoids starving
		// later targets when a large installation reaches the sweep deadline.
		if _, err = r.db.ExecContext(ctx, `UPDATE passive_quality_policies SET last_scanned_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
			return probes, err
		}
		for _, target := range targets {
			s, err := r.advance(ctx, p.ID, target.account, now, len(probes) < probeLimit, target.model)
			if err != nil {
				return probes, err
			}
			if s != nil {
				probes = append(probes, *s)
			}
		}
		if err = r.refreshGroupQualityTrafficSnapshot(ctx, p, now); err != nil {
			return probes, err
		}
	}

	return probes, nil
}
func (r *passiveQualityRepository) advance(ctx context.Context, pid, aid int64, now time.Time, canProbe bool, models ...string) (*service.PassiveQualityState, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize pool-wide expansion before policy/account locks across instances.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006961,3)`); err != nil {
		return nil, err
	}
	p, s, err := r.lockedState(ctx, tx, pid, aid, models...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	valid, err := passiveTarget(ctx, tx, p, aid)
	if err != nil {
		return nil, err
	}
	if !p.Enabled || !valid {
		if s.UpdatedAt.IsZero() {
			return nil, nil
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM passive_quality_states WHERE policy_id=$1 AND account_id=$2 AND model_key=$3`, pid, aid, passiveModelKey(p, s)); err != nil {
			return nil, err
		}
		if err = passiveEvent(ctx, tx, s, "scope_removed"); err != nil {
			return nil, err
		}
		if err = reconcilePassiveAccount(ctx, tx, aid); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	oldPhase := s.Phase
	oldReason := s.Reason
	if s.UpdatedAt.IsZero() {
		s.ObserveAfter = p.EvidenceSince()
	}
	metricEvents := []string{}
	metricPolicy := p
	if p.IsolatedMetrics() && !p.IsGroupQualityTarget() && s.Phase != "exempt" {
		p.PassiveQualityConfig = service.PassiveQualityMetricConfig(p.PassiveQualityConfig, "availability")
	}
	switch s.Phase {
	case "exempt":
		if !now.Before(s.NextAt) {
			s.Phase = "insufficient"
			s.Reason = "exemption_ended"
			s.ObserveAfter = now
			s.CacheObserveAfter = now
			s.Latency = nil
			s.Cache = nil
			s.Hits = 0
			s.Generation++
		}
	case "probing":
		if !now.Before(s.NextAt) {
			service.PassiveQualityPause(p.PassiveQualityConfig, &s, now, "probe_timeout")
		}
	case "paused":
		if p.IsGroupQualityTarget() {
			if !now.Before(s.NextAt) && !s.OtherHold {
				beginPassiveTrial(p, &s, now, "group_target_trial")
			}
			break
		}
		if !now.Before(s.NextAt) && canProbe && !s.OtherHold {
			if p.Model != "" {
				s.ProbeModel = p.Model
			}
			if s.ProbeModel == "" {
				s.Reason = "probe_model_unavailable"
				s.NextAt = now.Add(time.Minute)
				break
			}
			s.Phase = "probing"
			s.Reason = "probing"
			s.Generation++
			s.NextAt = now.Add(90 * time.Second)
		}
	default:
		e, err := passiveSamples(ctx, tx, p, s, now)
		if err != nil {
			return nil, err
		}
		if p.IsolatedMetrics() && !p.IsGroupQualityTarget() && !p.ErrorRate.Enabled && p.ConsecutiveFailures == 0 {
			s.Evidence = e
			s.Phase, s.Reason = "healthy", "availability_not_enabled"
			break
		}
		advancePassiveObserved(p, &s, e, now)
	}
	// Apply availability first so a new hard pause cannot simultaneously expand
	// cache restrictions. Replay uses the same order and transition helpers.
	if metricPolicy.IsolatedMetrics() && !metricPolicy.IsGroupQualityTarget() && oldPhase != "exempt" {
		metricEvents, err = advancePassiveMetrics(ctx, tx, metricPolicy, &s, now)
		if err != nil {
			return nil, err
		}
	}
	s.UpdatedAt = now
	if err = savePassiveState(ctx, tx, s); err != nil {
		return nil, err
	}
	for _, kind := range metricEvents {
		if err = passiveEvent(ctx, tx, s, kind); err != nil {
			return nil, err
		}
	}
	if oldPhase != s.Phase || (s.Phase == "paused" && oldReason != s.Reason) {
		if err = passiveEvent(ctx, tx, s, s.Phase); err != nil {
			return nil, err
		}
	}
	if err = reconcilePassiveAccount(ctx, tx, aid); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if s.Phase == "probing" && oldPhase != "probing" {
		return &s, nil
	}
	return nil, nil
}
func (r *passiveQualityRepository) FinishProbe(ctx context.Context, probe service.PassiveQualityState, success bool, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	p, s, err := r.lockedState(ctx, tx, probe.PolicyID, probe.AccountID, probe.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	valid, err := passiveTarget(ctx, tx, p, s.AccountID)
	if err != nil {
		return err
	}
	if !valid || !p.Enabled || s.Generation != probe.Generation || s.Version != probe.Version || s.Phase != "probing" || !now.Before(s.NextAt) {
		return nil
	}
	if p.IsGroupQualityTarget() {
		return nil // Group objectives never recover from a connectivity probe.
	}
	if success && !s.OtherHold {
		beginPassiveTrial(p, &s, now, "probe_passed")
	} else {
		service.PassiveQualityPause(p.PassiveQualityConfig, &s, now, "probe_failed_or_other_hold")
	}
	s.UpdatedAt = now
	if err = savePassiveState(ctx, tx, s); err != nil {
		return err
	}
	if err = passiveEvent(ctx, tx, s, s.Phase); err != nil {
		return err
	}
	if err = reconcilePassiveAccount(ctx, tx, s.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

// Every applicable hold must approve. Account locking serializes all policies,
// all gateway instances, membership edits, and trial lease reservations.
func (r *passiveQualityRepository) AcquirePassiveQuality(ctx context.Context, aid int64, model, lease string) (bool, bool, error) {
	// This only defers automatic-quality holds to the following durable emergency
	// gate. That gate fails closed without a matching policy and exact lease.
	if groupQualityFallbackMatches(ctx, aid, model) {
		return true, false, nil
	}
	var held bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE s.account_id=$1 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce' AND s.data->>'phase' IN ('paused','probing','trial') AND `+passiveMembership+` AND (p.config->>'scope'='account' OR COALESCE(NULLIF(s.model_key,''),p.config->>'model')=$2 OR $2=''))`, aid, model).Scan(&held); err != nil || !held {
		return !held, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	other, err := lockPassiveAccount(ctx, tx, aid)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil || other {
		return false, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.data,p.config,(SELECT count(*) FROM passive_quality_leases l WHERE l.policy_id=s.policy_id AND l.account_id=s.account_id AND l.model_key=s.model_key AND l.generation=(s.data->>'generation')::bigint)
 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE s.account_id=$1 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce' AND s.data->>'phase' IN ('paused','probing','trial') AND `+passiveMembership+` AND (p.config->>'scope'='account' OR COALESCE(NULLIF(s.model_key,''),p.config->>'model')=$2 OR $2='') ORDER BY s.policy_id,s.model_key FOR UPDATE OF s`, aid, model)
	if err != nil {
		return false, false, err
	}
	var states []service.PassiveQualityState
	allowed := true
	matched := false
	filteredTrial, matchedFilter := false, false
	now := time.Now()
	for rows.Next() {
		var raw, cfg []byte
		var inflight int
		var s service.PassiveQualityState
		var c service.PassiveQualityConfig
		if err = rows.Scan(&raw, &cfg, &inflight); err != nil {
			_ = rows.Close()
			return false, false, err
		}
		if err = json.Unmarshal(raw, &s); err != nil {
			_ = rows.Close()
			return false, false, err
		}
		if err = json.Unmarshal(cfg, &c); err != nil {
			_ = rows.Close()
			return false, false, err
		}
		// Unfiltered policies share one trial budget across models. A filtered
		// hold still needs its own affected traffic, even beside an all-model hold.
		if c.PerModel() {
			c.Model = s.Model
		}
		matches := model != "" && (c.Model == "" || model == c.Model)
		filteredTrial = filteredTrial || c.Model != ""
		matchedFilter = matchedFilter || (c.Model != "" && matches)
		if s.Phase != "trial" || now.Before(s.TrialStartedAt) || !now.Before(s.TrialEndsAt) || (matches && (s.Used >= c.TrialRequests || inflight >= c.TrialConcurrency)) {
			allowed = false
		}
		if !matches {
			continue
		}
		matched = true
		s.Used++
		s.Inflight = inflight + 1
		states = append(states, s)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil || !allowed || !matched || (filteredTrial && !matchedFilter) {
		return false, false, err
	}
	for _, s := range states {
		if err = savePassiveState(ctx, tx, s); err != nil {
			return false, false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO passive_quality_leases(id,policy_id,account_id,model_key,generation) SELECT $1,$2,$3,model_key,$4 FROM passive_quality_states WHERE policy_id=$2 AND account_id=$3 AND data->>'model'=$5`, lease, s.PolicyID, aid, s.Generation, s.Model); err != nil {
			return false, false, err
		}
	}
	if err = reconcilePassiveAccount(ctx, tx, aid); err != nil {
		return false, false, err
	}
	if err = tx.Commit(); err != nil {
		return false, true, err
	}
	return true, len(states) > 0, nil
}
func (r *passiveQualityRepository) ReleasePassiveQuality(ctx context.Context, lease string, unused bool) error {
	if !unused {
		_, err := r.db.ExecContext(ctx, `DELETE FROM passive_quality_leases WHERE id=$1`, lease)
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Find the account before locking state. A stale lease after a reset is benign.
	var aid int64
	err = tx.QueryRowContext(ctx, `SELECT account_id FROM passive_quality_leases WHERE id=$1 LIMIT 1`, lease).Scan(&aid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = lockPassiveAccount(ctx, tx, aid); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE passive_quality_states s SET data=jsonb_set(data,'{used}',to_jsonb(GREATEST((data->>'used')::int-1,0)))
 FROM passive_quality_leases l WHERE l.id=$1 AND l.policy_id=s.policy_id AND l.account_id=s.account_id AND l.model_key=s.model_key AND l.generation=(s.data->>'generation')::bigint`, lease)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM passive_quality_leases WHERE id=$1`, lease); err != nil {
		return err
	}
	if err = reconcilePassiveAccount(ctx, tx, aid); err != nil {
		return err
	}
	return tx.Commit()
}

// Use the existing shared Redis concurrency slots, including their crash
// cleanup, instead of introducing permanent leases for soft limitations.
func (r *passiveQualityRepository) PassiveQualityConcurrencyLimit(ctx context.Context, aid int64, model string) (int, error) {
	var limit int
	err := r.db.QueryRowContext(ctx, `WITH active AS (SELECT p.config,s.data
 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE s.account_id=$1 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND s.data->>'phase'<>'exempt' AND (p.config->>'scope'='account' OR COALESCE(NULLIF(s.model_key,''),p.config->>'model')=$2 OR $2='') AND `+passiveMembership+`
 ), limits AS (
 SELECT (config->>'concurrency_limit')::int AS value FROM active WHERE data->>'phase'='limited'
 UNION ALL SELECT (config->>'ttft_concurrency_limit')::int FROM active
 WHERE config->>'schema_version'='2' AND data->'latency'->>'phase'='limited'
 ) SELECT COALESCE(min(value) FILTER(WHERE value>0),0) FROM limits`, aid, model).Scan(&limit)
	return limit, err
}

// Called after membership changes while holding the account lock. No policy
// locks are acquired here, keeping the policy -> account lock order acyclic.
func releaseDetachedPassiveProtection(ctx context.Context, tx sqlExecutor, aid int64) error {
	_, err := tx.ExecContext(ctx, `WITH removed AS (DELETE FROM passive_quality_states s USING passive_quality_policies p WHERE p.id=s.policy_id AND s.account_id=$1 AND NOT (`+passiveMembership+`) RETURNING s.policy_id,s.account_id,s.data)
 INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail) SELECT policy_id,account_id,COALESCE(data->>'policy_name',''),'scope_removed',data FROM removed`, aid)
	if err != nil {
		return fmt.Errorf("release detached passive protection: %w", err)
	}
	return reconcilePassiveAccount(ctx, tx, aid)
}

// Budget and sentinel deferrals retain the existing hold without increasing
// failure backoff. The same generation fence as FinishProbe rejects stale work.
func (r *passiveQualityRepository) PostponePassiveProbe(ctx context.Context, probe service.PassiveQualityState, until time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, s, err := r.lockedState(ctx, tx, probe.PolicyID, probe.AccountID, probe.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.Generation != probe.Generation || s.Version != probe.Version || s.Phase != "probing" {
		return nil
	}
	s.Phase = "paused"
	s.Reason = "quality_probe_deferred"
	s.NextAt = until
	s.UpdatedAt = time.Now()
	s.Generation++
	if err = savePassiveState(ctx, tx, s); err != nil {
		return err
	}
	if err = passiveEvent(ctx, tx, s, "probe_deferred"); err != nil {
		return err
	}
	if err = reconcilePassiveAccount(ctx, tx, s.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}
