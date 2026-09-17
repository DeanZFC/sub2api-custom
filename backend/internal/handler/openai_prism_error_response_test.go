//go:build unit

package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Never contacts a real upstream. Failing at session bootstrap exercises both
// buffered JSON errors and errors after the gateway has begun an SSE response.
type prismRejectedSessionUpstream struct{ calls int }

func (u *prismRejectedSessionUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected non-TLS transport")
}

func (u *prismRejectedSessionUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"private upstream diagnostic"}`)),
		Request:    req,
	}, nil
}

func TestOpenAIPrismForwardErrorDoesNotAppendFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, chat := range []bool{false, true} {
		for _, stream := range []bool{false, true} {
			for _, validationError := range []bool{false, true} {
				t.Run(fmt.Sprintf("chat=%t/stream=%t/validation=%t", chat, stream, validationError), func(t *testing.T) {
					upstream := &prismRejectedSessionUpstream{}
					cfg := &config.Config{}
					svc := service.NewOpenAIGatewayService(
						nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil,
						nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil,
					)
					account := &service.Account{
						ID: 512, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
						Credentials: map[string]any{service.PrismCookieCredentialKey: "prism_oai_access_token=synthetic-access; prism_session_token=synthetic-session"},
						Extra:       map[string]any{service.PrismExtraKey: map[string]any{"enabled": true, "version": 1}},
					}
					path := EndpointResponses
					body := map[string]any{"model": "gpt-5.6-sol", "stream": stream}
					if chat {
						path = "/v1/chat/completions"
						body["messages"] = []map[string]string{{"role": "user", "content": "Hello"}}
					} else {
						body["input"] = "Hello"
					}
					if validationError {
						body["tools"] = []map[string]string{{"type": "function", "name": "unsupported_tool"}}
					}
					raw, err := json.Marshal(body)
					require.NoError(t, err)
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
					beforeForward := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)
					if chat {
						_, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, raw, "", "")
					} else {
						_, err = svc.Forward(c.Request.Context(), c, account, raw)
					}
					require.Error(t, err)
					var failover *service.UpstreamFailoverError
					require.False(t, errors.As(err, &failover), "Prism errors must not resubmit generation")
					forwardBody := rec.Body.String()
					require.NotEmpty(t, forwardBody)
					if validationError {
						require.Equal(t, http.StatusBadRequest, rec.Code)
						require.Equal(t, "prism_unsupported_request", gjson.Get(forwardBody, "error.code").String())
						require.Equal(t, "tools", gjson.Get(forwardBody, "error.param").String())
						require.Zero(t, upstream.calls)
					} else {
						require.Equal(t, 1, upstream.calls)
						require.Contains(t, forwardBody, "prism_")
					}

					// Follow the actual Responses/Chat handler error-finalization path.
					h := &OpenAIGatewayHandler{}
					wroteFallback := false
					if !openAIForwardErrorAlreadyCommunicated(c, beforeForward, err) {
						if chat {
							wroteFallback = h.ensureOpenAIStreamReadErrorResponse(c, err, false)
						}
						if !wroteFallback {
							wroteFallback = h.ensureForwardErrorResponse(c, false)
						}
					}
					require.False(t, wroteFallback, "a complete Prism error already reached the client")
					require.Equal(t, forwardBody, rec.Body.String(), "must not append generic JSON/SSE to a terminal response")
					require.NotContains(t, rec.Body.String(), "Upstream request failed")
					require.NotContains(t, rec.Body.String(), "private upstream diagnostic")
					if validationError || !stream {
						require.True(t, json.Valid(rec.Body.Bytes()), "pre-stream errors remain a single JSON document")
					} else {
						require.Equal(t, http.StatusOK, rec.Code)
						streamErr, ok := service.GetOpsStreamError(c)
						require.True(t, ok, "HTTP 200 streams must retain their error outcome in Ops")
						require.NotEmpty(t, streamErr.Message)
						if chat {
							require.Equal(t, 1, strings.Count(rec.Body.String(), "data: [DONE]"))
						} else {
							require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
						}
					}
				})
			}
		}
	}
}
