package handler

import (
	"context"
	"database/sql"
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

type userTestHistoryRepo struct {
	service.ScheduledTestResultRepository
	userID, resultID, beforeID int64
	limit                      int
	rows                       []*service.ScheduledTestResult
	err                        error
}

func (r *userTestHistoryRepo) ListVisibleHistory(_ context.Context, userID, resultID, beforeID int64, limit int) ([]*service.ScheduledTestResult, error) {
	r.userID, r.resultID, r.beforeID, r.limit = userID, resultID, beforeID, limit
	return r.rows, r.err
}

func TestUserTestHistoryValidationAndPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path string
		auth       bool
		err        error
		status     int
	}{
		{name: "anonymous", path: "/9/history", status: 401},
		{name: "bad id", path: "/0/history", auth: true, status: 400},
		{name: "bad cursor", path: "/9/history?before_id=-1", auth: true, status: 400},
		{name: "bad limit", path: "/9/history?limit=51", auth: true, status: 400},
		{name: "hidden result", path: "/9/history", auth: true, err: sql.ErrNoRows, status: 404},
		{name: "private database error", path: "/9/history", auth: true, err: errors.New("secret database detail"), status: 500},
		{name: "page", path: "/9/history?before_id=7&limit=2&group_id=999&account_id=123", auth: true, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userTestHistoryRepo{err: tc.err, rows: []*service.ScheduledTestResult{
				{ID: 6, Status: "success", OutputKind: "number", ResponseText: "29"},
				{ID: 5, Status: "success"}, {ID: 4, Status: "passed"},
			}}
			h := &UserHandler{scheduledTestSvc: service.NewScheduledTestService(nil, repo)}
			router := gin.New()
			if tc.auth {
				router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42}) })
			}
			router.GET("/:id/history", h.ListTestResultHistory)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			require.Equal(t, tc.status, response.Code, response.Body.String())
			require.NotContains(t, response.Body.String(), "secret database detail")
			if tc.status == http.StatusOK {
				var page service.ScheduledTestResultHistory
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
				require.Len(t, page.Items, 2)
				require.Equal(t, int64(5), *page.NextBeforeID)
				require.Equal(t, 29.0, *page.Items[0].OutputNumeric)
				require.Equal(t, int64(42), repo.userID)
				require.Equal(t, int64(9), repo.resultID)
				require.Equal(t, int64(7), repo.beforeID)
				require.Equal(t, 3, repo.limit, "fetch one extra result to determine whether another page exists")
			}
		})
	}
}

func TestUserTestHistoryEmptyPageHasNoCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &userTestHistoryRepo{}
	h := &UserHandler{scheduledTestSvc: service.NewScheduledTestService(nil, repo)}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Params = gin.Params{{Key: "id", Value: "9"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/9/history", nil)
	h.ListTestResultHistory(c)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"items":[]}`, response.Body.String())
	require.Equal(t, 21, repo.limit)
}
