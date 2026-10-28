package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// These expressions match the passive aggregator, including composite routing.
const channelMonitorV2RecentErrorPlatform = `lower(CASE
  WHEN g.platform = 'composite' THEN COALESCE(NULLIF(TRIM(a.platform), ''), NULLIF(NULLIF(lower(TRIM(current_error.platform)), ''), 'composite'), 'unknown')
  ELSE COALESCE(NULLIF(TRIM(current_error.platform), ''), 'unknown') END)`
const channelMonitorV2RecentErrorModel = `COALESCE(NULLIF(TRIM(current_error.requested_model), ''), NULLIF(TRIM(current_error.model), ''), 'unknown')`

type channelMonitorV2RecentDimension struct {
	Index    int     `json:"idx"`
	Platform string  `json:"platform"`
	GroupID  *int64  `json:"group_id"`
	Model    *string `json:"model"`
}

func channelMonitorV2RecentQuery(filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, items []service.ChannelMonitorV2MatrixRow) (string, []any, error) {
	dimensions := make([]channelMonitorV2RecentDimension, 0, len(items))
	for i, item := range items {
		dimension := channelMonitorV2RecentDimension{Index: i, Platform: item.Platform, GroupID: item.GroupID}
		if item.Model != "" {
			model := item.Model
			dimension.Model = &model
		}
		dimensions = append(dimensions, dimension)
	}
	encoded, err := json.Marshal(dimensions)
	if err != nil {
		return "", nil, err
	}
	platforms := channelMonitorV2EnabledPlatforms(cfg)
	if len(filter.Platforms) > 0 {
		platforms = intersectStrings(platforms, filter.Platforms)
	}
	args := []any{filter.Start, filter.End, pq.Array(platforms), string(encoded)}
	bind := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	// Preserve the same observed identities as the aggregate matrix.
	usageModel := channelMonitorV2ModelSQL
	errorModel := channelMonitorV2RecentErrorModel
	groups, empty := channelMonitorV2ScopedGroupIDs(filter, cfg)
	groupParam := ""
	if len(groups) > 0 {
		groupParam = bind(pq.Array(groups))
	}
	modelParam := ""
	if len(filter.Models) > 0 {
		modelParam = bind(pq.Array(filter.Models))
	}
	scope := func(alias, platform, model string) string {
		conditions := []string{
			alias + ".created_at >= $1", alias + ".created_at < $2",
			platform + " = ANY($3)", platform + " = d.platform",
			"(d.group_id IS NULL OR " + alias + ".group_id = d.group_id)",
			"(d.model IS NULL OR (" + model + ") = d.model)",
		}
		if empty {
			conditions = append(conditions, "FALSE")
		}
		if groupParam != "" {
			conditions = append(conditions, alias+".group_id = ANY("+groupParam+")")
		}
		if modelParam != "" {
			conditions = append(conditions, "("+model+") = ANY("+modelParam+")")
		}
		return strings.Join(conditions, " AND ")
	}
	// Each branch is capped per visible card before UNION; no global truncation
	// can starve quiet channels. Correlated lookups use source time/group indexes.
	query := channelMonitorV2FinalFailureKeysSQL + `SELECT d.idx, recent.id, recent.created_at, recent.model, recent.is_error, recent.first_token_ms, recent.duration_ms, recent.cache_rate, recent.output_tps,
    recent.error_type, recent.error_owner, recent.error_source, recent.status_code, recent.upstream_status_code, recent.message, recent.error_summary
  FROM jsonb_to_recordset($4::jsonb) AS d(idx integer, platform text, group_id bigint, model text)
  CROSS JOIN LATERAL (
    (SELECT ul.created_at, ul.id, ` + usageModel + ` AS model, FALSE AS is_error, ul.first_token_ms::bigint, ul.duration_ms::bigint,
      ul.cache_read_tokens::double precision /
        NULLIF(ul.input_tokens::bigint + ul.cache_creation_tokens::bigint + ul.cache_read_tokens::bigint, 0) AS cache_rate,
      CASE WHEN ` + outputTPSEligibleSQL("ul") + ` THEN ul.output_tokens::double precision*1000/(ul.duration_ms::bigint-ul.first_token_ms::bigint) END AS output_tps,
      ''::text AS error_type, ''::text AS error_owner, ''::text AS error_source, 0 AS status_code, 0 AS upstream_status_code, ''::text AS message, ''::text AS error_summary
    FROM usage_logs ul
    LEFT JOIN groups g ON g.id = ul.group_id
    LEFT JOIN accounts a ON a.id = ul.account_id
    WHERE ` + scope("ul", channelMonitorV2PlatformSQL, usageModel) + `
      AND COALESCE(ul.request_type, 0) NOT IN (4, 6) AND (` + usageLogSuccessFilterUL + ` OR ul.output_tokens>0)
      AND ` + channelMonitorV2NoFinalFailureSQL("ul") + `
      AND (COALESCE(ul.request_type,0)=3 OR COALESCE(ul.openai_ws_mode,false) OR NULLIF(ul.request_id, '') IS NULL OR NOT EXISTS (
        SELECT 1 FROM usage_logs newer WHERE newer.request_id = ul.request_id
          AND COALESCE(newer.api_key_id,0)=COALESCE(ul.api_key_id,0) AND COALESCE(newer.account_id,0)=COALESCE(ul.account_id,0)
          AND newer.created_at >= $1 AND newer.created_at < $2
          AND (newer.actual_cost > 0 OR newer.output_tokens>0) AND COALESCE(newer.request_type, 0) NOT IN (4, 6)
          AND (newer.created_at, newer.id) > (ul.created_at, ul.id)))
    ORDER BY ul.created_at DESC, ul.id DESC LIMIT 20)
    UNION ALL
    (SELECT current_error.created_at, current_error.id, ` + errorModel + ` AS model, TRUE AS is_error, current_error.time_to_first_token_ms AS first_token_ms,
      current_error.duration_ms::bigint, NULL::double precision AS cache_rate, NULL::double precision AS output_tps,
      COALESCE(current_error.error_type, ''), COALESCE(current_error.error_owner, ''), COALESCE(current_error.error_source, ''),
      COALESCE(current_error.status_code, 0), COALESCE(current_error.upstream_status_code, 0),
      CONCAT_WS(' ', current_error.error_message, current_error.upstream_error_message, current_error.upstream_error_detail, current_error.error_body),
      COALESCE(NULLIF(TRIM(current_error.upstream_error_message), ''), NULLIF(TRIM(current_error.upstream_error_detail), ''),
        NULLIF(TRIM(current_error.error_message), ''), current_error.error_body, '')
    FROM ops_error_logs current_error
    LEFT JOIN groups g ON g.id = current_error.group_id
    LEFT JOIN accounts a ON a.id = current_error.account_id
    WHERE ` + scope("current_error", channelMonitorV2RecentErrorPlatform, errorModel) + `
      AND NOT current_error.is_count_tokens
      AND (COALESCE(current_error.status_code, 0) >= 400 OR current_error.error_type = 'cyber_policy')
      AND (COALESCE(current_error.request_type,0)=3 OR NULLIF(current_error.request_id, '') IS NULL OR (
        NOT EXISTS (SELECT 1 FROM ops_error_logs newer WHERE newer.request_id = current_error.request_id
          AND COALESCE(newer.api_key_id,0)=COALESCE(current_error.api_key_id,0) AND COALESCE(newer.account_id,0)=COALESCE(current_error.account_id,0)
          AND newer.created_at >= $1 AND newer.created_at < $2 AND NOT newer.is_count_tokens
          AND (COALESCE(newer.status_code, 0) >= 400 OR newer.error_type = 'cyber_policy')
          AND (newer.created_at, newer.id) > (current_error.created_at, current_error.id))))
    ORDER BY current_error.created_at DESC, current_error.id DESC LIMIT 20)
    ORDER BY created_at DESC, id DESC, is_error DESC LIMIT 20
  ) recent
  ORDER BY d.idx, recent.created_at, recent.id, recent.is_error`
	return query, args, nil
}

