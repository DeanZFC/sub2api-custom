//go:build integration

package repository

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountProtectionPersistedOrdinarySaveKeepsLockedMarker(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	extra := map[string]any{
		"anti_degradation":        true,
		"anti_degrade":            map[string]any{"enabled": true, "mode": "legacy", "policy_version": 3, "max_concurrency": 3},
		"codex_fingerprint_mode":  "session",
		"codex_fingerprint_seed":  "22222222-2222-4222-8222-222222222222",
		"enable_tls_fingerprint":  true,
		"tls_fingerprint_builtin": "nodejs24",
	}
	row, err := tx.Client().Account.Create().SetName("protection-stale-extra").
		SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).
		SetCredentials(map[string]any{}).SetExtra(extra).SetConcurrency(3).Save(ctx)
	require.NoError(t, err)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	account, err := repo.GetByID(ctx, row.ID)
	require.NoError(t, err)
	account.Extra = map[string]any{"custom_setting": "new"}
	account.Concurrency = 2
	require.NoError(t, repo.Update(ctx, account))
	stored, err := tx.Client().Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, true, stored.Extra["anti_degradation"])
	require.Equal(t, "session", stored.Extra["codex_fingerprint_mode"])
	require.Equal(t, "new", stored.Extra["custom_setting"])
	marker := stored.Extra["anti_degrade"].(map[string]any)
	require.Equal(t, "legacy", marker["mode"])
	require.EqualValues(t, 2, marker["max_concurrency"])
	require.Equal(t, 2, stored.Concurrency)
}

func TestAccountProtectionBulkConcurrencyPersistsEffectiveCeiling(t *testing.T) {
	for _, tc := range []struct {
		name       string
		extra      map[string]any
		want       int
		wantMarker bool
	}{
		{"protected marker fallback", map[string]any{"anti_degrade": map[string]any{"enabled": true, "mode": "legacy", "policy_version": 3, "max_concurrency": 3}}, 16, true},
		{"explicit disable overrides stale marker", map[string]any{"anti_degradation": false, "anti_degrade": map[string]any{"enabled": true, "max_concurrency": 3}}, 0, true},
		{"explicit enable overrides stale marker", map[string]any{"anti_degradation": true, "anti_degrade": map[string]any{"enabled": false, "max_concurrency": 3}}, 16, true},
		{"malformed boolean uses marker fallback", map[string]any{"anti_degradation": "false", "anti_degrade": map[string]any{"enabled": true, "max_concurrency": 3}}, 16, true},
		{"legacy boolean", map[string]any{"anti_degradation": true}, 16, false},
		{"unprotected", map[string]any{}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testEntTx(t)
			ctx := dbent.NewTxContext(context.Background(), tx)
			row, err := tx.Client().Account.Create().SetName(tc.name).
				SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).
				SetCredentials(map[string]any{}).SetExtra(tc.extra).SetConcurrency(3).Save(ctx)
			require.NoError(t, err)
			repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
			limit := 0
			_, err = repo.BulkUpdate(ctx, []int64{row.ID}, service.AccountBulkUpdate{Concurrency: &limit})
			require.NoError(t, err)
			stored, err := tx.Client().Account.Get(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, stored.Concurrency)
			marker, exists := stored.Extra["anti_degrade"]
			require.Equal(t, tc.wantMarker, exists)
			if tc.wantMarker {
				wantMarker := 3
				if tc.want > 0 {
					wantMarker = tc.want
				}
				require.EqualValues(t, wantMarker, marker.(map[string]any)["max_concurrency"])
			}
		})
	}
}

func TestAccountProtectionBulkExtraUsesExplicitEnableState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		marker  map[string]any
		wantTLS string
	}{
		{"explicit false permits ordinary edit despite stale active marker", false, map[string]any{"enabled": true, "mode": "legacy", "policy_version": 3}, "nodejs22"},
		{"explicit true protects fields with missing marker mode", true, map[string]any{}, "nodejs24"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := testEntTx(t)
			ctx := dbent.NewTxContext(context.Background(), tx)
			row, err := tx.Client().Account.Create().SetName(tc.name).
				SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).
				SetCredentials(map[string]any{}).SetExtra(map[string]any{
				"anti_degradation": tc.enabled, "anti_degrade": tc.marker, "tls_fingerprint_builtin": "nodejs24",
			}).Save(ctx)
			require.NoError(t, err)
			repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
			_, err = repo.BulkUpdate(ctx, []int64{row.ID}, service.AccountBulkUpdate{Extra: map[string]any{"tls_fingerprint_builtin": "nodejs22"}})
			require.NoError(t, err)
			stored, err := tx.Client().Account.Get(ctx, row.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantTLS, stored.Extra["tls_fingerprint_builtin"])
			require.Equal(t, tc.enabled, stored.Extra["anti_degradation"])
		})
	}
}

func TestAccountProtectionMixedBulkPreservesOnlyProtectedIdentity(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	var ids []int64
	for _, mode := range []string{"legacy", "", "generic"} {
		extra := map[string]any{
			"codex_fingerprint_mode": "session", "enable_tls_fingerprint": true, "tls_fingerprint_builtin": "nodejs24",
		}
		if mode != "" {
			extra["anti_degradation"] = true
			extra["anti_degrade"] = map[string]any{"enabled": true, "mode": mode, "policy_version": 3, "max_concurrency": 3}
		}
		row, err := tx.Client().Account.Create().SetName("mixed-bulk-" + mode).
			SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).
			SetCredentials(map[string]any{}).SetExtra(extra).Save(ctx)
		require.NoError(t, err)
		ids = append(ids, row.ID)
	}
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	_, err := repo.BulkUpdate(ctx, ids, service.AccountBulkUpdate{Extra: map[string]any{
		"codex_fingerprint_mode": "device", "enable_tls_fingerprint": false, "tls_fingerprint_builtin": "nodejs22", "custom_setting": "new",
	}})
	require.NoError(t, err)
	for i, id := range ids {
		stored, err := tx.Client().Account.Get(ctx, id)
		require.NoError(t, err)
		require.Equal(t, "new", stored.Extra["custom_setting"])
		if i == 0 {
			require.Equal(t, "session", stored.Extra["codex_fingerprint_mode"])
			require.Equal(t, true, stored.Extra["enable_tls_fingerprint"])
			require.Equal(t, "nodejs24", stored.Extra["tls_fingerprint_builtin"])
		} else {
			require.Equal(t, "device", stored.Extra["codex_fingerprint_mode"])
			require.Equal(t, false, stored.Extra["enable_tls_fingerprint"])
			require.Equal(t, "nodejs22", stored.Extra["tls_fingerprint_builtin"])
		}
	}
}
