package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type sharedOAuthProxyRepo struct {
	service.ProxyRepository
	proxy   *service.Proxy
	lookups []int64
	deleted []int64
}

func (r *sharedOAuthProxyRepo) Create(_ context.Context, proxy *service.Proxy) error {
	proxy.ID = 77
	r.proxy = proxy
	return nil
}
func (r *sharedOAuthProxyRepo) GetByID(_ context.Context, id int64) (*service.Proxy, error) {
	r.lookups = append(r.lookups, id)
	return r.proxy, nil
}
func (r *sharedOAuthProxyRepo) Delete(_ context.Context, id int64) error {
	r.deleted = append(r.deleted, id)
	return nil
}

type sharedOAuthClient struct {
	service.OpenAIOAuthClient
	proxyURL  string
	exchanges int
}

func (client *sharedOAuthClient) ExchangeCode(_ context.Context, _, _, _, proxyURL, _ string) (*openai.TokenResponse, error) {
	client.proxyURL = proxyURL
	client.exchanges++
	return &openai.TokenResponse{AccessToken: "test-access", RefreshToken: "test-refresh", ExpiresIn: 3600}, nil
}
func sharedAuthRequest(h *SharedAccountPoolHandler, owner int64, path string, payload map[string]any) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if owner > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: owner})
			c.Set(string(middleware.ContextKeyUserRole), "user")
		}
	})
	router.POST("/oauth/:platform/:action", h.Authorize)
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
func TestSharedOAuthSessionOwnershipAndProxy(t *testing.T) {
	proxies := &sharedOAuthProxyRepo{}
	client := &sharedOAuthClient{}
	oauth := service.NewOpenAIOAuthService(proxies, client)
	defer oauth.Stop()
	h := &SharedAccountPoolHandler{openaiOAuth: oauth, uploader: service.NewSharedAccountUploadService(nil, nil, nil, proxies)}
	result := sharedAuthRequest(h, 42, "/oauth/openai/generate-auth-url", map[string]any{"proxy_id": 999, "proxy_url": "socks5://user:pass@example.com:1080"})
	require.Equal(t, 200, result.Code, result.Body.String())
	var response struct {
		Data struct {
			SessionID string `json:"session_id"`
			AuthURL   string `json:"auth_url"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
	require.NotEmpty(t, response.Data.SessionID)
	require.Equal(t, int64(42), *proxies.proxy.OwnerUserID)
	require.Equal(t, []int64{77}, proxies.lookups, "must not look up caller-supplied admin proxy IDs")
	require.Equal(t, []int64{77}, proxies.deleted, "authorization proxy is temporary")
	authURL, err := url.Parse(response.Data.AuthURL)
	require.NoError(t, err)
	payload := map[string]any{"session_id": response.Data.SessionID, "code": "test-code", "state": authURL.Query().Get("state")}
	result = sharedAuthRequest(h, 43, "/oauth/openai/exchange-code", payload)
	require.Equal(t, 400, result.Code)
	result = sharedAuthRequest(h, 42, "/oauth/antigravity/exchange-code", payload)
	require.Equal(t, 400, result.Code)
	require.Zero(t, client.exchanges)
	badState := map[string]any{"session_id": response.Data.SessionID, "code": "test-code", "state": "wrong"}
	result = sharedAuthRequest(h, 42, "/oauth/openai/exchange-code", badState)
	require.Equal(t, 400, result.Code)
	require.Zero(t, client.exchanges)
	result = sharedAuthRequest(h, 42, "/oauth/openai/exchange-code", payload)
	require.Equal(t, 200, result.Code, result.Body.String())
	require.Equal(t, 1, client.exchanges)
	require.Equal(t, "socks5h://user:pass@example.com:1080", client.proxyURL)
	require.Contains(t, result.Body.String(), "test-access")
	result = sharedAuthRequest(h, 42, "/oauth/openai/exchange-code", payload)
	require.Equal(t, 400, result.Code)
	require.Equal(t, 1, client.exchanges, "session cannot be replayed")
}
func TestSharedOAuthRejectsUnauthenticatedUnsafeAndAdminOperations(t *testing.T) {
	h := &SharedAccountPoolHandler{uploader: service.NewSharedAccountUploadService(nil, nil, nil, nil)}
	require.Equal(t, 401, sharedAuthRequest(h, 0, "/oauth/openai/generate-auth-url", map[string]any{}).Code)
	for _, action := range []string{"create-from-oauth", "create-from-codex-pat", "refresh-account", "delete"} {
		require.Equal(t, 404, sharedAuthRequest(h, 42, "/oauth/openai/"+action, map[string]any{}).Code)
	}
	for _, proxy := range []string{"http://127.0.0.1:8080", "http://169.254.169.254:80", "http://localhost:80"} {
		require.Equal(t, 400, sharedAuthRequest(h, 42, "/oauth/openai/generate-auth-url", map[string]any{"proxy_url": proxy}).Code)
	}
}
