package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const prismTestCookie = "prism_oai_access_token=access-secret; prism_session_token=session-secret"

func newPrismTestAccount() *Account {
	return &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{PrismCookieCredentialKey: prismTestCookie},
		Extra:       map[string]any{PrismExtraKey: map[string]any{"enabled": true, "version": 1}}}
}

func TestPrismAccountConfigDefaultsAndValidation(t *testing.T) {
	config, err := (*Account)(nil).PrismConfig()
	require.NoError(t, err)
	require.False(t, config.Enabled)
	require.Equal(t, 180, config.TimeoutSeconds)
	require.Empty(t, config.ConversationActionID)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		account := newPrismTestAccount()
		account.Type = accountType
		require.True(t, account.IsPrismEnabled())
		require.NoError(t, ValidatePrismAccountConfiguration(account))
	}
	tests := []struct {
		name   string
		change func(*Account)
	}{
		{"flag string", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["enabled"] = "true" }},
		{"object", func(a *Account) { a.Extra[PrismExtraKey] = true }},
		{"unknown", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["cookie"] = prismTestCookie }},
		{"version", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["version"] = 2 }},
		{"short timeout", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["timeout_seconds"] = 29 }},
		{"long timeout", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["timeout_seconds"] = 601 }},
		{"fractional timeout", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["timeout_seconds"] = 30.5 }},
		{"action", func(a *Account) { a.Extra[PrismExtraKey].(map[string]any)["conversation_action_id"] = "bad" }},
		{"old action length", func(a *Account) {
			a.Extra[PrismExtraKey].(map[string]any)["conversation_action_id"] = strings.Repeat("a1", 20)
		}},
		{"platform", func(a *Account) { a.Platform = PlatformAnthropic }},
		{"apikey", func(a *Account) { a.Type = AccountTypeAPIKey }},
		{"shared", func(a *Account) { a.AccountScope = "shared" }},
		{"shadow", func(a *Account) { id := int64(99); a.ParentAccountID = &id }},
		{"missing cookie", func(a *Account) { delete(a.Credentials, PrismCookieCredentialKey) }},
		{"missing session", func(a *Account) { a.Credentials[PrismCookieCredentialKey] = "prism_oai_access_token=access-secret" }},
		{"empty access", func(a *Account) {
			a.Credentials[PrismCookieCredentialKey] = "prism_oai_access_token=; prism_session_token=session-secret"
		}},
		{"crlf", func(a *Account) { a.Credentials[PrismCookieCredentialKey] = prismTestCookie + "\r\nX-Test: secret" }},
		{"oversize", func(a *Account) {
			a.Credentials[PrismCookieCredentialKey] = prismTestCookie + strings.Repeat("a", PrismMaxCookieBytes)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			account := newPrismTestAccount()
			tc.change(account)
			err := ValidatePrismAccountConfiguration(account)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "access-secret")
			require.NotContains(t, err.Error(), "session-secret")
		})
	}
	for _, timeout := range []any{30, float64(600), json.Number("180")} {
		account := newPrismTestAccount()
		account.Extra[PrismExtraKey].(map[string]any)["timeout_seconds"] = timeout
		account.Extra[PrismExtraKey].(map[string]any)["conversation_action_id"] = strings.Repeat("A1", 21)
		require.NoError(t, ValidatePrismAccountConfiguration(account))
		config, err := account.PrismConfig()
		require.NoError(t, err)
		require.Equal(t, strings.Repeat("a1", 21), config.ConversationActionID)
	}
}

func TestPrismDisabledDoesNotRequireCookieOrEligibleAccount(t *testing.T) {
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey,
		Credentials: map[string]any{PrismCookieCredentialKey: "old\r\ninvalid"},
		Extra:       map[string]any{PrismExtraKey: map[string]any{"enabled": false, "version": 1}}}
	require.False(t, account.IsPrismEnabled())
	require.NoError(t, ValidatePrismAccountConfiguration(account))
}

func TestPrismSensitiveCredentialPreservedAndDerivedMarkerDiscarded(t *testing.T) {
	account := newPrismTestAccount()
	merged := MergePreservingSensitiveCreds(account.Credentials, map[string]any{PrismCookieConfiguredCredentialKey: true, "model_mapping": map[string]any{"alias": PrismDefaultModel}})
	require.Equal(t, prismTestCookie, merged[PrismCookieCredentialKey])
	require.NotContains(t, merged, PrismCookieConfiguredCredentialKey)
	require.True(t, IsSensitiveCredentialKey(PrismCookieCredentialKey))
	SanitizeStoredCredentials(PlatformOpenAI, merged)
	require.Equal(t, prismTestCookie, merged[PrismCookieCredentialKey])
}

