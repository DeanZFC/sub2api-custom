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
	// Use the same named-model / __other__ mapping as the aggregate matrix.
	displayModel := func(platform, model string) string {
		clauses := []string{}
		for _, p := range cfg.Platforms {
			if len(p.Models) == 0 {
				continue
			}
			clauses = append(clauses, "WHEN "+platform+" = "+bind(p.Platform)+" AND NOT ("+model+" = ANY("+bind(pq.Array(p.Models))+")) THEN '__other__'")
		}
		if len(clauses) == 0 {
			return model
		}
		return "CASE " + strings.Join(clauses, " ") + " ELSE " + model + " END"
	}
	usageModel := displayModel(channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL)
	errorModel := displayModel(channelMonitorV2RecentErrorPlatform, channelMonitorV2RecentErrorModel)
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
	query := `SELECT d.idx, recent.created_at, recent.model, recent.is_error, recent.first_token_ms,
    recent.error_type, recent.error_owner, recent.error_source, recent.status_code, recent.upstream_status_code, recent.message
  FROM jsonb_to_recordset($4::jsonb) AS d(idx integer, platform text, group_id bigint, model text)
  CROSS JOIN LATERAL (
    (SELECT ul.created_at, ul.id, ` + usageModel + ` AS model, FALSE AS is_error, ul.first_token_ms::bigint,
      ''::text AS error_type, ''::text AS error_owner, ''::text AS error_source, 0 AS status_code, 0 AS upstream_status_code, ''::text AS message
    FROM usage_logs ul
    LEFT JOIN groups g ON g.id = ul.group_id
    LEFT JOIN accounts a ON a.id = ul.account_id
    WHERE ` + scope("ul", channelMonitorV2PlatformSQL, usageModel) + `
      AND COALESCE(ul.request_type, 0) NOT IN (4, 6) AND ` + usageLogSuccessFilterUL + `
      AND (NULLIF(ul.request_id, '') IS NULL OR NOT EXISTS (
        SELECT 1 FROM usage_logs newer WHERE newer.request_id = ul.request_id
          AND newer.created_at >= $1 AND newer.created_at < $2
          AND newer.actual_cost > 0 AND COALESCE(newer.request_type, 0) NOT IN (4, 6)
          AND (newer.created_at, newer.id) > (ul.created_at, ul.id)))
    ORDER BY ul.created_at DESC, ul.id DESC LIMIT 20)
    UNION ALL
    (SELECT current_error.created_at, current_error.id, ` + errorModel + ` AS model, TRUE AS is_error, NULL::bigint AS first_token_ms,
      COALESCE(current_error.error_type, ''), COALESCE(current_error.error_owner, ''), COALESCE(current_error.error_source, ''),
      COALESCE(current_error.status_code, 0), COALESCE(current_error.upstream_status_code, 0),
      CONCAT_WS(' ', current_error.error_message, current_error.upstream_error_message, current_error.upstream_error_detail, current_error.error_body)
    FROM ops_error_logs current_error
    LEFT JOIN groups g ON g.id = current_error.group_id
    LEFT JOIN accounts a ON a.id = current_error.account_id
    WHERE ` + scope("current_error", channelMonitorV2RecentErrorPlatform, errorModel) + `
      AND NOT current_error.is_count_tokens
      AND (COALESCE(current_error.status_code, 0) >= 400 OR current_error.error_type = 'cyber_policy')
      AND (NULLIF(current_error.request_id, '') IS NULL OR (
        NOT EXISTS (SELECT 1 FROM ops_error_logs newer WHERE newer.request_id = current_error.request_id
          AND newer.created_at >= $1 AND newer.created_at < $2 AND NOT newer.is_count_tokens
          AND (COALESCE(newer.status_code, 0) >= 400 OR newer.error_type = 'cyber_policy')
          AND (newer.created_at, newer.id) > (current_error.created_at, current_error.id))
        AND NOT EXISTS (SELECT 1 FROM usage_logs succeeded WHERE succeeded.request_id = current_error.request_id
          AND succeeded.created_at >= $1 AND succeeded.created_at < $2
          AND succeeded.actual_cost > 0 AND COALESCE(succeeded.request_type, 0) NOT IN (4, 6))))
    ORDER BY current_error.created_at DESC, current_error.id DESC LIMIT 20)
    ORDER BY created_at DESC, id DESC, is_error DESC LIMIT 20
  ) recent
  ORDER BY d.idx, recent.created_at, recent.id, recent.is_error`
	return query, args, nil
}

func (r *channelMonitorV2Repository) loadChannelMonitorV2RecentRequests(ctx context.Context, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, items []service.ChannelMonitorV2MatrixRow) error {
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
		var request service.ChannelMonitorV2RecentRequest
		var isError bool
		var ttft sql.NullInt64
		var input service.ChannelMonitorV2ErrorInput
		if err := rows.Scan(&index, &request.OccurredAt, &request.Model, &isError, &ttft, &input.ErrorType, &input.ErrorOwner, &input.ErrorSource, &input.StatusCode, &input.UpstreamStatusCode, &input.Message); err != nil {
			return err
		}
		if index < 0 || index >= len(items) {
			return fmt.Errorf("invalid recent request dimension %d", index)
		}
		request.Status = "success"
		if ttft.Valid && ttft.Int64 >= 0 {
			value := ttft.Int64
			request.TTFTMs = &value
			if value >= cfg.HealthThresholds.WarningTTFTMs {
				request.Status = "slow"
			}
		}
		if isError {
			request.Status = "error"
			request.ErrorCategory = service.ClassifyChannelMonitorV2Error(input)
			if ignored[request.ErrorCategory] {
				request.Status = "ignored"
			}
		}
		items[index].RecentRequests = append(items[index].RecentRequests, request)
	}
	return rows.Err()
}
