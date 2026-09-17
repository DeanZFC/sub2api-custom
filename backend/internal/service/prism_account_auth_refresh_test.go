//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type prismOAuthRefreshClientStub struct {
	mu           sync.Mutex
	refreshes    int32
	refreshToken string
	proxyURL     string
	clientID     string
}

func (s *prismOAuthRefreshClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("exchange is not used by Prism refresh")
}

func (s *prismOAuthRefreshClientStub) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("legacy refresh is not used by Prism refresh")
}

func (s *prismOAuthRefreshClientStub) RefreshTokenWithClientID(_ context.Context, refreshToken, proxyURL, clientID string) (*openai.TokenResponse, error) {
	atomic.AddInt32(&s.refreshes, 1)
	s.mu.Lock()
	s.refreshToken, s.proxyURL, s.clientID = refreshToken, proxyURL, clientID
	s.mu.Unlock()
	return &openai.TokenResponse{
		AccessToken:  "prism-rotated-access",
		RefreshToken: "prism-rotated-refresh",
		ExpiresIn:    3600,
	}, nil
}

func prismAuthRefreshAccount() *Account {
	return &Account{
		ID:       8101,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "prism-old-access",
			"refresh_token": "prism-old-refresh",
			"expires_at":    time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
			"client_id":     "prism-original-client",
			"id_token":      "prism-original-id-token",
		},
		Extra: map[string]any{PrismExtraKey: map[string]any{
			"enabled": true, "version": 1, "auth_mode": PrismAuthModeAccount,
		}},
	}
}