func TestPrismAdminCreateUpdateAndExtraValidation(t *testing.T) {
	repo := &upstreamBillingProbeAdminRepo{upstreamBillingProbeAccountRepo: &upstreamBillingProbeAccountRepo{}}
	svc := &adminServiceImpl{accountRepo: repo}
	account := newPrismTestAccount()
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{Name: "prism", Platform: account.Platform, Type: account.Type, Credentials: account.Credentials, Extra: account.Extra, SkipDefaultGroupBind: true})
	require.NoError(t, err)
	_, err = svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Credentials: map[string]any{PrismCookieConfiguredCredentialKey: true}})
	require.NoError(t, err)
	require.Equal(t, prismTestCookie, repo.accounts[created.ID].Credentials[PrismCookieCredentialKey])
	require.NotContains(t, repo.accounts[created.ID].Credentials, PrismCookieConfiguredCredentialKey)
	_, err = svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Credentials: map[string]any{PrismCookieCredentialKey: ""}})
	require.Error(t, err)
	require.Equal(t, prismTestCookie, repo.accounts[created.ID].Credentials[PrismCookieCredentialKey])
	err = svc.UpdateAccountExtra(context.Background(), created.ID, map[string]any{PrismExtraKey: map[string]any{"enabled": "true"}})
	require.Error(t, err)
	_, err = svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Credentials: map[string]any{PrismCookieCredentialKey: ""}, Extra: map[string]any{PrismExtraKey: map[string]any{"enabled": false}}})
	require.NoError(t, err)
	require.False(t, repo.accounts[created.ID].IsPrismEnabled())
	_, err = svc.CreateAccount(context.Background(), &CreateAccountInput{Name: "invalid", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: account.Extra, SkipDefaultGroupBind: true})
	require.Error(t, err)
	require.Len(t, repo.accounts, 1)
}

func TestPrismBulkRejectsUnsupportedTargetsBeforeWrite(t *testing.T) {
	account := newPrismTestAccount()
	account.Extra = nil
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account, 2: {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1, 2}, Credentials: map[string]any{PrismCookieCredentialKey: prismTestCookie}, Extra: map[string]any{PrismExtraKey: map[string]any{"enabled": true}}})
	require.Error(t, err)
	require.Empty(t, repo.bulkUpdates)
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Credentials: map[string]any{PrismCookieCredentialKey: prismTestCookie, PrismCookieConfiguredCredentialKey: true}, Extra: map[string]any{PrismExtraKey: map[string]any{"enabled": true}}})
	require.NoError(t, err)
	require.Len(t, repo.bulkUpdates, 1)
	require.NotContains(t, repo.bulkUpdates[0].Credentials, PrismCookieConfiguredCredentialKey)
	require.Nil(t, account.Extra)
}

func TestPrismProtocolAndCapabilityBoundaries(t *testing.T) {
	account := newPrismTestAccount()
	account.Extra["openai_passthrough"] = true
	account.Extra["openai_ws_enabled"] = true
	account.Extra["openai_oauth_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
	account.Extra["openai_compact_mode"] = OpenAICompactModeForceOn
	require.False(t, account.IsOpenAIPassthroughEnabled())
	require.False(t, account.IsOpenAIResponsesWebSocketV2Enabled())
	require.Equal(t, OpenAIWSIngressModeOff, account.ResolveOpenAIResponsesWebSocketV2Mode(OpenAIWSIngressModeHTTPBridge))
	require.False(t, account.AllowsOpenAICompact())
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityChatCompletions, OpenAIEndpointCapabilityResponses} {
		require.True(t, account.SupportsOpenAIEndpointCapability(capability))
	}
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityLive, OpenAIEndpointCapabilityAlphaSearch, OpenAIEndpointCapabilityEmbeddings} {
		require.False(t, account.SupportsOpenAIEndpointCapability(capability))
	}
	require.False(t, account.SupportsOpenAIImageCapability(OpenAIImagesCapabilityBasic))
	require.False(t, account.SupportsOpenAIImageCapability(OpenAIImagesCapabilityNative))
	decision := NewOpenAIWSProtocolResolver(&config.Config{}).Resolve(account)
	require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
	for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportResponsesWebsocket, OpenAIUpstreamTransportResponsesWebsocketV2, OpenAIUpstreamTransportResponsesWebsocketV2Ingress} {
		require.False(t, (*OpenAIGatewayService)(nil).isOpenAIAccountTransportCompatible(account, transport))
	}
	require.True(t, (*OpenAIGatewayService)(nil).isOpenAIAccountTransportCompatible(account, OpenAIUpstreamTransportHTTPSSE))
	require.True(t, account.IsModelSupported(PrismDefaultModel))
	require.False(t, account.IsModelSupported("gpt-6-astra"))
	require.False(t, account.IsModelSupported("unknown-model"))
	account.Credentials["model_mapping"] = map[string]any{"custom": "confirmed-upstream"}
	require.True(t, account.IsModelSupported("custom"))
	require.False(t, account.IsModelSupported(PrismDefaultModel))
}

