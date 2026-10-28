package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strconv"
)

// Reporting uses the same effective phases as the management UI.
const passiveStateReport = `WITH reporting AS (SELECT s.*,a.name AS account_name,
 (a.status<>'active' OR NOT a.schedulable OR COALESCE(a.temp_unschedulable_until>now(),false) OR COALESCE(a.rate_limit_reset_at>now(),false)) AS other_hold,p.config,
 CASE
 WHEN p.config->>'purpose' IN ('cache_target','group_quality_target') AND s.data->>'phase'='healthy' AND NOT COALESCE((s.data->'evidence'->>'complete')::boolean,false) THEN 'insufficient'
 WHEN s.data->>'phase' IN ('paused','probing','trial','exempt') THEN s.data->>'phase'
 WHEN s.data->>'phase'='limited' OR s.data->'latency'->>'phase'='limited' OR s.data->'speed'->>'phase'='limited' THEN 'limited'
 WHEN EXISTS (SELECT 1 FROM jsonb_each(COALESCE(NULLIF(s.data->'cache','null'::jsonb),'{}'::jsonb)) c WHERE c.value->>'phase' IN ('reduced','recovering')) THEN 'cache_limited'
 WHEN s.data->'latency'->>'phase'='watching' OR s.data->'speed'->>'phase'='watching' OR EXISTS (SELECT 1 FROM jsonb_each(COALESCE(NULLIF(s.data->'cache','null'::jsonb),'{}'::jsonb)) c WHERE c.value->>'phase'='watching') THEN 'observed'
 WHEN s.data->>'phase'='healthy' AND EXISTS (SELECT 1 FROM jsonb_each(COALESCE(NULLIF(s.data->'cache','null'::jsonb),'{}'::jsonb)) c WHERE c.value->>'phase'='insufficient') THEN 'insufficient'
 ELSE s.data->>'phase' END AS effective_phase
 FROM passive_quality_states s JOIN accounts a ON a.id=s.account_id JOIN passive_quality_policies p ON p.id=s.policy_id WHERE a.deleted_at IS NULL) `

type passiveStateCursor struct {
	Policy  int64  `json:"p"`
	Account int64  `json:"a"`
	Model   string `json:"m"`
}

