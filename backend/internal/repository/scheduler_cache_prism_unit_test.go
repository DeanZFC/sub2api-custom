//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCachePrismConfigProjectionExcludesCookie(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	account := &service.Account{ID: 9901, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{service.PrismCookieCredentialKey: "prism_oai_access_token=private-access; prism_session_token=private-session"},
		Extra:       map[string]any{service.PrismExtraKey: map[string]any{"enabled": true, "version": 1, "timeout_seconds": 240}}}
	require.NoError(t, cache.SetAccount(ctx, account))
	full, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, account.Credentials[service.PrismCookieCredentialKey], full.Credentials[service.PrismCookieCredentialKey])
	metaBytes, err := cache.rdb.Get(ctx, schedulerAccountMetaKey("9901")).Bytes()
	require.NoError(t, err)
	require.NotContains(t, string(metaBytes), "private-access")
	require.NotContains(t, string(metaBytes), service.PrismCookieCredentialKey)
	var projection service.Account
	require.NoError(t, json.Unmarshal(metaBytes, &projection))
	require.True(t, projection.IsPrismEnabled())
	config, err := projection.PrismConfig()
	require.NoError(t, err)
	require.Equal(t, 240, config.TimeoutSeconds)
	require.Equal(t, service.PrismAuthModeCookie, config.AuthMode)
	require.False(t, projection.UsesPrismAccountAuth())
	account.Credentials["access_token"] = "private-account-access"
	account.Credentials["refresh_token"] = "private-account-refresh"
	account.Extra[service.PrismExtraKey] = map[string]any{
		"enabled": true, "version": 1, "auth_mode": service.PrismAuthModeAccount,
		"access_token": "private-misplaced-token", "cookie": "private-misplaced-cookie",
	}
	require.NoError(t, cache.SetAccount(ctx, account))
	metaBytes, err = cache.rdb.Get(ctx, schedulerAccountMetaKey("9901")).Bytes()
	require.NoError(t, err)
	require.NotContains(t, string(metaBytes), "private-")
	require.NoError(t, json.Unmarshal(metaBytes, &projection))
	require.True(t, projection.UsesPrismAccountAuth())
	config, err = projection.PrismConfig()
	require.NoError(t, err)
	require.Equal(t, service.PrismAuthModeAccount, config.AuthMode)
	account.Extra[service.PrismExtraKey] = map[string]any{"enabled": false, "version": 1}
	require.NoError(t, cache.SetAccount(ctx, account))
	metaBytes, err = cache.rdb.Get(ctx, schedulerAccountMetaKey("9901")).Bytes()
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(metaBytes, &projection))
	require.False(t, projection.IsPrismEnabled())
	require.False(t, projection.UsesPrismAccountAuth())
}
