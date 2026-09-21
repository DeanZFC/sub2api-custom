//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountRateLimitPersistsAcrossDisableAndStaleWrites(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name: "rate-limit-persistence", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Extra: map[string]any{service.UpstreamBillingProbeEnabledExtraKey: true},
	})
	stale, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.NoError(t, repo.UpdateExtra(ctx, account.ID, map[string]any{
		service.UpstreamBillingRateLimitExtraKey:    0.5,
		service.UpstreamBillingProbeEnabledExtraKey: false,
	}))
	got, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, true, got.Extra[service.UpstreamBillingProbeEnabledExtraKey])

	// A completed probe loaded before the limit was configured may update only
	// the result, never the current limit or a stale persisted blocked flag.
	require.NoError(t, repo.UpdateUpstreamBillingProbeSnapshot(ctx, stale, &service.UpstreamBillingProbeSnapshot{
		Status: service.UpstreamBillingProbeStatusOK,
		Data:   map[string]any{"billing_scope": "token", "resolved_rate_multiplier": 0.8, "peak_rate_enabled": false},
	}, nil))
	stale.Name = "changed-only-name"
	require.NoError(t, repo.Update(ctx, stale))
	got, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, 0.5, got.Extra[service.UpstreamBillingRateLimitExtraKey])
	require.True(t, got.IsUpstreamBillingRateLimited())
	require.True(t, got.Schedulable, "manual scheduling setting is independent")

	_, err = repo.BulkUpdate(ctx, []int64{account.ID}, service.AccountBulkUpdate{ProbeEnabled: probeBoolPtr(false)})
	require.NoError(t, err)
	require.NoError(t, repo.UpdateExtra(ctx, account.ID, map[string]any{service.UpstreamBillingProbeEnabledExtraKey: false}))
	got, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, true, got.Extra[service.UpstreamBillingProbeEnabledExtraKey])
	require.True(t, got.IsUpstreamBillingRateLimited(), "failed disable must retain last expensive rate")

	// The ops projection must report the same restriction as full accounts.
	ops, err := repo.ListOpsAccountsForStats(ctx, service.PlatformOpenAI, nil)
	require.NoError(t, err)
	found := false
	for _, item := range ops {
		if item.ID == account.ID {
			found = true
			require.True(t, item.IsUpstreamBillingRateLimited())
		}
	}
	require.True(t, found)

	got.UpstreamBillingRateLimitChanged = true
	got.Extra[service.UpstreamBillingRateLimitExtraKey] = nil
	require.NoError(t, repo.UpdateWithAccountBillingSettings(ctx, got, probeBoolPtr(false), nil, nil))
	got, err = repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.False(t, got.IsUpstreamBillingRateLimited())
	require.Equal(t, false, got.Extra[service.UpstreamBillingProbeEnabledExtraKey])
}

func TestCreateAccountRateLimitForcesProbeOn(t *testing.T) {
	tx := testEntTx(t)
	account := &service.Account{
		Name: "rate-limit-create", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Extra: map[string]any{service.UpstreamBillingRateLimitExtraKey: 0.0, service.UpstreamBillingProbeEnabledExtraKey: false},
	}
	require.NoError(t, createAccountRecord(context.Background(), tx.Client(), account))
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	got, err := repo.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, true, got.Extra[service.UpstreamBillingProbeEnabledExtraKey])
}

func TestListDueAccountRateLimitCandidatesValidateValuesAndKeepOrder(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	_, err := tx.ExecContext(ctx, `UPDATE accounts SET extra = extra - 'upstream_billing_rate_limit' - 'upstream_billing_probe_enabled'`)
	require.NoError(t, err)
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	insert := func(name string, value any, next time.Time, enabled bool) int64 {
		t.Helper()
		extra, err := json.Marshal(map[string]any{
			service.UpstreamBillingRateLimitExtraKey:    value,
			service.UpstreamBillingProbeEnabledExtraKey: enabled,
			service.UpstreamBillingProbeExtraKey:        map[string]any{"status": "ok", "next_probe_at": next.Format(time.RFC3339Nano)},
		})
		require.NoError(t, err)
		var id int64
		err = scanSingleRow(ctx, tx, `INSERT INTO accounts(name, platform, type, status, extra) VALUES($1, 'openai', 'apikey', 'active', $2::jsonb) RETURNING id`, []any{name, string(extra)}, &id)
		require.NoError(t, err)
		return id
	}
	later := insert("later-limited", 0.5, now.Add(-time.Minute), false)
	earlier := insert("earlier-limited", 0.0, now.Add(-time.Hour), false)
	_ = insert("future-limited", 0.5, now.Add(time.Hour), false)
	optional := insert("optional-enabled", nil, now.Add(-2*time.Hour), true)
	for _, value := range []any{nil, "0.5", false, -1, map[string]any{}, []any{}, json.Number("1e500")} {
		_ = insert("invalid-limited", value, now.Add(-3*time.Hour), false)
	}
	accounts, err := repo.ListDueUpstreamBillingRateLimitedProbeAccounts(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	require.Equal(t, earlier, accounts[0].ID)
	require.Equal(t, later, accounts[1].ID)
	accounts, err = repo.ListDueUpstreamBillingProbeAccounts(ctx, now, 2)
	require.NoError(t, err)
	require.Len(t, accounts, 2)
	require.Equal(t, optional, accounts[0].ID)
	require.Equal(t, earlier, accounts[1].ID)
}
