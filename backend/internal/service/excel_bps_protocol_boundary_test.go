package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bpsProtocolSettingsRepo struct {
	SettingRepository
	values map[string]string
	err    error
}

func (r *bpsProtocolSettingsRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, r.err
}

func (r *bpsProtocolSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func TestExcelBPSGlobalSwitchAndCredentialProtocolBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		err         error
		enabled     bool
	}{
		{name: "legacy default", enabled: true},
		{name: "enabled", value: "true", enabled: true},
		{name: "disabled", value: "false"},
		{name: "settings unavailable", err: errors.New("offline")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := NewSettingService(&bpsProtocolSettingsRepo{values: map[string]string{SettingKeyExcelBPSEnabled: tc.value}, err: tc.err}, nil)
			gateway := &OpenAIGatewayService{settingService: settings}
			require.Equal(t, tc.enabled, gateway.excelBPSGloballyEnabled(t.Context()))
			account := excelAccount()
			if tc.enabled {
				require.Error(t, gateway.rejectExcelBPSWebSocket(t.Context(), account, "gpt-6-astra", ""))
			} else {
				require.NoError(t, gateway.rejectExcelBPSWebSocket(t.Context(), account, "gpt-6-astra", ""))
			}
			account.Credentials["client_id"] = openai.ExcelClientID
			require.Error(t, gateway.rejectExcelBPSWebSocket(t.Context(), account, "gpt-6-astra", ""), "Excel credentials never use native WebSocket")
			if tc.enabled {
				require.NoError(t, gateway.validateExcelOAuthRoute(t.Context(), account, "gpt-6-astra"))
			} else {
				require.Error(t, gateway.validateExcelOAuthRoute(t.Context(), account, "gpt-6-astra"))
			}
		})
	}
}

func TestExcelOAuthCannotFallThroughToNativeModelOrImageEndpoints(t *testing.T) {
	account := excelAccount()
	account.Credentials["client_id"] = openai.ExcelClientID
	account.Extra["openai_excel_bps"] = false
	gateway := &OpenAIGatewayService{}
	_, err := gateway.FetchOpenAIModelsList(t.Context(), account)
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	_, err = gateway.FetchCodexModelsManifest(t.Context(), account, "", "")
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	_, err = gateway.ForwardImages(t.Context(), ctx, account, nil, &OpenAIImagesRequest{}, "")
	require.ErrorIs(t, err, errExcelOAuthRouteUnavailable)
}

func TestBPSGlobalDisablePreventsRecoveryAndDiscoveryIO(t *testing.T) {
	repo := &recoveryTestRepo{claim: true}
	upstream := &bpsProbeUpstream{}
	gateway := &OpenAIGatewayService{accountRepo: repo, httpUpstream: upstream, settingService: NewSettingService(&bpsProtocolSettingsRepo{values: map[string]string{SettingKeyExcelBPSEnabled: "false"}}, nil)}
	now := time.Now().UTC()
	account := recoveryTestAccount(now)
	gateway.recoverExcelBPS403Account(t.Context(), account, now)
	require.Zero(t, repo.claims)
	require.Empty(t, upstream.bodies)
	_, err := gateway.FetchExcelBPSModelsList(t.Context(), excelAccount())
	require.Error(t, err)
	require.Empty(t, upstream.bodies)
}
