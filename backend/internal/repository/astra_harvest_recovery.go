package repository

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"time"
)

// Ticket/probe writes may advance updated_at without relinquishing a pause
// owned by ticket scheduling. Capture ownership while holding the account lock.
func astraOwnedHarvestPause(ctx context.Context, db sqlExecutor, id int64) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT NOT a.schedulable AND EXISTS (
 SELECT 1 FROM astra_scheduling_states s WHERE s.account_id=a.id AND s.state->>'mode'='account'
 AND (s.state->>'version')::timestamptz=a.updated_at) FROM accounts a WHERE a.id=$1 AND a.deleted_at IS NULL FOR UPDATE OF a`, id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var owned bool
	if rows.Next() {
		if err = rows.Scan(&owned); err != nil {
			return false, err
		}
	}
	return owned, rows.Err()
}
func advanceAstraHarvestPause(ctx context.Context, db sqlExecutor, id int64) error {
	_, err := db.ExecContext(ctx, `UPDATE astra_scheduling_states s SET state=jsonb_set(s.state,'{version}',to_jsonb(a.updated_at)) FROM accounts a WHERE a.id=$1 AND s.account_id=a.id AND s.state->>'mode'='account' AND NOT a.schedulable`, id)
	return err
}
func codexHarvestOnlyExtraUpdate(updates map[string]any) bool {
	if len(updates) == 0 {
		return false
	}
	for key := range updates {
		if !service.IsOpenAICodexTicketExtraKey(key) {
			return false
		}
	}
	return true
}

// Read ownership before admitting background recovery. Manual changes invalidate
// ownership; only the scheduling worker's own pause and removed groups qualify.
func (r *accountRepository) AstraHarvestRecoveryState(ctx context.Context, id int64) (service.AstraHarvestRecoveryState, error) {
	var out service.AstraHarvestRecoveryState
	rows, err := r.sql.QueryContext(ctx, `SELECT s.state,a.updated_at,a.schedulable FROM astra_scheduling_states s JOIN accounts a ON a.id=s.account_id WHERE a.id=$1 AND a.deleted_at IS NULL`, id)
	if err != nil {
		return out, err
	}
	var raw []byte
	var updated time.Time
	var sched bool
	found := rows.Next()
	if found {
		err = rows.Scan(&raw, &updated, &sched)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil || !found {
		return out, err
	}
	var state astraActionState
	if err = json.Unmarshal(raw, &state); err != nil {
		return out, err
	}
	if state.Mode == "account" {
		out.OwnedPause = !sched && state.Version.Equal(updated)
		return out, nil
	}
	if state.Mode != "groups" {
		return out, nil
	}
	groups, err := r.sql.QueryContext(ctx, `SELECT account_id,group_id,priority,created_at FROM account_groups WHERE account_id=$1`, id)
	if err != nil {
		return out, err
	}
	defer groups.Close()
	current := map[int64]*astraSchedulingGroup{}
	for groups.Next() {
		var g astraSchedulingGroup
		if err = groups.Scan(&g.AccountID, &g.GroupID, &g.Priority, &g.CreatedAt); err != nil {
			return out, err
		}
		current[g.GroupID] = &g
	}
	if err = groups.Err(); err != nil {
		return out, err
	}
	for _, m := range state.Memberships {
		if !astraMembershipEqual(current[m.GroupID], m.After) {
			return out, nil
		}
		if m.Before != nil {
			out.OriginalGroupIDs = append(out.OriginalGroupIDs, m.GroupID)
		}
	}
	return out, nil
}

// Native OAuth token rotation does not relinquish scheduling ownership when all
// non-token credentials are unchanged. Explicit account edits use Update instead.
func astraOwnedTokenRefreshPause(ctx context.Context, db sqlExecutor, id int64, payload []byte) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT platform='openai' AND type IN ('oauth','setup-token') AND
 (credentials - ARRAY['access_token','refresh_token','id_token','expires_at','expires_in','token_type','scope','last_refresh','last_refresh_at','token_expires_at']) =
 ($2::jsonb - ARRAY['access_token','refresh_token','id_token','expires_at','expires_in','token_type','scope','last_refresh','last_refresh_at','token_expires_at'])
 FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id, string(payload))
	if err != nil {
		return false, err
	}
	var same bool
	if rows.Next() {
		err = rows.Scan(&same)
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil || !same {
		return false, err
	}
	return astraOwnedHarvestPause(ctx, db, id)
}
