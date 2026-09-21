package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	rows             []*service.ScheduledTestVoteResult
	err              error
	calls            int
	userID, resultID int64
	vote             string
}

func (r *userTestVoteRepository) ListVotingResults(_ context.Context, userID int64) ([]*service.ScheduledTestVoteResult, error) {
	r.calls++
	r.userID = userID
	return r.rows, r.err
}
func (r *userTestVoteRepository) CastTestVote(_ context.Context, userID, resultID int64, vote string) (*service.ScheduledTestVoteResult, error) {
	r.calls++
	r.userID, r.resultID, r.vote = userID, resultID, vote
	if r.err != nil {
		return nil, r.err
	}
	return r.rows[0], nil
}

func userVotePrivateRow() *service.ScheduledTestVoteResult {
	accountID := int64(17)
	return &service.ScheduledTestVoteResult{
		Result: &service.ScheduledTestResult{
			ID: 9, AccountID: &accountID, AccountName: "private-account-name",
			ErrorMessage: "private-upstream-detail", Status: "success", OutputKind: "text", ResponseText: "public output",
		},
		Voting: service.ScheduledTestVotingSummary{Enabled: true, Open: true, ReferenceAnswer: "public reference", PassCount: 2, FailCount: 1, MyVote: "pass"},
	}
}

func userVoteRouter(repo *userTestVoteRepository, auth, unavailable bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &UserHandler{}
	if !unavailable {
		h.scheduledTestSvc = service.NewScheduledTestService(nil, repo)
	}
	router := gin.New()
	if auth {
		router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42}) })
	}
	router.GET("/votes", h.ListTestVotes)
	router.POST("/votes/:id", h.VoteTestResult)
	return router
}

func TestUserTestVoteHandlerValidationAuthorizationAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name, id, body    string
		auth, unavailable bool
		err               error
		want, calls       int
	}{
		{name: "authentication required", id: "9", body: `{"vote":"pass"}`, want: 401},
		{name: "nil service", id: "9", auth: true, unavailable: true, body: `{"vote":"pass"}`, want: 500},
		{name: "noninteger id", id: "abc", auth: true, body: `{"vote":"pass"}`, want: 400},
		{name: "zero id", id: "0", auth: true, body: `{"vote":"pass"}`, want: 400},
		{name: "negative id", id: "-1", auth: true, body: `{"vote":"pass"}`, want: 400},
		{name: "overflow id", id: "999999999999999999999999", auth: true, body: `{"vote":"pass"}`, want: 400},
		{name: "malformed body", id: "9", auth: true, body: `{"vote":`, want: 400},
		{name: "missing vote", id: "9", auth: true, body: `{}`, want: 400},
		{name: "wrong type", id: "9", auth: true, body: `{"vote":true}`, want: 400},
		{name: "unknown vote", id: "9", auth: true, body: `{"vote":"reject"}`, want: 400},
		{name: "uppercase rejected", id: "9", auth: true, body: `{"vote":"PASS"}`, want: 400},
		{name: "hidden or missing round", id: "9", auth: true, body: `{"vote":"pass"}`, err: sql.ErrNoRows, want: 404, calls: 1},
		{name: "expired round", id: "9", auth: true, body: `{"vote":"pass"}`, err: fmt.Errorf("private-vote-context: %w", service.ErrScheduledTestVoteUnavailable), want: 404, calls: 1},
		{name: "database error stays private", id: "9", auth: true, body: `{"vote":"pass"}`, err: errors.New("private-database-password"), want: 500, calls: 1},
		{name: "invalid vote wrapped detail stays private", id: "9", auth: true, body: `{"vote":"pass"}`, err: fmt.Errorf("private-validation-detail: %w", service.ErrScheduledTestVoteInvalid), want: 400, calls: 1},
		{name: "pass ignores forged user and account", id: "9", auth: true, body: `{"vote":"pass","user_id":777,"account_id":888,"group_id":999}`, want: 200, calls: 1},
		{name: "fail accepted", id: "9", auth: true, body: `{"vote":"fail"}`, want: 200, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userTestVoteRepository{rows: []*service.ScheduledTestVoteResult{userVotePrivateRow()}, err: tc.err}
			router := userVoteRouter(repo, tc.auth, tc.unavailable)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/votes/"+tc.id+"?user_id=777&account_id=888", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			require.Equal(t, tc.want, response.Code, response.Body.String())
			require.Equal(t, tc.calls, repo.calls)
			require.NotContains(t, response.Body.String(), "private-")
			require.NotContains(t, response.Body.String(), "account_name")
			if tc.calls > 0 {
				require.Equal(t, int64(42), repo.userID)
				require.Equal(t, int64(9), repo.resultID)
			}
			if tc.want == http.StatusOK {
				var row service.ScheduledTestVoteResult
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &row))
				require.Equal(t, int64(17), *row.Result.AccountID)
				require.Equal(t, "public output", row.Result.ResponseText)
				require.Equal(t, "public reference", row.Voting.ReferenceAnswer)
				require.Empty(t, row.Result.ErrorMessage)
				require.Contains(t, []string{"pass", "fail"}, repo.vote)
			}
		})
	}
}

func TestUserTestVoteHandlerListAuthenticationPrivacyAndEmptyArray(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		auth, unavailable, empty bool
		err                      error
		want, calls              int
	}{
		{name: "authentication required", want: 401},
		{name: "nil service", auth: true, unavailable: true, want: 500},
		{name: "list error stays private", auth: true, err: errors.New("private-database-detail"), want: 500, calls: 1},
		{name: "authorized rows", auth: true, want: 200, calls: 1},
		{name: "empty list", auth: true, empty: true, want: 200, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userTestVoteRepository{err: tc.err}
			if !tc.empty {
				repo.rows = []*service.ScheduledTestVoteResult{userVotePrivateRow()}
			}
			response := httptest.NewRecorder()
			userVoteRouter(repo, tc.auth, tc.unavailable).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/votes?user_id=777&account_id=888", nil))
			require.Equal(t, tc.want, response.Code, response.Body.String())
			require.Equal(t, tc.calls, repo.calls)
			require.NotContains(t, response.Body.String(), "private-")
			require.NotContains(t, response.Body.String(), "account_name")
			if tc.calls > 0 {
				require.Equal(t, int64(42), repo.userID)
			}
			if tc.want == 200 && tc.empty {
				require.JSONEq(t, `[]`, response.Body.String())
			}
		})
	}
}
