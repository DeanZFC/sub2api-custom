//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type configuredRetrySettingRepo struct{ service.SettingRepository }

func (*configuredRetrySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyUpstreamErrorRetry {
		return `{"enabled":true,"max_retries":2,"delay_ms":100,"errors":"currently overloaded"}`, nil
	}
	return "", service.ErrSettingNotFound
}

type configuredRetryHTTPUpstream struct {
	calls         int
	streamFailure bool
}

func (u *configuredRetryHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return service.DoWithConfiguredUpstreamRetry(req, func(r *http.Request) (*http.Response, error) {
		u.calls++
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		status := 200
		body := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
		contentType := "text/event-stream"
		if u.calls <= 2 {
			body = `{"error":{"code":"custom_busy","message":"Our servers are CURRENTLY OVERLOADED. Please try again later."}}`
			status, contentType = 503, "application/json"
			if u.streamFailure {
				status, contentType = 200, "text/event-stream"
				body = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"failed\"}}\n\nevent: error\ndata: " + `{"type":"error","error":{"code":"custom_busy","message":"Our servers are CURRENTLY OVERLOADED. Please try again later."}}` + "\n\n"
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}
func (u *configuredRetryHTTPUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

// Exercise the complete Responses handler: two upstream failures must produce
// one successful client response and release every acquired concurrency slot.
func TestUpstreamErrorRetryResponsesRecoversWithinOneClientRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, streamFailure := range []bool{false, true} {
		name := "http_error"
		if streamFailure {
			name = "sse_error"
		}
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.MaxAccountSwitches = 0
			repo := &grokCredentialHandlerRepo{accounts: []service.Account{{ID: 801, Name: "test", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "test"}}}}
			upstream := &configuredRetryHTTPUpstream{streamFailure: streamFailure}
			billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
			defer billingCache.Stop()
			defer gateway.CloseOpenAIWSPool()
			acquired := 0
			cache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { acquired++; return true, nil },
			}
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billingCache, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			groupID := int64(901)
			key := &service.APIKey{ID: 902, GroupID: &groupID, User: &service.User{ID: 903, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}}
			router := gin.New()
			router.Use(middleware.UpstreamErrorRetry(service.NewSettingService(&configuredRetrySettingRepo{}, cfg)))
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 903, Concurrency: 1})
				c.Next()
			})
			router.POST("/v1/responses", h.Responses)
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"gpt-5","input":"hello","stream":true}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, 3, upstream.calls)
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.completed"))
			require.NotContains(t, rec.Body.String(), "custom_busy")
			require.NotContains(t, rec.Body.String(), "currently overloaded")
			require.Equal(t, int32(acquired), atomic.LoadInt32(&cache.releaseAccountCalled))
		})
	}
}
