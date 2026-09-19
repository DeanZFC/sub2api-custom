//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type moderationWhitelistSettingsRepo struct {
	settingHandlerRepoStub
}

func (s *moderationWhitelistSettingsRepo) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func TestContentModerationConfigUserWhitelistUpdateRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &moderationWhitelistSettingsRepo{settingHandlerRepoStub{values: map[string]string{}}}
	svc := service.NewContentModerationService(repo, nil, nil, nil, nil, nil, nil, nil)
	handler := NewContentModerationHandler(svc)
	router := gin.New()
	router.PUT("/config", handler.UpdateConfig)
	router.GET("/config", handler.GetConfig)

	for _, step := range []struct {
		name string
		body string
		want []int64
	}{
		{"add and normalize", `{"user_whitelist_ids":[9,7,9,0,-1]}`, []int64{7, 9}},
		{"omission preserves", `{"sample_rate":50}`, []int64{7, 9}},
		{"clear", `{"user_whitelist_ids":[]}`, []int64{}},
	} {
		t.Run(step.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(step.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			var updated struct {
				Data service.ContentModerationConfigView `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &updated))
			require.Equal(t, step.want, updated.Data.UserWhitelistIDs)

			var saved service.ContentModerationConfig
			require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeyContentModerationConfig]), &saved))
			require.Equal(t, step.want, saved.UserWhitelistIDs)

			recorder = httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/config", nil))
			require.Equal(t, http.StatusOK, recorder.Code)
			var loaded struct {
				Data service.ContentModerationConfigView `json:"data"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &loaded))
			require.Equal(t, step.want, loaded.Data.UserWhitelistIDs)
		})
	}
}
