package handler

import (
	"context"
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
	userID    int64
	groupView bool
}

func (r *userTestMonitorRepo) ListVisible(_ context.Context, userID int64, _ int) ([]*service.ScheduledTestResult, error) {
	r.userID = userID
	return nil, nil
}

func (r *userTestMonitorRepo) ListVisibleByGroup(_ context.Context, userID int64) ([]*service.ScheduledTestResult, error) {
	r.userID, r.groupView = userID, true
	return []*service.ScheduledTestResult{{ID: 1, Status: "success", QualityVerdict: "fail", ProtectionDecision: &service.ScheduledTestProtectionDecision{Reason: "private detail"}}}, nil
}

func TestUserTestMonitorUsesAuthenticatedGroupScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		query           string
		auth, groupView bool
		status          int
	}{
		{"?group_view=all", false, false, 401},
		{"", true, false, 200},
		{"?group_view=all&user_id=999&group_id=999", true, true, 200},
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
			if tc.auth {
				require.Equal(t, int64(42), repo.userID)
			}
			require.NotContains(t, response.Body.String(), "private detail")
			if tc.groupView {
				require.Contains(t, response.Body.String(), `"quality_verdict":"fail"`)
			}
		})
	}
}
