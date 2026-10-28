package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.GroupQualityTrafficGate = (*passiveQualityRepository)(nil)

// Admission and policy edits serialize on the policy row; every model and every
// gateway then uses the same durable snapshot row. No usage-log query is on the
// admission path. Sweeps refresh the authoritative one-hour population.
func (r *passiveQualityRepository) AcquireGroupQualityTraffic(ctx context.Context, aid int64, model, lease string) (bool, bool, error) {
	req, ok := service.GroupQualityTrafficRequestFromContext(ctx)
	if !ok {
		_, marked := service.GroupQualityFallbackFromContext(ctx)
		return !marked, false, nil
	}
	if req.Model != "" {
		model = req.Model
	}
	model = strings.TrimSpace(model)
	_, fallbackMarked := service.GroupQualityFallbackFromContext(ctx)
	fallback := groupQualityFallbackMatches(ctx, aid, model)
	if fallbackMarked && !fallback {
		return false, false, nil
	}
	if lease == "" || aid <= 0 {
		return false, false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanPassivePolicy(tx.QueryRowContext(ctx, `SELECT id,version,updated_at,config FROM passive_quality_policies
 WHERE config->>'purpose' IN ('cache_target','group_quality_target')
 AND config->>'enabled'='true' AND config->>'mode'='enforce'
 AND config->'group_ids' @> to_jsonb(ARRAY[$1::bigint])
 AND EXISTS(SELECT 1 FROM groups g WHERE g.id=$1 AND g.platform='openai' AND g.deleted_at IS NULL AND g.status='active')
 AND (COALESCE(config->>'model','')='' OR config->>'model'=$2)
 ORDER BY id LIMIT 1 FOR SHARE`, req.GroupID, model))
	if errors.Is(err, sql.ErrNoRows) {
		return !fallback, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !p.CacheRate.Enabled && !fallback {
		return true, false, nil
	}
	if fallback {
		if err = lockGroupQualityFallbackMembers(ctx, tx, req.GroupID); err != nil {
			return false, false, err
		}
		eligible, e := groupQualityFallbackCandidate(ctx, tx, aid, model)
		if e != nil || !eligible {
			return false, false, e
		}
	} else {
		other, e := lockPassiveAccount(ctx, tx, aid)
		if errors.Is(e, sql.ErrNoRows) {
			return false, false, nil
		}
		if e != nil || other {
			return false, false, e
		}
	}
	// Membership edits lock accounts, and group deletion locks groups. Joining
	// both lock protocols fences a stale scheduler snapshot before reservation.
	if err = lockLiveGroups(ctx, tx, p.GroupIDs); err != nil {
		if errors.Is(err, service.ErrGroupNotFound) {
			return false, false, nil
		}
		return false, false, err
	}
	var supported bool
	if err = tx.QueryRowContext(ctx, `SELECT platform='openai' FROM groups WHERE id=$1`, req.GroupID).Scan(&supported); err != nil {
		return false, false, err
	}
	if !supported {
		return !fallback, false, nil
	}
	membership := p
	membership.ExcludedAccountIDs = []int64{}
	valid, err := passiveTarget(ctx, tx, membership, aid)
	if err != nil {
		return false, false, err
	}
	if !valid {
		return false, false, nil
	}
	for _, excluded := range p.ExcludedAccountIDs {
		if excluded == aid {
			return !fallback, false, nil
		}
	}
	// Decode and validate account/model evidence before taking the shared budget
	// lock. Account and policy locks above still fence policy/membership edits.
	now := time.Now()
	_, count, err := groupQualityVerified(ctx, tx, p, aid, model, now)
	if err != nil {
		return false, false, err
	}
	// Missing or stale evidence is not a quality hold. Only traffic recovering
	// from an actual pause shares the validation allowance; initial sampling and
	// pending confirmations keep serving through the ordinary admission gates.
	var recovering bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passive_quality_states
 WHERE policy_id=$1 AND account_id=$2 AND model_key=$3 AND data->>'phase' IN ('paused','probing','trial'))`, p.ID, aid, model).Scan(&recovering); err != nil {
		return false, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO group_quality_traffic_snapshots(policy_id,policy_version) VALUES($1,$2) ON CONFLICT DO NOTHING`, p.ID, p.DecisionVersion()); err != nil {
		return false, false, err
	}
	var version, input, read int64
	var complete bool
	var measured sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT policy_version,input_tokens,read_tokens,telemetry_complete,measured_at FROM group_quality_traffic_snapshots WHERE policy_id=$1 FOR UPDATE`, p.ID).Scan(&version, &input, &read, &complete, &measured)
	if err != nil {
		return false, false, err
	}
	// An idempotent retry may observe the same lease, but must never move it to a
	// different attempt/account/model or revive a previously released admission.
	var oldAid, oldGroup, oldVersion int64
	var oldKey, oldModel string
	var released, settled, oldExpires sql.NullTime
	var oldFallback bool
	err = tx.QueryRowContext(ctx, `SELECT account_id,group_id,policy_version,request_key,model,released_at,settled_at,fallback,fallback_expires_at FROM group_quality_traffic_leases WHERE policy_id=$1 AND lease_id=$2`, p.ID, lease).Scan(&oldAid, &oldGroup, &oldVersion, &oldKey, &oldModel, &released, &settled, &oldFallback, &oldExpires)
	if err == nil {
		return oldAid == aid && oldGroup == req.GroupID && oldVersion == p.DecisionVersion() && oldKey == req.Key && oldModel == model && !released.Valid && !settled.Valid && oldFallback == fallback && (!oldFallback || (oldExpires.Valid && time.Now().Before(oldExpires.Time))), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	if version != p.DecisionVersion() || !measured.Valid || now.Sub(measured.Time) > 2*time.Minute {
		input = 0
	}
	if version != p.DecisionVersion() || !complete || !measured.Valid || now.Sub(measured.Time) > 2*time.Minute {
		read = 0
	}
	_, budget := service.GroupQualityTrafficBudget(input, read, p.CacheRate.Pause, count)
	var spent int64
	if recovering {
		spent, err = groupQualityChargedTokens(ctx, tx, p.ID, now)
		if err != nil {
			return false, false, err
		}
	}
	estimate := max(req.EstimatedInput, int64(4096))
	denied := recovering && (spent >= budget || estimate > budget-spent)
	var fallbackExpires any
	if fallback {
		supply, e := groupQualityFallbackHasNormalSupply(ctx, tx, p, model)
		if e != nil {
			return false, false, e
		}
		// Fallback adds no concurrency cap or pacing delay. Each request keeps
		// its own usage lease; ordinary account concurrency still applies.
		denied = supply
		fallbackExpires = now.Add(groupQualityFallbackLease)
	}
	if denied {
		// A budget protects the quality sample, not the group's entire serving
		// capacity. In availability mode, keep all live-account and trial gates above
		// authoritative, but let this account serve without creating a validation
		// lease or consuming additional budget.
		if p.TrafficMode == "availability" && !fallback {
			return true, false, tx.Commit()
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO group_quality_traffic_denials(policy_id,minute,requests) VALUES($1,date_trunc('minute',$2::timestamptz),1)
 ON CONFLICT(policy_id,minute) DO UPDATE SET requests=group_quality_traffic_denials.requests+1`, p.ID, now); err != nil {
			return false, false, err
		}
		return false, false, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_quality_traffic_leases(lease_id,policy_id,policy_version,account_id,group_id,model,request_key,validation,reserved_input,created_at,fallback,fallback_expires_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, lease, p.ID, p.DecisionVersion(), aid, req.GroupID, model, req.Key, recovering || fallback, estimate, now, fallback, fallbackExpires)
	if err != nil {
		return false, false, err
	}
	if fallback {
		if _, err = tx.ExecContext(ctx, `UPDATE group_quality_traffic_snapshots SET last_fallback_at=$2 WHERE policy_id=$1`, p.ID, now); err != nil {
			return false, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, true, err
	}
	return true, true, nil
}

