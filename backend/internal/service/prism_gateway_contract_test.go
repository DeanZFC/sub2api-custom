//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPrismDisabledRetainsCodexCredentialsAndForwarding(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "disabled"}[present], func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"resp_original","object":"response","status":"completed","model":"gpt-5.4","output":[{"type":"message","id":"msg_original","role":"assistant","status":"completed","content":[{"type":"output_text","text":"original path"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 71, Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "original-token", "prism_cookie": "unused-cookie"}, Extra: map[string]any{}}
			if present {
				account.Extra[PrismExtraKey] = map[string]any{"enabled": false}
			}
			token, _, err := svc.GetAccessToken(context.Background(), account)
			require.NoError(t, err)
			require.Equal(t, "original-token", token)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			body := `{"model":"gpt-5.4","input":"hello","stream":false}`
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
			result, err := svc.Forward(c.Request.Context(), c, account, []byte(body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.UsageUnavailable)
			require.Equal(t, 3, result.Usage.InputTokens)
			require.Equal(t, "chatgpt.com", upstream.lastReq.URL.Host)
			require.Equal(t, "Bearer original-token", upstream.lastReq.Header.Get("Authorization"))
			require.Empty(t, upstream.lastReq.Header.Get("Cookie"))
		})
	}
}

func TestPrismMissingUsageNeverFallsBackToCharge(t *testing.T) {
	svc := &OpenAIGatewayService{billingService: newTestBillingService()}
	result := &OpenAIForwardResult{Model: "claude-sonnet-4", UpstreamEndpoint: prismUpstreamEndpoint, UsageUnavailable: true}
	// Deliberately nonzero pricing inputs verify the unknown-usage decision,
	// including future billing fallbacks, rather than only zero arithmetic.
	cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, &APIKey{}, []string{"claude-sonnet-4"}, 2, 2, 2, 2, UsageTokens{InputTokens: 1000, OutputTokens: 500}, "", nil, time.Time{})
	require.NoError(t, err)
	require.Zero(t, cost.ActualCost)
	require.Zero(t, cost.TotalCost)
	result.UsageUnavailable = false
	cost, err = svc.calculateOpenAIRecordUsageCost(context.Background(), result, &APIKey{}, []string{"claude-sonnet-4"}, 2, 2, 2, 2, UsageTokens{InputTokens: 1000, OutputTokens: 500}, "", nil, time.Time{})
	require.NoError(t, err)
	require.Greater(t, cost.ActualCost, 0.0)
}

func TestPrismRejectsUnsupportedEntrypointsWithoutCodexCredentials(t *testing.T) {
	account := newPrismTestAccount()
	account.Credentials["access_token"] = "original-token"
	svc := &OpenAIGatewayService{}
	_, _, err := svc.GetAccessToken(context.Background(), account)
	require.ErrorContains(t, err, "Prism")
	for _, endpoint := range []string{"messages", "input_tokens", "count_tokens", "images"} {
		t.Run(endpoint, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/"+endpoint, nil)
			switch endpoint {
			case "messages":
				_, err = svc.ForwardAsAnthropic(c.Request.Context(), c, account, nil, "", "")
			case "input_tokens":
				err = svc.ForwardResponsesInputTokens(c.Request.Context(), c, account, nil)
			case "count_tokens":
				err = svc.ForwardCountTokensAsAnthropic(c.Request.Context(), c, account, nil, "")
			case "images":
				_, err = svc.ForwardImages(c.Request.Context(), c, account, nil, nil, "")
			}
			require.Error(t, err)
			require.Equal(t, 400, rec.Code)
			require.NotContains(t, rec.Body.String(), "original-token")
		})
	}
}
