package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexWSAccountRepo struct {
	AccountRepository
	account *Account
	reads   int
}

func (r *codexWSAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, nil
}

func codexWSFixture(t *testing.T) (*OpenAIGatewayService, *Account, *openAICodexWSBinding, *codexWSAccountRepo) {
	t.Helper()
	account := ticketTestAccount(104)
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, FailClosed: true, Models: []string{"gpt-6-astra"}}, nil)
	repo := &codexWSAccountRepo{account: account}
	ticket := &openAICodexTicket{AccountID: account.ID, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Transport: "websocket", HarvestSessionID: "harvest-session", HarvestProxyURL: "http://harvest.invalid:8080"}
	require.NoError(t, s.storeOpenAICodexTicket(context.Background(), account, ticket))
	s.accountRepo = repo
	headers := http.Header{}
	require.NoError(t, s.applyOpenAICodexTicket(context.Background(), account, ticket.Model, headers, "websocket"))
	binding := s.bindCodexTicketWS(context.Background(), account, ticket.Model, headers)
	return s, account, binding, repo
}

func TestCodexWSBindingChecksFreshAccountAndPreservesBoundIdentity(t *testing.T) {
	s, account, binding, repo := codexWSFixture(t)
	require.NoError(t, s.checkCodexTicketWS(context.Background(), account, binding.model, binding))
	require.Equal(t, 1, repo.reads)
	raw, err := binding.pinPayload([]byte(`{"type":"response.create","model":"gpt-6-astra","prompt_cache_key":"client","client_metadata":{"thread_id":"foreign"},"device_id":"foreign","input":"hello"}`))
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(raw, "prompt_cache_key").Exists())
	require.False(t, gjson.GetBytes(raw, "client_metadata").Exists())
	require.False(t, gjson.GetBytes(raw, "device_id").Exists())
	require.Equal(t, "hello", gjson.GetBytes(raw, "input").String())
	h := http.Header{"Session-Id": []string{"foreign"}, "X-Codex-Turn-State": []string{"rotated"}}
	binding.restoreHeaders(h)
	require.Equal(t, binding.ticket.State, h.Get(openAICodexTurnStateHeader))
	require.Equal(t, "harvest-session", h.Get("session_id"))
	require.Empty(t, h.Get("session-id"))
}

func TestCodexWSBindingRejectsExpiredRevokedAndChangedModel(t *testing.T) {
	for _, kind := range []string{"expired", "revoked", "changed_model", "disabled_account", "changed_proxy"} {
		t.Run(kind, func(t *testing.T) {
			s, account, binding, repo := codexWSFixture(t)
			model := binding.model
			switch kind {
			case "expired":
				binding.ticket.ExpiresAt = time.Now().Add(-time.Second)
			case "revoked":
				revoked := *binding.ticket
				revoked.Revoked = true
				s.openaiCodexTickets.Store(openAICodexTicketKey(account.ID, model), &revoked)
			case "changed_model":
				model = "gpt-5.6-sol"
			case "disabled_account":
				fresh := *account
				fresh.Schedulable = false
				repo.account = &fresh
			case "changed_proxy":
				fresh := *account
				id := int64(9)
				fresh.ProxyID = &id
				fresh.Proxy = &Proxy{ID: id, Protocol: "http", Host: "changed.invalid", Port: 80}
				repo.account = &fresh
			}
			require.Error(t, s.checkCodexTicketWS(context.Background(), account, model, binding))
		})
	}
}

func TestCodexWSBindingDisabledFeatureAddsNoAccountReads(t *testing.T) {
	s, account, binding, repo := codexWSFixture(t)
	s.cfg.Gateway.OpenAICodexTicket.Enabled = false
	require.NoError(t, s.checkCodexTicketWS(context.Background(), account, binding.model, binding))
	require.Zero(t, repo.reads)
}

func TestCodexWSBindingAllowsOriginalTicketRetainedAsStandby(t *testing.T) {
	s, account, binding, _ := codexWSFixture(t)
	current := *binding.ticket
	current.State = fakeCodexTicketStateSalt(292, 7)
	current.Standby = binding.ticket
	s.openaiCodexTickets.Store(openAICodexTicketKey(account.ID, binding.model), &current)
	require.NoError(t, s.checkCodexTicketWS(context.Background(), account, binding.model, binding))
	current.Standby = nil
	require.Error(t, s.checkCodexTicketWS(context.Background(), account, binding.model, binding))
}

func TestCodexWSHandshakeKeepsTicketInPoolCompatibility(t *testing.T) {
	account := ticketTestAccount(104)
	first := http.Header{}
	first.Set(openAICodexTurnStateHeader, "first-ticket")
	second := first.Clone()
	second.Set(openAICodexTurnStateHeader, "second-ticket")
	require.NotEqual(t, normalizeOpenAIWSHandshakeCompatibility(account, first), normalizeOpenAIWSHandshakeCompatibility(account, second))
}

func TestCodexWSHeadersUseHarvestIdentity(t *testing.T) {
	s, account, binding, _ := codexWSFixture(t)
	c := &gin.Context{Request: &http.Request{Header: http.Header{"Session-Id": []string{"client-session"}, "X-Codex-Installation-Id": []string{"client-device"}}}}
	headers, _, err := s.buildOpenAIWSHeaders(context.Background(), c, account, "token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "client-ticket", "client-metadata", "client-cache", binding.model, "")
	require.NoError(t, err)
	require.Equal(t, binding.ticket.State, headers.Get(openAICodexTurnStateHeader))
	require.Equal(t, "harvest-session", headers.Get("session_id"))
	require.Empty(t, headers.Get("session-id"))
	require.Empty(t, headers.Get("x-codex-installation-id"))
	require.Empty(t, headers.Get(openAIWSTurnMetadataHeader))
}

func TestExcelBPSWebSocketRejectedByMappedModel(t *testing.T) {
	account := ticketTestAccount(104)
	account.Extra = map[string]any{"openai_excel_bps": true, "openai_excel_bps_models": []string{"gpt-6-astra"}}
	account.Credentials["model_mapping"] = map[string]any{"alias": "gpt-6-astra"}
	require.Error(t, openAIWSRejectExcelBPS(account, "alias", ""))
	require.Error(t, openAIWSRejectExcelBPS(account, "", "gpt-6-astra"))
	require.NoError(t, openAIWSRejectExcelBPS(account, "gpt-5.6-sol", "gpt-5.6-sol"))
}
