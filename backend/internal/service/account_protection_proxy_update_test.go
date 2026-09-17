package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type protectionProxyTestRepository struct{ ProxyRepository }

func (*protectionProxyTestRepository) GetByID(_ context.Context, id int64) (*Proxy, error) {
	return &Proxy{ID: id, Status: StatusActive}, nil
}

func protectedProxyAdmin(t *testing.T) (*adminServiceImpl, *upstreamBillingProbeAccountRepo) {
	t.Helper()
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{}}
	protection, repo := newAntiDegradeTestService(account)
	_, err := protection.ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeModeLegacy)
	require.NoError(t, err)
	admin := &adminServiceImpl{
		accountRepo: &upstreamBillingProbeAdminRepo{repo},
		proxyRepo:   &protectionProxyTestRepository{},
	}
	return admin, repo
}

func TestAntiDegradeOrdinaryUpdateRejectsRandomAndMultipleProxies(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input UpdateAccountInput
	}{
		{"random", UpdateAccountInput{Extra: map[string]any{"proxy_mode": " RANDOM "}}},
		{"multiple", UpdateAccountInput{ProxyIDs: &[]int64{10, 20}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin, repo := protectedProxyAdmin(t)
			_, err := admin.UpdateAccount(context.Background(), 1, &tc.input)
			require.ErrorIs(t, err, ErrProtectedProxyModeChange)
			require.Empty(t, repo.accounts[1].ProxyIDs)
			require.True(t, repo.accounts[1].AntiDegradationEnabled())
		})
	}
}

func TestAntiDegradeProxyUpdateRetainsSingleAndDisabledBehavior(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable bool
		ids     []int64
	}{
		{"fixed", false, []int64{10}},
		{"direct", false, []int64{}},
		{"duplicates remain one proxy", false, []int64{10, 10}},
		{"disabled allows multiple", true, []int64{10, 20}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin, repo := protectedProxyAdmin(t)
			if tc.disable {
				_, err := NewAntiDegradeService(admin).Revert(context.Background(), 1)
				require.NoError(t, err)
			}
			updated, err := admin.UpdateAccount(context.Background(), 1, &UpdateAccountInput{ProxyIDs: &tc.ids})
			require.NoError(t, err)
			if tc.disable {
				require.Equal(t, tc.ids, updated.ProxyIDs)
			} else {
				require.Empty(t, updated.ProxyIDs)
				require.True(t, updated.AntiDegradationEnabled())
			}
			if len(tc.ids) == 0 {
				require.Nil(t, repo.accounts[1].ProxyID)
			} else {
				require.Equal(t, tc.ids[0], *repo.accounts[1].ProxyID)
			}
		})
	}
}

func TestAntiDegradeBulkProxyConflictRejectsWholeBatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input BulkUpdateAccountsInput
	}{
		{"random", BulkUpdateAccountsInput{Extra: map[string]any{"proxy_mode": "random"}}},
		{"multiple", BulkUpdateAccountsInput{ProxyIDs: &[]int64{10, 20}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin, repo := protectedProxyAdmin(t)
			repo.accounts[2] = &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
			tc.input.AccountIDs = []int64{2, 1}
			_, err := admin.BulkUpdateAccounts(context.Background(), &tc.input)
			require.ErrorIs(t, err, ErrProtectedProxyModeChange)
			require.Empty(t, repo.bulkUpdates)
		})
	}
}

func TestAntiDegradeBulkKeepsAdaptivePolicyIndependentlyEditable(t *testing.T) {
	admin, repo := protectedProxyAdmin(t)
	policy := map[string]any{"enabled": true, "min_concurrency": 1}
	_, err := admin.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1},
		Extra:      map[string]any{AccountProtectionPolicyKey: policy},
	})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.Equal(t, policy, repo.bulkUpdates[0].Extra[AccountProtectionPolicyKey])
}

func TestAntiDegradeMixedBulkKeepsIdentityEditsForUnprotectedTargets(t *testing.T) {
	admin, repo := protectedProxyAdmin(t)
	repo.accounts[2] = &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	_, err := admin.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2},
		Extra: map[string]any{
			"codex_fingerprint_mode": "session", "enable_tls_fingerprint": true, "tls_fingerprint_builtin": "nodejs22",
		},
	})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.Equal(t, "session", repo.bulkUpdates[0].Extra["codex_fingerprint_mode"])
	require.Equal(t, true, repo.bulkUpdates[0].Extra["enable_tls_fingerprint"])
	require.Equal(t, "nodejs22", repo.bulkUpdates[0].Extra["tls_fingerprint_builtin"])
}
