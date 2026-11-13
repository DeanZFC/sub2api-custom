package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestManagedIPv6DataExportDoesNotTransferLeaseCredentials(t *testing.T) {
	for _, username := range []string{"ovh6_lease", "v6-sub2-lease"} {
		t.Run(username, func(t *testing.T) {
			router, svc := setupAccountDataRouter()
			id := int64(11)
			proxy := service.Proxy{ID: id, Name: "managed", Protocol: "http", Host: "private-egress", Port: 8080, Username: username, Password: "lease-secret", Status: service.StatusActive}
			svc.proxies = []service.Proxy{proxy}
			svc.accounts = []service.Account{{ID: 1, Name: "account", Platform: "openai", Type: "oauth", Credentials: map[string]any{"token": "account-secret"}, ProxyID: &id, Proxy: &proxy, Extra: map[string]any{"openai_ipv6_bound": true, "ovh_ipv6_bound": true, "keep": "policy"}}}
			for _, query := range []string{"", "?include_proxies=false"} {
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data"+query, nil))
				require.Equal(t, http.StatusOK, rec.Code)
				require.NotContains(t, rec.Body.String(), "lease-secret")
				require.NotContains(t, rec.Body.String(), "private-egress")
				require.NotContains(t, rec.Body.String(), username)
				var resp proxyDataResponse
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
				require.Empty(t, resp.Data.Proxies)
				require.Len(t, resp.Data.Accounts, 1)
				account := resp.Data.Accounts[0]
				require.Equal(t, "account-secret", account.Credentials["token"])
				require.Equal(t, "policy", account.Extra["keep"])
				require.NotContains(t, account.Extra, "openai_ipv6_bound")
				require.NotContains(t, account.Extra, "ovh_ipv6_bound")
				if query == "" {
					require.Equal(t, managedIPv6DataProxyKey, *account.ProxyKey)
				} else {
					require.Nil(t, account.ProxyKey)
				}
			}
			proxyRouter, proxySvc := setupProxyDataRouter()
			proxySvc.proxies = []service.Proxy{proxy}
			rec := httptest.NewRecorder()
			proxyRouter.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/proxies/data", nil))
			require.Equal(t, http.StatusOK, rec.Code)
			require.NotContains(t, rec.Body.String(), "lease-secret")
			var resp proxyDataResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Empty(t, resp.Data.Proxies)
			require.Equal(t, true, svc.accounts[0].Extra["openai_ipv6_bound"], "export must not mutate live account state")
		})
	}
}

func TestManagedIPv6DataImportRejectsCredentialsAndAliasedProxyKeys(t *testing.T) {
	for _, username := range []string{"ovh6_lease", "v6-sub2-lease"} {
		t.Run(username, func(t *testing.T) {
			item := DataProxy{ProxyKey: "safe-looking-alias", Name: "managed", Protocol: "http", Host: "private", Port: 8080, Username: username, Password: "lease-secret", Status: "active"}
			key := item.ProxyKey
			request := DataImportRequest{Data: DataPayload{Proxies: []DataProxy{item}, Accounts: []DataAccount{{Name: "account", Platform: "openai", Type: "oauth", Credentials: map[string]any{"token": "x"}, Concurrency: 1, ProxyKey: &key}}}}
			svc := newStubAdminService()
			h := &AccountHandler{adminService: svc}
			result, err := h.importData(context.Background(), request)
			require.NoError(t, err)
			require.Equal(t, 1, result.ProxyFailed)
			require.Equal(t, 1, result.AccountFailed)
			require.Empty(t, svc.createdProxies)
			require.Empty(t, svc.createdAccounts)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "lease-secret")

			router, proxySvc := setupProxyDataRouter()
			body, err := json.Marshal(map[string]any{"data": request.Data})
			require.NoError(t, err)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/proxies/data", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			var response proxyImportResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			require.Equal(t, 1, response.Data.ProxyFailed)
			require.Empty(t, proxySvc.createdProxies)
			require.NotContains(t, rec.Body.String(), "lease-secret")
		})
	}
}

func TestManagedIPv6AccountImportRequiresExplicitNewSelection(t *testing.T) {
	for _, mode := range []string{"", "ovh_ipv6", "direct", "random", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			key := managedIPv6DataProxyKey
			originalExtra := map[string]any{"openai_ipv6_bound": true, "ovh_ipv6_bound": true, "keep": "policy"}
			req := DataImportRequest{ProxyMode: mode, Data: DataPayload{Accounts: []DataAccount{{Name: "account", Platform: "openai", Type: "oauth", Credentials: map[string]any{"token": "x"}, Concurrency: 1, ProxyKey: &key, Extra: originalExtra}}}}
			svc := newStubAdminService()
			result, err := (&AccountHandler{adminService: svc}).importData(context.Background(), req)
			require.NoError(t, err)
			if mode == "" || mode == "invalid" {
				require.Equal(t, 1, result.AccountFailed)
				require.Empty(t, svc.createdAccounts)
				return
			}
			require.Equal(t, 1, result.AccountCreated)
			require.Len(t, svc.createdAccounts, 1)
			input := svc.createdAccounts[0]
			require.Equal(t, mode, input.ProxyMode)
			require.Nil(t, input.ProxyID)
			require.NotContains(t, input.Extra, "openai_ipv6_bound")
			require.NotContains(t, input.Extra, "ovh_ipv6_bound")
			require.Equal(t, "policy", input.Extra["keep"])
			require.Equal(t, true, originalExtra["openai_ipv6_bound"])
		})
	}
}
