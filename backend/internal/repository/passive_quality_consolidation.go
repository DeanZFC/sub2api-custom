package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SavePolicy locks every policy before calling this. Retire only the group
// owned by the unified target, preserving custom rules and evidence elsewhere.
func (r *passiveQualityRepository) consolidateGroupProtection(ctx context.Context, tx *sql.Tx, groupID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,version,updated_at,config FROM passive_quality_policies WHERE COALESCE(config->>'purpose','') NOT IN ('cache_target','group_quality_target') AND config->'group_ids' @> to_jsonb(ARRAY[$1::bigint]) ORDER BY id`, groupID)
	if err != nil {
		return err
	}
	var policies []service.PassiveQualityPolicy
	for rows.Next() {
		p, e := scanPassivePolicy(rows)
		if e != nil {
			_ = rows.Close()
			return e
		}
		policies = append(policies, p)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, p := range policies {
		p.GroupIDs = slices.DeleteFunc(p.GroupIDs, func(id int64) bool { return id == groupID })
		if !slices.Contains(p.ConsolidatedGroupIDs, groupID) {
			p.ConsolidatedGroupIDs = append(p.ConsolidatedGroupIDs, groupID)
		}
		if len(p.GroupIDs) == 0 {
			p.Enabled = false
		}
		// Unchanged members retain their decision version and sample boundary.
		since := p.EvidenceSince()
		p.EvidenceStartedAt, p.EvaluationVersion = &since, p.DecisionVersion()
		raw, e := json.Marshal(p.PassiveQualityConfig)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `UPDATE passive_quality_policies SET config=$2,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, p.ID, raw); err != nil {
			return err
		}
		accounts, e := tx.QueryContext(ctx, `SELECT DISTINCT s.account_id FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE p.id=$1 AND NOT (`+passiveMembership+`) ORDER BY s.account_id`, p.ID)
		if e != nil {
			return e
		}
		var ids []int64
		for accounts.Next() {
			var id int64
			if err = accounts.Scan(&id); err != nil {
				_ = accounts.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = accounts.Err()
		_ = accounts.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err = lockPassiveAccount(ctx, tx, id); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `WITH removed AS (DELETE FROM passive_quality_states WHERE policy_id=$1 AND account_id=$2 RETURNING *) INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail) SELECT policy_id,account_id,$3,'group_target_consolidated',data FROM removed`, p.ID, id, p.Name); err != nil {
				return err
			}
			if err = reconcilePassiveAccount(ctx, tx, id); err != nil {
				return err
			}
		}
	}
	return nil
}
