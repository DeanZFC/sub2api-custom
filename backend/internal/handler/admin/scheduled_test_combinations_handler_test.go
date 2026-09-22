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

func TestScheduledTestCombinationHandlerPreservesNestedConditionsAndActions(t *testing.T) {
	repo := &qualityPlanHandlerRepo{}
	svc := service.NewScheduledTestService(repo, nil)
	svc.SetDefinitionRepository(protectionDefinitionHandlerRepo{})
	handler := NewScheduledTestHandler(svc)
	router := gin.New()
	router.POST("/plans", handler.Create)
	router.PUT("/plans/:id", handler.Update)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	created := send(http.MethodPost, "/plans", `{
 "name":"Combined quality","group_ids":[3,4],"test_definition_ids":[1,2],
 "model_id":"model","cron_expression":"0 * * * *",
 "protection":{"enabled":true,"mode":"combined",
 "rules":[{"test_definition_id":1,"pause_on_failure":true,"min_samples":10,"thresholds":[{"metric":"cache_rate","operator":"lt","value":80}]},
          {"test_definition_id":2,"pause_on_failure":true,"vote":{"enabled":true,"reject_above":3,"pass_at_least":3}}],
 "combinations":[{"id":"reject","name":"Either check fails","priority":10,
 "condition":{"operator":"any","conditions":[
 {"operator":"test","test_definition_id":1,"verdict":"fail"},
 {"operator":"all","conditions":[{"operator":"test","test_definition_id":2,"verdict":"fail"}]}]},
 "action":{"scheduling":"pause","group_mode":"assign","group_ids":[4]}}]}}
 `)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	var plan service.ScheduledTestPlan
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &plan))
	require.Equal(t, "combined", plan.Protection.Mode)
	require.Len(t, plan.Protection.Combinations, 1)
	rule := plan.Protection.Combinations[0]
	require.Equal(t, "any", rule.Condition.Operator)
	require.Equal(t, "all", rule.Condition.Conditions[1].Operator)
	require.Equal(t, int64(2), rule.Condition.Conditions[1].Conditions[0].TestDefinitionID)
	require.Equal(t, "pause", rule.Action.Scheduling)
	require.Equal(t, []int64{4}, rule.Action.GroupIDs)
	updated := send(http.MethodPut, "/plans/7", `{"name":"Renamed"}`)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.Equal(t, plan.Protection.Combinations, repo.plan.Protection.Combinations, "partial updates must preserve the expression and actions")
}
