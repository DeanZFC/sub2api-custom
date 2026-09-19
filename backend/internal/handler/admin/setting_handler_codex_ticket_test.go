package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsCodexTicketProxyWriteReadAndHotReload(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	oldProxy := "http://user:old-secret@old.example.com:8080"
	newProxy := "socks5h://user:new-secret@new.example.com:1080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: oldProxy})
	require.Equal(t, oldProxy, h.settingService.GetOpenAICodexTicketHarvestProxyURL(context.Background()))
	rec := doUpdateSettings(t, h, map[string]any{key: newProxy}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, newProxy, repo.values[key])
	require.Equal(t, newProxy, h.settingService.GetOpenAICodexTicketHarvestProxyURL(context.Background()))
	require.NotContains(t, rec.Body.String(), "new-secret")
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_harvest_proxy_configured":true`)
	// Omission and the masked GET value preserve the real secret.
	for _, body := range []map[string]any{{"site_name": "updated"}, {key: service.MaskProxyURL(newProxy)}} {
		rec = doUpdateSettings(t, h, body, nil)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Equal(t, newProxy, repo.values[key])
	}
	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	require.NotContains(t, get.Body.String(), "new-secret")
	require.Contains(t, get.Body.String(), "new.example.com")
}

func TestSettingsCodexTicketRejectInvalidProxyWithoutLeakingPassword(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: "http://previous.example.com:8080"})
	rec := doUpdateSettings(t, h, map[string]any{key: "ftp://user:invalid-secret@proxy.example.com:21"}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "invalid-secret")
	require.Equal(t, "http://previous.example.com:8080", repo.values[key])
}

func TestSettingsCodexTicketProxyPoolUpdateReorderAndClear(t *testing.T) {
	key := service.SettingKeyOpenAICodexTicketHarvestProxyURL
	first := "http://user:first-secret@first.example:8080"
	second := "socks5h://user:second-secret@second.example:1080"
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{key: first + "\n" + second})
	rec := doUpdateSettings(t, h, map[string]any{key: service.MaskProxyURL(second) + "\r\n\r\n" + service.MaskProxyURL(first)}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, second+"\n"+first, repo.values[key])
	require.Equal(t, []string{second, first}, h.settingService.GetOpenAICodexTicketHarvestProxyPool(context.Background(), "http://fallback.example:8080"))
	require.NotContains(t, rec.Body.String(), "first-secret")
	require.NotContains(t, rec.Body.String(), "second-secret")

	get := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(get)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, get.Code)
	require.NotContains(t, get.Body.String(), "first-secret")
	require.NotContains(t, get.Body.String(), "second-secret")
	require.Contains(t, get.Body.String(), "first.example")
	require.Contains(t, get.Body.String(), "second.example")

	rec = doUpdateSettings(t, h, map[string]any{key: service.MaskProxyURL("http://user:new-secret@changed.example:8080")}, nil)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, second+"\n"+first, repo.values[key])
	require.NotContains(t, rec.Body.String(), "secret")

	rec = doUpdateSettings(t, h, map[string]any{key: ""}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Empty(t, repo.values[key])
	require.Empty(t, h.settingService.GetOpenAICodexTicketHarvestProxyPool(context.Background(), "http://fallback.example:8080"))
	require.Contains(t, rec.Body.String(), `"openai_codex_ticket_harvest_proxy_configured":false`)
}

func TestSettingsCodexTicketOmittedProxyKeepsMissingKey(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})
	rec := doUpdateSettings(t, h, map[string]any{"site_name": "updated"}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotContains(t, repo.values, service.SettingKeyOpenAICodexTicketHarvestProxyURL)
}
