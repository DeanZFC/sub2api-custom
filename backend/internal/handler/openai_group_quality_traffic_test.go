//go:build unit

package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type handlerGroupTrafficGate struct {
	service.PassiveQualityRepository
	mu       sync.Mutex
	allow    bool
	acquired []service.GroupQualityTrafficRequest
}

func (g *handlerGroupTrafficGate) AcquirePassiveQuality(ctx context.Context, _ int64, model, _ string) (bool, bool, error) {
	r, ok := service.GroupQualityTrafficRequestFromContext(ctx)
	if !ok {
		return false, false, fmt.Errorf("missing quality evidence identity")
	}
	r.Model = model
	g.mu.Lock()
	g.acquired = append(g.acquired, r)
	allow := g.allow
	g.mu.Unlock()
	return allow, false, nil
}
func (*handlerGroupTrafficGate) ReleasePassiveQuality(context.Context, string, bool) error {
	return nil
}
func (g *handlerGroupTrafficGate) requests() []service.GroupQualityTrafficRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]service.GroupQualityTrafficRequest(nil), g.acquired...)
}

func newGroupTrafficHandler(t *testing.T, gate service.PassiveQualityRepository, upstreamURL string, upstream service.HTTPUpstream, pool *service.UsageRecordWorkerPool, accountChanges ...func(*service.Account)) *OpenAIGatewayHandler {
	t.Helper()
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 3
	account := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "fixture", "base_url": upstreamURL}, Extra: map[string]any{"openai_apikey_responses_websockets_v2_enabled": true, "openai_apikey_responses_websockets_v2_mode": service.OpenAIWSIngressModePassthrough}}
	for _, change := range accountChanges {
		change(&account)
	}
	repo := groupTrafficAccountRepo{openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}}
	concurrency := service.ProvideConcurrencyService(&fakeConcurrencyCache{}, nil, nil, gate)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	gateway := service.NewOpenAIGatewayService(repo, &openAIWSUsageHandlerUsageLogRepoStub{}, nil, nil, nil, nil, nil, cfg, nil, concurrency, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	return NewOpenAIGatewayHandler(gateway, concurrency, billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), pool, nil, nil, nil, cfg)
}

type groupTrafficAccountRepo struct {
	openAIImagesFailoverAccountRepo
}

func (r groupTrafficAccountRepo) ListByGroup(ctx context.Context, groupID int64) ([]service.Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(ctx, groupID, service.PlatformOpenAI)
}

func TestOpenAIGroupTrafficHTTPQualityDenialDoesNotForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/v1/messages", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			gate := &handlerGroupTrafficGate{}
			upstream := &openAIResponsesFailoverCancelUpstream{}
			h := newGroupTrafficHandler(t, gate, "https://api.example.test", upstream, nil)
			c, w := newOpenAIResponsesFailoverTestContext(t, context.Background())
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader("{\"model\":\"gpt-5.1\",\"input\":\"hello\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}"))
			key, _ := middleware2.GetAPIKeyFromContext(c)
			key.Group.AllowMessagesDispatch = true
			service.PassiveQualityBeginTurn(c)
			switch path {
			case "/v1/responses":
				h.Responses(c)
			case "/v1/messages":
				h.Messages(c)
			default:
				h.ChatCompletions(c)
			}
			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			require.Empty(t, w.Header().Get("Retry-After"))
			require.Equal(t, accountQualityHeldErrorCode, gjson.Get(w.Body.String(), "error.code").String())
			require.Empty(t, upstream.calls())
			requests := gate.requests()
			require.NotEmpty(t, requests)
			for _, r := range requests {
				require.EqualValues(t, 3131, r.GroupID)
				require.Equal(t, "gpt-5.1", r.Model)
			}
			require.Empty(t, c.GetString(service.OpsUpstreamErrorMessageKey))
		})
	}
}

