package admin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type availableModelsAdminService struct {
	*stubAdminService
	account service.Account
}

type batchSyncAdminService struct {
	*stubAdminService
	accounts []*service.Account
}

func (s *batchSyncAdminService) GetAccountsByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	byID := make(map[int64]*service.Account, len(s.accounts))
	for _, account := range s.accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	result := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		if account := byID[id]; account != nil {
			result = append(result, account)
		}
	}
	return result, nil
}

func (s *availableModelsAdminService) GetAccount(_ context.Context, id int64) (*service.Account, error) {
	if s.account.ID == id {
		acc := s.account
		return &acc, nil
	}
	return s.stubAdminService.GetAccount(context.Background(), id)
}

func setupAvailableModelsRouter(adminSvc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.GET("/api/v1/admin/accounts/:id/models", handler.GetAvailableModels)
	return router
}

type syncUpstreamHTTPUpstream struct {
	mu        sync.Mutex
	resp      *http.Response
	responses []*http.Response
	byAccount map[int64]*http.Response
	err       error
}

func (u *syncUpstreamHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.err != nil {
		return nil, u.err
	}
	if response, ok := u.byAccount[accountID]; ok {
		return response, nil
	}
	if len(u.responses) > 0 {
		resp := u.responses[0]
		u.responses = u.responses[1:]
		return resp, nil
	}
	return u.resp, nil
}

func (u *syncUpstreamHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

type modelSyncAccountRepo struct {
	service.AccountRepository
	mu      sync.Mutex
	updates map[int64]service.AccountBulkUpdate
}

func (r *modelSyncAccountRepo) BulkUpdate(_ context.Context, ids []int64, updates service.AccountBulkUpdate) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updates == nil {
		r.updates = make(map[int64]service.AccountBulkUpdate)
	}
	for _, id := range ids {
		r.updates[id] = updates
	}
	return int64(len(ids)), nil
}

func (r *modelSyncAccountRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

func setupSyncUpstreamModelsRouter(adminSvc service.AdminService, upstream service.HTTPUpstream, repos ...service.AccountRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var repo service.AccountRepository
	if len(repos) > 0 {
		repo = repos[0]
	}
	accountTestSvc := service.NewAccountTestService(
		repo,
		nil,
		nil,
		nil,
		nil,
		upstream,
		&config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		nil,
	)
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, accountTestSvc, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/accounts/:id/models/sync-upstream", handler.SyncUpstreamModels)
	router.POST("/api/v1/admin/accounts/models/sync-upstream-batch", handler.BatchSyncUpstreamModels)
	router.POST("/api/v1/admin/accounts/models/sync-upstream-preview", handler.SyncUpstreamModelsPreview)
	return router
}

func TestAccountHandlerGetAvailableModels_GrokUsesXAIModels(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       44,
			Name:     "grok-oauth",
			Platform: service.PlatformGrok,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"grok-4.3": "grok-4.3",
				},
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/44/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, "grok-4.3", resp.Data[0].ID)
}

func TestAccountHandlerGetAvailableModels_GrokDefaultsToXAIModelsWithoutMapping(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       45,
			Name:     "grok-oauth-defaults",
			Platform: service.PlatformGrok,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/45/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data)

	var ids []string
	for _, model := range resp.Data {
		id := model.ID
		ids = append(ids, id)
		require.NotContains(t, strings.ToLower(id), "claude")
	}
	require.Contains(t, ids, "grok-4.3")
	require.Contains(t, ids, "grok-build-0.1")
}

func TestAccountHandlerGetAvailableModels_OpenAIOAuthUsesExplicitModelMapping(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       42,
			Name:     "openai-oauth",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5": "gpt-5.1",
				},
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/42/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, "gpt-5", resp.Data[0].ID)
}

func TestAccountHandlerGetAvailableModels_OpenAIOAuthPassthroughFallsBackToDefaults(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       43,
			Name:     "openai-oauth-passthrough",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5": "gpt-5.1",
				},
			},
			Extra: map[string]any{
				"openai_passthrough": true,
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/43/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data)
	require.NotEqual(t, "gpt-5", resp.Data[0].ID)
}

func TestAccountHandlerGetAvailableModels_OpenAIAPIKeyDefaultsToConcreteGPT56Sol(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       46,
			Name:     "openai-apikey",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"api_key": "test-key",
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/46/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data)
	require.Equal(t, "gpt-5.6-sol", resp.Data[0].ID)
}

