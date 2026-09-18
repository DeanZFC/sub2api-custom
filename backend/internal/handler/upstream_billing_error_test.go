package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamBillingFailoverExhaustedHidesProviderDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	err := &service.UpstreamFailoverError{
		StatusCode:       http.StatusPaymentRequired,
		ResponseBody:     []byte(`{"error":{"code":"insufficient_balance","message":"provider balance is zero"}}`),
		Reason:           service.UpstreamBillingExhaustedReason,
		ClientStatusCode: http.StatusBadGateway,
		ClientMessage:    service.UpstreamBillingExhaustedClientMessage,
	}
	for _, tc := range []struct {
		name  string
		write func(*gin.Context)
	}{
		{"responses", func(c *gin.Context) { (&OpenAIGatewayHandler{}).handleFailoverExhausted(c, err, false) }},
		{"messages", func(c *gin.Context) {
			(&GatewayHandler{}).handleFailoverExhausted(c, err, service.PlatformAnthropic, false)
		}},
		{"chat completions", func(c *gin.Context) { (&GatewayHandler{}).handleCCFailoverExhausted(c, err, false) }},
		{"responses compatibility", func(c *gin.Context) { (&GatewayHandler{}).handleResponsesFailoverExhausted(c, err, false) }},
		{"messages compatibility", func(c *gin.Context) { (&OpenAIGatewayHandler{}).handleAnthropicFailoverExhausted(c, err, false) }},
		{"gemini", func(c *gin.Context) { (&GatewayHandler{}).handleGeminiFailoverExhausted(c, err) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, reason := range []service.GatewayFailureReason{service.UpstreamBillingExhaustedReason, ""} {
				err.Reason = reason
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				tc.write(c)
				require.Equal(t, http.StatusBadGateway, rec.Code)
				require.Contains(t, rec.Body.String(), service.UpstreamBillingExhaustedClientMessage)
				require.NotContains(t, rec.Body.String(), "provider balance")
				require.NotContains(t, rec.Body.String(), "insufficient_balance")
			}
		})
	}
}
