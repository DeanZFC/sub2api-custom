package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *passiveQualityRepository) RecordFailures(ctx context.Context, items []service.PassiveQualityFailure, gap *time.Time) error {
	var gaps []service.PassiveQualityTelemetryGap
	if gap != nil {
		gaps = []service.PassiveQualityTelemetryGap{{From: *gap, To: *gap, Kind: "all"}}
	}
	return r.RecordTelemetry(ctx, items, gaps)
}

func (r *passiveQualityRepository) RecordTelemetry(ctx context.Context, items []service.PassiveQualityFailure, gaps []service.PassiveQualityTelemetryGap) error {
	type failure struct {
		AccountID  int64     `json:"account_id"`
		GroupID    int64     `json:"group_id"`
		Model      string    `json:"model"`
		RequestKey string    `json:"request_key"`
		StartedAt  time.Time `json:"started_at"`
		At         time.Time `json:"created_at"`
		NonText    bool      `json:"non_text"`
	}
	type report struct {
		AccountID     int64           `json:"account_id"`
		RequestKey    string          `json:"request_key"`
		At            time.Time       `json:"created_at"`
		StartedAt     *time.Time      `json:"started_at"`
		AttemptKey    string          `json:"attempt_key"`
		CacheReported bool            `json:"cache_reported"`
		Metadata      json.RawMessage `json:"cache_metadata"`
	}
	type key struct {
		account int64
		request string
	}
	reports := make(map[key]report)
	failures := make(map[key]failure)
	for _, f := range items {
		k := key{f.AccountID, f.RequestKey}
		if f.CacheReported || f.AttemptKey != "" || f.CacheMetadata != nil {
			item, exists := reports[k]
			if !exists {
				item = report{AccountID: f.AccountID, RequestKey: f.RequestKey, At: f.At, Metadata: json.RawMessage(`{}`)}
			}
			if !f.StartedAt.IsZero() && (item.StartedAt == nil || f.StartedAt.Before(*item.StartedAt)) {
				started := f.StartedAt
				item.StartedAt = &started
			}
			item.CacheReported = item.CacheReported || f.CacheReported
			if item.AttemptKey == "" {
				item.AttemptKey = f.AttemptKey
			}
			if string(item.Metadata) == "{}" && f.CacheMetadata != nil {
				raw, err := json.Marshal(f.CacheMetadata)
				if err != nil {
					return err
				}
				item.Metadata = raw
			}
			reports[k] = item
		} else if _, exists := failures[k]; !exists {
			failures[k] = failure{f.AccountID, f.GroupID, f.Model, f.RequestKey, f.StartedAt, f.At, f.NonText}
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if len(reports) > 0 {
		rows := make([]report, 0, len(reports))
		for _, row := range reports {
			rows = append(rows, row)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO passive_quality_cache_reports(account_id,request_key,created_at,attempt_key,cache_reported,cache_metadata,started_at)
 SELECT x.account_id,x.request_key,x.created_at,x.attempt_key,x.cache_reported,x.cache_metadata,x.started_at
 FROM jsonb_to_recordset($1::jsonb) AS x(account_id bigint,request_key text,created_at timestamptz,attempt_key text,cache_reported boolean,cache_metadata jsonb,started_at timestamptz)
 JOIN accounts a ON a.id=x.account_id ORDER BY x.account_id,x.request_key
 ON CONFLICT(account_id,request_key) DO UPDATE SET cache_reported=passive_quality_cache_reports.cache_reported OR excluded.cache_reported,
 attempt_key=COALESCE(NULLIF(passive_quality_cache_reports.attempt_key,''),excluded.attempt_key),
 started_at=LEAST(passive_quality_cache_reports.started_at,excluded.started_at),
 cache_metadata=CASE WHEN passive_quality_cache_reports.cache_metadata='{}'::jsonb THEN excluded.cache_metadata ELSE passive_quality_cache_reports.cache_metadata END`, raw); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		rows := make([]failure, 0, len(failures))
		for _, row := range failures {
			rows = append(rows, row)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO passive_quality_failures(account_id,model,request_key,started_at,created_at,group_id,non_text)
 SELECT x.account_id,x.model,x.request_key,x.started_at,x.created_at,x.group_id,x.non_text
 FROM jsonb_to_recordset($1::jsonb) AS x(account_id bigint,model text,request_key text,started_at timestamptz,created_at timestamptz,group_id bigint,non_text boolean)
 JOIN accounts a ON a.id=x.account_id ORDER BY x.account_id,x.request_key ON CONFLICT(account_id,request_key) DO NOTHING`, raw); err != nil {
			return err
		}
	}
	if len(gaps) > 0 {
		raw, err := json.Marshal(gaps)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO passive_quality_telemetry_gap_scopes(created_at,ended_at,account_id,group_id,model,kind,dropped_records)
 SELECT x."from",x."to",x.account_id,x.group_id,x.model,x.kind,x.dropped_records
 FROM jsonb_to_recordset($1::jsonb) AS x("from" timestamptz,"to" timestamptz,account_id bigint,group_id bigint,model text,kind text,dropped_records bigint)`, raw); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// A target dimension of zero/empty spans all values; a gap dimension of
// zero/empty means unknown. Unknown data must never establish recovery.
func passiveTelemetryComplete(ctx context.Context, tx interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, from, to time.Time, accountID int64, model string, groupID int64, kind string) (bool, error) {
	var complete bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gaps
 WHERE created_at>=$1 AND created_at<=$2)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gap_scopes
 WHERE created_at<=$2 AND ended_at>=$1
 AND ($3::bigint=0 OR account_id=0 OR account_id=$3)
 AND ($4='' OR model='' OR model=$4)
 AND ($5::bigint=0 OR group_id=0 OR group_id=$5)
 AND ($6='all' OR kind='all' OR kind=$6))`, from, to, accountID, model, groupID, kind).Scan(&complete)
	return complete, err
}

// Retention runs independently of evaluation and every statement is bounded.
func (r *passiveQualityRepository) PrunePassiveQuality(ctx context.Context) error {
	for _, q := range []string{
		`DELETE FROM group_quality_budget_minutes WHERE (policy_id,minute) IN (SELECT policy_id,minute FROM group_quality_budget_minutes WHERE minute<now()-INTERVAL '2 hours' LIMIT 5000)`,
		`DELETE FROM group_quality_traffic_leases WHERE (lease_id,policy_id) IN (SELECT lease_id,policy_id FROM group_quality_traffic_leases WHERE created_at<now()-INTERVAL '2 days' LIMIT 5000)`,
		`DELETE FROM group_quality_traffic_denials WHERE (policy_id,minute) IN (SELECT policy_id,minute FROM group_quality_traffic_denials WHERE minute<now()-INTERVAL '2 days' LIMIT 1000)`,
		`DELETE FROM passive_quality_failures WHERE id IN (SELECT id FROM passive_quality_failures WHERE created_at<now()-INTERVAL '2 days' LIMIT 5000)`,
		`DELETE FROM passive_quality_events WHERE id IN (SELECT id FROM passive_quality_events WHERE created_at<now()-INTERVAL '30 days' LIMIT 1000)`,
		`DELETE FROM passive_quality_telemetry_gaps WHERE created_at IN (SELECT created_at FROM passive_quality_telemetry_gaps WHERE created_at<now()-INTERVAL '2 days' LIMIT 1000)`,
		`DELETE FROM passive_quality_telemetry_gap_scopes WHERE id IN (SELECT id FROM passive_quality_telemetry_gap_scopes WHERE ended_at<now()-INTERVAL '2 days' LIMIT 1000)`,
		`DELETE FROM passive_quality_cache_reports WHERE (account_id,request_key) IN (SELECT account_id,request_key FROM passive_quality_cache_reports WHERE created_at<now()-INTERVAL '2 days' LIMIT 5000)`,
		`DELETE FROM passive_quality_leases WHERE (id,policy_id,model_key) IN (SELECT l.id,l.policy_id,l.model_key FROM passive_quality_leases l JOIN passive_quality_states s ON l.policy_id=s.policy_id AND l.account_id=s.account_id AND l.model_key=s.model_key WHERE (s.data->>'generation')::bigint<>l.generation LIMIT 5000)`,
	} {
		if _, err := r.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