func TestPrismSharedUploadRejectsBeforeExternalSideEffects(t *testing.T) {
	_, err := (&SharedAccountUploadService{}).uploadUnlocked(context.Background(), 1, SharedAccountUploadInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: newPrismTestAccount().Extra})
	require.ErrorContains(t, err, "independent")
}

func TestPrismModelDiscoveryDoesNotAdvertiseCodexCatalog(t *testing.T) {
	account := newPrismTestAccount()
	models := PrismAccountModels(account)
	require.Len(t, models, 1)
	require.Equal(t, PrismDefaultModel, models[0].ID)
	repo := &modelsListAccountRepoStub{all: []Account{*account}}
	svc := &GatewayService{accountRepo: repo}
	require.Equal(t, []string{PrismDefaultModel}, svc.GetAvailableModels(context.Background(), nil, PlatformOpenAI))
	require.Equal(t, []string{PrismDefaultModel}, supplementUnmappedOpenAIModels([]Account{*account}, []string{PrismDefaultModel}))
	account.Credentials["model_mapping"] = map[string]any{"custom": "confirmed-upstream"}
	require.Equal(t, "custom", PrismAccountModels(account)[0].ID)
}

func TestPrismIgnoresCodexQuotaAndDoesNotProbeUsage(t *testing.T) {
	account := newPrismTestAccount()
	account.Status = StatusActive
	account.Schedulable = true
	now := time.Now()
	reset := now.Add(time.Hour)
	account.SessionWindowEnd = &reset
	for key, value := range map[string]any{
		"codex_5h_used_percent": 100.0, "codex_7d_used_percent": 100.0,
		"codex_5h_reset_at": reset.Format(time.RFC3339), "codex_7d_reset_at": reset.Format(time.RFC3339),
		"codex_usage_updated_at":  now.Format(time.RFC3339),
		"auto_pause_5h_threshold": 0.9, "auto_pause_7d_threshold": 0.9,
		OpenAIAutoResetCreditEnabledExtraKey: true, "codex_cli_only": true,
	} {
		account.Extra[key] = value
	}
	paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
	require.False(t, paused)
	require.False(t, EvaluateAccountSchedulingThreshold(account, map[string]int{PlatformOpenAI: 90}, now).ShouldPause)
	require.Equal(t, openAIQuotaHeadroomNeutralFactor, openAIQuotaHeadroomFactor(account, now))
	_, hasReset := openAISchedulingResetWindowEnd(account, now)
	require.False(t, hasReset)
	require.False(t, ResolveOpenAIAutoResetCreditConfig(account).Enabled)
	require.False(t, account.IsCodexCLIOnlyEnabled())
	account.Credentials["refresh_token"] = "stale-codex-refresh-token"
	account.Credentials["expires_at"] = now.Add(-time.Hour).Format(time.RFC3339)
	require.False(t, (&OpenAITokenRefresher{}).CanRefresh(account))
	require.False(t, (&OpenAITokenRefresher{}).NeedsRefresh(account, time.Hour))
	// No configured client/repository is needed: neither a probe nor status write
	// may happen, even for an explicit forced refresh.
	for _, force := range []bool{false, true} {
		usage, err := (&AccountUsageService{}).GetUsageForAccount(context.Background(), account, force)
		require.NoError(t, err)
		require.Equal(t, "not_supported", usage.ErrorCode)
		require.Nil(t, usage.FiveHour)
		require.Nil(t, usage.SevenDay)
	}
	require.Equal(t, StatusActive, account.Status)
	require.Equal(t, 100.0, account.Extra["codex_5h_used_percent"])
	require.True(t, account.IsSchedulable())
	account.Schedulable = false
	require.False(t, account.IsSchedulable())
	account.Schedulable = true
	account.RateLimitResetAt = &reset
	require.False(t, account.IsSchedulable())
	account.RateLimitResetAt = nil
	past := now.Add(-time.Hour)
	account.ExpiresAt, account.AutoPauseOnExpired = &past, true
	require.False(t, account.IsSchedulable())
}
