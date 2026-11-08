//go:build unit

package routes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type keyTestRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *keyTestRepo) GetByIDForAuth(_ context.Context, id int64) (*service.APIKey, error) {
	if id != r.key.ID {
		return nil, service.ErrAPIKeyNotFound
	}
	clone := *r.key
	return &clone, nil
}
func (r *keyTestRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

type keyTestBalance struct{ service.BillingCache }

func (*keyTestBalance) GetUserBalance(context.Context, int64) (float64, error)  { return 100, nil }
func (*keyTestBalance) DeductUserBalance(context.Context, int64, float64) error { return nil }
func (*keyTestBalance) InvalidateUserBalance(context.Context, int64) error      { return nil }

type keyTestBilling struct {
	service.UsageBillingRepository
	mu       sync.Mutex
	commands []*service.UsageBillingCommand
}

func (r *keyTestBilling) Apply(_ context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, cmd)
	return &service.UsageBillingApplyResult{Applied: true}, nil
}

type keyTestAccounts struct{ compatibleImagesAccounts }

func (r keyTestAccounts) ListSchedulableByGroupID(context.Context, int64) ([]service.Account, error) {
	return r.accounts, nil
}

type keyTestUpstream struct {
	service.HTTPUpstream
	calls int
	body  []byte
}

