package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type groupProbeRepository struct{ db *sql.DB }

// Allow for both the observation window and a long interval within a round.
const groupProbeResultLifetimeSQL = `2 * greatest(make_interval(mins => idle_window_minutes),
    CASE WHEN round_until>now() THEN make_interval(secs => interval_seconds) ELSE interval '0' END)`

func NewGroupProbeRepository(db *sql.DB) service.GroupProbeRepository {
	return &groupProbeRepository{db}
}

func (r *groupProbeRepository) List(ctx context.Context) ([]service.GroupProbe, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT p.group_id,g.name,p.model,p.enabled,p.idle_window_minutes,p.min_requests,p.duration_minutes,p.interval_seconds,
    CASE WHEN p.enabled AND g.status='active' AND round_until>now() THEN round_until END,
    manual_requested OR COALESCE(lease_until>now(),false),last_user_at,last_probe_at,last_result,deferred_reason,
    CASE WHEN NOT enabled OR g.status<>'active' THEN 'disabled'
         WHEN deferred_reason<>'' THEN 'stale'
         WHEN last_probe_at IS NULL THEN 'unknown'
         WHEN last_probe_at<now()-(`+groupProbeResultLifetimeSQL+`) THEN 'stale'
         WHEN failures>=2 THEN 'failed' WHEN failures=1 THEN 'confirming'
         WHEN last_result->>'recovered'='true' THEN 'recovered' ELSE 'healthy' END
    FROM group_probes p JOIN groups g ON g.id=p.group_id WHERE g.deleted_at IS NULL ORDER BY g.sort_order,g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.GroupProbe{}
	for rows.Next() {
		var p service.GroupProbe
		var raw []byte
		if err = rows.Scan(&p.GroupID, &p.GroupName, &p.Model, &p.Enabled, &p.IdleWindowMinutes, &p.MinRequests, &p.DurationMinutes, &p.IntervalSeconds,
			&p.RoundUntil, &p.Pending, &p.LastUserAt, &p.LastProbeAt, &raw, &p.DeferredReason, &p.Status); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &p.Result); err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *groupProbeRepository) Configure(ctx context.Context, id int64, model string, enabled bool, rule service.GroupProbeRuleUpdate) error {
	const sameRule = `(p.model,p.enabled,p.idle_window_minutes,p.min_requests,p.duration_minutes,p.interval_seconds)
        = (EXCLUDED.model,EXCLUDED.enabled,COALESCE($4,p.idle_window_minutes),COALESCE($5,p.min_requests),
           COALESCE($6,p.duration_minutes),COALESCE($7,p.interval_seconds))`
	res, err := r.db.ExecContext(ctx, `INSERT INTO group_probes AS p(group_id,model,enabled,idle_window_minutes,min_requests,duration_minutes,interval_seconds)
    VALUES($1,$2,$3,COALESCE($4::integer,5),COALESCE($5::integer,5),COALESCE($6::integer,10),COALESCE($7::integer,30))
    ON CONFLICT(group_id) DO UPDATE SET model=EXCLUDED.model,enabled=EXCLUDED.enabled,
    next_poll_at=CASE WHEN `+sameRule+` THEN p.next_poll_at ELSE now() END,
    round_until=CASE WHEN `+sameRule+` THEN p.round_until END,
    idle_window_minutes=COALESCE($4,p.idle_window_minutes),min_requests=COALESCE($5,p.min_requests),
    duration_minutes=COALESCE($6,p.duration_minutes),interval_seconds=COALESCE($7,p.interval_seconds),
    manual_requested=p.manual_requested AND `+sameRule+`,
    deferred_reason=CASE WHEN p.model=EXCLUDED.model AND EXCLUDED.enabled THEN p.deferred_reason ELSE '' END,
    deferred_until=CASE WHEN p.model=EXCLUDED.model AND EXCLUDED.enabled THEN p.deferred_until END,
    last_probe_at=CASE WHEN p.model=EXCLUDED.model THEN p.last_probe_at END,
    last_result=CASE WHEN p.model=EXCLUDED.model THEN p.last_result END,
    last_user_at=CASE WHEN p.model=EXCLUDED.model THEN p.last_user_at END,
    failures=CASE WHEN p.model=EXCLUDED.model THEN p.failures ELSE 0 END
    WHERE p.lease_until IS NULL OR p.lease_until<=now() OR (NOT EXCLUDED.enabled AND p.model=EXCLUDED.model)`,
		id, model, enabled, rule.IdleWindowMinutes, rule.MinRequests, rule.DurationMinutes, rule.IntervalSeconds)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if n == 0 && err == nil {
		return service.ErrGroupProbeBusy
	}
	return err
}
func (r *groupProbeRepository) Request(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `UPDATE group_probes p SET manual_requested=true,next_poll_at=now()
    FROM groups g WHERE p.group_id=$1 AND g.id=p.group_id AND g.status='active' AND g.deleted_at IS NULL
    AND enabled AND NOT manual_requested AND (lease_until IS NULL OR lease_until<=now())`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n > 0 {
		return err
	}
	var enabled bool
	err = r.db.QueryRowContext(ctx, `SELECT p.enabled AND g.status='active' AND g.deleted_at IS NULL FROM group_probes p JOIN groups g ON p.group_id=g.id WHERE group_id=$1`, id).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !enabled) {
		return service.ErrGroupProbeDisabled
	}
	if err != nil {
		return err
	}
	return service.ErrGroupProbeBusy
}