func TestAccountHandlerGetAvailableModels_OpenAISparkShadowReturnsMappingModels(t *testing.T) {
	parentID := int64(100)
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:              44,
			Name:            "openai-spark-shadow",
			Platform:        service.PlatformOpenAI,
			Type:            service.AccountTypeOAuth,
			Status:          service.StatusActive,
			ParentAccountID: &parentID,
			QuotaDimension:  service.QuotaDimensionSpark,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
				},
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/44/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	ids := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		ids = append(ids, m.ID)
	}
	require.ElementsMatch(t, []string{
		"gpt-5.3-codex-spark",
	}, ids, "影子可用模型由 model_mapping 派生（非写死）")
}

func TestAccountHandlerGetAvailableModels_GeminiGoogleOneUsesConservativeCatalog(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       45,
			Name:     "google-one",
			Platform: service.PlatformGemini,
			Type:     service.AccountTypeOAuth,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"oauth_type": "google_one",
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/45/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	ids := make([]string, 0, len(resp.Data))
	for _, model := range resp.Data {
		ids = append(ids, model.ID)
	}
	require.ElementsMatch(t, []string{"gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-pro"}, ids)
	require.NotContains(t, ids, "gemini-3.5-flash")
	require.NotContains(t, ids, "gemini-2.5-flash-image")
}

func TestAccountHandlerGetAvailableModels_Antigravity(t *testing.T) {
	defaults := antigravity.DefaultModels()
	known := defaults[0]
	custom := antigravity.ClaudeModel{ID: "claude-custom-alias", Type: "model", DisplayName: "claude-custom-alias"}
	cases := []struct {
		name        string
		credentials map[string]any
		want        []antigravity.ClaudeModel
	}{
		{name: "nil credentials", want: defaults},
		{name: "missing mapping", credentials: map[string]any{}, want: defaults},
		{name: "empty JSON mapping", credentials: map[string]any{"model_mapping": map[string]any{}}, want: defaults},
		{name: "empty string mapping", credentials: map[string]any{"model_mapping": map[string]string{}}, want: defaults},
		{name: "blank key", credentials: map[string]any{"model_mapping": map[string]any{" ": "upstream-model"}}, want: defaults},
		{name: "wildcard only falls back to defaults", credentials: map[string]any{"model_mapping": map[string]any{"claude-*": "claude-sonnet-4-6", "gemini-3*": "gemini-3-flash"}}, want: defaults},
		{name: "JSON mapping keys and metadata skips wildcards", credentials: map[string]any{"model_mapping": map[string]any{
			known.ID: known.ID, custom.ID: "upstream-model", "claude-*": "claude-sonnet-4-6",
		}}, want: []antigravity.ClaudeModel{custom, known}},
		{name: "string mapping keys and metadata skips wildcards", credentials: map[string]any{"model_mapping": map[string]string{
			known.ID: known.ID, custom.ID: "upstream-model", "gemini-*": "gemini-3-flash",
		}}, want: []antigravity.ClaudeModel{custom, known}},
		{name: "JSON mapping skips wildcards", credentials: map[string]any{"model_mapping": map[string]any{
			known.ID: known.ID, "claude-*": "upstream-model",
		}}, want: []antigravity.ClaudeModel{known}},
		{name: "string mapping skips wildcards", credentials: map[string]any{"model_mapping": map[string]string{
			known.ID: known.ID, "claude-*": "upstream-model",
		}}, want: []antigravity.ClaudeModel{known}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, accountType := range []string{service.AccountTypeOAuth, service.AccountTypeAPIKey} {
				t.Run(accountType, func(t *testing.T) {
					svc := &availableModelsAdminService{
						stubAdminService: newStubAdminService(),
						account:          service.Account{ID: 51, Platform: service.PlatformAntigravity, Type: accountType, Credentials: tc.credentials},
					}
					rec := httptest.NewRecorder()
					setupAvailableModelsRouter(svc).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/51/models", nil))
					require.Equal(t, http.StatusOK, rec.Code)
					var resp struct {
						Data []antigravity.ClaudeModel `json:"data"`
					}
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
					require.Equal(t, tc.want, resp.Data)
				})
			}
		})
	}
}