// Only a positively unused lease refunds an admission without usage. A used
// admission with explicit positive usage settles its estimate to real input;
// cancellation, failure, missing reports and zero usage retain a conservative
// estimate until the rolling hour expires.
func lockGroupQualityTrafficLease(ctx context.Context, tx *sql.Tx, lease string) error {
	rows, err := tx.QueryContext(ctx, `SELECT policy_id FROM group_quality_traffic_snapshots WHERE policy_id IN
 (SELECT policy_id FROM group_quality_traffic_leases WHERE lease_id=$1) ORDER BY policy_id FOR UPDATE`, lease)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *passiveQualityRepository) ReleaseGroupQualityTraffic(ctx context.Context, lease string, unused bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockGroupQualityTrafficLease(ctx, tx, lease); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE group_quality_traffic_leases SET released_at=COALESCE(released_at,clock_timestamp()),
 unused=CASE WHEN released_at IS NULL AND settled_at IS NULL THEN $2 ELSE unused END WHERE lease_id=$1`, lease, unused)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *passiveQualityRepository) SettleGroupQualityTraffic(ctx context.Context, req service.GroupQualityTrafficRequest, aid, input, read int64, reported bool) error {
	if req.LeaseID == "" || req.Key == "" || req.GroupID <= 0 || aid <= 0 || req.NonText || (req.AccountID > 0 && req.AccountID != aid) {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize actual-usage adjustments with admission: new attempts cannot
	// consume headroom while a settlement increases this group's charged cost.
	if err = lockGroupQualityTrafficLease(ctx, tx, req.LeaseID); err != nil {
		return err
	}
	// The admitted policy version is immutable on this exact lease. A config or
	// membership edit cannot redirect old usage into a newer attempt, while its
	// real spent tokens still count against the shared rolling group allowance.
	_, err = tx.ExecContext(ctx, `UPDATE group_quality_traffic_leases SET input_tokens=$6,read_tokens=$7,cache_reported=$8,settled_at=clock_timestamp()
 WHERE lease_id=$1 AND account_id=$2 AND request_key=$3 AND group_id=$4 AND model=$5
 AND NOT unused AND settled_at IS NULL`, req.LeaseID, aid, req.Key, req.GroupID, req.Model, max(input, 0), max(min(read, input), 0), reported)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func groupQualityStateVerified(p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) bool {
	if s.Version != p.DecisionVersion() || s.Phase != "healthy" || s.Model == "" || s.UpdatedAt.IsZero() || s.Evidence.Latest.IsZero() {
		return false
	}
	from := now.Add(-time.Duration(p.WindowSeconds) * time.Second)
	if s.UpdatedAt.Before(from) || s.Evidence.Latest.Before(from) {
		return false
	}
	verdict, _ := service.EvaluatePassiveQuality(p.PassiveQualityConfig, s.Evidence, false)
	return verdict == "pass"
}

// count is healthy account/model pairs over all public models; the bypass is
// checked against this exact account/model. Other models cannot verify it.
func groupQualityVerified(ctx context.Context, tx sqlExecutor, p service.PassiveQualityPolicy, aid int64, model string, now time.Time) (bool, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.account_id,s.data FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 JOIN accounts a ON a.id=s.account_id WHERE s.policy_id=$1 AND a.deleted_at IS NULL AND a.status='active' AND a.schedulable
 AND NOT COALESCE(a.temp_unschedulable_until>now(),false) AND NOT COALESCE(a.rate_limit_reset_at>now(),false)
 AND s.data->>'phase'='healthy' AND (COALESCE(p.config->>'model','')='' OR s.model_key=p.config->>'model') AND `+passiveMembership, p.ID)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	verified := false
	for rows.Next() {
		var id int64
		var raw []byte
		var state service.PassiveQualityState
		if err = rows.Scan(&id, &raw); err != nil {
			return false, 0, err
		}
		if err = json.Unmarshal(raw, &state); err != nil {
			return false, 0, err
		}
		if groupQualityStateVerified(p, state, now) {
			count++
			verified = verified || (id == aid && state.Model == model)
		}
	}
	return verified, count, rows.Err()
}

