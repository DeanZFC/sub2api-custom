//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type proxyModeOpenAIOAuth struct {
	openAIOAuthOperations
	calls    int
	proxyID  *int64
	proxyURL string
}

func (s *proxyModeOpenAIOAuth) ExchangeCode(_ context.Context, input *service.OpenAIExchangeCodeInput) (*service.OpenAITokenInfo, error) {
	s.calls++
	s.proxyID = input.ProxyID
	return &service.OpenAITokenInfo{AccessToken: "synthetic-token"}, nil
}

func (s *proxyModeOpenAIOAuth) ValidateCodexPersonalAccessToken(_ context.Context, _, proxyURL string) (*service.OpenAITokenInfo, error) {
	s.calls++
	s.proxyURL = proxyURL
	return &service.OpenAITokenInfo{AccessToken: "at-synthetic-token"}, nil
}

func (s *proxyModeOpenAIOAuth) BuildAccountCredentials(info *service.OpenAITokenInfo) map[string]any {
	return map[string]any{"access_token": info.AccessToken}
}

func proxyModeRequest(t *testing.T, handler gin.HandlerFunc, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/create", handler)
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/create", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestOAuthProxyModeOpenAICreateForwardsSelection(t *testing.T) {
	for _, endpoint := range []string{"oauth", "pat"} {
		for _, mode := range []string{"", "direct", "random", "least_used", "ovh_ipv6", "specified"} {
			t.Run(endpoint+"/"+mode, func(t *testing.T) {
				admin := newStubAdminService()
				admin.proxies = []service.Proxy{{ID: 11, Protocol: "http", Host: "127.0.0.1", Port: 1080, Status: service.StatusActive}}
				oauth := &proxyModeOpenAIOAuth{}
				h := &OpenAIOAuthHandler{openaiOAuthService: oauth, adminService: admin}
				body := map[string]any{"proxy_mode": mode, "access_token": "at-synthetic", "session_id": "session", "code": "code", "state": "state"}
				if mode == "specified" {
					body["proxy_id"] = 11
				}
				if mode == "" && endpoint == "pat" {
					body["proxy_ids"] = []int64{11, 12}
				}
				handler := h.CreateAccountFromOAuth
				if endpoint == "pat" {
					handler = h.CreateAccountFromCodexPAT
				}
				rec := proxyModeRequest(t, handler, body)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Equal(t, 1, oauth.calls)
				require.Len(t, admin.createdAccounts, 1)
				input := admin.createdAccounts[0]
				require.Equal(t, mode, input.ProxyMode)
				if mode == "specified" {
					require.Equal(t, int64(11), *input.ProxyID)
					if endpoint == "oauth" {
						require.Equal(t, int64(11), *oauth.proxyID)
					} else {
						require.Equal(t, "http://127.0.0.1:1080", oauth.proxyURL)
					}
				} else {
					require.Nil(t, input.ProxyID)
					require.Nil(t, oauth.proxyID, "automatic account selection must not change token exchange")
					require.Empty(t, oauth.proxyURL, "automatic account selection must not change PAT validation")
				}
				if mode == "" && endpoint == "pat" {
					require.Equal(t, []int64{11, 12}, input.ProxyIDs)
				}
			})
		}
	}
}