func TestOpenAITokenRefresher_PrismAccountModeRefreshPolicy(t *testing.T) {
	refresher := NewOpenAITokenRefresher(nil, nil)

	manual := prismAuthRefreshAccount()
	manual.Extra[PrismExtraKey].(map[string]any)["auth_mode"] = PrismAuthModeCookie
	require.False(t, manual.UsesPrismAccountAuth())
	require.False(t, refresher.CanRefresh(manual))
	require.False(t, refresher.NeedsRefresh(manual, 5*time.Minute))
	delete(manual.Extra[PrismExtraKey].(map[string]any), "auth_mode")
	require.False(t, refresher.CanRefresh(manual), "legacy Cookie mode remains isolated")
	require.False(t, refresher.NeedsRefresh(manual, 5*time.Minute))

	auto := prismAuthRefreshAccount()
	require.True(t, auto.UsesPrismAccountAuth())
	require.True(t, refresher.CanRefresh(auto))
	require.True(t, refresher.NeedsRefresh(auto, 5*time.Minute), "expired Prism account tokens should refresh")

	noExpiry := prismAuthRefreshAccount()
	delete(noExpiry.Credentials, "expires_at")
	require.True(t, refresher.NeedsRefresh(noExpiry, 5*time.Minute), "Prism account mode refreshes when expiry is absent")

	missingAccess := prismAuthRefreshAccount()
	missingAccess.Credentials["expires_at"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	delete(missingAccess.Credentials, "access_token")
	require.True(t, refresher.NeedsRefresh(missingAccess, 5*time.Minute), "Prism account mode must obtain a missing access token")

	withoutRefresh := prismAuthRefreshAccount()
	delete(withoutRefresh.Credentials, "refresh_token")
	require.False(t, refresher.NeedsRefresh(withoutRefresh, 5*time.Minute), "without refresh token there is no refresh operation")
	delete(withoutRefresh.Credentials, "expires_at")
	require.False(t, refresher.NeedsRefresh(withoutRefresh, 5*time.Minute))
}

func TestOpenAITokenProvider_PrismRefreshPersistsRotationAndCachesAccessToken(t *testing.T) {
	for _, scenario := range []string{"expired", "missing_access_future_expiry", "missing_expiry", "Prism_disabled"} {
		t.Run(scenario, func(t *testing.T) {
			client := &prismOAuthRefreshClientStub{}
			proxyID := int64(909)
			account := prismAuthRefreshAccount()
			account.ProxyID = &proxyID
			switch scenario {
			case "missing_access_future_expiry":
				delete(account.Credentials, "access_token")
				account.Credentials["expires_at"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
			case "missing_expiry":
				delete(account.Credentials, "expires_at")
			case "Prism_disabled":
				account.Extra[PrismExtraKey].(map[string]any)["enabled"] = false
			}
			staleSnapshot := snapshotOAuthRefreshAccount(account)
			repo := &refreshAPIAccountRepo{account: account}
			proxyRepo := &mockProxyRepoForOAuth{getByIDFunc: func(_ context.Context, id int64) (*Proxy, error) {
				require.Equal(t, proxyID, id)
				return &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example", Port: 18080}, nil
			}}
			oauthService := NewOpenAIOAuthService(proxyRepo, client)
			defer oauthService.Stop()
			var privacyCalls int32
			oauthService.SetPrivacyClientFactory(func(string) (*req.Client, error) {
				atomic.AddInt32(&privacyCalls, 1)
				return nil, errors.New("stop before ChatGPT request")
			})

			cache := newOpenAITokenCacheStub()
			refreshAPI := NewOAuthRefreshAPI(repo, cache)
			refresher := NewOpenAITokenRefresher(oauthService, repo)
			provider := NewOpenAITokenProvider(repo, cache, oauthService)
			provider.SetRefreshAPI(refreshAPI, refresher)

			token, err := provider.GetAccessToken(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, "prism-rotated-access", token)
			require.Equal(t, int32(1), atomic.LoadInt32(&client.refreshes))
			if scenario == "Prism_disabled" {
				require.Positive(t, atomic.LoadInt32(&privacyCalls), "disabled Prism retains normal ChatGPT enrichment")
			} else {
				require.Zero(t, atomic.LoadInt32(&privacyCalls), "Prism refresh must not invoke ChatGPT privacy/subscription enrichment")
			}

			client.mu.Lock()
			require.Equal(t, "prism-old-refresh", client.refreshToken)
			require.Equal(t, "http://proxy.example:18080", client.proxyURL)
			require.Equal(t, "prism-original-client", client.clientID)
			client.mu.Unlock()

			require.Equal(t, "prism-rotated-access", repo.account.Credentials["access_token"])
			require.Equal(t, "prism-rotated-refresh", repo.account.Credentials["refresh_token"])
			require.Equal(t, "prism-original-client", repo.account.Credentials["client_id"])
			require.Equal(t, "prism-original-id-token", repo.account.Credentials["id_token"])
			require.Equal(t, 1, repo.updateCredentialsCalls)
			require.Equal(t, &proxyID, repo.account.ProxyID)
			require.Positive(t, repo.account.GetCredentialAsInt64("_token_version"))
			expiresAt := repo.account.GetCredentialAsTime("expires_at")
			require.NotNil(t, expiresAt)
			require.WithinDuration(t, time.Now().Add(time.Hour), *expiresAt, 5*time.Second)
			cached, err := cache.GetAccessToken(context.Background(), OpenAITokenCacheKey(account))
			require.NoError(t, err)
			require.Equal(t, "prism-rotated-access", cached)
			token, err = provider.GetAccessToken(context.Background(), staleSnapshot)
			require.NoError(t, err)
			require.Equal(t, "prism-rotated-access", token)
			require.Equal(t, int32(1), atomic.LoadInt32(&client.refreshes), "a subsequent request uses the cached rotation")
			require.Equal(t, int32(1), atomic.LoadInt32(&cache.lockCalled))
			require.Equal(t, int32(1), atomic.LoadInt32(&cache.unlockCalled))
		})
	}
}

func TestOpenAIOAuthService_PrismRefreshDoesNotBypassMissingProxy(t *testing.T) {
	for _, scenario := range []string{"no_repository", "lookup_error", "not_found"} {
		t.Run(scenario, func(t *testing.T) {
			client := &prismOAuthRefreshClientStub{}
			account := prismAuthRefreshAccount()
			proxyID := int64(909)
			account.ProxyID = &proxyID
			var proxyRepo ProxyRepository
			if scenario != "no_repository" {
				proxyRepo = &mockProxyRepoForOAuth{getByIDFunc: func(_ context.Context, id int64) (*Proxy, error) {
					require.Equal(t, proxyID, id)
					if scenario == "lookup_error" {
						return nil, errors.New("sensitive-proxy-error")
					}
					return nil, nil
				}}
			}
			svc := NewOpenAIOAuthService(proxyRepo, client)
			defer svc.Stop()
			info, err := svc.RefreshAccountToken(context.Background(), account)
			require.ErrorContains(t, err, "Prism account proxy is unavailable")
			require.NotContains(t, err.Error(), "sensitive-proxy-error")
			require.Nil(t, info)
			require.Zero(t, atomic.LoadInt32(&client.refreshes), "selected proxy failure must not lead to direct OAuth traffic")
			require.Equal(t, "prism-old-refresh", account.Credentials["refresh_token"])
		})
	}
}

func TestTokenRefreshService_PrismDoesNotRunPrivacyHook(t *testing.T) {
	for _, mode := range []string{PrismAuthModeAccount, PrismAuthModeCookie} {
		t.Run(mode, func(t *testing.T) {
			account := prismAuthRefreshAccount()
			account.Extra[PrismExtraKey].(map[string]any)["auth_mode"] = mode
			account.Extra["privacy_mode"] = PrivacyModeFailed
			repo := &tokenRefreshAccountRepo{}
			var privacyCalls int32
			svc := &TokenRefreshService{
				accountRepo: repo,
				privacyClientFactory: func(string) (*req.Client, error) {
					atomic.AddInt32(&privacyCalls, 1)
					return nil, errors.New("stop before ChatGPT request")
				},
			}
			svc.ensureOpenAIPrivacy(context.Background(), account)
			require.Zero(t, atomic.LoadInt32(&privacyCalls))
			require.Zero(t, repo.updateExtraCalls)

			account.Extra[PrismExtraKey].(map[string]any)["enabled"] = false
			svc.ensureOpenAIPrivacy(context.Background(), account)
			require.Equal(t, int32(1), atomic.LoadInt32(&privacyCalls), "disabled Prism keeps the ordinary background privacy hook")
			require.Equal(t, 1, repo.updateExtraCalls)
		})
	}
}