func TestOpenAIGroupTrafficWaitPlanPreservesStickyOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprint(wait), func(t *testing.T) {
			bindings := &grokMediaSlotBindings{owner: 1, key: "existing-owner"}
			gw := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, bindings, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			calls := 0
			cache := &concurrencyCacheMock{acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { calls++; return !wait || calls > 1, nil }}
			h := &OpenAIGatewayHandler{gatewayService: gw, concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0)}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			selection := &service.AccountSelectionResult{Account: &service.Account{ID: 2, Concurrency: 1}, PreserveStickyBinding: true, WaitPlan: &service.AccountWaitPlan{AccountID: 2, MaxConcurrency: 1, Timeout: time.Second, MaxWaiting: 2}}
			groupID := int64(24)
			started := false
			release, result := h.acquireResponsesAccountSlot(c, &groupID, "existing-owner", selection, false, &started, zap.NewNop())
			require.Equal(t, openAISlotAcquireOK, result)
			require.NotNil(t, release)
			release()
			require.EqualValues(t, 1, bindings.owner)
			require.Zero(t, bindings.writes)
		})
	}
}

func startGroupTrafficWS(t *testing.T, h *OpenAIGatewayHandler, groupChanges ...func(*service.Group)) (*coderws.Conn, <-chan struct{}) {
	t.Helper()
	done := make(chan struct{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		gid := int64(3131)
		group := &service.Group{ID: gid, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}
		for _, change := range groupChanges {
			change(group)
		}
		c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 99, UserID: 100, GroupID: &gid, User: &service.User{ID: 100, Status: service.StatusActive}, Group: group})
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 1})
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.Group, group))
		service.PassiveQualityBeginTurn(c)
		c.Next()
	})
	router.GET("/v1/responses", func(c *gin.Context) { defer close(done); h.ResponsesWebSocket(c) })
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn, done
}

func TestOpenAIGroupTrafficWSQualityDenialDoesNotForward(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reached := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached <- struct{}{}
		http.Error(w, "must not forward", 500)
	}))
	defer upstream.Close()
	gate := &handlerGroupTrafficGate{}
	h := newGroupTrafficHandler(t, gate, upstream.URL, nil, nil)
	conn, done := startGroupTrafficWS(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte("{\"type\":\"response.create\",\"model\":\"gpt-5.1\",\"input\":\"hello\"}")))
	_, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, accountQualityHeldErrorCode, gjson.GetBytes(payload, "error.code").String())
	require.False(t, gjson.GetBytes(payload, "error.retry_after").Exists())
	_, _, err = conn.Read(ctx)
	require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err))
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("handler did not finish")
	}
	require.Empty(t, reached)
	require.NotEmpty(t, gate.requests())
}

func TestOpenAIGroupTrafficWSTurnsPreserveAdmissionIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for turn := 1; turn <= 2; turn++ {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			_, request, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			model := gjson.GetBytes(request, "model").String()
			response := fmt.Sprintf("{\"type\":\"response.completed\",\"response\":{\"id\":\"resp_%d\",\"model\":%q,\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":1000,\"output_tokens\":10,\"input_tokens_details\":{\"cached_tokens\":800}}}}", turn, model)
			_ = conn.Write(ctx, coderws.MessageText, []byte(response))
			cancel()
		}
	}))
	defer upstream.Close()
	gate := &handlerGroupTrafficGate{allow: true}
	h := newGroupTrafficHandler(t, gate, upstream.URL, nil, nil)
	conn, _ := startGroupTrafficWS(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, model := range []string{"gpt-5.1", "gpt-5.2"} {
		body := fmt.Sprintf("{\"type\":\"response.create\",\"model\":%q,\"input\":\"hello\"}", model)
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(body)))
		_, response, err := conn.Read(ctx)
		require.NoError(t, err)
		require.Equal(t, "response.completed", gjson.GetBytes(response, "type").String())
	}
	admissions := gate.requests()
	require.Len(t, admissions, 2)
	require.NotEqual(t, admissions[0].Key, admissions[1].Key)
	require.Equal(t, "gpt-5.1", admissions[0].Model)
	require.Equal(t, "gpt-5.2", admissions[1].Model)
}