// Admission reads at most 60 minute buckets plus the exact partial boundary.
// Settlement and release keep these aggregates current in the same transaction.
func groupQualityChargedTokens(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, policyID int64, now time.Time) (int64, error) {
	var spent int64
	cutoff := now.Add(-time.Hour)
	boundary := cutoff.Truncate(time.Minute).Add(time.Minute)
	err := tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT sum(charged_tokens) FROM group_quality_budget_minutes WHERE policy_id=$1 AND minute>=$3),0)
 +COALESCE((SELECT sum(group_quality_lease_charge(l)) FROM group_quality_traffic_leases l
 WHERE policy_id=$1 AND validation AND NOT unused AND created_at>=$2 AND created_at<$3),0)`, policyID, cutoff, boundary).Scan(&spent)
	return spent, err
}

func writeGroupQualityTrafficSnapshot(ctx context.Context, tx sqlExecutor, p service.PassiveQualityPolicy, now time.Time, input, read int64, complete bool) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO group_quality_traffic_snapshots(policy_id,policy_version,input_tokens,read_tokens,telemetry_complete,measured_at,updated_at)
 SELECT id,$7,$3,$4,$5,$6,clock_timestamp() FROM passive_quality_policies WHERE id=$1 AND version=$2
 ON CONFLICT(policy_id) DO UPDATE SET policy_version=excluded.policy_version,input_tokens=excluded.input_tokens,read_tokens=excluded.read_tokens,
 telemetry_complete=excluded.telemetry_complete,measured_at=excluded.measured_at,updated_at=excluded.updated_at
 WHERE group_quality_traffic_snapshots.policy_version<>excluded.policy_version OR group_quality_traffic_snapshots.measured_at IS NULL OR group_quality_traffic_snapshots.measured_at<=excluded.measured_at`, p.ID, p.Version, max(input, 0), max(min(read, input), 0), complete, now, p.DecisionVersion())
	return err
}

