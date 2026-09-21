package admin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type adminReviewHandlerRepo struct {
	service.ScheduledTestResultRepository
	calls int
	actor int64
	err   error
}

func (r *adminReviewHandlerRepo) ListAdminReviews(context.Context) ([]*service.ScheduledTestAdminReview, error) {
	r.calls++
	return []*service.ScheduledTestAdminReview{}, nil
}
func (r *adminReviewHandlerRepo) DecideTestResult(_ context.Context, actor, id, generation int64, verdict string) error {
	r.calls++
	r.actor = actor
	return r.err
}

func TestScheduledTestAdminDecisionAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, role, method, path, body string
		user                           int64
		status, calls                  int
		err                            error
	}{
		{name: "anonymous read", method: "GET", path: "/test-reviews", status: 401},
		{name: "user read", user: 8, role: "user", method: "GET", path: "/test-reviews", status: 403},
		{name: "admin read", user: 8, role: "admin", method: "GET", path: "/test-reviews", status: 200, calls: 1},
		{name: "anonymous decision", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"pass","generation":2}`, status: 401},
		{name: "user decision", user: 8, role: "user", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"fail","generation":2}`, status: 403},
		{name: "admin pass", user: 8, role: "admin", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"pass","generation":2,"admin_id":999}`, status: 200, calls: 1},
		{name: "admin fail", user: 8, role: "admin", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"fail","generation":2}`, status: 200, calls: 1},
		{name: "missing generation", user: 8, role: "admin", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"pass"}`, status: 400},
		{name: "invalid verdict", user: 8, role: "admin", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"upgrade","generation":2}`, status: 400},
		{name: "stale round", user: 8, role: "admin", method: "POST", path: "/test-results/9/decision", body: `{"verdict":"fail","generation":2}`, status: 409, calls: 1, err: service.ErrScheduledTestVoteUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &adminReviewHandlerRepo{err: tc.err}
			h := NewScheduledTestHandler(service.NewScheduledTestService(nil, repo))
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tc.user > 0 {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: tc.user})
					c.Set(string(middleware.ContextKeyUserRole), tc.role)
				}
				c.Next()
			})
			router.GET("/test-reviews", h.ListAdminReviews)
			router.POST("/test-results/:id/decision", h.DecideResult)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			require.Equal(t, tc.status, out.Code, out.Body.String())
			require.Equal(t, tc.calls, repo.calls)
			if tc.method == "POST" && tc.calls > 0 {
				require.Equal(t, tc.user, repo.actor)
			}
		})
	}
}
