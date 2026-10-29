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
	ID                      int64                  `json:"id"`
	Name                    string                 `json:"name"`
	Platform                string                 `json:"platform"`
	Type                    string                 `json:"type"`
	Credentials             map[string]any         `json:"credentials"`
	Extra                   map[string]any         `json:"extra"`
	ProxyID                 *int64                 `json:"proxy_id"`
	ProxyIDs                []int64                `json:"proxy_ids"`
	Proxies                 []groupQualityProxyRow `json:"proxies"`
	Concurrency             int                    `json:"concurrency"`
	LoadFactor              *int                   `json:"load_factor"`
	Priority                int                    `json:"priority"`
	Status                  string                 `json:"status"`
	Schedulable             bool                   `json:"schedulable"`
	RateMultiplier          *float64               `json:"rate_multiplier"`
	DeletedAt               *time.Time             `json:"deleted_at"`
	ExpiresAt               *time.Time             `json:"expires_at"`
	AutoPauseOnExpired      bool                   `json:"auto_pause_on_expired"`
	RateLimitedAt           *time.Time             `json:"rate_limited_at"`
	RateLimitResetAt        *time.Time             `json:"rate_limit_reset_at"`
	OverloadUntil           *time.Time             `json:"overload_until"`
	TempUnschedulableUntil  *time.Time             `json:"temp_unschedulable_until"`
	TempUnschedulableReason string                 `json:"temp_unschedulable_reason"`
	SessionWindowStart      *time.Time             `json:"session_window_start"`
	SessionWindowEnd        *time.Time             `json:"session_window_end"`
	SessionWindowStatus     string                 `json:"session_window_status"`
	ParentAccountID         *int64                 `json:"parent_account_id"`
	QuotaDimension          string                 `json:"quota_dimension"`
}

