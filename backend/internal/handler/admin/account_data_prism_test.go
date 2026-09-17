package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPrismExportDropsSessionAndDisablesRoutingWithoutMutatingSource(t *testing.T) {
	router, svc := setupAccountDataRouter()
	prism := map[string]any{"enabled": true, "version": 1, "timeout_seconds": 240}
	cookie := "prism_oai_access_token=private-access; prism_session_token=private-session"
	svc.accounts = []service.Account{{ID: 21, Name: "Prism", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-retained", service.PrismCookieCredentialKey: cookie, service.PrismCookieConfiguredCredentialKey: true},
		Extra:       map[string]any{service.PrismExtraKey: prism, "ordinary": "kept"}}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data?include_proxies=false", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var exported dataResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &exported))
	require.Len(t, exported.Data.Accounts, 1)
	account := exported.Data.Accounts[0]
	require.Equal(t, "oauth-retained", account.Credentials["access_token"])
	require.NotContains(t, account.Credentials, service.PrismCookieCredentialKey)
	require.NotContains(t, account.Credentials, service.PrismCookieConfiguredCredentialKey)
	require.Equal(t, false, account.Extra[service.PrismExtraKey].(map[string]any)["enabled"])
	require.Equal(t, float64(240), account.Extra[service.PrismExtraKey].(map[string]any)["timeout_seconds"])
	require.NotContains(t, response.Body.String(), "private-access")
	require.NotContains(t, response.Body.String(), "private-session")
	require.True(t, svc.accounts[0].IsPrismEnabled())
	require.Equal(t, cookie, svc.accounts[0].Credentials[service.PrismCookieCredentialKey])
	require.Equal(t, true, prism["enabled"])
}
