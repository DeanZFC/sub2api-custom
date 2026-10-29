package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Recheck target holds after the caller reserved ordinary account concurrency.
// Shared account locks let healthy admissions run together while fencing all
// protection writers, which take NO KEY UPDATE before changing state.
func groupQualityFinalAdmission(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}, accountID int64, model string) (bool, error) {
	var governed bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passive_quality_policies p
 WHERE p.config->>'purpose' IN ('group_quality_target','cache_target')
 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND (COALESCE(p.config->>'model','')='' OR p.config->>'model'=$2 OR $2='')
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(p.config->'excluded_account_ids','null'::jsonb),'[]')) x WHERE x::bigint=$1)
 AND EXISTS(SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
 WHERE ag.account_id=$1 AND g.status='active' AND g.deleted_at IS NULL
 AND p.config->'group_ids' @> to_jsonb(ARRAY[ag.group_id])))`, accountID, model).Scan(&governed)
	if err != nil || !governed {
		return err == nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT status='active' AND schedulable AND deleted_at IS NULL
 AND NOT COALESCE(temp_unschedulable_until>clock_timestamp(),false)
 AND NOT COALESCE(rate_limit_reset_at>clock_timestamp(),false)
 FROM accounts WHERE id=$1 FOR SHARE`, accountID).Scan(&eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !eligible {
		return false, err
	}
	var held bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE s.account_id=$1 AND p.config->>'purpose' IN ('group_quality_target','cache_target')
 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND s.data->>'phase' IN ('paused','probing','trial','limited')
 AND `+passiveMembership+` AND (p.config->>'scope'='account' OR COALESCE(NULLIF(s.model_key,''),p.config->>'model')=$2 OR $2=''))`, accountID, model).Scan(&held)
	if err != nil {
		return false, err
	}
	if held {
		return false, service.ErrAccountQualityHeld
	}
	return true, tx.Commit()
}
