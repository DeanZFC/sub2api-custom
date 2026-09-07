package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiAlways429Upstream struct {
	calls int
}

func (s *geminiAlways429Upstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"0"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		Request:    req,
	}, nil
}

func (s *geminiAlways429Upstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestGeminiForwardPreservesExhaustedAccount429MarkerOnFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &geminiAlways429Upstream{}
	svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
	retryCount := 1
	account := &Account{
		ID:       901,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":   "test-key",
			"pool_mode": true,
		},
		RateLimit429RetryCount: &retryCount,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`)
	requestCtx := WithAccount429RetryScope(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(requestCtx)

	result, err := svc.Forward(requestCtx, c, account, body)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, failoverErr.Account429RetryExhausted)
	require.Equal(t, geminiMaxRetries, upstream.calls, "同账号透明 429 重试已关闭，只保留 Gemini 原有重试")
}