func passiveQueryLimit(n int) int {
	if n <= 0 {
		return 100
	}
	if n > 200 {
		return 200
	}
	return n
}
func passiveStateWhere(q service.PassiveQualityQuery) (string, []any) {
	return ` WHERE ($1::bigint=0 OR s.policy_id=$1) AND ($2::bigint=0 OR s.account_id=$2) AND ($3::text='' OR s.model_key=$3) AND ($4::text='' OR s.effective_phase=$4 OR ($4='paused' AND s.effective_phase='probing') OR ($4='limited' AND s.effective_phase='cache_limited'))`, []any{q.PolicyID, q.AccountID, q.Model, q.Phase}
}
func (r *passiveQualityRepository) QueryStates(ctx context.Context, q service.PassiveQualityQuery) (*service.PassiveQualityStatePage, error) {
	out := &service.PassiveQualityStatePage{Items: []service.PassiveQualityState{}}
	where, args := passiveStateWhere(q)
	var cursor passiveStateCursor
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Policy <= 0 || cursor.Account <= 0 || len(cursor.Model) > 200 {
			return nil, service.ErrPassiveQualityCursor
		}
	}
	if err := r.db.QueryRowContext(ctx, passiveStateReport+`SELECT count(*) FROM reporting s`+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	if q.Cursor != "" {
		where += ` AND (s.policy_id,s.account_id,s.model_key)>($5::bigint,$6::bigint,$7::text)`
		args = append(args, cursor.Policy, cursor.Account, cursor.Model)
	}
	args = append(args, passiveQueryLimit(q.Limit)+1)
	query := passiveStateReport + `SELECT s.data,s.account_name,s.other_hold,s.model_key,(SELECT count(*) FROM passive_quality_leases l WHERE l.policy_id=s.policy_id AND l.account_id=s.account_id AND l.model_key=s.model_key AND l.generation=(s.data->>'generation')::bigint) FROM reporting s` + where + fmt.Sprintf(` ORDER BY s.policy_id,s.account_id,s.model_key LIMIT $%d`, len(args))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var last passiveStateCursor
	for rows.Next() {
		var s service.PassiveQualityState
		var raw []byte
		var key, name string
		var held bool
		var inflight int
		if err := rows.Scan(&raw, &name, &held, &key, &inflight); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		s.AccountName = name
		s.OtherHold = held
		s.Inflight = inflight
		if len(out.Items) == passiveQueryLimit(q.Limit) {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, s)
		last = passiveStateCursor{Policy: s.PolicyID, Account: s.AccountID, Model: key}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out.HasMore {
		raw, _ := json.Marshal(last)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out, nil
}
func (r *passiveQualityRepository) QueryEvents(ctx context.Context, q service.PassiveQualityQuery) (*service.PassiveQualityEventPage, error) {
	before := int64(0)
	if q.Cursor != "" {
		var err error
		before, err = strconv.ParseInt(q.Cursor, 10, 64)
		if err != nil || before <= 0 {
			return nil, service.ErrPassiveQualityCursor
		}
	}
	out := &service.PassiveQualityEventPage{Items: []service.PassiveQualityEvent{}}
	rows, err := r.db.QueryContext(ctx, `SELECT id,policy_id,account_id,policy_name,kind,detail,created_at FROM passive_quality_events WHERE ($1::bigint=0 OR policy_id=$1) AND ($2::bigint=0 OR account_id=$2) AND ($3::text='' OR COALESCE(detail->>'model','')=$3) AND ($4::text='' OR kind=$4) AND ($5::timestamptz IS NULL OR created_at >= $5) AND ($6::timestamptz IS NULL OR created_at <= $6) AND ($7::bigint=0 OR id<$7) ORDER BY id DESC LIMIT $8`, q.PolicyID, q.AccountID, q.Model, q.Kind, q.Since, q.Until, before, passiveQueryLimit(q.Limit)+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e service.PassiveQualityEvent
		var raw []byte
		if err := rows.Scan(&e.ID, &e.PolicyID, &e.AccountID, &e.PolicyName, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &e.Detail); err != nil {
			return nil, err
		}
		if len(out.Items) == passiveQueryLimit(q.Limit) {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out.HasMore {
		out.NextCursor = strconv.FormatInt(out.Items[len(out.Items)-1].ID, 10)
	}
	return out, nil
}

// Aggregation deliberately has no page limit; counts never depend on loaded rows.
func (r *passiveQualityRepository) StateSummary(ctx context.Context) (*service.PassiveQualityStateSummary, error) {
	out := &service.PassiveQualityStateSummary{ByPolicy: []service.PassiveQualityPolicyCounts{}}
	err := r.db.QueryRowContext(ctx, passiveStateReport+`SELECT count(*),count(DISTINCT account_id) FILTER (WHERE effective_phase IN ('paused','probing')),count(DISTINCT account_id) FILTER (WHERE effective_phase IN ('limited','cache_limited')),count(DISTINCT account_id) FILTER (WHERE effective_phase='trial'),count(DISTINCT account_id) FILTER (WHERE effective_phase='observed') FROM reporting`).Scan(&out.Total, &out.PausedAccounts, &out.LimitedAccounts, &out.TrialAccounts, &out.ObservedAccounts)
	if err != nil {
		return nil, err
	}
	verified := `data->>'phase'='healthy' AND NOT other_hold AND COALESCE((data->'evidence'->>'complete')::boolean,false)`
	for _, m := range []struct{ config, metric, samples, comparison string }{{"cache_rate", "cache_rate", "cache_samples", ">"}, {"error_rate", "error_rate", "samples", "<"}, {"ttft", "ttft", "ttft_samples", "<"}} {
		verified += fmt.Sprintf(` AND (NOT COALESCE((config->'%s'->>'enabled')::boolean,false) OR ((data->'evidence'->>'%s')::numeric >= COALESCE((config->>'min_samples')::int,5) AND (data->'evidence'->>'%s')::numeric %s (config->'%s'->>'pause')::numeric))`, m.config, m.samples, m.metric, m.comparison, m.config)
	}
	rows, err := r.db.QueryContext(ctx, passiveStateReport+`SELECT policy_id,effective_phase,count(*),count(*) FILTER (WHERE `+verified+`) FROM reporting GROUP BY policy_id,effective_phase ORDER BY policy_id,effective_phase`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pid, count, v int64
		var phase string
		if err := rows.Scan(&pid, &phase, &count, &v); err != nil {
			return nil, err
		}
		if len(out.ByPolicy) == 0 || out.ByPolicy[len(out.ByPolicy)-1].PolicyID != pid {
			out.ByPolicy = append(out.ByPolicy, service.PassiveQualityPolicyCounts{PolicyID: pid, Phases: map[string]int64{}})
		}
		p := &out.ByPolicy[len(out.ByPolicy)-1]
		p.Total += count
		p.Verified += v
		p.Phases[phase] = count
	}
	return out, rows.Err()
}
