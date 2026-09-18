package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAINonStreamingHTTP200BillingJSONFailsOverBeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const billingBody = `{"error":{"code":"insufficient_balance","message":"provider account has no credits"}}`
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(billingBody)),
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			if passthrough {
				_, err = svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, account, "gpt-5", "gpt-5")
			} else {
				_, err = svc.handleNonStreamingResponse(context.Background(), resp, c, account, "gpt-5", "gpt-5")
			}
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.IsUpstreamBillingExhausted())
			require.Equal(t, http.StatusPaymentRequired, failure.StatusCode)
			require.Equal(t, http.StatusBadGateway, failure.ClientStatusCode)
			require.Equal(t, UpstreamBillingExhaustedClientMessage, failure.ClientMessage)
			require.Equal(t, billingBody, string(failure.ResponseBody), "keep provider details for Ops only")
			require.Empty(t, recorder.Body.String(), "account failover must happen before client output")
		})
	}
}
