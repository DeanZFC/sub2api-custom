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

func TestScheduledTestHandlerCreatesMultipleModels(t *testing.T) {
	repo := &qualityPlanHandlerRepo{}
	svc := service.NewScheduledTestService(repo, nil)
	svc.SetDefinitionRepository(qualityDefinitionHandlerRepo{})
	router := gin.New()
	router.POST("/plans", NewScheduledTestHandler(svc).Create)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/plans", strings.NewReader(`{"group_ids":[3],"test_definition_ids":[1],"model_ids":["model-b","model-a","model-b"],"cron_expression":"* * * * *"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var plan service.ScheduledTestPlan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plan))
	require.Equal(t, []string{"model-b", "model-a"}, plan.ModelIDs)
	require.Equal(t, "model-b", plan.ModelID)
}

func TestScheduledTestHandlerUpdatesMultipleModels(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		models     []string
		status     int
	}{
		{"replace", `{"model_ids":["model-c","model-b"]}`, []string{"model-c", "model-b"}, http.StatusOK},
		{"partial keeps selection", `{"enabled":false}`, []string{"model-a", "model-b"}, http.StatusOK},
		{"legacy scalar replaces", `{"model_id":"model-c"}`, []string{"model-c"}, http.StatusOK},
		{"empty rejected", `{"model_ids":[]}`, nil, http.StatusBadRequest},
		{"null rejected", `{"model_ids":null}`, nil, http.StatusBadRequest},
		{"blank rejected", `{"model_ids":[" "]}`, nil, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &qualityPlanHandlerRepo{plan: &service.ScheduledTestPlan{ID: 7, GroupIDs: []int64{3}, TestDefinitionIDs: []int64{1}, ModelID: "model-a", ModelIDs: []string{"model-a", "model-b"}, CronExpression: "* * * * *"}}
			svc := service.NewScheduledTestService(repo, nil)
			svc.SetDefinitionRepository(qualityDefinitionHandlerRepo{})
			router := gin.New()
			router.PUT("/plans/:id", NewScheduledTestHandler(svc).Update)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/plans/7", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status == http.StatusOK {
				var plan service.ScheduledTestPlan
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plan))
				require.Equal(t, tc.models, plan.ModelIDs)
				require.Equal(t, tc.models[0], plan.ModelID)
			} else {
				require.Zero(t, repo.writes)
			}
		})
	}
}
