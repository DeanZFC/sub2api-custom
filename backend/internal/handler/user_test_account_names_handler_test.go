package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userTestAccountNamesRepo struct {
	service.ScheduledTestResultRepository
	ids    []int64
	calls  int
	userID int64
	err    error
}

func (r *userTestAccountNamesRepo) results(userID int64) ([]*service.ScheduledTestResult, error) {
	r.userID = userID
	accountID, hiddenID := int64(2296), int64(88)
	return []*service.ScheduledTestResult{
		{ID: 3, AccountID: &accountID, AccountName: "stale private name", TargetMode: "all_accounts", Status: "success", OutputKind: "number", ResponseText: "21"},
		{ID: 2, AccountID: &accountID, TargetMode: "account", Status: "failed"},
		{ID: 1, AccountID: &hiddenID, AccountName: "hidden executing account", TargetMode: "group", Status: "success"},
	}, nil
}
func (r *userTestAccountNamesRepo) ListVisible(_ context.Context, userID int64, _ int) ([]*service.ScheduledTestResult, error) {
	return r.results(userID)
}
func (r *userTestAccountNamesRepo) ListVisibleWithRecentHTML(_ context.Context, userID int64, _ int) ([]*service.ScheduledTestResult, error) {
	return r.results(userID)
}
func (r *userTestAccountNamesRepo) ListVisibleWithRecentResults(_ context.Context, userID int64, _ int) ([]*service.ScheduledTestResult, error) {
	return r.results(userID)
}
func (r *userTestAccountNamesRepo) ListVisibleByGroup(_ context.Context, userID int64) ([]*service.ScheduledTestResult, error) {
	return r.results(userID)
}
func (r *userTestAccountNamesRepo) ListVisibleHistory(_ context.Context, userID, _, _ int64, _ int) ([]*service.ScheduledTestResult, error) {
	return r.results(userID)
}
func (r *userTestAccountNamesRepo) GetAdminResultAccountNames(_ context.Context, ids []int64) (map[int64]string, error) {
	r.ids = append([]int64(nil), ids...)
	r.calls++
	return map[int64]string{2296: "GPT-SN-1", 88: "hidden executing account"}, r.err
}

func TestUserTestAccountNamesRequireAuthenticatedAdminRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, role := range []string{service.RoleAdmin, service.RoleUser, ""} {
		for _, path := range []string{"/test-results", "/test-results?recent_html=3", "/test-results?recent_results=4", "/test-results?group_view=all", "/test-results/3/history"} {
			t.Run(role+path, func(t *testing.T) {
				repo := &userTestAccountNamesRepo{}
				if role != service.RoleAdmin {
					repo.err = errors.New("ordinary users must not run the administrator name lookup")
				}
				h := &UserHandler{scheduledTestSvc: service.NewScheduledTestService(nil, repo)}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
					if role != "" {
						c.Set(string(middleware.ContextKeyUserRole), role)
					}
				})
				router.GET("/test-results", h.ListTestResults)
				router.GET("/test-results/:id/history", h.ListTestResultHistory)
				separator := "?"
				for _, char := range path {
					if char == '?' {
						separator = "&"
						break
					}
				}
				req := httptest.NewRequest(http.MethodGet, path+separator+"role=admin&is_admin=true&user_id=1&include_account_names=true", nil)
				req.Header.Set("X-User-Role", "admin")
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				require.Equal(t, http.StatusOK, res.Code, res.Body.String())
				require.Equal(t, int64(42), repo.userID)
				var rows []*service.ScheduledTestResult
				if path == "/test-results/3/history" {
					var page service.ScheduledTestResultHistory
					require.NoError(t, json.Unmarshal(res.Body.Bytes(), &page))
					rows = page.Items
				} else {
					require.NoError(t, json.Unmarshal(res.Body.Bytes(), &rows))
				}
				require.Len(t, rows, 3)
				require.Empty(t, rows[2].AccountName, "group checks must not reveal the executing account name")
				if role == service.RoleAdmin {
					require.Equal(t, []int64{2296}, repo.ids)
					require.Equal(t, 1, repo.calls, "load current names once without depending on review records")
					require.Equal(t, "GPT-SN-1", rows[0].AccountName)
					require.Equal(t, "GPT-SN-1", rows[1].AccountName)
					require.Equal(t, "private, no-store", res.Header().Get("Cache-Control"))
				} else {
					require.Zero(t, repo.calls)
					require.NotContains(t, res.Body.String(), "account_name")
					require.NotContains(t, res.Body.String(), "GPT-SN-1")
				}
				require.NotContains(t, res.Body.String(), "stale private name")
				require.NotContains(t, res.Body.String(), "hidden executing account")
			})
		}
	}
}

func TestUserTestAdminAccountNameLookupFailureHidesDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, history := range []bool{false, true} {
		repo := &userTestAccountNamesRepo{err: errors.New("private database detail")}
		h := &UserHandler{scheduledTestSvc: service.NewScheduledTestService(nil, repo)}
		res := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(res)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Request = httptest.NewRequest(http.MethodGet, "/test-results", nil)
		if history {
			c.Params = gin.Params{{Key: "id", Value: "3"}}
			h.ListTestResultHistory(c)
		} else {
			h.ListTestResults(c)
		}
		require.Equal(t, http.StatusInternalServerError, res.Code)
		require.NotContains(t, res.Body.String(), "private database detail")
	}
}