func (u *keyTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.body, _ = io.ReadAll(req.Body)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"owned-key-test"}}, Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl-test","object":"chat.completion","model":"gpt-5.4-mini","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`))}, nil
}

func newKeyTestRouter(t *testing.T, key *service.APIKey, resolvers ...*service.CompositeRouteResolver) (*gin.Engine, *keyTestUpstream, *keyTestBilling, *compatibleImagesUsage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	keys := service.NewAPIKeyService(&keyTestRepo{key: key}, nil, nil, nil, nil, nil, cfg)
	accounts := keyTestAccounts{compatibleImagesAccounts{accounts: []service.Account{{
		ID: 99, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Extra:       map[string]any{"openai_responses_supported": false},
		Credentials: map[string]any{"api_key": "upstream-test-only", "base_url": "https://upstream.example/v1", "model_mapping": map[string]any{"gpt-5.4-mini": "gpt-5.4-mini"}},
	}}}}
	upstream, billing, usage := &keyTestUpstream{}, &keyTestBilling{}, &compatibleImagesUsage{}
	billingCache := service.NewBillingCacheService(&keyTestBalance{}, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(accounts, usage, billing, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billingCache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	var resolver *service.CompositeRouteResolver
	if len(resolvers) > 0 {
		resolver = resolvers[0]
	}
	modelGateway := service.NewGatewayService(accounts, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := &handler.Handlers{
		Gateway:       handler.NewGatewayHandler(modelGateway, gateway, nil, nil, nil, nil, nil, nil, keys, nil, nil, nil, nil, cfg, nil),
		OpenAIGateway: handler.NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, keys, nil, nil, nil, nil, cfg),
	}
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		switch c.GetHeader("Authorization") {
		case "Bearer owner":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
		case "Bearer other":
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID + 1})
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	router := gin.New()
	RegisterAPIKeyTestRoutes(router, h, jwt, middleware.PanelAPIKeyAuth(keys, nil, cfg), nil, nil, resolver, nil)
	return router, upstream, billing, usage
}

func fixtureTestKey() *service.APIKey {
	group := &service.Group{ID: 5, Status: service.StatusActive, Hydrated: true, Platform: service.PlatformOpenAI, RateMultiplier: 1.5}
	return &service.APIKey{ID: 7, UserID: 3, Status: service.StatusActive, Quota: 10, User: &service.User{ID: 3, Status: service.StatusActive, Balance: 100}, GroupID: &group.ID, Group: group}
}

func TestAPIKeyTestChargesSelectedKeyThroughGateway(t *testing.T) {
	key := fixtureTestKey()
	router, upstream, billing, usage := newKeyTestRouter(t, key)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/key-tests/7", strings.NewReader(`{"model":"gpt-5.4-mini"}`))
	req.Header.Set("Authorization", "Bearer owner")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, 1, upstream.calls)
	require.Contains(t, rec.Body.String(), "OK")
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(upstream.body, &body))
	require.Equal(t, float64(256), body["max_tokens"])
	require.Equal(t, false, body["stream"])
	require.Equal(t, []any{map[string]any{"role": "user", "content": "hi"}}, body["messages"])
	billing.mu.Lock()
	defer billing.mu.Unlock()
	require.Len(t, billing.commands, 1, "must settle through production billing exactly once")
	charge := billing.commands[0]
	require.Equal(t, key.ID, charge.APIKeyID)
	require.Equal(t, key.UserID, charge.UserID)
	require.Greater(t, charge.APIKeyQuotaCost, 0.0)
	require.Equal(t, charge.APIKeyQuotaCost, charge.BalanceCost)
	require.Equal(t, 10, charge.InputTokens)
	require.Equal(t, 2, charge.OutputTokens)
	require.Len(t, usage.logs, 1)
	require.Equal(t, key.ID, usage.logs[0].APIKeyID)
	require.NotNil(t, usage.logs[0].InboundEndpoint)
	require.Equal(t, "/v1/chat/completions", *usage.logs[0].InboundEndpoint)
}

func TestAPIKeyTestRejectsUnauthorizedAndInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, auth, body string
		alter            func(*service.APIKey)
		want             int
	}{
		{"no login", "", `{"model":"gpt-5.4-mini"}`, nil, 401},
		{"other owner", "other", `{"model":"gpt-5.4-mini"}`, nil, 404},
		{"disabled", "owner", `{"model":"gpt-5.4-mini"}`, func(k *service.APIKey) { k.Status = "inactive" }, 403},
		{"quota exhausted", "owner", `{"model":"gpt-5.4-mini"}`, func(k *service.APIKey) { k.QuotaUsed = k.Quota }, 429},
		{"expired", "owner", `{"model":"gpt-5.4-mini"}`, func(k *service.APIKey) { past := time.Now().Add(-time.Hour); k.ExpiresAt = &past }, 403},
		{"IP denied", "owner", `{"model":"gpt-5.4-mini"}`, func(k *service.APIKey) { k.IPWhitelist = []string{"192.0.2.1"} }, 403},
		{"model denied", "owner", `{"model":"gpt-5.4-mini"}`, func(k *service.APIKey) {
			k.Group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"allowed-only"}}
		}, 404},
		{"token override", "owner", `{"model":"gpt-5.4-mini","max_tokens":99999}`, nil, 400},
		{"duplicate model", "owner", `{"model":"gpt-5.4-mini","model":"other"}`, nil, 400},
		{"missing model", "owner", `{}`, nil, 400},
		{"custom prompt", "owner", `{"model":"gpt-5.4-mini","prompt":"custom"}`, nil, 400},
		{"too large", "owner", "{\"model\":\"gpt-5.4-mini\",\"prompt\":\"" + strings.Repeat("x", 17000) + "\"}", nil, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := fixtureTestKey()
			if tc.alter != nil {
				tc.alter(key)
			}
			router, upstream, billing, _ := newKeyTestRouter(t, key)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/key-tests/7", strings.NewReader(tc.body))
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("Content-Type", "application/json")
			if tc.auth != "" {
				req.Header.Set("Authorization", "Bearer "+tc.auth)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
			require.Zero(t, upstream.calls)
			require.Empty(t, billing.commands)
		})
	}
}

func TestAPIKeyTestModelDiscoveryDoesNotBill(t *testing.T) {
	key := fixtureTestKey()
	router, upstream, billing, usage := newKeyTestRouter(t, key)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/key-tests/7/models", nil)
	req.Header.Set("Authorization", "Bearer owner")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "gpt-5.4-mini")
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Zero(t, upstream.calls)
	require.Empty(t, billing.commands)
	require.Empty(t, usage.logs)
}

func TestAPIKeyTestCompositeRoutingPreservesKeyBilling(t *testing.T) {
	key := fixtureTestKey()
	key.Group.Platform = service.PlatformComposite
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{routes: []service.CompositeModelRoute{{
		ID: 1, GroupID: key.Group.ID, PublicModel: "public-model", MatchType: service.CompositeRouteMatchExact,
		TargetPlatform: service.PlatformOpenAI, UpstreamModel: "gpt-5.4-mini",
		Endpoint: service.CompositeRouteEndpointChatCompletions, Enabled: true,
	}}})
	router, upstream, billing, _ := newKeyTestRouter(t, key, resolver)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/key-tests/7", strings.NewReader(`{"model":"public-model"}`))
	req.Header.Set("Authorization", "Bearer owner")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, upstream.calls)
	var body map[string]any
	require.NoError(t, json.Unmarshal(upstream.body, &body))
	require.Equal(t, "gpt-5.4-mini", body["model"])
	require.Len(t, billing.commands, 1)
	require.Equal(t, key.ID, billing.commands[0].APIKeyID)
	require.Greater(t, billing.commands[0].APIKeyQuotaCost, 0.0)
}
