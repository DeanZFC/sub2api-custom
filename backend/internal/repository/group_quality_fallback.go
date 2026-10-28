package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const groupQualityFallbackLease = 120 * time.Second

func groupQualityFallbackMatches(ctx context.Context, aid int64, model string) bool {
	fallback, ok := service.GroupQualityFallbackFromContext(ctx)
	req, hasTraffic := service.GroupQualityTrafficRequestFromContext(ctx)
	return ok && hasTraffic && req.Model != "" && fallback.AccountID == aid && fallback.GroupID == req.GroupID && fallback.Model == req.Model && fallback.Model == model
}

// Lock every current member in stable order before the emergency decision.
// Other quality writers also lock accounts, so supply cannot recover between
// the live check and reservation. Normal admissions lock only their candidate.
func lockGroupQualityFallbackMembers(ctx context.Context, tx *sql.Tx, gid int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM accounts WHERE id IN(SELECT account_id FROM account_groups WHERE group_id=$1) ORDER BY id FOR NO KEY UPDATE`, gid)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Match model holds to this public model. Account-wide statistics and aggregate
// pauses retain their existing scope; unrelated model evidence is not proof.
const groupQualityActiveHolds = `WITH live_holds AS (
 SELECT s.model_key,(s.model_key='' OR d.output_kind='statistics') AS account_wide
 FROM scheduled_test_protection_states s LEFT JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=$1 AND s.blocked
 UNION ALL SELECT model_key,(model_key='' OR account_wide) FROM scheduled_test_combination_states WHERE account_id=$1 AND blocked
 UNION ALL SELECT model_key,(model_key='') FROM scheduled_test_pass_rate_states WHERE account_id=$1 AND blocked
)`

type groupQualityAccountRow struct {
	ID                     int64          `json:"id"`
	Platform               string         `json:"platform"`
	Type                   string         `json:"type"`
	Credentials            map[string]any `json:"credentials"`
	Extra                  map[string]any `json:"extra"`
	Status                 string         `json:"status"`
	Schedulable            bool           `json:"schedulable"`
	RateMultiplier         *float64       `json:"rate_multiplier"`
	DeletedAt              *time.Time     `json:"deleted_at"`
	ExpiresAt              *time.Time     `json:"expires_at"`
	AutoPauseOnExpired     bool           `json:"auto_pause_on_expired"`
	RateLimitResetAt       *time.Time     `json:"rate_limit_reset_at"`
	OverloadUntil          *time.Time     `json:"overload_until"`
	TempUnschedulableUntil *time.Time     `json:"temp_unschedulable_until"`
	ParentAccountID        *int64         `json:"parent_account_id"`
}

func groupQualityAccountEligible(ctx context.Context, raw []byte, model string, fallback, activeProof, modelProof bool) (bool, error) {
	var row groupQualityAccountRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return false, err
	}
	if row.DeletedAt != nil {
		return false, nil
	}
	if row.Extra == nil {
		row.Extra = map[string]any{}
	}
	if (&service.Account{Extra: row.Extra}).ActiveTestModelBlocked(model) && (!fallback || !activeProof) {
		return false, nil
	}
	if fallback {
		// Ignore only the aggregate flag for this check. Explicit per-model
		// projections still require live proof for the same model or account.
		projected := make(map[string]any, len(row.Extra))
		for key, value := range row.Extra {
			projected[key] = value
		}
		delete(projected, "active_test_all_models_paused")
		if (&service.Account{Extra: projected}).ActiveTestModelBlocked(model) && !modelProof {
			return false, nil
		}
		// Only automatic active protection owns quality_paused. A reason string or
		// scheduler projection alone cannot turn a disabled account back on.
		if row.Status == "quality_paused" && activeProof {
			row.Status = "active"
		}
		if activeProof {
			row.Extra = service.GroupQualityFallbackExtra(row.Extra, model)
			delete(row.Extra, "quality_protection_trial")
		}
		entries, _ := row.Extra["passive_quality"].([]any)
		kept := make([]any, 0, len(entries))
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			phase, _ := entry["phase"].(string)
			matches := entry["scope"] == "account" || entry["model"] == model
			if ok && matches && (phase == "paused" || phase == "probing" || phase == "trial") {
				continue
			}
			kept = append(kept, rawEntry)
		}
		row.Extra["passive_quality"] = kept
	}
	a := &service.Account{ID: row.ID, Platform: row.Platform, Type: row.Type, Credentials: row.Credentials, Extra: row.Extra, Status: row.Status, Schedulable: row.Schedulable, RateMultiplier: row.RateMultiplier, ExpiresAt: row.ExpiresAt, AutoPauseOnExpired: row.AutoPauseOnExpired, RateLimitResetAt: row.RateLimitResetAt, OverloadUntil: row.OverloadUntil, TempUnschedulableUntil: row.TempUnschedulableUntil, ParentAccountID: row.ParentAccountID}
	// The live row has already been filtered; do not let a routing marker loosen
	// any additional common durable scheduling condition during this check.
	ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{})
	if veto, _ := service.OpenAIProfitControlVeto(ctx, a); veto {
		return false, nil
	}
	return service.PassiveQualityPoolAccountEligible(ctx, a, model), nil
}

func groupQualityFallbackCandidate(ctx context.Context, tx *sql.Tx, aid int64, model string) (bool, error) {
	var raw []byte
	var active, matching bool
	err := tx.QueryRowContext(ctx, groupQualityActiveHolds+` SELECT to_jsonb(a),EXISTS(SELECT 1 FROM live_holds),
 EXISTS(SELECT 1 FROM live_holds WHERE account_wide OR model_key=$2)
 FROM accounts a WHERE a.id=$1 FOR NO KEY UPDATE`, aid, model).Scan(&raw, &active, &matching)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return groupQualityAccountEligible(ctx, raw, model, true, active, matching)
}

func groupQualityFallbackHasVerifiedSupply(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, model string, now time.Time) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT s.data,to_jsonb(a) FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 JOIN accounts a ON a.id=s.account_id WHERE s.policy_id=$1 AND s.model_key=$2 AND s.data->>'phase'='healthy'
 AND a.deleted_at IS NULL AND a.status='active' AND a.schedulable
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states capability WHERE capability.account_id=a.id AND capability.blocked AND ($2='' OR capability.model_key='' OR capability.model_key=$2))
 AND `+passiveMembership, p.ID, model)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var stateRaw, accountRaw []byte
		var state service.PassiveQualityState
		if err = rows.Scan(&stateRaw, &accountRaw); err != nil {
			return false, err
		}
		if err = json.Unmarshal(stateRaw, &state); err != nil {
			return false, err
		}
		if state.Model != model || !groupQualityStateVerified(p, state, now) {
			continue
		}
		eligible, e := groupQualityAccountEligible(ctx, accountRaw, model, false, false, false)
		if e != nil {
			return false, e
		}
		if eligible {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Renewal never revives an expired or released emergency request. The caller
// cancels forwarding when ownership is lost and always releases on completion.
func (r *passiveQualityRepository) RefreshGroupQualityFallback(ctx context.Context, lease string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Renewal changes only expiry, so the lease row itself is sufficient;
	// it does not compete with admissions for the shared budget lock.
	result, err := tx.ExecContext(ctx, `UPDATE group_quality_traffic_leases SET fallback_expires_at=clock_timestamp()+($2*interval '1 second')
 WHERE lease_id=$1 AND fallback AND released_at IS NULL AND NOT unused AND fallback_expires_at>clock_timestamp()`, lease, int(groupQualityFallbackLease/time.Second))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return count == 1, nil
}

func readGroupQualityFallbackSummary(ctx context.Context, reader groupQualitySummaryReader, p service.PassiveQualityPolicy, now time.Time) (*service.GroupQualityFallbackSummary, error) {
	s := &service.GroupQualityFallbackSummary{LeaseSeconds: int(groupQualityFallbackLease / time.Second)}
	var supported bool
	if err := reader.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND platform='openai' AND deleted_at IS NULL AND status='active')`, p.GroupIDs[0]).Scan(&supported); err != nil {
		return nil, err
	}
	s.Enabled = supported && p.Enabled && p.Mode == "enforce"
	err := reader.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE released_at IS NULL AND fallback_expires_at>$3),count(*) FILTER(WHERE created_at>=$2),
 COALESCE(sum(CASE WHEN cache_reported AND input_tokens>0 THEN input_tokens ELSE 0 END) FILTER(WHERE created_at>=$2),0),
 COALESCE(sum(CASE WHEN cache_reported AND input_tokens>0 THEN 0 ELSE GREATEST(reserved_input,COALESCE(input_tokens,0)) END) FILTER(WHERE created_at>=$2),0)
 FROM group_quality_traffic_leases WHERE group_id=$1 AND fallback AND NOT unused AND created_at<=$3`, p.GroupIDs[0], now.Add(-time.Hour), now).Scan(&s.InFlight, &s.Requests, &s.InputTokens, &s.ReservedInputTokens)
	if err != nil {
		return nil, err
	}
	var at time.Time
	var aid int64
	err = reader.QueryRowContext(ctx, `SELECT created_at,account_id FROM group_quality_traffic_leases WHERE group_id=$1 AND fallback AND NOT unused ORDER BY created_at DESC LIMIT 1`, p.GroupIDs[0]).Scan(&at, &aid)
	if err == nil {
		s.LastUsedAt = &at
		s.LastAccountID = &aid
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return s, nil
}
