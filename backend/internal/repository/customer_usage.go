package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type customerUsageRepository struct{ db *sql.DB }

func NewCustomerUsageRepository(db *sql.DB) service.CustomerUsageRepository {
	return &customerUsageRepository{db}
}

func (r *customerUsageRepository) Key(ctx context.Context, userID, keyID int64) (*service.CustomerUsageKey, error) {
	var key service.CustomerUsageKey
	err := r.db.QueryRowContext(ctx, "SELECT id,name,status,deleted_at FROM api_keys WHERE user_id=$1 AND id=$2", userID, keyID).Scan(&key.ID, &key.Name, &key.Status, &key.DeletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &key, nil
}

func (r *customerUsageRepository) Keys(ctx context.Context, userID int64, p pagination.PaginationParams) ([]service.CustomerUsageKey, int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int64
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM api_keys WHERE user_id=$1", userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,name,status,deleted_at FROM api_keys WHERE user_id=$1 ORDER BY id LIMIT $2 OFFSET $3", userID, p.Limit(), p.Offset())
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	keys := make([]service.CustomerUsageKey, 0)
	for rows.Next() {
		var k service.CustomerUsageKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Status, &k.DeletedAt); err != nil {
			return nil, 0, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return keys, total, tx.Commit()
}

// A scalar key lookup keeps the shared usage predicates unambiguous. It also
// retrieves historical names without reading key material or upstream accounts.
const customerUsageColumns = `SELECT id,created_at,COALESCE(request_id,''),api_key_id,
 COALESCE((SELECT k.name FROM api_keys k WHERE k.user_id=usage_logs.user_id AND k.id=usage_logs.api_key_id),''),
 COALESCE(NULLIF(TRIM(requested_model),''),model),input_tokens,output_tokens,cache_read_tokens,cache_creation_tokens,
 actual_cost::text,total_cost::text,rate_multiplier::text,http_status,
 COALESCE(NULLIF(TRIM(requested_reasoning_effort),''),reasoning_effort,''),COALESCE(inbound_endpoint,''),
 request_type,stream,openai_ws_mode,COALESCE(NULLIF(billing_mode,''),CASE WHEN image_count>0 THEN 'image' ELSE 'token' END),
 duration_ms,first_token_ms,COALESCE(sync_xid,0) FROM usage_logs `

func scanCustomerRow(rows *sql.Rows) (service.CustomerUsageRow, int64, error) {
	var row service.CustomerUsageRow
	var xid int64
	var request service.UsageLog
	err := rows.Scan(
		&row.ID, &row.CreatedAt, &row.RequestID, &row.APIKeyID, &row.KeyName, &row.Model,
		&row.InputTokens, &row.OutputTokens, &row.CacheReadTokens, &row.CacheCreationTokens,
		&row.ActualCost, &row.TotalCost, &row.RateMultiplier, &row.HTTPStatus,
		&row.ReasoningEffort, &row.Endpoint, &request.RequestType, &request.Stream, &request.OpenAIWSMode,
		&row.BillingMode, &row.DurationMS, &row.FirstTokenMS, &xid,
	)
	row.RequestType = request.EffectiveRequestType().String()
	return row, xid, err
}

func (r *customerUsageRepository) Sync(ctx context.Context, f UsageLogFilters, pos service.UsageSyncPosition, limit int) ([]service.CustomerUsageRow, service.UsageSyncPosition, bool, error) {
	if f.UserID <= 0 || limit < 1 || limit > 1000 {
		return nil, pos, false, errors.New("invalid customer usage scope or limit")
	}
	// xmin excludes every in-flight writer. An ID or created_at upper bound alone
	// would permanently skip an older insert that commits after a newer one.
	if pos.Fence == 0 {
		if err := r.db.QueryRowContext(ctx, "SELECT txid_snapshot_xmin(txid_current_snapshot())").Scan(&pos.Fence); err != nil {
			return nil, pos, false, err
		}
	}
	cond, args := usageLogConditions(f)
	n := len(args)
	cond = append(cond, fmt.Sprintf("(COALESCE(sync_xid,0),id)>($%d,$%d) AND COALESCE(sync_xid,0)<$%d", n+1, n+2, n+3))
	query := customerUsageColumns + buildWhere(cond) + fmt.Sprintf(" ORDER BY COALESCE(sync_xid,0),id LIMIT $%d", n+4)
	rows, err := r.db.QueryContext(ctx, query, append(args, pos.XID, pos.ID, pos.Fence, limit+1)...)
	if err != nil {
		return nil, pos, false, err
	}
	defer rows.Close()
	data := make([]service.CustomerUsageRow, 0, limit)
	for rows.Next() {
		if len(data) == limit {
			return data, pos, true, nil
		}
		row, xid, err := scanCustomerRow(rows)
		if err != nil {
			return nil, pos, false, err
		}
		data = append(data, row)
		pos.XID = xid
		pos.ID = row.ID
	}
	if err := rows.Err(); err != nil {
		return nil, pos, false, err
	}
	pos.XID, pos.ID, pos.Fence = pos.Fence-1, math.MaxInt64, 0
	return data, pos, false, nil
}

func (r *customerUsageRepository) Export(ctx context.Context, f UsageLogFilters, p pagination.PaginationParams, visit func(service.CustomerUsageRow) error) error {
	if f.UserID <= 0 {
		return errors.New("missing customer usage scope")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked bool
	if err := tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended('customer-usage-csv:' || $1::text,0))", fmt.Sprint(f.UserID)).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return service.ErrUsageExportBusy
	}
	cond, args := usageLogConditions(f)
	rows, err := tx.QueryContext(ctx, customerUsageColumns+buildWhere(cond)+" ORDER BY "+usageLogOrderBy(p), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		row, _, err := scanCustomerRow(rows)
		if err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
