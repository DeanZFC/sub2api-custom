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

func TestScheduledTestHandlerExcludedAccounts(t *testing.T) {
	for _, tc := range []struct {
		name, method, body string
		want               []int64
		status             int
	}{
		{"create deduplicates", "POST", `{"group_ids":[3],"test_definition_ids":[1],"model_id":"model","cron_expression":"* * * * *","excluded_account_ids":[12,13,12]}`, []int64{12, 13}, 200},
		{"omitted preserves", "PUT", `{"enabled":false}`, []int64{12}, 200},
		{"replace", "PUT", `{"excluded_account_ids":[13,14,13]}`, []int64{13, 14}, 200},
		{"clear", "PUT", `{"excluded_account_ids":[]}`, []int64{}, 200},
		{"null rejected", "PUT", `{"excluded_account_ids":null}`, nil, 400},
		{"scalar rejected", "PUT", `{"excluded_account_ids":12}`, nil, 400},
		{"zero rejected", "PUT", `{"excluded_account_ids":[0]}`, nil, 400},
		{"negative rejected", "PUT", `{"excluded_account_ids":[-1]}`, nil, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &qualityPlanHandlerRepo{plan: &service.ScheduledTestPlan{ID: 7, GroupIDs: []int64{3}, TestDefinitionIDs: []int64{1}, ExcludedAccountIDs: []int64{12}, ModelID: "model", CronExpression: "* * * * *"}}
			svc := service.NewScheduledTestService(repo, nil)
			svc.SetDefinitionRepository(qualityDefinitionHandlerRepo{})
			h := NewScheduledTestHandler(svc)
			router := gin.New()
			router.POST("/plans/7", h.Create)
			router.PUT("/plans/:id", h.Update)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, "/plans/7", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
			if tc.status == http.StatusOK {
				var plan service.ScheduledTestPlan
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &plan))
				require.Equal(t, tc.want, plan.ExcludedAccountIDs)
			} else {
				require.Zero(t, repo.writes)
			}
		})
	}
}
