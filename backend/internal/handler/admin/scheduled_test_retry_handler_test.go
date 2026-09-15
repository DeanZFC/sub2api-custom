package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type retryHandlerPlanRepo struct {
	service.ScheduledTestPlanRepository
	plan *service.ScheduledTestPlan
}

func (r retryHandlerPlanRepo) GetByID(context.Context, int64) (*service.ScheduledTestPlan, error) {
	return r.plan, nil
}

type retryHandlerResultRepo struct {
	service.ScheduledTestResultRepository
	result *service.ScheduledTestResult
}

func (r retryHandlerResultRepo) GetByID(context.Context, int64) (*service.ScheduledTestResult, error) {
	if r.result == nil {
		return nil, sql.ErrNoRows
	}
	return r.result, nil
}

func TestScheduledTestRetryResultHandler(t *testing.T) {
	accountID := int64(12)
	for _, tc := range []struct {
		name       string
		id         string
		result     *service.ScheduledTestResult
		runErr     error
		statusCode int
	}{
		{name: "queues result", id: "9", result: &service.ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, Status: "failed"}, statusCode: http.StatusAccepted},
		{name: "already running", id: "9", result: &service.ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, Status: "failed"}, runErr: service.ErrScheduledTestAccountRunning, statusCode: http.StatusConflict},
		{name: "changed result", id: "9", result: &service.ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, Status: "failed"}, runErr: service.ErrScheduledTestResultNotFailed, statusCode: http.StatusConflict},
		{name: "running result", id: "9", result: &service.ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, Status: "running"}, statusCode: http.StatusConflict},
		{name: "missing result", id: "9", statusCode: http.StatusNotFound},
		{name: "invalid id", id: "bad", statusCode: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := service.NewScheduledTestService(retryHandlerPlanRepo{plan: &service.ScheduledTestPlan{ID: 3, TargetMode: "all_accounts"}}, retryHandlerResultRepo{result: tc.result})
			svc.SetRetryFunc(func(_ context.Context, plan *service.ScheduledTestPlan, previous *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
				if tc.runErr != nil {
					return nil, tc.runErr
				}
				return &service.ScheduledTestResult{ID: previous.ID, PlanID: plan.ID, AccountID: previous.AccountID, Status: "running"}, nil
			})
			router := gin.New()
			router.POST("/test-results/:id/retry", NewScheduledTestHandler(svc).RetryResult)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/test-results/"+tc.id+"/retry", nil))
			require.Equal(t, tc.statusCode, response.Code)
			if tc.statusCode == http.StatusAccepted {
				var result service.ScheduledTestResult
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				require.Equal(t, tc.result.ID, result.ID)
				require.Equal(t, accountID, *result.AccountID)
				require.Equal(t, "running", result.Status)
			}
		})
	}
}
