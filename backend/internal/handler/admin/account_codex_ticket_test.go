package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountResponseCodexTicketAccountPolicyWithinGateway(t *testing.T) {
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{
		service.OpenAICodexTicketEnabledExtraKey:         true,
		service.OpenAICodexTicketFailClosedExtraKey:      false,
		service.OpenAICodexTicketHarvestProxyIDsExtraKey: []any{float64(8), float64(2)},
		"codex_turn_ticket:gpt-6-astra":                  map[string]any{"state": "private-ticket-material"},
		"codex_turn_cookies":                             map[string]any{"__cflb": "private-cookie-material"},
	}}
	h := &AccountHandler{cfg: &config.Config{}}
	h.cfg.Gateway.OpenAICodexTicket.Enabled = true
	for _, got := range []*dto.Account{h.accountResponseFromService(account), h.accountListResponseFromService(account)} {
		require.NotNil(t, got.CodexTicketConfig)
		require.True(t, got.CodexTicketConfig.Enabled)
		require.False(t, got.CodexTicketConfig.FailClosed)
		require.True(t, got.CodexTicketConfig.GatewayEnabled)
		require.True(t, got.CodexTicketConfig.AccountEnabled)
		require.Equal(t, 292, got.CodexTicketConfig.TargetLength)
		require.NotEmpty(t, got.CodexTurnTickets)
		payload, err := json.Marshal(got)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(payload), "private-ticket-material"))
		require.NotContains(t, string(payload), "private-cookie-material")
		require.NotContains(t, got.Extra, "codex_turn_ticket:gpt-6-astra")
		require.NotContains(t, got.Extra, "codex_turn_cookies")
		require.NotContains(t, got.Extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
		compact := dto.AccountListItemFromAccount(got)
		require.Equal(t, got.CodexTicketConfig, compact.CodexTicketConfig)
	}
}

func TestAccountResponseCodexTicketsUsesConfiguredPolicy(t *testing.T) {
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
	h := &AccountHandler{cfg: &config.Config{}}
	require.Empty(t, h.accountResponseFromService(account).CodexTurnTickets)
	require.Empty(t, h.accountListResponseFromService(account).CodexTurnTickets)
	h.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"configured-model"}, FailClosed: false}
	status := h.accountListResponseFromService(account).CodexTurnTickets
	require.Len(t, status, 1)
	require.Equal(t, "configured-model", status[0].Model)
	require.False(t, status[0].Blocked)
	h.cfg.Gateway.OpenAICodexTicket.FailClosed = true
	require.True(t, h.accountResponseFromService(account).CodexTurnTickets[0].Blocked)
}

func TestAccountResponseCodexTicketsReadsLiveSettingsAfterRestart(t *testing.T) {
	cfg := &config.Config{}
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyOpenAICodexTicketEnabled: "true"}}
	settings := service.NewSettingService(repo, cfg)
	h := &AccountHandler{cfg: cfg}
	h.SetCodexTicketSettings(settings)
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken}
	require.Len(t, h.accountListResponseFromService(account).CodexTurnTickets, 2)
	require.False(t, cfg.Gateway.OpenAICodexTicket.Enabled)
	repo.values[service.SettingKeyOpenAICodexTicketEnabled] = "false"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.Empty(t, h.accountResponseFromService(account).CodexTurnTickets)
}

func TestAccountResponseCodexTicketGatewayOffPreservesAccountChoice(t *testing.T) {
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "team"},
		Extra:       map[string]any{service.OpenAICodexTicketEnabledExtraKey: true, service.OpenAICodexTicketFailClosedExtraKey: true},
	}
	h := &AccountHandler{cfg: &config.Config{}}
	for _, got := range []*dto.Account{h.accountResponseFromService(account), h.accountListResponseFromService(account)} {
		require.NotNil(t, got.CodexTicketConfig)
		require.False(t, got.CodexTicketConfig.GatewayEnabled)
		require.False(t, got.CodexTicketConfig.Enabled)
		require.True(t, got.CodexTicketConfig.AccountEnabled)
		require.True(t, got.CodexTicketConfig.FailClosed)
		require.Equal(t, 332, got.CodexTicketConfig.TargetLength)
		require.Empty(t, got.CodexTurnTickets)
	}
}

type accountCodexTicketDiagnosticsStub struct {
	diagnostics service.OpenAICodexTicketDiagnostics
	accountIDs  []int64
}

func (s *accountCodexTicketDiagnosticsStub) EnrichOpenAICodexTicketDiagnostics(account *service.Account, statuses []service.OpenAICodexTicketStatus) {
	if len(statuses) == 0 {
		return
	}
	s.accountIDs = append(s.accountIDs, account.ID)
	for i := range statuses {
		statuses[i].OpenAICodexTicketDiagnostics = s.diagnostics
	}
}

