package repository

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type bpsRetrySettingsRepo struct {
	service.SettingRepository
}

func (*bpsRetrySettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyUpstreamErrorRetry {
		return `{"enabled":true,"max_retries":1,"delay_ms":100,"errors":"503"}`, nil
	}
	return "", service.ErrSettingNotFound
}

func TestBPSDoesNotReplayConfiguredHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile service.HTTPUpstreamProfile
		calls   int64
	}{
		{name: "default_uses_configured_retry", profile: service.HTTPUpstreamProfileDefault, calls: 2},
		{name: "bps_owns_retry_policy", profile: service.HTTPUpstreamProfileExcelBPS, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"message":"busy"}}`)
			}))
			t.Cleanup(server.Close)
			settings := service.NewSettingService(&bpsRetrySettingsRepo{}, &config.Config{})
			ctx := service.WithUpstreamErrorRetry(t.Context(), settings)
			ctx = service.WithHTTPUpstreamProfile(ctx, tc.profile)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(`{"model":"gpt-5"}`))
			require.NoError(t, err)
			resp, err := NewHTTPUpstream(nil).Do(req, "", 1, 1)
			require.NoError(t, err)
			require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, tc.calls, calls.Load())
		})
	}
}
