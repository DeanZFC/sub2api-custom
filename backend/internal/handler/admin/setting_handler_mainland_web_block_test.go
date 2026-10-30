package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMainlandWebBlockAdminSettingGetSaveAndOmission(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	get := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
		h.GetSettings(c)
		return rec
	}
	rec := get()
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"mainland_web_block_enabled":false`)
	for _, enabled := range []bool{true, false} {
		saved := doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": enabled}, nil)
		require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
		want := "false"
		if enabled {
			want = "true"
		}
		require.Equal(t, want, repo.values[service.SettingKeyMainlandWebBlockEnabled])
		require.Contains(t, saved.Body.String(), `"mainland_web_block_enabled":`+want)
		require.Contains(t, get().Body.String(), `"mainland_web_block_enabled":`+want)
	}
	require.Equal(t, http.StatusOK, doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": true}, nil).Code)
	require.Equal(t, http.StatusOK, doUpdateSettings(t, h, map[string]any{"registration_enabled": true}, nil).Code)
	require.Equal(t, "true", repo.values[service.SettingKeyMainlandWebBlockEnabled], "older clients do not reset the switch")
	require.NotContains(t, repo.lastUpdates, service.SettingKeyMainlandWebBlockEnabled, "omitted field is not rewritten")
}

func TestMainlandWebBlockAdminSaveChangesExistingMiddlewareImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyMainlandWebBlockEnabled: "false"}}
	cfg := &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}}
	svc := service.NewSettingService(repo, cfg)
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	block, err := middleware.NewMainlandWebBlock(cfg.Security.MainlandWebBlock, svc.IsMainlandWebBlockEnabled)
	require.NoError(t, err)
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(block)
	r.GET("/login", func(c *gin.Context) { c.String(http.StatusOK, "normal login") })
	request := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.RemoteAddr = "223.5.5.5:12345"
		r.ServeHTTP(rec, req)
		return rec
	}
	require.Equal(t, http.StatusOK, request().Code)
	require.Equal(t, http.StatusOK, doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": true}, nil).Code)
	rec := request()
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "当前地区暂不支持访问")
	require.Equal(t, http.StatusOK, doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": false}, nil).Code)
	require.Equal(t, http.StatusOK, request().Code, "same router and middleware resume immediately without restart")
}

func TestMainlandWebBlockAdminValidationAndAudit(t *testing.T) {
	repo := &settingHandlerRepoStub{values: map[string]string{}}
	cfg := &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}}
	cfg.Security.MainlandWebBlock.CIDRFile = filepath.Join(t.TempDir(), "missing.zone")
	h := NewSettingHandler(service.NewSettingService(repo, cfg), nil, nil, nil, nil, nil, nil)
	rec := doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": true}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "INVALID_MAINLAND_WEB_BLOCK_DATA")
	require.NotContains(t, repo.values, service.SettingKeyMainlandWebBlockEnabled)
	require.Equal(t, http.StatusOK, doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": false}, nil).Code)
	require.Contains(t, diffSettings(&service.SystemSettings{}, &service.SystemSettings{MainlandWebBlockEnabled: true}, nil, nil, UpdateSettingsRequest{}), service.SettingKeyMainlandWebBlockEnabled)
	require.NotContains(t, diffSettings(&service.SystemSettings{}, &service.SystemSettings{}, nil, nil, UpdateSettingsRequest{}), service.SettingKeyMainlandWebBlockEnabled)
}

type mainlandAdminFailingRepo struct {
	*settingHandlerRepoStub
}

func (r *mainlandAdminFailingRepo) SetMultiple(ctx context.Context, values map[string]string) error {
	if _, writesSwitch := values[service.SettingKeyMainlandWebBlockEnabled]; writesSwitch {
		return errors.New("database switch write failed")
	}
	return r.settingHandlerRepoStub.SetMultiple(ctx, values)
}

func TestMainlandWebBlockAdminFailedSaveDoesNotChangeRuntime(t *testing.T) {
	repo := &mainlandAdminFailingRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{service.SettingKeyMainlandWebBlockEnabled: "false"}}}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	require.False(t, svc.IsMainlandWebBlockEnabled(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": true}, nil)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "false", repo.values[service.SettingKeyMainlandWebBlockEnabled])
	require.False(t, svc.IsMainlandWebBlockEnabled(context.Background()))
}

func TestMainlandWebBlockAdminPaymentFailureDoesNotEnable(t *testing.T) {
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyMainlandWebBlockEnabled: "false"}}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	paymentCfg := service.NewPaymentConfigService(nil, repo, nil)
	h := NewSettingHandler(svc, nil, nil, nil, paymentCfg, nil, nil)
	require.False(t, svc.IsMainlandWebBlockEnabled(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": true, "payment_balance_recharge_multiplier": -1}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "INVALID_BALANCE_RECHARGE_MULTIPLIER")
	require.Equal(t, "false", repo.values[service.SettingKeyMainlandWebBlockEnabled])
	require.False(t, svc.IsMainlandWebBlockEnabled(context.Background()), "a failed payment configuration save must not activate the region filter")
}

type mainlandAdminReadAfterSaveFailureRepo struct {
	*settingHandlerRepoStub
	reads int
}

func (r *mainlandAdminReadAfterSaveFailureRepo) GetAll(ctx context.Context) (map[string]string, error) {
	r.reads++
	if r.reads > 1 {
		return nil, errors.New("post-save settings reload failed")
	}
	return r.settingHandlerRepoStub.GetAll(ctx)
}

func TestMainlandWebBlockAdminFinalReloadFailureDoesNotEnable(t *testing.T) {
	repo := &mainlandAdminReadAfterSaveFailureRepo{settingHandlerRepoStub: &settingHandlerRepoStub{values: map[string]string{service.SettingKeyMainlandWebBlockEnabled: "false"}}}
	svc := service.NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	require.False(t, svc.IsMainlandWebBlockEnabled(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{"mainland_web_block_enabled": true}, nil)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Equal(t, "false", repo.values[service.SettingKeyMainlandWebBlockEnabled])
	require.False(t, svc.IsMainlandWebBlockEnabled(context.Background()))
}