// Expensive report work finishes before publication takes the policy/snapshot
// locks. The two snapshots are published atomically, fenced against policy edits.
func (r *passiveQualityRepository) publishGroupQualitySummary(ctx context.Context, p service.PassiveQualityPolicy, s service.GroupQualityTargetSummary) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM passive_quality_policies WHERE id=$1 FOR SHARE`, p.ID).Scan(&version); err != nil {
		return err
	}
	if version != p.Version {
		return nil
	}
	if err = writeGroupQualityTrafficSnapshot(ctx, tx, p, s.GeneratedAt, s.TotalInputTokens, s.CacheReadTokens, s.TelemetryComplete); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO group_quality_report_snapshots(policy_id,policy_version,measured_at,data) VALUES($1,$2,$3,$4)
 ON CONFLICT(policy_id) DO UPDATE SET policy_version=excluded.policy_version,measured_at=excluded.measured_at,data=excluded.data
 WHERE group_quality_report_snapshots.policy_version<>excluded.policy_version OR group_quality_report_snapshots.measured_at<=excluded.measured_at`, p.ID, p.DecisionVersion(), s.GeneratedAt, raw)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func readGroupQualityTrafficSummary(ctx context.Context, reader groupQualitySummaryReader, p service.PassiveQualityPolicy, now time.Time, input, read int64, complete bool) (*service.GroupQualityTrafficSummary, error) {
	var supported bool
	if err := reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND platform='openai' AND deleted_at IS NULL AND status='active')`, p.GroupIDs[0]).Scan(&supported); err != nil {
		return nil, err
	}
	s := &service.GroupQualityTrafficSummary{Enabled: supported && p.Enabled && p.Mode == "enforce" && p.CacheRate.Enabled, TotalInputTokens: input}
	_, verified, err := groupQualityVerified(ctx, reader, p, 0, "", now)
	if err != nil {
		return nil, err
	}
	s.VerifiedAccounts = verified
	if !complete {
		read = 0
	}
	s.Mode, s.BudgetTokens = service.GroupQualityTrafficBudget(input, read, p.CacheRate.Pause, verified)
	var charged int64
	err = reader.QueryRowContext(ctx, `SELECT COALESCE(sum(CASE WHEN cache_reported AND input_tokens>0 THEN input_tokens ELSE 0 END),0),
 COALESCE(sum(CASE WHEN cache_reported AND input_tokens>0 THEN 0 ELSE GREATEST(reserved_input,COALESCE(input_tokens,0)) END),0),
 COALESCE(sum(CASE WHEN cache_reported AND input_tokens>0 THEN input_tokens ELSE GREATEST(reserved_input,COALESCE(input_tokens,0)) END),0)
 FROM group_quality_traffic_leases WHERE policy_id=$1 AND validation AND NOT unused AND created_at>=$2 AND created_at<=$3`, p.ID, now.Add(-time.Hour), now).Scan(&s.ValidationInputTokens, &s.ReservedInputTokens, &charged)
	if err != nil {
		return nil, err
	}
	s.RemainingTokens = max(s.BudgetTokens-charged, 0)
	if input > 0 {
		s.ValidationSharePercent = 100 * float64(s.ValidationInputTokens) / float64(input)
	}
	if err = reader.QueryRowContext(ctx, `SELECT COALESCE(sum(requests),0) FROM group_quality_traffic_denials WHERE policy_id=$1 AND minute>=$2 AND minute<=$3`, p.ID, now.Add(-time.Hour), now).Scan(&s.LimitedRequests); err != nil {
		return nil, err
	}
	return s, nil
}

// Sweep cadence, rather than traffic volume, bounds authoritative usage scans.
func (r *passiveQualityRepository) refreshGroupQualityTrafficSnapshot(ctx context.Context, p service.PassiveQualityPolicy, now time.Time) error {
	if !p.IsGroupQualityTarget() || len(p.GroupIDs) != 1 {
		return nil
	}
	var fresh bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM group_quality_report_snapshots WHERE policy_id=$1 AND policy_version=$2 AND measured_at>=$3)`, p.ID, p.DecisionVersion(), now.Add(-time.Minute)).Scan(&fresh); err != nil {
		return err
	}
	if fresh {
		return nil
	}
	summary, err := r.groupQualityTargetSummary(ctx, p, now)
	if err != nil {
		return err
	}
	return r.publishGroupQualitySummary(ctx, p, summary)
}
