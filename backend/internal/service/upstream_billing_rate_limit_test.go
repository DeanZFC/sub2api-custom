package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rateLimitedTestAccount(id int64, limit, rate float64) *Account {
	return &Account{
		ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://upstream.example"},
		Extra: map[string]any{
			UpstreamBillingRateLimitExtraKey:    limit,
			UpstreamBillingProbeEnabledExtraKey: true,
			UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{
				Status: UpstreamBillingProbeStatusOK,
				Data: map[string]any{
					"billing_scope": "token", "resolved_rate_multiplier": rate, "peak_rate_enabled": false,
				},
			},
		},
	}
}

func TestUpstreamBillingRateLimitSchedulingAndRecovery(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 1.2)
	require.True(t, account.IsUpstreamBillingRateLimited())
	require.False(t, account.IsSchedulable())
	require.True(t, account.Schedulable, "automatic protection must not overwrite the manual switch")
	snapshot := account.Extra[UpstreamBillingProbeExtraKey].(*UpstreamBillingProbeSnapshot)
	snapshot.Status = UpstreamBillingProbeStatusFailed
	expired := time.Now().Add(-time.Hour)
	snapshot.FreshUntil = &expired
	require.True(t, account.IsUpstreamBillingRateLimited(), "a failed or stale probe retains protection")
	snapshot.Status = UpstreamBillingProbeStatusOK
	snapshot.Data["resolved_rate_multiplier"] = 1.0
	require.False(t, account.IsUpstreamBillingRateLimited())
	require.True(t, account.IsSchedulable(), "equality permits scheduling")
	account.Schedulable = false
	require.False(t, account.IsSchedulable(), "recovery must retain an administrator's pause")
	account.Schedulable = true
	account.Status = StatusDisabled
	require.False(t, account.IsSchedulable(), "recovery must retain an administrator's disabled status")
	account.Status = StatusActive
	delete(account.Extra, UpstreamBillingProbeExtraKey)
	require.True(t, account.IsSchedulable(), "before the first successful detection no rate is known")
	account.Extra[UpstreamBillingProbeExtraKey] = snapshot
	account.Extra[UpstreamBillingRateLimitExtraKey] = 0.0
	snapshot.Data["resolved_rate_multiplier"] = 0.0000000001
	require.True(t, account.IsUpstreamBillingRateLimited(), "zero permits only a genuinely free rate")
	snapshot.Data["resolved_rate_multiplier"] = 0.0
	require.False(t, account.IsUpstreamBillingRateLimited())
	account.Extra[UpstreamBillingRateLimitExtraKey] = nil
	snapshot.Data["resolved_rate_multiplier"] = 100.0
	require.False(t, account.IsUpstreamBillingRateLimited(), "null disables the limit")
}

func TestUpstreamBillingRateLimitRecomputesPeakWindow(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 0.75)
	snapshot := account.Extra[UpstreamBillingProbeExtraKey].(*UpstreamBillingProbeSnapshot)
	snapshot.Data["peak_rate_enabled"] = true
	snapshot.Data["peak_start"] = "09:00"
	snapshot.Data["peak_end"] = "18:00"
	snapshot.Data["peak_rate_multiplier"] = 2.0
	snapshot.Data["timezone"] = "Asia/Shanghai"
	require.False(t, account.IsUpstreamBillingRateLimitedAt(time.Date(2026, 9, 21, 0, 59, 0, 0, time.UTC)))
	require.True(t, account.IsUpstreamBillingRateLimitedAt(time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)))
	require.False(t, account.IsUpstreamBillingRateLimitedAt(time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)))
	account.Extra[UpstreamBillingRateLimitExtraKey] = 0.3
	snapshot.Data["resolved_rate_multiplier"] = 0.1
	snapshot.Data["peak_rate_multiplier"] = 3.0
	peakTime := time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)
	require.False(t, account.IsUpstreamBillingRateLimitedAt(peakTime), "decimal equality must tolerate multiplication rounding")
	snapshot.Data["resolved_rate_multiplier"] = 0.100000000001
	require.True(t, account.IsUpstreamBillingRateLimitedAt(peakTime), "real differences must remain blocked")
}

