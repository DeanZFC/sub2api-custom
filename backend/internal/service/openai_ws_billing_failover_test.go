package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSBillingStatus_MapsSemanticEventsToPaymentRequired(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "error event",
			body: `{"type":"error","error":{"code":"insufficient_quota","message":"quota exhausted"}}`,
		},
		{
			name: "response failed event",
			body: `{"type":"response.failed","response":{"error":{"type":"insufficient_balance","message":"余额不足"}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, http.StatusPaymentRequired, openAIWSBillingStatus([]byte(tt.body), "quota exhausted"))
		})
	}
}

func TestOpenAIWSBillingStatus_DoesNotMatchRequestPolicyError(t *testing.T) {
	body := []byte(`{"type":"error","error":{"code":"invalid_request_error","message":"prompt mentions insufficient balance"}}`)
	require.Zero(t, openAIWSBillingStatus(body, "prompt mentions insufficient balance"))
}

func TestOpenAIWSErrorHTTPStatusFromRaw_BillingCodeIsPaymentRequired(t *testing.T) {
	require.Equal(t, http.StatusPaymentRequired, openAIWSErrorHTTPStatusFromRaw("insufficient_quota", ""))
}

func TestOpenAIWSBillingFailoverError_HidesProviderMessage(t *testing.T) {
	err := newUpstreamBillingFailoverError(
		http.StatusPaymentRequired,
		http.Header{},
		[]byte(`{"error":{"code":"insufficient_quota","message":"provider balance is zero"}}`),
		false,
	)
	require.True(t, err.IsUpstreamBillingExhausted())
	require.Equal(t, UpstreamBillingExhaustedClientMessage, err.ClientMessage)
}

func newOpenAIWSBillingForwardFixture(t *testing.T, events ...string) (*OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder, *Account) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3

	conn := &openAIWSCaptureConn{}
	for _, event := range events {
		conn.events = append(conn.events, []byte(event))
	}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID:          1401,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
	return svc, c, recorder, account
}

func TestOpenAIWSBillingErrorBeforeOutputReturnsSafeNonStreamingResponse(t *testing.T) {
	for _, tt := range []struct {
		name  string
		event string
	}{
		{
			name:  "error event",
			event: `{"type":"error","error":{"code":"insufficient_balance","message":"provider balance is zero"}}`,
		},
		{
			name:  "failed response",
			event: `{"type":"response.failed","response":{"id":"resp_billing","error":{"type":"insufficient_quota","message":"provider balance is zero"}}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, c, recorder, account := newOpenAIWSBillingForwardFixture(t, tt.event)
			body := []byte(`{"model":"gpt-5.1","stream":false,"input":"hi"}`)
			result, err := svc.Forward(context.Background(), c, account, body)
			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.IsUpstreamBillingExhausted())
			require.Equal(t, http.StatusBadGateway, failover.ClientStatusCode)
			require.Equal(t, UpstreamBillingExhaustedClientMessage, failover.ClientMessage)
			require.Empty(t, recorder.Body.String(), "service must let the handler select another account before writing")
		})
	}
}

func TestOpenAIWSBillingErrorAfterStreamOutputHidesProviderAndReturnedMessage(t *testing.T) {
	svc, c, recorder, account := newOpenAIWSBillingForwardFixture(t,
		`{"type":"response.created","response":{"id":"resp_billing","model":"gpt-5.1"}}`,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"error","error":{"code":"insufficient_balance","message":"provider balance is zero"}}`,
	)
	body := []byte(`{"model":"gpt-5.1","stream":true,"input":"hi"}`)
	result, err := svc.Forward(context.Background(), c, account, body)
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, err.Error(), "provider balance")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "hello")
	require.Contains(t, recorder.Body.String(), UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, recorder.Body.String(), "provider balance")
}
