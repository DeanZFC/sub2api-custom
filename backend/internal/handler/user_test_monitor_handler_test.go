package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userTestMonitorRepo struct {
	service.ScheduledTestResultRepository
	userID        int64
	groupView     bool
	recentHTML    int
	recentResults int
}

func (r *userTestMonitorRepo) ListVisible(_ context.Context, userID int64, _ int) ([]*service.ScheduledTestResult, error) {
	r.userID = userID
	return nil, nil
}

func (r *userTestMonitorRepo) ListVisibleByGroup(_ context.Context, userID int64) ([]*service.ScheduledTestResult, error) {
	r.userID, r.groupView = userID, true
	return []*service.ScheduledTestResult{{ID: 1, Status: "success", QualityVerdict: "fail", ProtectionDecision: &service.ScheduledTestProtectionDecision{Reason: "private detail"}}}, nil
}

func (r *userTestMonitorRepo) ListVisibleWithRecentHTML(_ context.Context, userID int64, total int) ([]*service.ScheduledTestResult, error) {
	r.userID, r.recentHTML = userID, total
	return []*service.ScheduledTestResult{
		{ID: 3, Status: "success", OutputKind: "html", OutputHTML: "<html>latest</html>", ErrorMessage: "private upstream", ProtectionDecision: &service.ScheduledTestProtectionDecision{Reason: "private protection"}},
		{ID: 2, Status: "passed", OutputKind: "html", OutputHTML: "<html>previous</html>", ExecutionInfo: &service.ScheduledTestExecutionInfo{AutoReview: &service.ScheduledTestAutoReviewSummary{Reason: "private review", JudgeAccountID: 999}}},
	}, nil
}

func (r *userTestMonitorRepo) ListVisibleWithRecentResults(_ context.Context, userID int64, total int) ([]*service.ScheduledTestResult, error) {
	r.userID, r.recentResults = userID, total
	zero := 0.0
	return []*service.ScheduledTestResult{
		{ID: 3, Status: "success", OutputKind: "html", OutputHTML: "<html>latest</html>", ErrorMessage: "private upstream", ProtectionDecision: &service.ScheduledTestProtectionDecision{Reason: "private protection"}},
		{ID: 2, Status: "passed", OutputKind: "number", OutputNumeric: &zero, ExecutionInfo: &service.ScheduledTestExecutionInfo{AutoReview: &service.ScheduledTestAutoReviewSummary{Reason: "private review", JudgeAccountID: 999}}},
	}, nil
}

func TestUserTestMonitorUsesAuthenticatedGroupScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		query           string
		auth, groupView bool
		recentHTML      int
		recentResults   int
		status          int
	}{
		{"?group_view=all", false, false, 0, 0, 401},
		{"", true, false, 0, 0, 200},
		{"?group_view=all&user_id=999&group_id=999", true, true, 0, 0, 200},
		{"?group_view=all&recent_html=invalid&recent_results=invalid", true, true, 0, 0, 200},
		{"?recent_html=3&user_id=999&group_id=999", true, false, 3, 0, 200},
		{"?recent_results=4", false, false, 0, 0, 401},
		{"?recent_results=4&user_id=999&group_id=999", true, false, 0, 4, 200},
		{"?recent_results=4&recent_html=invalid", true, false, 0, 4, 200},
	} {
		t.Run(tc.query, func(t *testing.T) {
			repo := &userTestMonitorRepo{}
			h := &UserHandler{scheduledTestSvc: service.NewScheduledTestService(nil, repo)}
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			if tc.auth {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
			}
			c.Request = httptest.NewRequest(http.MethodGet, "/test-results"+tc.query, nil)
			h.ListTestResults(c)
			require.Equal(t, tc.status, response.Code)
			require.Equal(t, tc.groupView, repo.groupView)
			require.Equal(t, tc.recentHTML, repo.recentHTML)
			require.Equal(t, tc.recentResults, repo.recentResults)
			if tc.auth {
				require.Equal(t, int64(42), repo.userID)
			}
			require.NotContains(t, response.Body.String(), "private detail")
			if tc.groupView {
				require.Contains(t, response.Body.String(), `"quality_verdict":"fail"`)
			}
			if tc.recentHTML > 0 {
				var rows []*service.ScheduledTestResult
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rows))
				require.Len(t, rows, 2)
				require.Equal(t, "<html>latest</html>", rows[0].OutputHTML)
				require.Equal(t, "<html>previous</html>", rows[1].OutputHTML)
				require.NotContains(t, response.Body.String(), "private")
				require.NotContains(t, response.Body.String(), "auto_review")
			}
			if tc.recentResults > 0 {
				var rows []*service.ScheduledTestResult
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &rows))
				require.Len(t, rows, 2)
				require.Equal(t, "<html>latest</html>", rows[0].OutputHTML)
				require.NotNil(t, rows[1].OutputNumeric)
				require.Zero(t, *rows[1].OutputNumeric)
				require.NotContains(t, response.Body.String(), "private")
				require.NotContains(t, response.Body.String(), "auto_review")
			}
		})
	}
}

func TestUserTestResultsRejectInvalidRecentCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, parameter := range []string{"recent_html", "recent_results"} {
		for _, value := range []string{"0", "-1", "6", "3.5", "invalid"} {
			t.Run(parameter+"/"+value, func(t *testing.T) {
				repo := &userTestMonitorRepo{}
				h := &UserHandler{scheduledTestSvc: service.NewScheduledTestService(nil, repo)}
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				c.Request = httptest.NewRequest(http.MethodGet, "/test-results?"+parameter+"="+value, nil)
				h.ListTestResults(c)
				require.Equal(t, http.StatusBadRequest, response.Code)
				require.Zero(t, repo.userID, "invalid requests must not query results")
			})
		}
	}
}