func TestOAuthProxyModeOpenAIRejectsBeforeTokenRequest(t *testing.T) {
	for _, endpoint := range []string{"oauth", "pat"} {
		for _, tc := range []struct {
			name   string
			fields map[string]any
			reason string
		}{
			{"unknown", map[string]any{"proxy_mode": "typo"}, "PROXY_SELECTION_INVALID"},
			{"missing_specified", map[string]any{"proxy_mode": "specified"}, "PROXY_SELECTION_INVALID"},
			{"auto_conflict", map[string]any{"proxy_mode": "ovh_ipv6", "proxy_id": 11}, "PROXY_SELECTION_CONFLICT"},
			{"direct_conflict", map[string]any{"proxy_mode": "direct", "proxy_id": 11}, "PROXY_SELECTION_CONFLICT"},
			{"negative_id", map[string]any{"proxy_id": -1}, "PROXY_SELECTION_INVALID"},
			{"pool_conflict", map[string]any{"proxy_mode": "random", "proxy_ids": []int64{11}}, "PROXY_SELECTION_CONFLICT"},
		} {
			if endpoint == "oauth" && tc.name == "pool_conflict" {
				continue // This existing endpoint has no proxy pool field.
			}
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				admin := newStubAdminService()
				oauth := &proxyModeOpenAIOAuth{}
				h := &OpenAIOAuthHandler{openaiOAuthService: oauth, adminService: admin}
				body := map[string]any{"access_token": "at-synthetic", "session_id": "session", "code": "code", "state": "state"}
				for key, value := range tc.fields {
					body[key] = value
				}
				handler := h.CreateAccountFromOAuth
				if endpoint == "pat" {
					handler = h.CreateAccountFromCodexPAT
				}
				rec := proxyModeRequest(t, handler, body)
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), tc.reason)
				require.Zero(t, oauth.calls)
				require.Empty(t, admin.createdAccounts)
			})
		}
	}
}

type proxyModeGrokOAuth struct {
	service.GrokOAuthClient
	mu      sync.Mutex
	proxies []string
}

func (s *proxyModeGrokOAuth) ConvertSSOToBuild(_ context.Context, _, proxyURL string) (*xai.TokenResponse, error) {
	s.mu.Lock()
	s.proxies = append(s.proxies, proxyURL)
	s.mu.Unlock()
	return &xai.TokenResponse{AccessToken: "synthetic-token", RefreshToken: "synthetic-refresh", ExpiresIn: 3600}, nil
}

func TestOAuthProxyModeGrokSSOForwardsOrdinarySelection(t *testing.T) {
	for _, mode := range []string{"", "direct", "random", "least_used"} {
		t.Run(mode, func(t *testing.T) {
			admin := newStubAdminService()
			client := &proxyModeGrokOAuth{}
			oauth := service.NewGrokOAuthService(nil, client)
			defer oauth.Stop()
			h := &GrokOAuthHandler{grokOAuthService: oauth, adminService: admin}
			body := map[string]any{"proxy_mode": mode, "sso_tokens": []string{"synthetic-one", "synthetic-two"}}
			if mode == "" {
				body["proxy_ids"] = []int64{11, 12}
			}
			rec := proxyModeRequest(t, h.CreateAccountsFromSSO, body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Len(t, admin.createdAccounts, 2)
			for _, input := range admin.createdAccounts {
				require.Equal(t, mode, input.ProxyMode)
				require.Nil(t, input.ProxyID)
				if mode == "" {
					require.Equal(t, []int64{11, 12}, input.ProxyIDs)
				}
			}
			require.Equal(t, []string{"", ""}, client.proxies, "account selection must not change SSO conversion egress")
		})
	}
}

func TestOAuthProxyModeGrokSSORejectsBeforeTokenRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		reason string
	}{
		{"managed", map[string]any{"proxy_mode": "ovh_ipv6"}, "OVH_IPV6_INELIGIBLE"},
		{"unknown", map[string]any{"proxy_mode": "typo"}, "PROXY_SELECTION_INVALID"},
		{"specified", map[string]any{"proxy_mode": "specified"}, "PROXY_SELECTION_INVALID"},
		{"conflict", map[string]any{"proxy_mode": "least_used", "proxy_id": 11}, "PROXY_SELECTION_CONFLICT"},
		{"pool_conflict", map[string]any{"proxy_mode": "random", "proxy_ids": []int64{11}}, "PROXY_SELECTION_CONFLICT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admin := newStubAdminService()
			client := &proxyModeGrokOAuth{}
			oauth := service.NewGrokOAuthService(nil, client)
			defer oauth.Stop()
			h := &GrokOAuthHandler{grokOAuthService: oauth, adminService: admin}
			body := map[string]any{"sso_token": "synthetic"}
			for key, value := range tc.fields {
				body[key] = value
			}
			rec := proxyModeRequest(t, h.CreateAccountsFromSSO, body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.reason)
			require.Empty(t, client.proxies)
			require.Empty(t, admin.createdAccounts)
		})
	}
}