// Keep the ten-minute server-error check independent of the configured idle
// window. The latest min_requests successful calls suffice to decide the
// threshold and to determine whether the last two outcomes were failures.
// usage_logs already enforces uniqueness on (api_key_id, request_id).
var groupProbeActivitySQL = `WITH failures AS MATERIALIZED (
    SELECT e.id,e.api_key_id,e.request_type,e.created_at,e.request_id,e.client_request_id,e.status_code
    FROM ops_error_logs e
    WHERE e.group_id=$1 AND COALESCE(NULLIF(e.requested_model,''),e.model)=$2
      AND e.created_at >= LEAST($3::timestamptz, now()-interval '10 minutes')
      AND e.api_key_id IS NOT NULL AND NOT COALESCE(e.is_count_tokens,false)
      AND ` + channelMonitorV2FailureSQL("e") + `
), failure_keys AS (
    SELECT DISTINCT e.api_key_id, k.request_id
    FROM failures e CROSS JOIN LATERAL unnest(ARRAY[
        NULLIF(e.request_id,''), 'local:' || NULLIF(e.request_id,''),
        NULLIF(e.client_request_id,''), 'client:' || NULLIF(e.client_request_id,'')
    ]) k(request_id)
    WHERE COALESCE(e.request_type,0)<>3 AND k.request_id IS NOT NULL
), recent_successes AS (
    SELECT ul.created_at,false AS server_failure
    FROM usage_logs ul
    LEFT JOIN failure_keys fk ON fk.api_key_id=ul.api_key_id AND fk.request_id=ul.request_id
        AND COALESCE(ul.request_type,0)<>3 AND NOT COALESCE(ul.openai_ws_mode,false)
    WHERE ul.group_id=$1 AND COALESCE(NULLIF(ul.requested_model,''),ul.model)=$2
      AND ul.created_at >= LEAST($3::timestamptz, now()-interval '10 minutes')
      AND ul.api_key_id IS NOT NULL AND COALESCE(ul.request_type,0) NOT IN(4,6)
      AND (ul.http_status BETWEEN 200 AND 299 OR (ul.http_status IS NULL AND (ul.output_tokens>0 OR ul.actual_cost>0)))
      AND fk.request_id IS NULL
    ORDER BY ul.created_at DESC LIMIT $4
), outcomes AS (
    SELECT * FROM recent_successes
    UNION ALL
    SELECT max(e.created_at) AS created_at,bool_and(COALESCE(e.status_code,0)>=500) AS server_failure
    FROM failures e
    GROUP BY e.api_key_id, COALESCE(NULLIF(e.client_request_id,''),NULLIF(e.request_id,'')),
        CASE WHEN e.request_type=3 OR COALESCE(NULLIF(e.client_request_id,''),NULLIF(e.request_id,'')) IS NULL THEN e.id END
), latest AS (
    SELECT * FROM outcomes ORDER BY created_at DESC, server_failure ASC LIMIT 2
)
SELECT max(created_at),
       (SELECT count(*) FROM outcomes WHERE created_at >= $3),
       count(*)=2 AND COALESCE(bool_and(server_failure AND created_at >= now()-interval '10 minutes'),false)
FROM latest`