func TestCreateAccountUpstreamBillingRateLimitValidation(t *testing.T) {
	for _, raw := range []any{-1.0, math.NaN(), math.Inf(1), "1", true, map[string]any{}} {
		_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, map[string]any{UpstreamBillingRateLimitExtraKey: raw})
		require.Error(t, err, "invalid value: %v", raw)
	}
	for _, raw := range []any{0.0, 1.25, 2} {
		disabled := false
		created, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ProbeEnabled: &disabled}, map[string]any{UpstreamBillingRateLimitExtraKey: raw})
		require.NoError(t, err)
		require.Equal(t, true, created.Extra[UpstreamBillingProbeEnabledExtraKey])
	}
	_, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, map[string]any{UpstreamBillingRateLimitExtraKey: 1.0})
	require.ErrorIs(t, err, ErrUpstreamBillingProbeAccountInvalid)
}

func TestUpdateAccountUpstreamBillingRateLimitPreservesForUnrelatedEdit(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 1.2)
	repo := &upstreamBillingProbeAdminRepo{&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}}
	svc := &adminServiceImpl{accountRepo: repo}
	disabled := false
	for _, input := range []*UpdateAccountInput{
		{Name: "renamed"},
		{Extra: map[string]any{"custom_note": "test"}},
		{ProbeEnabled: &disabled},
	} {
		updated, err := svc.UpdateAccount(context.Background(), 1, input)
		require.NoError(t, err)
		limit, configured := updated.UpstreamBillingRateLimit()
		require.True(t, configured)
		require.Equal(t, 1.0, limit)
		require.False(t, updated.UpstreamBillingRateLimitChanged)
		require.Equal(t, true, updated.Extra[UpstreamBillingProbeEnabledExtraKey])
		require.True(t, updated.IsUpstreamBillingRateLimited())
	}
	updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{UpstreamBillingRateLimitExtraKey: nil}, ProbeEnabled: &disabled})
	require.NoError(t, err)
	require.True(t, updated.UpstreamBillingRateLimitChanged)
	require.False(t, updated.IsUpstreamBillingRateLimited())
	require.Equal(t, false, updated.Extra[UpstreamBillingProbeEnabledExtraKey])
}

func TestBulkAccountUpstreamBillingRateLimitEnforcesDetection(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 1.2)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}
	svc := &adminServiceImpl{accountRepo: repo}
	disabled := false
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, ProbeEnabled: &disabled})
	require.ErrorIs(t, err, ErrUpstreamBillingRateLimitRequiresProbe)
	require.Empty(t, repo.bulkUpdates)
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1}, ProbeEnabled: &disabled,
		Extra: map[string]any{UpstreamBillingRateLimitExtraKey: 0.5},
	})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.Equal(t, true, repo.bulkUpdates[0].Extra[UpstreamBillingProbeEnabledExtraKey])
	require.Equal(t, 0.5, repo.bulkUpdates[0].Extra[UpstreamBillingRateLimitExtraKey])
}

func TestUpstreamBillingRateLimitCannotDisableDedicatedProbeSwitch(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 1.2)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}
	svc := newUpstreamBillingProbeTestService(repo, &upstreamBillingProbeHTTPStub{}, &upstreamBillingProbeSettingRepo{})
	require.ErrorIs(t, svc.SetAccountEnabled(context.Background(), 1, false), ErrUpstreamBillingRateLimitRequiresProbe)
	require.Equal(t, true, account.Extra[UpstreamBillingProbeEnabledExtraKey])
}

func TestUpstreamBillingRateLimitRunnerWorksWithGlobalDetectionDisabled(t *testing.T) {
	limited := rateLimitedTestAccount(1, 0.1, 1.2)
	ordinary := rateLimitedTestAccount(2, 1, 1)
	delete(ordinary.Extra, UpstreamBillingRateLimitExtraKey)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: limited, 2: ordinary}}
	upstream := &upstreamBillingProbeHTTPStub{}
	svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{values: map[string]string{
		SettingKeyUpstreamBillingProbeSettings: `{"enabled":false,"interval_minutes":30}`,
	}})
	svc.now = func() time.Time { return time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC) }
	require.NoError(t, svc.RunDue(context.Background()))
	require.Equal(t, int64(1), upstream.calls.Load())
	require.False(t, decodeUpstreamBillingProbeSnapshot(limited.Extra).LastAttemptAt.IsZero())
	require.True(t, decodeUpstreamBillingProbeSnapshot(ordinary.Extra).LastAttemptAt.IsZero())
	require.True(t, limited.Schedulable)
}

