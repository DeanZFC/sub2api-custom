//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdminCodexTicketCreateInitializesExplicitPolicy(t *testing.T) {
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, nil)
	require.NoError(t, err)
	require.Equal(t, false, account.Extra[OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, true, account.Extra[OpenAICodexTicketFailClosedExtraKey])
	require.Equal(t, []int64{}, account.Extra[OpenAICodexTicketHarvestProxyIDsExtraKey])
}

func TestAdminCodexTicketRejectsMalformedPolicy(t *testing.T) {
	for _, extra := range []map[string]any{
		{OpenAICodexTicketEnabledExtraKey: "true"},
		{OpenAICodexTicketFailClosedExtraKey: nil},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: nil},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: "1,2"},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{0}},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{-1}},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{1.5}},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{"1"}},
		{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{float64(1 << 63)}},
	} {
		_, err := normalizeOpenAICodexTicketAccountExtra(PlatformOpenAI, extra, false)
		require.Error(t, err, "extra=%v", extra)
	}
}

func TestAdminCodexTicketProxyIDsKeepOrderAndExplicitEmpty(t *testing.T) {
	ids, err := normalizeOpenAICodexTicketHarvestProxyIDs([]any{float64(8), int64(2), json.Number("8"), 3})
	require.NoError(t, err)
	require.Equal(t, []int64{8, 2, 3}, ids)
	empty, err := normalizeOpenAICodexTicketHarvestProxyIDs([]any{})
	require.NoError(t, err)
	require.NotNil(t, empty)
	require.Empty(t, empty)
}

func TestAdminCodexTicketUpdatePreservesPolicyAndBusinessProxy(t *testing.T) {
	ctx := context.Background()
	proxyID := int64(17)
	ticket := &openAICodexTicket{Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, ExpiresAt: time.Now().Add(time.Hour)}
	account := ticketTestAccount(41)
	account.ProxyID = &proxyID
	account.ProxyIDs = []int64{17, 19}
	account.Extra = map[string]any{
		OpenAICodexTicketEnabledExtraKey:         true,
		OpenAICodexTicketFailClosedExtraKey:      false,
		OpenAICodexTicketHarvestProxyIDsExtraKey: []int64{99},
		openAICodexTicketExtraKey(ticket.Model):  ticket,
	}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{41: account}}
	svc := &adminServiceImpl{accountRepo: repo}
	// A removed/expired harvest proxy must not prevent unrelated account edits.
	updated, err := svc.UpdateAccount(ctx, 41, &UpdateAccountInput{Extra: map[string]any{"custom": true}})
	require.NoError(t, err)
	require.Equal(t, true, updated.Extra[OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, false, updated.Extra[OpenAICodexTicketFailClosedExtraKey])
	require.Equal(t, []int64{99}, updated.Extra[OpenAICodexTicketHarvestProxyIDsExtraKey])
	require.Equal(t, ticket, updated.Extra[openAICodexTicketExtraKey(ticket.Model)])
	require.Equal(t, &proxyID, updated.ProxyID)
	require.Equal(t, []int64{17, 19}, updated.ProxyIDs)

	updated, err = svc.UpdateAccount(ctx, 41, &UpdateAccountInput{Extra: map[string]any{
		OpenAICodexTicketEnabledExtraKey:         false,
		OpenAICodexTicketHarvestProxyIDsExtraKey: []any{},
	}})
	require.NoError(t, err)
	require.Equal(t, false, updated.Extra[OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, false, updated.Extra[OpenAICodexTicketFailClosedExtraKey])
	require.Equal(t, []int64{}, updated.Extra[OpenAICodexTicketHarvestProxyIDsExtraKey])
}

func TestAdminCodexTicketCreationValidatesDedicatedProxyWithoutBusinessRules(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	proxies := &contentModerationTestProxyRepo{proxies: map[int64]*Proxy{8: {ID: 8, Status: "inactive", ExpiresAt: &expired}, 2: {ID: 2}}}
	repo := &upstreamBillingProbeAccountRepo{}
	svc := &adminServiceImpl{accountRepo: repo, proxyRepo: proxies}
	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Platform: PlatformOpenAI, Type: AccountTypeOAuth, SkipDefaultGroupBind: true,
		Extra: map[string]any{OpenAICodexTicketEnabledExtraKey: true, OpenAICodexTicketHarvestProxyIDsExtraKey: []any{8, 2, 8}},
	})
	require.NoError(t, err)
	require.Equal(t, []int64{8, 2}, account.Extra[OpenAICodexTicketHarvestProxyIDsExtraKey])
	require.Nil(t, account.ProxyID)
	require.Empty(t, account.ProxyIDs)
	_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{
		Platform: PlatformOpenAI, Type: AccountTypeOAuth, SkipDefaultGroupBind: true,
		Extra: map[string]any{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{404}},
	})
	require.Error(t, err)
}

func TestAdminCodexTicketBulkValidatesBeforeWritingAndKeepsFalse(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}}
	svc := &adminServiceImpl{accountRepo: repo, proxyRepo: &contentModerationTestProxyRepo{proxies: map[int64]*Proxy{8: {ID: 8}}}}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1}, Extra: map[string]any{OpenAICodexTicketHarvestProxyIDsExtraKey: []any{404}},
	})
	require.Error(t, err)
	require.Zero(t, repo.bulkUpdateCalls)
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1}, Extra: map[string]any{OpenAICodexTicketEnabledExtraKey: false, OpenAICodexTicketFailClosedExtraKey: false, OpenAICodexTicketHarvestProxyIDsExtraKey: []any{8, 8}},
	})
	require.NoError(t, err)
	require.Equal(t, false, repo.lastBulkUpdate.Extra[OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, false, repo.lastBulkUpdate.Extra[OpenAICodexTicketFailClosedExtraKey])
	require.Equal(t, []int64{8}, repo.lastBulkUpdate.Extra[OpenAICodexTicketHarvestProxyIDsExtraKey])
	require.Nil(t, repo.lastBulkUpdate.ProxyID)
	require.Nil(t, repo.lastBulkUpdate.ProxyIDs)
}
