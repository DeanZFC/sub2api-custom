package repository

import (
	"context"
	"database/sql"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.GroupQualityActiveRecoveryGate = (*passiveQualityRepository)(nil)

func (r *passiveQualityRepository) GroupQualityActiveRecoveryEnabled(ctx context.Context, groupID int64, model string) (bool, error) {
	var enabled bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passive_quality_policies p
 JOIN groups g ON g.id=$1 AND g.deleted_at IS NULL AND g.status='active'
 WHERE p.config->>'purpose' IN ('group_quality_target','cache_target')
 AND p.config->>'recovery_mode'='active' AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND p.config->'group_ids' @> to_jsonb(ARRAY[$1::bigint])
 AND (COALESCE(p.config->>'model','')='' OR p.config->>'model'=$2))`, groupID, model).Scan(&enabled)
	return enabled, err
}

// Membership and the public model, rather than a caller-supplied fallback
// marker, decide whether this account is governed by active-only recovery.
func groupQualityAccountActiveRecovery(ctx context.Context, tx *sql.Tx, accountID int64, model string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM passive_quality_policies p
 WHERE p.config->>'purpose' IN ('group_quality_target','cache_target')
 AND p.config->>'recovery_mode'='active' AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND (COALESCE(p.config->>'model','')='' OR p.config->>'model'=$2)
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(p.config->'excluded_account_ids','null'::jsonb),'[]')) x WHERE x::bigint=$1)
 AND EXISTS(SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
 WHERE ag.account_id=$1 AND g.status='active' AND g.deleted_at IS NULL
 AND p.config->'group_ids' @> to_jsonb(ARRAY[ag.group_id])))`, accountID, model).Scan(&active)
	return active, err
}
