package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) ListRequestDetails(ctx context.Context, filter *service.OpsRequestDetailFilter) ([]*service.OpsRequestDetail, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, fmt.Errorf("nil ops repository")
	}

	page, pageSize, startTime, endTime := filter.Normalize()
	offset := (page - 1) * pageSize

	conditions := make([]string, 0, 16)
	args := make([]any, 0, 24)

	// Placeholders $1/$2 reserved for time window inside the CTE.
	args = append(args, startTime.UTC(), endTime.UTC())

	addCondition := func(condition string, values ...any) {
		conditions = append(conditions, condition)
		args = append(args, values...)
	}

	if filter != nil {
		if kind := strings.TrimSpace(strings.ToLower(filter.Kind)); kind != "" && kind != "all" {
			if kind != string(service.OpsRequestKindSuccess) && kind != string(service.OpsRequestKindError) {
				return nil, 0, fmt.Errorf("invalid kind")
			}
			addCondition(fmt.Sprintf("kind = $%d", len(args)+1), kind)
		}

		if platform := strings.TrimSpace(strings.ToLower(filter.Platform)); platform != "" {
			addCondition(fmt.Sprintf("platform = $%d", len(args)+1), platform)
		}
		if filter.GroupID != nil && *filter.GroupID > 0 {
			addCondition(fmt.Sprintf("group_id = $%d", len(args)+1), *filter.GroupID)
		}

		if filter.UserID != nil && *filter.UserID > 0 {
			addCondition(fmt.Sprintf("user_id = $%d", len(args)+1), *filter.UserID)
		}
		if filter.APIKeyID != nil && *filter.APIKeyID > 0 {
			addCondition(fmt.Sprintf("api_key_id = $%d", len(args)+1), *filter.APIKeyID)
		}
		if filter.AccountID != nil && *filter.AccountID > 0 {
			addCondition(fmt.Sprintf("account_id = $%d", len(args)+1), *filter.AccountID)
		}

		if model := strings.TrimSpace(filter.Model); model != "" {
			addCondition(fmt.Sprintf("model = $%d", len(args)+1), model)
		}
		if requestID := strings.TrimSpace(filter.RequestID); requestID != "" {
			addCondition(fmt.Sprintf("request_id = $%d", len(args)+1), requestID)
		}
		if q := strings.TrimSpace(filter.Query); q != "" {
			like := "%" + strings.ToLower(q) + "%"
			startIdx := len(args) + 1
			addCondition(
				fmt.Sprintf("(LOWER(COALESCE(request_id,'')) LIKE $%d OR LOWER(COALESCE(model,'')) LIKE $%d OR LOWER(COALESCE(message,'')) LIKE $%d)",
					startIdx, startIdx+1, startIdx+2,
				),
				like, like, like,
			)
		}

		if filter.MinDurationMs != nil {
			addCondition(fmt.Sprintf("duration_ms >= $%d", len(args)+1), *filter.MinDurationMs)
		}
		if filter.MaxDurationMs != nil {
			addCondition(fmt.Sprintf("duration_ms <= $%d", len(args)+1), *filter.MaxDurationMs)
		}
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	cte := `
WITH combined AS (
  SELECT
    'success'::TEXT AS kind,
    ul.id AS log_id,
    ul.created_at AS created_at,
    ul.request_id AS request_id,
    COALESCE(NULLIF(g.platform, ''), NULLIF(a.platform, ''), '') AS platform,
    ul.model AS model,
    ul.duration_ms AS duration_ms,
    NULL::INT AS status_code,
    NULL::BIGINT AS error_id,
    NULL::TEXT AS phase,
    NULL::TEXT AS severity,
    NULL::TEXT AS message,
    ul.user_id AS user_id,
    ul.api_key_id AS api_key_id,
    ul.account_id AS account_id,
    ul.group_id AS group_id,
    ul.stream AS stream,
    ul.first_token_ms AS first_token_ms,
    ul.request_type AS request_type,
    ul.openai_ws_mode AS openai_ws_mode
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $1 AND ul.created_at < $2

  UNION ALL

  SELECT
    'error'::TEXT AS kind,
    o.id AS log_id,
    o.created_at AS created_at,
    COALESCE(NULLIF(o.request_id,''), NULLIF(o.client_request_id,''), '') AS request_id,
    COALESCE(NULLIF(o.platform, ''), NULLIF(g.platform, ''), NULLIF(a.platform, ''), '') AS platform,
    o.model AS model,
    o.duration_ms AS duration_ms,
    o.status_code AS status_code,
    o.id AS error_id,
    o.error_phase AS phase,
    o.severity AS severity,
    o.error_message AS message,
    o.user_id AS user_id,
    o.api_key_id AS api_key_id,
    o.account_id AS account_id,
    o.group_id AS group_id,
    o.stream AS stream,
    o.time_to_first_token_ms AS first_token_ms,
    o.request_type AS request_type,
    FALSE AS openai_ws_mode
  FROM ops_error_logs o
  LEFT JOIN groups g ON g.id = o.group_id
  LEFT JOIN accounts a ON a.id = o.account_id
  WHERE o.created_at >= $1 AND o.created_at < $2
    AND COALESCE(o.status_code, 0) >= 400
)
`

	countQuery := fmt.Sprintf(`%s SELECT COUNT(1) FROM combined %s`, cte, where)
	var total int64
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		if err == sql.ErrNoRows {
			total = 0
		} else {
			return nil, 0, err
		}
	}

	sort := "ORDER BY created_at DESC, log_id DESC, kind"
	resultSort := "ORDER BY p.created_at DESC, p.log_id DESC, p.kind"
	if filter != nil {
		switch strings.TrimSpace(strings.ToLower(filter.Sort)) {
		case "", "created_at_desc":
			// default
		case "duration_desc":
			sort = "ORDER BY duration_ms DESC NULLS LAST, created_at DESC, log_id DESC, kind"
			resultSort = "ORDER BY p.duration_ms DESC NULLS LAST, p.created_at DESC, p.log_id DESC, p.kind"
		default:
			return nil, 0, fmt.Errorf("invalid sort")
		}
	}

	// Enrich only the selected page, keeping user/name lookups and usage details
	// out of the count query and the full request history scan.
	listQuery := fmt.Sprintf(`
%s
SELECT
  p.kind, p.created_at, p.request_id, p.platform, p.model,
  p.duration_ms, p.status_code, p.error_id, p.phase, p.severity, p.message,
  p.user_id, p.api_key_id, p.account_id, p.group_id, p.stream,
  u.email, g.name, a.name, k.name,
  p.first_token_ms, p.request_type, p.openai_ws_mode, ul.upstream_model,
  ul.input_tokens, ul.output_tokens, ul.cache_read_tokens, ul.cache_creation_tokens,
  ul.image_input_tokens, ul.image_output_tokens, ul.actual_cost,
  COALESCE(ul.account_stats_cost, ul.total_cost) * COALESCE(ul.account_rate_multiplier, 1) AS account_cost
FROM (
  SELECT * FROM combined
  %s
  %s
  LIMIT $%d OFFSET $%d
) p
LEFT JOIN users u ON u.id = p.user_id
LEFT JOIN groups g ON g.id = p.group_id
LEFT JOIN accounts a ON a.id = p.account_id
LEFT JOIN api_keys k ON k.id = p.api_key_id
LEFT JOIN usage_logs ul ON p.kind = 'success' AND ul.id = p.log_id AND ul.created_at = p.created_at
%s
`, cte, where, sort, len(args)+1, len(args)+2, resultSort)

	listArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := r.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	toIntPtr := func(v sql.NullInt64) *int {
		if !v.Valid {
			return nil
		}
		i := int(v.Int64)
		return &i
	}
	toInt64Ptr := func(v sql.NullInt64) *int64 {
		if !v.Valid {
			return nil
		}
		i := v.Int64
		return &i
	}
	toFloat64Ptr := func(v sql.NullFloat64) *float64 {
		if !v.Valid {
			return nil
		}
		return &v.Float64
	}

	out := make([]*service.OpsRequestDetail, 0, pageSize)
	for rows.Next() {
		var (
			kind      string
			createdAt time.Time
			requestID sql.NullString
			platform  sql.NullString
			model     sql.NullString

			durationMs sql.NullInt64
			statusCode sql.NullInt64
			errorID    sql.NullInt64

			phase    sql.NullString
			severity sql.NullString
			message  sql.NullString

			userID    sql.NullInt64
			apiKeyID  sql.NullInt64
			accountID sql.NullInt64
			groupID   sql.NullInt64

			stream bool

			userEmail, groupName, accountName, apiKeyName sql.NullString
			firstTokenMs, requestType                     sql.NullInt64
			openaiWSMode                                  sql.NullBool
			upstreamModel                                 sql.NullString
			inputTokens, outputTokens                     sql.NullInt64
			cacheReadTokens, cacheCreationTokens          sql.NullInt64
			imageInputTokens, imageOutputTokens           sql.NullInt64
			actualCost, accountCost                       sql.NullFloat64
		)

		if err := rows.Scan(
			&kind,
			&createdAt,
			&requestID,
			&platform,
			&model,
			&durationMs,
			&statusCode,
			&errorID,
			&phase,
			&severity,
			&message,
			&userID,
			&apiKeyID,
			&accountID,
			&groupID,
			&stream,
			&userEmail, &groupName, &accountName, &apiKeyName,
			&firstTokenMs, &requestType, &openaiWSMode, &upstreamModel,
			&inputTokens, &outputTokens, &cacheReadTokens, &cacheCreationTokens,
			&imageInputTokens, &imageOutputTokens, &actualCost, &accountCost,
		); err != nil {
			return nil, 0, err
		}

		item := &service.OpsRequestDetail{
			Kind:      service.OpsRequestKind(kind),
			CreatedAt: createdAt,
			RequestID: strings.TrimSpace(requestID.String),
			Platform:  strings.TrimSpace(platform.String),
			Model:     strings.TrimSpace(model.String),

			DurationMs: toIntPtr(durationMs),
			StatusCode: toIntPtr(statusCode),
			ErrorID:    toInt64Ptr(errorID),
			Phase:      phase.String,
			Severity:   severity.String,
			Message:    message.String,

			UserID:    toInt64Ptr(userID),
			APIKeyID:  toInt64Ptr(apiKeyID),
			AccountID: toInt64Ptr(accountID),
			GroupID:   toInt64Ptr(groupID),

			UserEmail:           userEmail.String,
			GroupName:           groupName.String,
			AccountName:         accountName.String,
			APIKeyName:          apiKeyName.String,
			FirstTokenMs:        toIntPtr(firstTokenMs),
			UpstreamModel:       upstreamModel.String,
			InputTokens:         toIntPtr(inputTokens),
			OutputTokens:        toIntPtr(outputTokens),
			CacheReadTokens:     toIntPtr(cacheReadTokens),
			CacheCreationTokens: toIntPtr(cacheCreationTokens),
			ImageInputTokens:    toIntPtr(imageInputTokens),
			ImageOutputTokens:   toIntPtr(imageOutputTokens),
			ActualCost:          toFloat64Ptr(actualCost),
			AccountCost:         toFloat64Ptr(accountCost),

			Stream: stream,
		}
		resolvedType := service.RequestTypeFromInt16(int16(requestType.Int64))
		if resolvedType == service.RequestTypeUnknown {
			resolvedType = service.RequestTypeFromLegacy(stream, openaiWSMode.Bool)
		}
		item.RequestType = resolvedType.String()

		if item.Platform == "" {
			item.Platform = "unknown"
		}

		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return out, total, nil
}
