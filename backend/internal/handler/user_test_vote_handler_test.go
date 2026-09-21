package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userTestVoteRepository struct {
	service.ScheduledTestResultRepository
	service.ScheduledTestProtectionRepository
	calls int
}

func (r *userTestVoteRepository) ListVotingResults(context.Context, int64) ([]*service.ScheduledTestVoteResult, error) {
	r.calls++
	return nil, nil
}

func (r *userTestVoteRepository) CastTestVote(context.Context, int64, int64, string) (*service.ScheduledTestVoteResult, error) {
	r.calls++
	return nil, nil
}

func TestUserTestVoteHandlersDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, path, body string
		auth, unavailable        bool
		want                     int
	}{
		{name: "list requires authentication", method: http.MethodGet, path: "/votes", want: http.StatusUnauthorized},
		{name: "vote requires authentication", method: http.MethodPost, path: "/votes/9", body: `{"vote":"pass"}`, want: http.StatusUnauthorized},
		{name: "old client sees empty list", method: http.MethodGet, path: "/votes", auth: true, want: http.StatusOK},
		{name: "list needs no voting service", method: http.MethodGet, path: "/votes", auth: true, unavailable: true, want: http.StatusOK},
		{name: "direct pass rejected", method: http.MethodPost, path: "/votes/9", body: `{"vote":"pass"}`, auth: true, want: http.StatusForbidden},
		{name: "direct fail rejected", method: http.MethodPost, path: "/votes/9", body: `{"vote":"fail"}`, auth: true, want: http.StatusForbidden},
		{name: "forged identity rejected", method: http.MethodPost, path: "/votes/9?user_id=777", body: `{"vote":"fail","user_id":777,"account_id":888,"role":"admin"}`, auth: true, want: http.StatusForbidden},
		{name: "malformed requests cannot bypass block", method: http.MethodPost, path: "/votes/invalid", body: `{`, auth: true, want: http.StatusForbidden},
		{name: "disabled without voting service", method: http.MethodPost, path: "/votes/9", body: `{"vote":"fail"}`, auth: true, unavailable: true, want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userTestVoteRepository{}
			h := &UserHandler{}
			if !tc.unavailable {
				h.scheduledTestSvc = service.NewScheduledTestService(nil, repo)
			}
			router := gin.New()
			if tc.auth {
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
				})
			}
			router.GET("/votes", h.ListTestVotes)
			router.POST("/votes/:id", h.VoteTestResult)
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.want, recorder.Code, recorder.Body.String())
			require.Zero(t, repo.calls, "disabled endpoints must never read ballots or change account routing")
			if tc.want == http.StatusOK {
				require.JSONEq(t, `[]`, recorder.Body.String())
			} else if tc.want == http.StatusForbidden {
				require.Contains(t, recorder.Body.String(), "user test voting is disabled")
			}
		})
	}
}
