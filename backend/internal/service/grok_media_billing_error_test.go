package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGrokMediaBillingErrorBypassesPassthroughAndFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	rules := &ErrorPassthroughService{}
	rules.setLocalCache([]*model.ErrorPassthroughRule{
		newNonFailoverPassthroughRule(http.StatusBadRequest, "insufficient balance", http.StatusTeapot, "balance: $37"),
	})
	BindErrorPassthroughService(c, rules)

	account := &Account{ID: 86001, Platform: PlatformGrok, Type: AccountTypeAPIKey}
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"X-Request-Id": []string{"grok-billing-1"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"insufficient_balance","message":"insufficient balance: $37"}}`)),
	}
	result, err := (&OpenAIGatewayService{}).handleGrokMediaErrorResponse(context.Background(), resp, c, account, "grok-billing-1", "grok-imagine")

	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.True(t, errors.As(err, &failover))
	require.True(t, failover.IsUpstreamBillingExhausted())
	require.Equal(t, http.StatusBadGateway, failover.ClientStatusCode)
	require.Equal(t, UpstreamBillingExhaustedClientMessage, failover.ClientMessage)
	require.False(t, failover.RetryableOnSameAccount)
	require.Empty(t, recorder.Body.String())
	require.False(t, c.Writer.Written())
	require.False(t, IsResponseCommitted(c))
	require.Equal(t, "grok-billing-1", failover.ResponseHeaders.Get("x-request-id"))
}
