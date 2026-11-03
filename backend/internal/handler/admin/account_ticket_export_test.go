package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountExportExcludesServerManagedTicketMaterial(t *testing.T) {
	router, svc := setupAccountDataRouter()
	extra := map[string]any{
		"codex_turn_ticket:gpt-6-astra": map[string]any{"state": "private-ticket"},
		"codex_turn_cookies":            "private-cookie",
		"codex_harvest_proxy_url":       "http://user:private-password@proxy.invalid",
		"openai_excel_bps":              true,
	}
	svc.accounts = []service.Account{{ID: 1, Name: "fixture", Extra: extra}}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data?include_proxies=false", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	for _, secret := range []string{"private-ticket", "private-cookie", "private-password"} {
		require.NotContains(t, recorder.Body.String(), secret)
	}
	require.Contains(t, recorder.Body.String(), "openai_excel_bps")
	require.Len(t, extra, 4)
}
