package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountDataCodexTicketProxiesAreRetired(t *testing.T) {
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
			"codex_turn_cookies":                             map[string]any{"__cflb": "private-cookie", "__oailb": "private-cookie"},
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
			require.NotContains(t, rec.Body.String(), "codex_ticket_proxy_keys")
			require.NotContains(t, account.Extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
			require.NotContains(t, rec.Body.String(), "private-state")
			require.NotContains(t, rec.Body.String(), "private-cookie")
			require.NotContains(t, account.Extra, "codex_turn_cookies")
			if include {
				require.Len(t, exported.Data.Proxies, 1, "only business proxies are exported")
				require.Equal(t, "business.example", exported.Data.Proxies[0].Host)
			} else {
				require.Empty(t, exported.Data.Proxies)
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
			require.NotContains(t, created.Extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
			require.NotContains(t, created.Extra, "codex_turn_cookies")
			if include {
				require.Equal(t, int64(111), *created.ProxyID)
			} else {
				require.Nil(t, created.ProxyID)
			}
		})
	}
	require.Equal(t, []int64{33, 22}, source.accounts[0].Extra[service.OpenAICodexTicketHarvestProxyIDsExtraKey], "export must not mutate source routing")
}

type codexTicketImportAccountRepo struct {
	service.AdminAccountRepository
	created []*service.Account
}

func (r *codexTicketImportAccountRepo) Create(_ context.Context, account *service.Account) error {
	account.ID = 1
	r.created = append(r.created, account)
	return nil
}

type codexTicketImportAdminService struct {
	*stubAdminService
	createService service.AdminService
}

func (s *codexTicketImportAdminService) CreateAccount(ctx context.Context, input *service.CreateAccountInput) (*service.Account, error) {
	return s.createService.CreateAccount(ctx, input)
}

func TestImportCodexTicketCookiesAreStrippedBeforePersistence(t *testing.T) {
	repo := &codexTicketImportAccountRepo{}
	adminSvc := &codexTicketImportAdminService{
		stubAdminService: newStubAdminService(),
		createService: service.NewAdminService(
			nil, nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		),
	}
	h := &AccountHandler{adminService: adminSvc}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/admin/accounts/data", h.ImportData)
	body := []byte(`{"data":{"accounts":[{"name":"imported", "platform":"openai", "type":"setup-token", "credentials":{"access_token":"test-token"}, "extra":{"codex_ticket_enabled":true,"codex_turn_cookies":{"__cflb":"spoofed-cookie","__oailb":"spoofed-cookie"},"codex_turn_ticket:gpt-6-astra":{"state":"spoofed-ticket"},"ordinary":"retained"}}],"proxies":[]},"skip_default_group_bind":true}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, repo.created, 1, rec.Body.String())
	extra := repo.created[0].Extra
	require.NotContains(t, extra, "codex_turn_cookies")
	require.NotContains(t, extra, "codex_turn_ticket:gpt-6-astra")
	require.Equal(t, true, extra[service.OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, "retained", extra["ordinary"])
}

func TestImportCodexTicketProxiesIgnoresRetiredConfiguration(t *testing.T) {
	router, target := setupAccountDataRouter()
	body := []byte(`{"data":{"accounts":[{"name":"legacy", "platform":"openai", "type":"oauth", "credentials":{"access_token":"test-token"}, "extra":{"codex_ticket_enabled":true,"codex_ticket_harvest_proxy_ids":[404]}, "codex_ticket_proxy_keys":["http|missing.example|8080|user|secret"]}],"proxies":[]}}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, target.createdAccounts, 1, rec.Body.String())
	require.Equal(t, true, target.createdAccounts[0].Extra[service.OpenAICodexTicketEnabledExtraKey])
	require.NotContains(t, target.createdAccounts[0].Extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
	require.Empty(t, target.createdProxies)
}