type groupQualityProxyRow struct {
	ID        int64      `json:"id"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func groupQualityAccountEligible(ctx context.Context, raw []byte, model string, fallback, activeProof, modelProof bool) (bool, error) {
	account, err := groupQualityLiveAccount(ctx, raw, model, fallback, activeProof, modelProof)
	return account != nil, err
}

// Return the same live account used for the durable eligibility decision, so
// request-specific supply checks cannot fall back to a scheduler snapshot.
func groupQualityLiveAccount(ctx context.Context, raw []byte, model string, fallback, activeProof, modelProof bool) (*service.Account, error) {
	var row groupQualityAccountRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return nil, err
	}
	if row.DeletedAt != nil {
		return nil, nil
	}
	if row.Extra == nil {
		row.Extra = map[string]any{}
	}
	if (&service.Account{Extra: row.Extra}).ActiveTestModelBlocked(model) && (!fallback || !activeProof) {
		return nil, nil
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
			return nil, nil
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
	a := &service.Account{
		ID: row.ID, Name: row.Name, Platform: row.Platform, Type: row.Type,
		Credentials: row.Credentials, Extra: row.Extra, ProxyID: row.ProxyID, ProxyIDs: row.ProxyIDs,
		Concurrency: row.Concurrency, LoadFactor: row.LoadFactor, Priority: row.Priority,
		Status: row.Status, Schedulable: row.Schedulable, RateMultiplier: row.RateMultiplier,
		ExpiresAt: row.ExpiresAt, AutoPauseOnExpired: row.AutoPauseOnExpired,
		RateLimitedAt: row.RateLimitedAt, RateLimitResetAt: row.RateLimitResetAt, OverloadUntil: row.OverloadUntil,
		TempUnschedulableUntil: row.TempUnschedulableUntil, TempUnschedulableReason: row.TempUnschedulableReason,
		SessionWindowStart: row.SessionWindowStart, SessionWindowEnd: row.SessionWindowEnd, SessionWindowStatus: row.SessionWindowStatus,
		ParentAccountID: row.ParentAccountID, QuotaDimension: row.QuotaDimension,
	}
	for _, proxy := range row.Proxies {
		a.Proxies = append(a.Proxies, &service.Proxy{ID: proxy.ID, Status: proxy.Status, ExpiresAt: proxy.ExpiresAt})
	}
	// The live row has already been filtered; do not let a routing marker loosen
	// any additional common durable scheduling condition during this check.
	admissionModel := service.GroupQualityFallbackAdmissionModel(ctx, model)
	ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{})
	if veto, _ := service.OpenAIProfitControlVeto(ctx, a); veto {
		return nil, nil
	}
	if !service.PassiveQualityPoolAccountEligible(ctx, a, admissionModel) {
		return nil, nil
	}
	return a, nil
}

func groupQualityFallbackCandidate(ctx context.Context, tx *sql.Tx, aid int64, model string) (bool, error) {
	if active, err := groupQualityAccountActiveRecovery(ctx, tx, aid, model); err != nil || active {
		return false, err
	}
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

// Every unrestricted account is normal supply, including accounts with no
// samples, pending confirmation or expired evidence. Trial windows are bounded
// recovery capacity, so they cannot prevent emergency fallback when exhausted.
// The database still proves current membership and protection state; the
// request checker then verifies those live rows can serve this exact request.
func groupQualityFallbackHasNormalSupply(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, model string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT to_jsonb(a) || jsonb_build_object('proxy_ids',
 COALESCE((SELECT jsonb_agg(ap.proxy_id ORDER BY ap.position) FROM account_proxies ap WHERE ap.account_id=a.id),'[]'::jsonb),'proxies',
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',pr.id,'status',pr.status,'expires_at',pr.expires_at) ORDER BY ap.position)
 FROM account_proxies ap JOIN proxies pr ON pr.id=ap.proxy_id AND pr.deleted_at IS NULL WHERE ap.account_id=a.id),'[]'::jsonb)) FROM accounts a
 WHERE a.deleted_at IS NULL AND a.status='active' AND a.schedulable
 AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$1)
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_protection_states h LEFT JOIN scheduled_test_definitions d ON d.id=h.test_definition_id
 WHERE h.account_id=a.id AND h.blocked AND (h.model_key='' OR h.model_key=$2 OR d.output_kind='statistics'))
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_combination_states h WHERE h.account_id=a.id AND h.blocked AND (h.model_key='' OR h.model_key=$2 OR h.account_wide))
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states h WHERE h.account_id=a.id AND h.blocked AND (h.model_key='' OR h.model_key=$2))
 AND NOT EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE s.account_id=a.id AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND s.data->>'phase' IN ('paused','probing','trial') AND `+passiveMembership+`
 AND (p.config->>'scope'='account' OR COALESCE(NULLIF(s.model_key,''),p.config->>'model')=$2))`, p.GroupIDs[0], model)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	accounts := make([]*service.Account, 0)
	for rows.Next() {
		var accountRaw []byte
		if err = rows.Scan(&accountRaw); err != nil {
			return false, err
		}
		account, e := groupQualityLiveAccount(ctx, accountRaw, model, false, false, false)
		if e != nil {
			return false, e
		}
		if account != nil {
			account.GroupIDs = []int64{p.GroupIDs[0]}
			accounts = append(accounts, account)
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	// Release the cursor before the batch callback performs its fresh runtime
	// capacity read. Account locks remain owned by this admission transaction.
	if err = rows.Close(); err != nil {
		return false, err
	}
	return service.GroupQualityFallbackHasRequestSupply(ctx, accounts)
}

// Renewal never revives an expired or released emergency request. The caller
// cancels forwarding when ownership is lost and always releases on completion.
func (r *passiveQualityRepository) RefreshGroupQualityFallback(ctx context.Context, lease string) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// A policy may switch to active recovery while this request is streaming.
	// Recheck membership across policies before renewing the existing lease.
	var accountID int64
	var model string
	err = tx.QueryRowContext(ctx, `SELECT account_id,model FROM group_quality_traffic_leases WHERE lease_id=$1 AND fallback AND released_at IS NULL AND NOT unused AND fallback_expires_at>clock_timestamp() FOR UPDATE`, lease).Scan(&accountID, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if active, err := groupQualityAccountActiveRecovery(ctx, tx, accountID, model); err != nil || active {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE group_quality_traffic_leases SET fallback_expires_at=clock_timestamp()+($2*interval '1 second')
 WHERE lease_id=$1 AND fallback AND released_at IS NULL AND NOT unused AND fallback_expires_at>clock_timestamp()
 AND EXISTS(SELECT 1 FROM passive_quality_policies p WHERE p.id=group_quality_traffic_leases.policy_id AND p.version=group_quality_traffic_leases.policy_version
 AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
 AND COALESCE(p.config->>'recovery_mode','')<>'active')`, lease, int(groupQualityFallbackLease/time.Second))
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
	s.Enabled = supported && p.Enabled && p.Mode == "enforce" && !p.IsActiveRecovery()
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
