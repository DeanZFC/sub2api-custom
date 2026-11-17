package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type ccFallbackHandlerTestUpstream struct {
	service.HTTPUpstream
	body       string
	accountIDs []int64
	paths      []string
	onEOF      context.CancelFunc
}

func (u *ccFallbackHandlerTestUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.accountIDs = append(u.accountIDs, accountID)
	u.paths = append(u.paths, req.URL.Path)
	var body io.Reader = strings.NewReader(u.body)
	if u.onEOF != nil {
		body = &ccFallbackCancelReader{Reader: body, cancel: u.onEOF}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"cc-fallback-stream-test"}},
		Body:       io.NopCloser(body),
	}, nil
}

type ccFallbackCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *ccFallbackCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
		return n, context.Canceled
	}
	return n, err
}

func newCCFallbackStreamHandlerTest(t *testing.T, endpoint, upstreamBody string, cancelAtEOF bool) (*OpenAIGatewayHandler, *gin.Context, *httptest.ResponseRecorder, *ccFallbackHandlerTestUpstream) {
	t.Helper()
	groupID := int64(4220)
	accounts := make([]service.Account, 0, 2)
	for i := 0; i < 2; i++ {
		accounts = append(accounts, service.Account{
			ID: int64(9930 + i), Name: "chat-fallback-account", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: i + 1,
			Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.example.test"},
			Extra: map[string]any{
				openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
			},
		})
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	upstream := &ccFallbackHandlerTestUpstream{body: upstreamBody}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, nil, nil, nil, nil, nil, nil,
		cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := `{"model":"deepseek-v4.1-flash","input":"hello","stream":true}`
	if endpoint == "messages" {
		body = `{"model":"deepseek-v4.1-flash","max_tokens":1024,"messages":[{"role":"user","content":"hello"}],"stream":true}`
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if cancelAtEOF {
		ctx, cancel := context.WithCancel(c.Request.Context())
		t.Cleanup(cancel)
		c.Request = c.Request.WithContext(ctx)
		upstream.onEOF = cancel
	}
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 1820, GroupID: &groupID, User: &service.User{ID: 1720, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1720})
	return h, c, recorder, upstream
}

const ccFallbackHandlerPartial = "data: " + `{"id":"chatcmpl_partial","choices":[{"index":0,"delta":{"content":"partial answer"},"finish_reason":null}]}` + "\n\n"

// Exercise the public handlers through the real force_chat_completions service
// path. A failed response must have one protocol-correct error terminal, retain
// the stable failure code and count towards SLA without replaying the request.
func TestOpenAICCFallbackHandlers_StreamFailuresReturnOneErrorWithoutReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "messages"} {
		for _, tc := range []struct {
			name string
			body string
			code string
		}{
			{name: "partial_eof", body: ccFallbackHandlerPartial, code: "upstream_stream_truncated"},
			{name: "partial_then_error", body: ccFallbackHandlerPartial + "data: {\"error\":{\"message\":\"provider-private-detail\"}}\n\n", code: "upstream_stream_error"},
			{name: "usage_only", body: "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":94682,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n", code: "upstream_empty_response"},
			{name: "done_only", body: "data: [DONE]\n\n", code: "upstream_empty_response"},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				h, c, recorder, upstream := newCCFallbackStreamHandlerTest(t, endpoint, tc.body, false)
				if endpoint == "responses" {
					h.Responses(c)
				} else {
					h.Messages(c)
				}

				require.Equal(t, []int64{9930}, upstream.accountIDs, "a failed attempt must not replay to another account")
				require.Equal(t, []string{"/v1/chat/completions"}, upstream.paths)
				body := recorder.Body.String()
				require.NotContains(t, body, "provider-private-detail")
				require.NotContains(t, body, "event: response.completed\n")
				require.NotContains(t, body, "event: message_stop\n")
				require.NotContains(t, body, "data: [DONE]")
				if strings.HasPrefix(tc.name, "partial") {
					require.Contains(t, body, "partial answer")
				}
				if recorder.Code == http.StatusBadGateway {
					require.Equal(t, tc.code, gjson.Get(body, "error.code").String())
					return
				}
				require.Equal(t, http.StatusOK, recorder.Code, "a committed stream reports its failure in-band")
				marker, codePath := "event: response.failed\n", "response.error.code"
				if endpoint == "messages" {
					marker, codePath = "event: error\n", "error.code"
					require.NotContains(t, body, "event: response.failed\n")
				} else {
					require.NotContains(t, body, "event: error\n")
				}
				require.Equal(t, 1, strings.Count(body, marker))
				terminal := body[strings.Index(body, marker):]
				payload := strings.TrimSpace(strings.SplitN(terminal, "data: ", 2)[1])
				require.True(t, gjson.Valid(payload), "the failure terminal must not have trailing events")
				require.Equal(t, tc.code, gjson.Get(payload, codePath).String())
				streamErr, ok := service.GetOpsStreamError(c)
				require.True(t, ok)
				require.True(t, streamErr.CountTowardsSLA)
				require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
				require.Equal(t, tc.code, streamErr.Code)
				require.True(t, service.IsResponseCommitted(c))
			})
		}
	}
}

func TestOpenAICCFallbackHandlers_CompleteStreamsRemainSuccessful(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			body := ccFallbackHandlerPartial + "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n"
			h, c, recorder, upstream := newCCFallbackStreamHandlerTest(t, endpoint, body, false)
			if endpoint == "responses" {
				h.Responses(c)
			} else {
				h.Messages(c)
			}
			require.Equal(t, []int64{9930}, upstream.accountIDs)
			require.Equal(t, []string{"/v1/chat/completions"}, upstream.paths)
			require.Equal(t, http.StatusOK, recorder.Code)
			out := recorder.Body.String()
			require.Contains(t, out, "partial answer")
			require.NotContains(t, out, "event: response.failed\n")
			require.NotContains(t, out, "event: error\n")
			marker := "event: response.completed\n"
			if endpoint == "messages" {
				marker = "event: message_stop\n"
			}
			require.Equal(t, 1, strings.Count(out, marker))
			_, failed := service.GetOpsStreamError(c)
			require.False(t, failed)
		})
	}
}

func TestOpenAICCFallbackHandlers_ClientCancellationDoesNotEmitUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			h, c, recorder, upstream := newCCFallbackStreamHandlerTest(t, endpoint, ccFallbackHandlerPartial, true)
			if endpoint == "responses" {
				h.Responses(c)
			} else {
				h.Messages(c)
			}
			require.Equal(t, []int64{9930}, upstream.accountIDs)
			require.ErrorIs(t, c.Request.Context().Err(), context.Canceled)
			require.NotContains(t, recorder.Body.String(), "event: response.failed\n")
			require.NotContains(t, recorder.Body.String(), "event: error\n")
			_, failed := service.GetOpsStreamError(c)
			require.False(t, failed)
		})
	}
}
