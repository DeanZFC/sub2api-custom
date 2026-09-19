package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountDataCodexTicketProxiesRemapAcrossInstances(t *testing.T) {
	router, source := setupAccountDataRouter()
	businessID := int64(11)
	source.proxies = []service.Proxy{
		{ID: 11, Name: "business", Protocol: "http", Host: "business.example", Port: 8080, Status: service.StatusActive},
		{ID: 22, Name: "harvest A", Protocol: "http", Host: "harvest-a.example", Port: 8080, Status: service.StatusActive},
		{ID: 33, Name: "harvest B", Protocol: "socks5", Host: "harvest-b.example", Port: 1080, Status: service.StatusActive},
	}
	source.accounts = []service.Account{{
		ID: 1, Name: "account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "test-token"}, ProxyID: &businessID,
		Extra: map[string]any{
			service.OpenAICodexTicketEnabledExtraKey:         true,
			service.OpenAICodexTicketFailClosedExtraKey:      true,
			service.OpenAICodexTicketHarvestProxyIDsExtraKey: []int64{33, 22},
			"codex_turn_ticket:gpt-6-astra":                  map[string]any{"state": "private-state"},
		},
	}}
	for _, include := range []bool{true, false} {
		t.Run(map[bool]string{true: "include proxies", false: "exclude proxies"}[include], func(t *testing.T) {
			url := "/api/v1/admin/accounts/data"
			if !include {
				url += "?include_proxies=false"
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			require.Equal(t, http.StatusOK, rec.Code)
			var exported struct {
				Data DataPayload `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &exported))
			require.Len(t, exported.Data.Accounts, 1)
			account := exported.Data.Accounts[0]
			require.NotNil(t, account.CodexTicketProxyKeys)
			require.NotContains(t, account.Extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
			require.NotContains(t, rec.Body.String(), "private-state")
			if include {
				require.Len(t, exported.Data.Proxies, 3, "dedicated proxies must be included alongside the business proxy")
				require.Equal(t, []string{"socks5|harvest-b.example|1080||", "http|harvest-a.example|8080||"}, *account.CodexTicketProxyKeys)
			} else {
				require.Empty(t, exported.Data.Proxies)
				require.Empty(t, *account.CodexTicketProxyKeys)
			}
			targetRouter, target := setupAccountDataRouter()
			target.proxies = append([]service.Proxy(nil), source.proxies...)
			for i := range target.proxies {
				target.proxies[i].ID += 100
			}
			body, err := json.Marshal(DataImportRequest{Data: exported.Data})
			require.NoError(t, err)
			rec = httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			targetRouter.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code)
			require.Len(t, target.createdAccounts, 1, rec.Body.String())
			created := target.createdAccounts[0]
			require.Equal(t, true, created.Extra[service.OpenAICodexTicketEnabledExtraKey])
			if include {
				require.Equal(t, int64(111), *created.ProxyID)
				require.Equal(t, []int64{133, 122}, created.Extra[service.OpenAICodexTicketHarvestProxyIDsExtraKey])
			} else {
				require.Nil(t, created.ProxyID)
				require.Equal(t, []int64{}, created.Extra[service.OpenAICodexTicketHarvestProxyIDsExtraKey])
			}
		})
	}
	require.Equal(t, []int64{33, 22}, source.accounts[0].Extra[service.OpenAICodexTicketHarvestProxyIDsExtraKey], "export must not mutate source routing")
}

func TestImportCodexTicketProxiesRejectsUnmappedReferences(t *testing.T) {
	_, err := importCodexTicketProxyExtra(map[string]any{service.OpenAICodexTicketHarvestProxyIDsExtraKey: []int64{22}}, nil, map[string]int64{})
	require.Error(t, err)
	keys := []string{"http|missing.example|8080|user|secret"}
	_, err = importCodexTicketProxyExtra(nil, &keys, map[string]int64{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret")
}
