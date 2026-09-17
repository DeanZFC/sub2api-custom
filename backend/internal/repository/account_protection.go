package repository

import (
	"context"
	"encoding/json"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const accountProtectionEnabledSQL = "(CASE WHEN jsonb_typeof(extra -> 'anti_degradation') = 'boolean' THEN extra -> 'anti_degradation' = 'true'::jsonb ELSE COALESCE(extra #> '{anti_degrade,enabled}' = 'true'::jsonb, false) END)"

// preserveProtectionExtraSQL keeps the dedicated protection fields from being
// replaced by stale full-object/key-level writes. Dedicated transitions opt
// out through a private context value and are then checked by the revision CAS.
func preserveProtectionExtraSQL(ctx context.Context, expression string) string {
	if service.ProtectionManagedWrite(ctx) {
		return expression
	}
	base := "ARRAY['anti_degradation','protection_scope','anti_degrade']::text[]"
	managed := "ARRAY['anti_degradation','protection_scope','anti_degrade','codex_fingerprint_mode','enable_tls_fingerprint','tls_fingerprint_builtin','tls_fingerprint_profile_id','proxy_mode']::text[]"
	keys := "(CASE WHEN " + accountProtectionEnabledSQL + " AND COALESCE(extra #>> '{anti_degrade,mode}', '') <> 'generic' THEN " + managed + " ELSE " + base + " END)"
	return "((" + expression + ") - " + keys + ") || COALESCE((SELECT jsonb_object_agg(key,value) FROM jsonb_each(COALESCE(extra,'{}'::jsonb)) WHERE key = ANY(" + keys + ")), '{}'::jsonb)"
}

func protectedConcurrencySQL(expression string) string {
	return "CASE WHEN " + accountProtectionEnabledSQL + " AND " + expression + " <= 0 THEN 16 ELSE " + expression + " END"
}

// preserveLockedAccountProtection runs after lockAndMergeAccountProbeExtra,
// which has already acquired FOR NO KEY UPDATE on the account row.
func preserveLockedAccountProtection(ctx context.Context, client *dbent.Client, account *service.Account) error {
	expected, compareVersion := service.GetProtectionWriteExpectation(ctx)
	query := "SELECT extra FROM accounts WHERE id = $1 AND deleted_at IS NULL"
	if compareVersion {
		query = "SELECT extra, updated_at FROM accounts WHERE id = $1 AND deleted_at IS NULL"
	}
	rows, err := client.QueryContext(ctx, query, account.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}
	var raw []byte
	var revision time.Time
	if compareVersion {
		if err := rows.Scan(&raw, &revision); err != nil {
			return err
		}
		if expected.AccountID != account.ID || !expected.UpdatedAt.Equal(revision) {
			return service.ErrProtectionConflict
		}
	} else if err := rows.Scan(&raw); err != nil {
		return err
	}
	var currentExtra map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &currentExtra); err != nil {
			return err
		}
	}
	current := &service.Account{ID: account.ID, Platform: account.Platform, Type: account.Type, Extra: currentExtra}
	if !service.ProtectionManagedWrite(ctx) && service.ProtectedProxyModeConflict(current, account.Extra) {
		return service.ErrProtectedProxyModeChange
	}
	if !service.ProtectionManagedWrite(ctx) && service.ProtectedProxyPoolConflict(current, account.ProxyIDs) {
		return service.ErrProtectedProxyModeChange
	}
	account.Extra = service.PreserveAccountProtection(ctx, current, account.Extra)
	service.BoundAccountProtectionConcurrency(account)
	return service.ValidateAccountProtectionConfiguration(account)
}

func validateLockedBulkProxyMode(ctx context.Context, exec sqlExecutor, ids []int64, incoming map[string]any, proxyIDs *[]int64) error {
	if service.ProtectionManagedWrite(ctx) {
		return nil
	}
	rows, err := exec.QueryContext(ctx, `SELECT platform,type,extra FROM accounts WHERE id=ANY($1) AND deleted_at IS NULL ORDER BY id FOR NO KEY UPDATE`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a service.Account
		var raw []byte
		if err := rows.Scan(&a.Platform, &a.Type, &raw); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &a.Extra); err != nil {
			return err
		}
		if service.ProtectedProxyModeConflict(&a, incoming) ||
			(proxyIDs != nil && service.ProtectedProxyPoolConflict(&a, *proxyIDs)) {
			return service.ErrProtectedProxyModeChange
		}
	}
	return rows.Err()
}