func TestAccountHandlerSyncUpstreamModels_ConfigErrorReturnsBadRequest(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       44,
			Name:     "openai-apikey-missing-key",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"base_url": "https://openai.example.com/v1",
			},
		},
	}
	router := setupSyncUpstreamModelsRouter(svc, &syncUpstreamHTTPUpstream{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/44/models/sync-upstream", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "No OpenAI API key is available")
}

func TestAccountHandlerBatchSyncUpstreamModelsReturnsPerAccountResults(t *testing.T) {
	accounts := []*service.Account{
		{
			ID: 48, Name: "first", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Credentials: map[string]any{"api_key": "key", "base_url": "https://provider.example/v1"},
		},
		{
			ID: 49, Name: "second", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Credentials: map[string]any{"api_key": "key", "base_url": "https://provider.example/v1"},
		},
	}
	upstreamBody := `{"models":[{"id":"custom-thinking-model","reasoning":true,"supported_reasoning_levels":["low"],"input_modalities":["text"],"context_window":256000}]}`
	upstream := &syncUpstreamHTTPUpstream{byAccount: map[int64]*http.Response{
		48: {StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))},
		49: {StatusCode: http.StatusBadGateway, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"SECRET_TOKEN must not be returned"}`))},
	}}
	svc := &batchSyncAdminService{stubAdminService: newStubAdminService(), accounts: accounts}
	repo := &modelSyncAccountRepo{}
	router := setupSyncUpstreamModelsRouter(svc, upstream, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/models/sync-upstream-batch", strings.NewReader(`{"account_ids":[49,48,999,48,-1]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Total      int     `json:"total"`
			Success    int     `json:"success"`
			Failed     int     `json:"failed"`
			SuccessIDs []int64 `json:"success_ids"`
			FailedIDs  []int64 `json:"failed_ids"`
			Errors     []struct {
				AccountID int64  `json:"account_id"`
				Error     string `json:"error"`
			} `json:"errors"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 3, resp.Data.Total)
	require.Equal(t, 1, resp.Data.Success)
	require.Equal(t, 2, resp.Data.Failed)
	require.Equal(t, []int64{48}, resp.Data.SuccessIDs)
	require.Equal(t, []int64{49, 999}, resp.Data.FailedIDs)
	require.Len(t, resp.Data.Errors, 2)
	require.Equal(t, int64(49), resp.Data.Errors[0].AccountID)
	require.Contains(t, resp.Data.Errors[0].Error, "HTTP 502")
	require.NotContains(t, rec.Body.String(), "SECRET_TOKEN")
	require.Equal(t, int64(999), resp.Data.Errors[1].AccountID)
	require.Len(t, repo.updates, 1)
	require.Equal(t, map[string]any{"model_mapping": map[string]any{"custom-thinking-model": "custom-thinking-model"}}, repo.updates[48].Credentials)
}

func TestAccountHandlerSyncUpstreamModelsReplacesOnlyWhenRequested(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "capabilities only", true: "replace mapping"}[replace], func(t *testing.T) {
			svc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: service.Account{
				ID: 48, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "key", "base_url": "https://provider.example/v1"},
			}}
			upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"new-model","reasoning":false,"input_modalities":["text"],"context_window":128000}]}`)),
			}}
			repo := &modelSyncAccountRepo{}
			router := setupSyncUpstreamModelsRouter(svc, upstream, repo)
			url := "/api/v1/admin/accounts/48/models/sync-upstream"
			if replace {
				url += "?replace=true"
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, nil))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			if replace {
				require.Equal(t, map[string]any{"model_mapping": map[string]any{"new-model": "new-model"}}, repo.updates[48].Credentials)
			} else {
				require.Empty(t, repo.updates)
			}
		})
	}
}

func TestAccountHandlerSyncUpstreamModelsReturnsCapabilityMetadata(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID: 48, Name: "custom-openai", Platform: service.PlatformOpenAI,
			Type: service.AccountTypeAPIKey, Status: service.StatusActive,
			Credentials: map[string]any{"api_key": "key", "base_url": "https://provider.example/v1"},
		},
	}
	upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"models":[{
			"id":"custom-thinking-model",
			"reasoning":true,
			"default_reasoning_level":"high",
			"supported_reasoning_levels":["low","high"],
			"input_modalities":["text","image"],
			"context_window":256000
		}]}`)),
	}}
	router := setupSyncUpstreamModelsRouter(svc, upstream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/48/models/sync-upstream", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data service.UpstreamModelCatalog `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, []string{"custom-thinking-model"}, resp.Data.Models)
	metadata := resp.Data.Metadata["custom-thinking-model"]
	require.NotNil(t, metadata.Reasoning)
	require.True(t, *metadata.Reasoning)
	require.Equal(t, []string{"low", "high"}, metadata.SupportedReasoningLevels)
	require.Equal(t, []string{"text", "image"}, metadata.InputModalities)
}