func (r *groupProbeRepository) Claim(ctx context.Context) (*service.GroupProbeJob, time.Duration, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var job service.GroupProbeJob
	var manual bool
	var failures, windowMinutes, minRequests, durationMinutes int
	var last, deferred, roundUntil *time.Time
	var nextPoll, now time.Time
	err = tx.QueryRowContext(ctx, `SELECT group_id,model,manual_requested,failures,idle_window_minutes,min_requests,duration_minutes,
    last_probe_at,deferred_until,round_until,next_poll_at,now()
    FROM group_probes p JOIN groups g ON g.id=p.group_id
    WHERE p.enabled AND g.status='active' AND g.deleted_at IS NULL AND (lease_until IS NULL OR lease_until<=now())
    AND next_poll_at<=now()+make_interval(secs => $1)
    ORDER BY manual_requested DESC,next_poll_at,group_id LIMIT 1 FOR UPDATE OF p SKIP LOCKED`, service.GroupProbePollInterval.Seconds()).Scan(&job.GroupID, &job.Model, &manual, &failures, &windowMinutes, &minRequests,
		&durationMinutes, &last, &deferred, &roundUntil, &nextPoll, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.GroupProbePollInterval, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if nextPoll.After(now) {
		return nil, nextPoll.Sub(now), nil
	}

	// During a round the persisted deadline and schedule drive each probe.
	// User traffic only decides whether to start a new round.
	active := roundUntil != nil && roundUntil.After(now)
	run := manual || active
	var recent *time.Time
	nextCheck := now.Add(time.Minute)
	if !manual && deferred != nil && deferred.After(now) {
		run = false
		nextCheck = *deferred
	} else if !manual && !active {
		window := time.Duration(windowMinutes) * time.Minute
		var requests int
		var burst bool
		err = tx.QueryRowContext(ctx, groupProbeActivitySQL, job.GroupID, job.Model, now.Add(-window), minRequests).Scan(&recent, &requests, &burst)
		if err != nil {
			return nil, 0, err
		}
		ready := roundUntil == nil || !now.Before(roundUntil.Add(window))
		if ready && requests < minRequests {
			end := now.Add(time.Duration(durationMinutes) * time.Minute)
			roundUntil = &end
			run = true
		} else {
			delay := window
			if failures == 1 {
				delay = time.Minute
			}
			early := burst && (last == nil || (recent.After(*last) && now.Sub(*last) >= time.Minute))
			run = early || (ready && failures > 0 && (last == nil || !now.Before(last.Add(delay))))
			if !ready && roundUntil.Add(window).Before(nextCheck) {
				nextCheck = roundUntil.Add(window)
			}
		}
	}
	if run {
		job.Owner = uuid.NewString()
		_, err = tx.ExecContext(ctx, `UPDATE group_probes SET manual_requested=false,lease_owner=$2,lease_until=now()+interval '2 minutes',
        last_user_at=greatest(last_user_at,$3::timestamptz),round_until=$4 WHERE group_id=$1`, job.GroupID, job.Owner, recent, roundUntil)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE group_probes SET next_poll_at=$3,last_user_at=greatest(last_user_at,$2::timestamptz) WHERE group_id=$1`, job.GroupID, recent, nextCheck)
	}
	if err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	if run {
		return &job, 0, nil
	}
	return nil, 0, nil
}

func (r *groupProbeRepository) Finish(ctx context.Context, job *service.GroupProbeJob, result service.GroupProbeResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous int
	err = tx.QueryRowContext(ctx, `SELECT failures FROM group_probes WHERE group_id=$1 AND lease_owner=$2 AND lease_until>now() FOR UPDATE`, job.GroupID, job.Owner).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		service.GroupProbeResult
		Recovered bool `json:"recovered,omitempty"`
	}{result, result.Success && previous > 0})
	if err != nil {
		return err
	}
	if result.Deferred != nil {
		_, err = tx.ExecContext(ctx, `UPDATE group_probes SET lease_owner=NULL,lease_until=NULL,next_poll_at=greatest(now()+interval '1 second',$3),
        deferred_reason=$2,deferred_until=$3 WHERE group_id=$1`, job.GroupID, result.Deferred.Reason, result.Deferred.Until)
	} else {
		// Anchor all ticks to the first claim. A slow probe skips missed ticks
		// without shortening the remaining intervals or extending the round.
		_, err = tx.ExecContext(ctx, `UPDATE group_probes SET lease_owner=NULL,lease_until=NULL, deferred_reason='',deferred_until=NULL,
        last_probe_at=now(),last_result=$2,failures=CASE WHEN $3 THEN 0 WHEN last_probe_at<now()-(`+groupProbeResultLifetimeSQL+`) THEN 1 ELSE least(failures+1,2) END,
        next_poll_at=CASE WHEN round_until>now() THEN least(round_until,
            date_bin(make_interval(secs => interval_seconds),now(),round_until-make_interval(mins => duration_minutes))
              + make_interval(secs => interval_seconds))
            ELSE now()+interval '1 minute' END WHERE group_id=$1`, job.GroupID, raw, result.Success)
	}
	if err != nil {
		return err
	}
	if result.Deferred == nil || result.AccountID != 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO group_probe_runs(group_id,model,result) VALUES($1,$2,$3)`, job.GroupID, job.Model, raw); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM group_probe_runs WHERE group_id=$1 AND checked_at<now()-interval '30 days'`, job.GroupID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
