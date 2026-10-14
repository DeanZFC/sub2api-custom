package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestHandlerTimePeriods(t *testing.T) {
	repo := &qualityPlanHandlerRepo{}
	svc := service.NewScheduledTestService(repo, nil)
	svc.SetDefinitionRepository(qualityDefinitionHandlerRepo{})
	router := gin.New()
	handler := NewScheduledTestHandler(svc)
	router.POST("/plans", handler.Create)
	router.PUT("/plans/:id", handler.Update)
	send := func(method, path, body string, status int) *service.ScheduledTestPlan {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, status, response.Code, response.Body.String())
		var plan service.ScheduledTestPlan
		if status == http.StatusOK {
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &plan))
		}
		return &plan
	}
	created := send(http.MethodPost, "/plans", `{"group_ids":[8],"test_definition_ids":[1],"model_id":"model","schedule":{"timezone":"Asia/Shanghai","periods":[{"start":"08:00","end":"20:00","interval_minutes":60},{"start":"20:00","end":"08:00","interval_minutes":180}]}}`, http.StatusOK)
	require.Len(t, created.Schedule.Periods, 2)
	require.NotNil(t, created.NextRunAt)
	updated := send(http.MethodPut, "/plans/7", `{"enabled":false}`, http.StatusOK)
	require.Equal(t, created.Schedule, updated.Schedule, "partial updates preserve periods")
	updated = send(http.MethodPut, "/plans/7", `{"schedule":null,"cron_expression":"0 9 * * *"}`, http.StatusOK)
	require.Nil(t, updated.Schedule)
	require.Equal(t, "0 9 * * *", updated.CronExpression)
	writes := repo.writes
	send(http.MethodPut, "/plans/7", `{"schedule":{"timezone":"UTC","periods":[{"start":"20:00","end":"08:00","interval_minutes":180},{"start":"07:00","end":"09:00","interval_minutes":60}]}}`, http.StatusBadRequest)
	require.Equal(t, writes, repo.writes, "invalid overlaps must not persist")
}
