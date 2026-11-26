package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Record the usage and its optional terminal error together. The reservation
// row serializes duplicate writes without a new index on the full usage table.
func (r *qualityOperationsRepository) RecordScheduledTestUsage(ctx context.Context, u service.ScheduledTestUsage) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var recorded bool
	err = tx.QueryRowContext(ctx, `SELECT usage_recorded FROM quality_probe_attempts WHERE id=$1 AND account_id=$2 FOR UPDATE`, u.ReservationID, u.AccountID).Scan(&recorded)
	if err != nil || recorded {
		return err
	}

	var group, setID *int64
	if len(u.GroupIDs) > 0 {
		group = &u.GroupIDs[0]
	}
	if len(u.GroupIDs) > 1 {
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO channel_quality_group_sets(group_ids) VALUES ($1)
 ON CONFLICT(group_ids) DO UPDATE SET group_ids=EXCLUDED.group_ids RETURNING id`, pq.Array(u.GroupIDs)).Scan(&id)
		if err != nil {
			return err
		}
		setID = &id
	}
	a := u.Attempt
	raw, err := json.Marshal(a.ScheduledTestTokenUsage)
	if err != nil {
		return err
	}
	requestID := fmt.Sprintf("channel-quality:%d", u.ReservationID)
	status, detail := scheduledTestTransportStatus(a)
	typ := service.RequestTypeFromLegacy(u.Stream, false)
	// usage_logs has integer counters. Preserve the nullable, original provider
	// counters alongside normalized columns, including unrepresentable counts.
	counter := func(n *int64) int64 {
		if n == nil || *n < 0 || *n > math.MaxInt32 {
			return 0
		}
		return *n
	}
	ms := func(v *float64) *int64 {
		if v == nil || *v < 0 || *v > math.MaxInt32 {
			return nil
		}
		n := int64(math.Round(*v))
		return &n
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_logs
 (account_id,group_id,quality_group_set_id,request_id,model,requested_model,upstream_model,reasoning_effort,
  inbound_endpoint,http_status,request_type,stream,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,
  quality_usage,first_token_ms,duration_ms,created_at)
 VALUES ($1,$2,$3,$4,left($5,100),left($5,100),nullif(left($6,100),''),nullif($7,''),
 '/internal/channel-quality',$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		u.AccountID, group, setID, requestID, u.Model, a.UpstreamModel, u.ReasoningEffort, status, typ, u.Stream, counter(&u.InputTokens),
		counter(a.OutputTokens), counter(a.CacheCreationTokens), counter(a.CacheReadTokens), string(raw), ms(a.FirstTokenMS), ms(a.DurationMS), a.StartedAt)
	if err != nil {
		return err
	}
	if detail != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs
 (account_id,group_id,quality_group_set_id,platform,request_id,model,requested_model,request_path,request_type,stream,
 error_phase,error_type,error_owner,error_source,status_code,upstream_status_code,error_message,time_to_first_token_ms,duration_ms,created_at)
 VALUES ($1,$2,$3,$4,$5,left($6,100),left($6,100),'/internal/channel-quality',$7,$8,'upstream','upstream_error','provider',
 'upstream_http',$9,nullif($10,0),$11,$12,$13,$14)`, u.AccountID, group, setID, u.Platform, requestID, u.Model, typ, u.Stream,
			status, a.HTTPStatus, detail, ms(a.FirstTokenMS), ms(a.DurationMS), a.StartedAt)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE quality_probe_attempts SET usage_recorded=TRUE WHERE id=$1`, u.ReservationID); err != nil {
		return err
	}
	return tx.Commit()
}

func scheduledTestTransportStatus(a service.ScheduledTestRequestAttempt) (int, string) {
	status := a.HTTPStatus
	if a.FailureKind == "" && status >= 200 && status < 300 {
		return status, ""
	}
	// An HTTP 200 can end in a broken stream. Preserve upstream_status_code
	// separately from the effective terminal status used by the monitor.
	if status < 400 {
		status = http.StatusBadGateway
		if a.FailureKind == "canceled" {
			status = 499
		}
	}
	message := map[string]string{"network": "upstream transport error", "canceled": "context canceled", "quota": "insufficient quota", "rate_limit": "rate limit", "auth": "upstream credentials rejected", "timeout": "upstream timeout"}[a.FailureKind]
	if message == "" {
		message = "upstream request failed"
	}
	return status, message
}

var _ service.QualityOperationsRepository = (*qualityOperationsRepository)(nil)
