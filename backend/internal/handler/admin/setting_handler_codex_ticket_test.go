package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingsCodexTicketLegacyProxySettingsAreIgnored(t *testing.T) {
	const proxyKey = "openai_codex_ticket_harvest_proxy_url"
	const modeKey = "openai_codex_ticket_harvest_proxy_mode"
	const legacyProxy = "socks5h://legacy-user:legacy-secret@legacy-proxy.example:1080"
	for _, stored := range []bool{false, true} {
		t.Run(map[bool]string{false: "without saved pool", true: "with saved pool"}[stored], func(t *testing.T) {
			values := map[string]string{}
			if stored {
				values[proxyKey] = legacyProxy
				values[modeKey] = "pool"
			}
			h, repo := newStepUpSwitchTestHandler(t, values)
			assertRemovedFields := func(rec *httptest.ResponseRecorder) {
				t.Helper()
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var envelope struct {
					Data map[string]json.RawMessage `json:"data"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
				for _, key := range []string{proxyKey, modeKey, "openai_codex_ticket_harvest_proxy_configured", "openai_codex_ticket_harvest_proxy_count"} {
					require.NotContains(t, envelope.Data, key)
				}
				require.NotContains(t, rec.Body.String(), "legacy-secret")
				require.NotContains(t, rec.Body.String(), "legacy-proxy.example")
				require.NotContains(t, rec.Body.String(), "submitted-secret")
			}
			get := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(get)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
			h.GetSettings(c)
			assertRemovedFields(get)

			// Stale browser forms cannot restore the removed route or prevent the
			// remaining gateway switch from being saved, even with invalid values.
			for _, enabled := range []bool{true, false} {
				rec := doUpdateSettings(t, h, map[string]any{
					proxyKey: "ftp://submitted:submitted-secret@invalid.example:21",
					modeKey:  "removed-mode",
					service.SettingKeyOpenAICodexTicketEnabled: enabled,
				}, nil)
				assertRemovedFields(rec)
				require.Equal(t, enabled, h.settingService.GetOpenAICodexTicketEnabled(context.Background(), !enabled))
				if stored {
					require.Equal(t, legacyProxy, repo.values[proxyKey])
					require.Equal(t, "pool", repo.values[modeKey])
				} else {
					require.NotContains(t, repo.values, proxyKey)
					require.NotContains(t, repo.values, modeKey)
				}
			}
		})
	}
}