func (r *channelMonitorV2Repository) loadChannelMonitorV2RecentRequests(ctx context.Context, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, items []service.ChannelMonitorV2MatrixRow, admin bool) error {
	if len(items) == 0 || channelMonitorV2RestrictedGroupScopeEmpty(filter, cfg) {
		return nil
	}
	query, args, err := channelMonitorV2RecentQuery(filter, cfg, items)
	if err != nil {
		return err
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	ignored := make(map[string]bool, len(cfg.IgnoredErrorCategories))
	for _, category := range cfg.IgnoredErrorCategories {
		ignored[category] = true
	}
	for rows.Next() {
		var index int
		var logID int64
		var summary string
		var request service.ChannelMonitorV2RecentRequest
		var isError bool
		var ttft, duration sql.NullInt64
		var cacheRate sql.NullFloat64
		var input service.ChannelMonitorV2ErrorInput
		if err := rows.Scan(&index, &logID, &request.OccurredAt, &request.Model, &isError, &ttft, &duration, &cacheRate, &request.OutputTPS, &input.ErrorType, &input.ErrorOwner, &input.ErrorSource, &input.StatusCode, &input.UpstreamStatusCode, &input.Message, &summary); err != nil {
			return err
		}
		if index < 0 || index >= len(items) {
			return fmt.Errorf("invalid recent request dimension %d", index)
		}
		request.Status = "success"
		if duration.Valid && duration.Int64 >= 0 {
			value := duration.Int64
			request.DurationMs = &value
		}
		if cacheRate.Valid && cacheRate.Float64 >= 0 && cacheRate.Float64 <= 1 {
			value := cacheRate.Float64
			request.CacheRate = &value
		}
		if ttft.Valid && ttft.Int64 >= 0 {
			value := ttft.Int64
			request.TTFTMs = &value
			if value >= cfg.HealthThresholds.WarningTTFTMs {
				request.Status = "slow"
			}
		}
		if isError {
			request.Status = "error"
			category := service.ClassifyChannelMonitorV2Error(input)
			if admin {
				request.ErrorCategory = category
				request.Error = &service.ChannelMonitorV2RecentError{
					LogID: logID, ErrorType: input.ErrorType,
					StatusCode: input.StatusCode, UpstreamStatusCode: input.UpstreamStatusCode,
					Message: sanitizeChannelMonitorV2ErrorDetail(summary),
				}
			}
			if admin && ignored[category] {
				request.Status = "ignored"
			}
		}
		items[index].RecentRequests = append(items[index].RecentRequests, request)
	}
	return rows.Err()
}
