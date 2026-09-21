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

type protectionDefinitionHandlerRepo struct {
	service.ScheduledTestDefinitionRepository
}

func (protectionDefinitionHandlerRepo) GetByID(_ context.Context, id int64) (*service.ScheduledTestDefinition, error) {
	kind := "html"
	if id == 1 {
		kind = "statistics"
	}
	return &service.ScheduledTestDefinition{ID: id, Enabled: true, OutputKind: kind}, nil
}

func TestScheduledTestProtectionHandlerCreateAndUpdate(t *testing.T) {
	repo := &qualityPlanHandlerRepo{}
	svc := service.NewScheduledTestService(repo, nil)
	svc.SetDefinitionRepository(protectionDefinitionHandlerRepo{})
	h := NewScheduledTestHandler(svc)
	router := gin.New()
	router.POST("/plans", h.Create)
	router.PUT("/plans/:id", h.Update)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	created := send(http.MethodPost, "/plans", `{"name":"quality","group_id":3,"target_mode":"all_accounts","test_definition_ids":[1,2],"model_id":"model","cron_expression":"* * * * *","protection":{"enabled":true,"rules":[{"test_definition_id":1,"min_samples":10,"thresholds":[{"metric":"cache_rate","operator":"lt","value":80}]},{"test_definition_id":2,"expected_answer":"pelican riding a bicycle","vote":{"enabled":true,"reject_above":2,"pass_at_least":3}}]}}`)
	require.Equal(t, http.StatusOK, created.Code, created.Body.String())
	var plan service.ScheduledTestPlan
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &plan))
	require.True(t, plan.Protection.Enabled)
	require.Len(t, plan.Protection.Rules, 2)
	require.Equal(t, 80.0, plan.Protection.Rules[0].Thresholds[0].Value)
	require.Equal(t, 2, plan.Protection.Rules[1].Vote.RejectAbove)

	updated := send(http.MethodPut, "/plans/7", `{"name":"renamed"}`)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.True(t, repo.plan.Protection.Enabled, "omitting protection must preserve it")
	require.Len(t, repo.plan.Protection.Rules, 2)
	updated = send(http.MethodPut, "/plans/7", `{"protection":{"enabled":false,"rules":[{"test_definition_id":2,"expected_answer":"reference","vote":{"enabled":true,"reject_above":1,"pass_at_least":2}}]}}`)
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.False(t, repo.plan.Protection.Enabled)
	require.Len(t, repo.plan.Protection.Rules, 1, "disabled configuration remains editable")
}

func TestScheduledTestProtectionHandlerRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, target, rules string }{
		{"group cannot pause accounts", "group", `[{"test_definition_id":1,"pause_on_failure":true}]`},
		{"unknown selected type", "all_accounts", `[{"test_definition_id":5,"pause_on_failure":true}]`},
		{"statistics cannot vote", "all_accounts", `[{"test_definition_id":1,"vote":{"enabled":true,"reject_above":1,"pass_at_least":2}}]`},
		{"HTML cannot have cache rate", "all_accounts", `[{"test_definition_id":2,"thresholds":[{"metric":"cache_rate","operator":"lt","value":80}]}]`},
		{"invalid vote threshold", "all_accounts", `[{"test_definition_id":2,"vote":{"enabled":true,"reject_above":-1,"pass_at_least":0}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &qualityPlanHandlerRepo{}
			svc := service.NewScheduledTestService(repo, nil)
			svc.SetDefinitionRepository(protectionDefinitionHandlerRepo{})
			router := gin.New()
			router.POST("/plans", NewScheduledTestHandler(svc).Create)
			body := `{"group_id":3,"target_mode":"` + tc.target + `","test_definition_ids":[1,2],"model_id":"model","cron_expression":"* * * * *","protection":{"enabled":true,"rules":` + tc.rules + `}}`
			req := httptest.NewRequest(http.MethodPost, "/plans", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Zero(t, repo.writes)
		})
	}
}