func TestAccountResponseCodexTicketDiagnosticsAPIWiring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	lastAttempt := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	nextRetry := lastAttempt.Add(30 * time.Second)
	const ticketSecret = "private-ticket-material"
	const cookieSecret = "private-cookie-material"
	const proxySecret = "http://harvest-user:private-proxy-password@proxy.example:8080"
	account := service.Account{
		ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Credentials: map[string]any{"plan_type": "team", "access_token": "private-access-token", "refresh_token": "private-refresh-token"},
		Extra: map[string]any{
			service.OpenAICodexTicketEnabledExtraKey:         true,
			service.OpenAICodexTicketFailClosedExtraKey:      true,
			service.OpenAICodexTicketHarvestProxyIDsExtraKey: []any{float64(8)},
			"codex_turn_ticket:gpt-6-astra":                  map[string]any{"state": ticketSecret},
			"codex_turn_cookies":                             map[string]any{"__cflb": cookieSecret, "__oailb": cookieSecret},
			"codex_harvest_proxy_url":                        proxySecret,
		},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: config.OpenAICodexTicketConfig{
		Enabled: true, Models: []string{"gpt-6-astra"},
	}}}
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyOpenAICodexTicketEnabled: "true"}}
	settings := service.NewSettingService(repo, cfg)
	adminSvc := newStubAdminService()
	adminSvc.accounts = []service.Account{account}
	adminSvc.getAccountResult = &account
	diagnostics := &accountCodexTicketDiagnosticsStub{diagnostics: service.OpenAICodexTicketDiagnostics{
		Attempts: 9, ConsecutiveFailures: 3, InProgress: false,
		LastAttemptAt: &lastAttempt, NextRetryAt: &nextRetry,
		LastErrorCode: "rate_limited", LastError: "Upstream rate limit reached", LastHTTPStatus: 429,
		LastLength: 292, LastProxyIndex: 2, Paused: true, PlanKnown: true,
	}}
	h := &AccountHandler{adminService: adminSvc, cfg: cfg}
	h.SetCodexTicketSettings(settings)
	h.SetCodexTicketGateway(diagnostics)
	router := gin.New()
	router.GET("/api/v1/admin/accounts", h.List)
	router.GET("/api/v1/admin/accounts/:id", h.GetByID)

	for _, endpoint := range []string{
		"/api/v1/admin/accounts?lite=1",
		"/api/v1/admin/accounts",
		"/api/v1/admin/accounts/41",
	} {
		t.Run(endpoint, func(t *testing.T) {
			for _, enabled := range []bool{true, false} {
				if enabled {
					repo.values[service.SettingKeyOpenAICodexTicketEnabled] = "true"
				} else {
					repo.values[service.SettingKeyOpenAICodexTicketEnabled] = "false"
				}
				settings.InvalidateOpenAICodexTicketEnabledCache()
				callsBefore := len(diagnostics.accountIDs)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, endpoint, nil))
				require.Equal(t, http.StatusOK, recorder.Code)
				payload := recorder.Body.String()
				for _, secret := range []string{ticketSecret, cookieSecret, proxySecret, "private-proxy-password", "private-access-token", "private-refresh-token"} {
					require.NotContains(t, payload, secret)
				}
				require.NotContains(t, payload, "codex_turn_ticket:gpt-6-astra")
				require.NotContains(t, payload, "codex_turn_cookies")
				require.NotContains(t, payload, "codex_harvest_proxy_url")
				require.NotContains(t, payload, service.OpenAICodexTicketHarvestProxyIDsExtraKey)

				var envelope struct {
					Data json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
				var item map[string]json.RawMessage
				if strings.HasSuffix(endpoint, "/41") {
					require.NoError(t, json.Unmarshal(envelope.Data, &item))
				} else {
					var page struct {
						Items []map[string]json.RawMessage `json:"items"`
					}
					require.NoError(t, json.Unmarshal(envelope.Data, &page))
					require.Len(t, page.Items, 1)
					item = page.Items[0]
				}
				var policy service.OpenAICodexTicketAccountConfig
				require.NoError(t, json.Unmarshal(item["codex_ticket_config"], &policy))
				require.Equal(t, enabled, policy.GatewayEnabled)
				require.Equal(t, enabled, policy.Enabled)
				require.True(t, policy.AccountEnabled)
				require.True(t, policy.FailClosed)
				require.Equal(t, 332, policy.TargetLength)
				if !enabled {
					require.NotContains(t, item, "codex_turn_tickets", "runtime diagnostics must stay hidden with the gateway disabled")
					require.Len(t, diagnostics.accountIDs, callsBefore)
					continue
				}
				var tickets []service.OpenAICodexTicketStatus
				require.NoError(t, json.Unmarshal(item["codex_turn_tickets"], &tickets))
				require.Len(t, tickets, 1)
				require.Equal(t, "gpt-6-astra", tickets[0].Model)
				require.Equal(t, 332, tickets[0].TargetLength)
				require.True(t, tickets[0].Blocked)
				require.Equal(t, diagnostics.diagnostics, tickets[0].OpenAICodexTicketDiagnostics)
				require.Greater(t, len(diagnostics.accountIDs), callsBefore)
				require.Equal(t, int64(41), diagnostics.accountIDs[len(diagnostics.accountIDs)-1])
			}
		})
	}
}
