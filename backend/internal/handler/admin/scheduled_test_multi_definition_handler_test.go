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

type qualityPlanHandlerRepo struct {
	service.ScheduledTestPlanRepository
	plan   *service.ScheduledTestPlan
	writes int
}

func (r *qualityPlanHandlerRepo) GetByID(context.Context, int64) (*service.ScheduledTestPlan, error) {
	return r.plan, nil
}
func (r *qualityPlanHandlerRepo) Create(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	plan.ID = 7
	r.plan = plan
	r.writes++
	return plan, nil
}
func (r *qualityPlanHandlerRepo) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	r.plan = plan
	r.writes++
	return plan, nil
}

type qualityDefinitionHandlerRepo struct {
	service.ScheduledTestDefinitionRepository
}

func (qualityDefinitionHandlerRepo) GetByID(_ context.Context, id int64) (*service.ScheduledTestDefinition, error) {
	return &service.ScheduledTestDefinition{ID: id, Enabled: true}, nil
}

func TestScheduledTestHandlerCreatesSingleRuleWithMultipleTypes(t *testing.T) {
	repo := &qualityPlanHandlerRepo{}
	svc := service.NewScheduledTestService(repo, nil)
	svc.SetDefinitionRepository(qualityDefinitionHandlerRepo{})
	router := gin.New()
	router.POST("/test-plans", NewScheduledTestHandler(svc).Create)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/test-plans", strings.NewReader(`{"name":"quality","group_id":3,"target_mode":"all_accounts","test_definition_ids":[2,1,2],"model_id":"model","cron_expression":"* * * * *"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var plan service.ScheduledTestPlan
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &plan))
	require.Equal(t, []int64{2, 1}, plan.TestDefinitionIDs)
	require.Equal(t, int64(2), *plan.TestDefinitionID)
	require.Equal(t, 1, repo.writes)
}

func TestScheduledTestHandlerUpdatesMultipleTypesAndLegacyScalar(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		want   []int64
		status int
	}{
		{"multiple types", `{"test_definition_ids":[2,3,2]}`, []int64{2, 3}, http.StatusOK},
		{"state only preserves types", `{"enabled":false}`, []int64{1, 2}, http.StatusOK},
		{"legacy scalar replaces selection", `{"test_definition_id":3}`, []int64{3}, http.StatusOK},
		{"legacy alias replaces selection", `{"test_type_id":3}`, []int64{3}, http.StatusOK},
		{"array wins over legacy scalar", `{"test_definition_ids":[2,3],"test_definition_id":1}`, []int64{2, 3}, http.StatusOK},
		{"empty group selection rejected", `{"test_definition_ids":[]}`, nil, http.StatusBadRequest},
		{"null selection rejected", `{"test_definition_ids":null}`, nil, http.StatusBadRequest},
		{"nonpositive type rejected", `{"test_definition_ids":[1,0]}`, nil, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID, definitionID := int64(3), int64(1)
			repo := &qualityPlanHandlerRepo{plan: &service.ScheduledTestPlan{ID: 7, GroupID: &groupID, TargetMode: "all_accounts", TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{1, 2}, ModelID: "model", CronExpression: "* * * * *", Enabled: true}}
			svc := service.NewScheduledTestService(repo, nil)
			svc.SetDefinitionRepository(qualityDefinitionHandlerRepo{})
			router := gin.New()
			router.PUT("/test-plans/:id", NewScheduledTestHandler(svc).Update)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/test-plans/7", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			if tc.status == http.StatusOK {
				var plan service.ScheduledTestPlan
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &plan))
				require.Equal(t, tc.want, plan.TestDefinitionIDs)
				require.Equal(t, tc.want[0], *plan.TestDefinitionID)
				require.Equal(t, int64(7), plan.ID)
				require.Equal(t, 1, repo.writes)
			} else {
				require.Zero(t, repo.writes)
			}
		})
	}
}