// Scenario: 创建账号 preview 将具体 mapping 传给 404/405 配置回退。
func TestAccountHandlerSyncUpstreamModelsPreviewUsesProvidedModelMapping(t *testing.T) {
	upstream := &syncUpstreamHTTPUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"not found"}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"configured-provider": {
					"api": "https://provider.example/v1",
					"models": {
						"glm-5.3": {
							"id": "glm-5.3",
							"reasoning": true,
							"reasoning_options": [{"type":"effort","values":["low","high"]}],
							"modalities": {"input":["text"],"output":["text"]},
							"limit": {"context":1000000,"output":131072}
						}
					}
				}
			}`)),
		},
	}}
	router := setupSyncUpstreamModelsRouter(newStubAdminService(), upstream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/models/sync-upstream-preview",
		strings.NewReader(`{
			"platform":"openai",
			"type":"apikey",
			"base_url":"https://provider.example/v1",
			"api_key":"key",
			"model_mapping":{"public-glm":"glm-5.3"}
		}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data service.UpstreamModelCatalog `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, []string{"glm-5.3"}, resp.Data.Models)
	require.Equal(t, []string{"low", "high"}, resp.Data.Metadata["glm-5.3"].SupportedReasoningLevels)
}

func TestAccountHandlerSyncUpstreamModels_UpstreamErrorDoesNotExposeBody(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       45,
			Name:     "openai-apikey-upstream-error",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"api_key":  "openai-key",
				"base_url": "https://openai.example.com/v1",
			},
		},
	}
	upstream := &syncUpstreamHTTPUpstream{resp: &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":"SECRET_TOKEN should not be exposed"}`)),
	}}
	router := setupSyncUpstreamModelsRouter(svc, upstream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/45/models/sync-upstream", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "Upstream model list request failed with HTTP 502")
	require.NotContains(t, rec.Body.String(), "SECRET_TOKEN")
}

// Scenario: 能力补全失败显示部分成功。
func TestAccountHandlerSyncUpstreamModels_MetadataEnrichmentFailureReturnsWarning(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       46,
			Name:     "opencode-id-only-model-list",
			Platform: service.PlatformOpenAI,
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"api_key":  "opencode-key",
				"base_url": "https://opencode.ai/zen/v1",
			},
		},
	}
	upstream := &syncUpstreamHTTPUpstream{responses: []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"x-preview-f-free"}]}`)),
		},
		{
			StatusCode: http.StatusBadGateway,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"registry unavailable"}`)),
		},
	}}
	router := setupSyncUpstreamModelsRouter(svc, upstream)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/46/models/sync-upstream", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Data struct {
			Models   []string `json:"models"`
			Warnings []struct {
				Code string `json:"code"`
			} `json:"warnings"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, []string{"x-preview-f-free"}, resp.Data.Models)
	require.Len(t, resp.Data.Warnings, 1)
	require.Equal(t, "upstream_model_metadata_incomplete", resp.Data.Warnings[0].Code)
}

func TestAccountHandlerGetAvailableModels_TypeSafeOnlyReturnsJev(t *testing.T) {
	for _, credentials := range []map[string]any{
		{"api_key": "ts-secret"},
		{"api_key": "ts-secret", "model_mapping": map[string]any{"jev-latest": "jev-latest"}},
	} {
		svc := &availableModelsAdminService{
			stubAdminService: newStubAdminService(),
			account: service.Account{
				ID:          46,
				Name:        "typesafe",
				Platform:    service.PlatformTypeSafe,
				Type:        service.AccountTypeAPIKey,
				Status:      service.StatusActive,
				Credentials: credentials,
			},
		}
		router := setupAvailableModelsRouter(svc)

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/46/models", nil))
		require.Equal(t, http.StatusOK, rec.Code)

		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp.Data, 1)
		require.Equal(t, "jev-latest", resp.Data[0].ID)
	}
}