func TestUpstreamBillingRateLimitedSnapshotProjection(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 2)
	items := BuildUpstreamBillingRateSnapshotItems([]Account{*account})
	require.True(t, items[0].UpstreamBillingRateLimited)
}

type rateLimitOnlyDueRepo struct {
	*upstreamBillingProbeAccountRepo
	due          []Account
	limitedCalls int
}

func (r *rateLimitOnlyDueRepo) ListDueUpstreamBillingRateLimitedProbeAccounts(context.Context, time.Time, int) ([]Account, error) {
	r.limitedCalls++
	return r.due, nil
}

func TestUpstreamBillingRateLimitRunnerRechecksLimitAfterDueSelection(t *testing.T) {
	for _, removed := range []bool{false, true} {
		account := rateLimitedTestAccount(1, 1, 2)
		// The derived setting still requires detection when a legacy row or stale
		// writer incorrectly stored a disabled probe flag.
		account.Extra[UpstreamBillingProbeEnabledExtraKey] = false
		staleDue := *account
		staleDue.Extra = mergeMap(nil, account.Extra)
		if removed {
			account.Extra[UpstreamBillingRateLimitExtraKey] = nil
			account.Extra[UpstreamBillingProbeEnabledExtraKey] = true
		}
		repo := &rateLimitOnlyDueRepo{
			upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}},
			due:                             []Account{staleDue},
		}
		upstream := &upstreamBillingProbeHTTPStub{}
		svc := newUpstreamBillingProbeTestService(repo, upstream, &upstreamBillingProbeSettingRepo{values: map[string]string{
			SettingKeyUpstreamBillingProbeSettings: `{"enabled":false,"interval_minutes":30}`,
		}})
		svc.now = func() time.Time { return time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC) }
		require.NoError(t, svc.RunDue(context.Background()))
		require.Equal(t, 1, repo.limitedCalls)
		if removed {
			require.Zero(t, upstream.calls.Load())
		} else {
			require.Equal(t, int64(1), upstream.calls.Load())
		}
	}
}

func TestUpdateAccountUpstreamBillingRateLimitIdentityTransition(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 2)
	repo := &upstreamBillingProbeAdminRepo{&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{
		Type: AccountTypeOAuth, Extra: map[string]any{UpstreamBillingRateLimitExtraKey: 1.0},
	})
	require.ErrorIs(t, err, ErrUpstreamBillingProbeAccountInvalid)
	updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Type: AccountTypeOAuth})
	require.NoError(t, err)
	require.True(t, updated.UpstreamBillingRateLimitChanged)
	require.NotContains(t, updated.Extra, UpstreamBillingRateLimitExtraKey)
	require.NotContains(t, updated.Extra, UpstreamBillingProbeExtraKey)
}

func TestUpdateAccountExtraUpstreamBillingRateLimitValidation(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 2)
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}
	svc := &adminServiceImpl{accountRepo: repo}
	require.Error(t, svc.UpdateAccountExtra(context.Background(), 1, map[string]any{UpstreamBillingRateLimitExtraKey: -1.0}))
	require.Empty(t, repo.updates)
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), 1, map[string]any{UpstreamBillingRateLimitExtraKey: 0.5}))
	require.Equal(t, true, repo.updates[1][0][UpstreamBillingProbeEnabledExtraKey])
}

func TestCRSSyncPreservesLocalUpstreamBillingRateLimit(t *testing.T) {
	account := rateLimitedTestAccount(1, 1, 2)
	account.Extra[UpstreamBillingProbeEnabledExtraKey] = false
	extra := map[string]any{UpstreamBillingRateLimitExtraKey: 99.0}
	reconcileCRSUpstreamBillingProbeExtra(account, account.Platform, account.Type, account.Credentials, extra)
	require.Equal(t, 1.0, extra[UpstreamBillingRateLimitExtraKey])
	require.Equal(t, true, extra[UpstreamBillingProbeEnabledExtraKey])
	reconcileCRSUpstreamBillingProbeExtra(account, account.Platform, AccountTypeOAuth, account.Credentials, extra)
	require.NotContains(t, extra, UpstreamBillingRateLimitExtraKey)
}
