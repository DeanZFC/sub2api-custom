//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexRetrySettingRepoStub struct {
	settingHandlerRepoStub
}

func (s *codexRetrySettingRepoStub) Set(ctx context.Context, key, value string) error {
	return s.SetMultiple(ctx, map[string]string{key: value})
}

func TestCodexPreOutputRetrySettingsRoundTrip(t *testing.T) {
	repo := &codexRetrySettingRepoStub{}
	svc := service.NewSettingService(repo, &config.Config{})
	h := NewSettingHandler(svc, nil, nil, nil, nil, nil, nil)
	settings := service.DefaultCodexPreOutputRetrySettings()
	settings.Enabled = true
	settings.Keywords = []string{" overloaded ", "OVERLOADED", "custom_busy"}
	for _, maxRetries := range []int{11, 4} {
		settings.MaxRetries = maxRetries
		payload, err := json.Marshal(settings)
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/admin/settings/codex-pre-output-retry", bytes.NewReader(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		h.UpdateCodexPreOutputRetrySettings(c)
		if maxRetries == 11 {
			require.Equal(t, 400, recorder.Code)
			require.Empty(t, repo.values)
		} else {
			require.Equal(t, 200, recorder.Code)
			require.True(t, gjson.Get(recorder.Body.String(), "data.enabled").Bool())
			require.Equal(t, int64(4), gjson.Get(recorder.Body.String(), "data.max_retries").Int())
			require.Equal(t, `["overloaded","custom_busy"]`, gjson.Get(recorder.Body.String(), "data.keywords").Raw)
		}
	}
}
