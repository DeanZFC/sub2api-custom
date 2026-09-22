package handler

import (
	"context"
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
	calls            int
	userID, resultID int64
	vote             string
	rows             []*service.ScheduledTestVoteResult
	row              *service.ScheduledTestVoteResult
	err              error
}

func (r *userTestVoteRepository) ListVotingResults(_ context.Context, userID int64) ([]*service.ScheduledTestVoteResult, error) {
	r.calls++
	r.userID = userID
	return r.rows, r.err
}

func (r *userTestVoteRepository) CastTestVote(_ context.Context, userID, resultID int64, vote string) (*service.ScheduledTestVoteResult, error) {
	r.calls++
	r.userID, r.resultID, r.vote = userID, resultID, vote
	return r.row, r.err
}

func serveUserTestVote(t *testing.T, repo *userTestVoteRepository, method, path, body string, userID int64, unavailable bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &UserHandler{}
	if !unavailable {
		h.scheduledTestSvc = service.NewScheduledTestService(nil, repo)
	}
	router := gin.New()
	if userID != 0 {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID})
		})
	}
	router.GET("/votes", h.ListTestVotes)
	router.POST("/votes/:id", h.VoteTestResult)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestUserTestVoteHandlersValidationAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		userID                   int64
		unavailable              bool
		err                      error
		want, calls              int
	}{
		{name: "list requires authentication", method: http.MethodGet, path: "/votes", want: http.StatusUnauthorized},
		{name: "vote requires authentication", method: http.MethodPost, path: "/votes/9", body: `{"vote":"pass"}`, want: http.StatusUnauthorized},
		{name: "list rejects invalid auth identity", method: http.MethodGet, path: "/votes", userID: -1, want: http.StatusUnauthorized},
		{name: "vote rejects invalid auth identity", method: http.MethodPost, path: "/votes/9", userID: -1, body: `{"vote":"pass"}`, want: http.StatusUnauthorized},
		{name: "list unavailable service", method: http.MethodGet, path: "/votes", userID: 42, unavailable: true, want: http.StatusServiceUnavailable},
		{name: "vote unavailable service", method: http.MethodPost, path: "/votes/9", body: `{"vote":"pass"}`, userID: 42, unavailable: true, want: http.StatusServiceUnavailable},
		{name: "invalid id", method: http.MethodPost, path: "/votes/nope", body: `{"vote":"pass"}`, userID: 42, want: http.StatusBadRequest},
		{name: "zero id", method: http.MethodPost, path: "/votes/0", body: `{"vote":"pass"}`, userID: 42, want: http.StatusBadRequest},
		{name: "negative id", method: http.MethodPost, path: "/votes/-1", body: `{"vote":"pass"}`, userID: 42, want: http.StatusBadRequest},
		{name: "overflow id", method: http.MethodPost, path: "/votes/9223372036854775808", body: `{"vote":"pass"}`, userID: 42, want: http.StatusBadRequest},
		{name: "malformed body", method: http.MethodPost, path: "/votes/9", body: `{`, userID: 42, want: http.StatusBadRequest},
		{name: "missing vote", method: http.MethodPost, path: "/votes/9", body: `{}`, userID: 42, want: http.StatusBadRequest},
		{name: "unknown vote", method: http.MethodPost, path: "/votes/9", body: `{"vote":"other"}`, userID: 42, want: http.StatusBadRequest},
		{name: "case sensitive vote", method: http.MethodPost, path: "/votes/9", body: `{"vote":"PASS"}`, userID: 42, want: http.StatusBadRequest},
		{name: "repository rejects invalid vote", method: http.MethodPost, path: "/votes/9", body: `{"vote":"pass"}`, userID: 42, err: fmt.Errorf("wrapped: %w", service.ErrScheduledTestVoteInvalid), want: http.StatusBadRequest, calls: 1},
		{name: "closed or unauthorized round", method: http.MethodPost, path: "/votes/9", body: `{"vote":"pass"}`, userID: 42, err: fmt.Errorf("wrapped: %w", service.ErrScheduledTestVoteUnavailable), want: http.StatusConflict, calls: 1},
		{name: "voting listing unavailable", method: http.MethodGet, path: "/votes", userID: 42, err: fmt.Errorf("wrapped: %w", service.ErrScheduledTestVoteUnavailable), want: http.StatusServiceUnavailable, calls: 1},
		{name: "private listing error", method: http.MethodGet, path: "/votes", userID: 42, err: errors.New("private database detail"), want: http.StatusInternalServerError, calls: 1},
		{name: "private vote error", method: http.MethodPost, path: "/votes/9", body: `{"vote":"fail"}`, userID: 42, err: errors.New("private database detail"), want: http.StatusInternalServerError, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &userTestVoteRepository{err: tc.err}
			recorder := serveUserTestVote(t, repo, tc.method, tc.path, tc.body, tc.userID, tc.unavailable)
			require.Equal(t, tc.want, recorder.Code, recorder.Body.String())
			require.Equal(t, tc.calls, repo.calls)
			require.NotContains(t, recorder.Body.String(), "private database detail")
			if tc.calls > 0 {
				require.Equal(t, tc.userID, repo.userID)
			}
		})
	}
}

func TestUserTestVoteListUsesAuthenticatedUserAndSanitizedResults(t *testing.T) {
	repo := &userTestVoteRepository{rows: []*service.ScheduledTestVoteResult{{
		Result: &service.ScheduledTestResult{ID: 9, AccountName: "private-account", ErrorMessage: "private-upstream-error"},
		Voting: service.ScheduledTestVotingSummary{Enabled: true, Open: true, PassCount: 3, MyVote: "pass"},
	}}}
	recorder := serveUserTestVote(t, repo, http.MethodGet, "/votes?user_id=777&group_id=888", "", 42, false)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, repo.calls)
	require.Equal(t, int64(42), repo.userID)
	var rows []*service.ScheduledTestVoteResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, int64(9), rows[0].Result.ID)
	require.Equal(t, 3, rows[0].Voting.PassCount)
	require.Equal(t, "pass", rows[0].Voting.MyVote)
	require.True(t, rows[0].Voting.Open)
	require.NotContains(t, recorder.Body.String(), "private-account")
	require.NotContains(t, recorder.Body.String(), "private-upstream-error")
}

func TestUserTestVoteListReturnsEmptyArray(t *testing.T) {
	repo := &userTestVoteRepository{}
	recorder := serveUserTestVote(t, repo, http.MethodGet, "/votes", "", 42, false)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `[]`, recorder.Body.String())
	require.Equal(t, 1, repo.calls)
}

func TestUserTestVoteUsesAuthenticatedIdentity(t *testing.T) {
	for _, vote := range []string{"pass", "fail"} {
		t.Run(vote, func(t *testing.T) {
			repo := &userTestVoteRepository{row: &service.ScheduledTestVoteResult{
				Result: &service.ScheduledTestResult{ID: 9, AccountName: "private-account", ErrorMessage: "private-upstream-error"},
				Voting: service.ScheduledTestVotingSummary{Enabled: true, Open: true, MyVote: vote},
			}}
			body := fmt.Sprintf(`{"vote":%q,"user_id":777,"result_id":888,"account_id":999,"role":"admin"}`, vote)
			recorder := serveUserTestVote(t, repo, http.MethodPost, "/votes/9?user_id=777", body, 42, false)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, 1, repo.calls)
			require.Equal(t, int64(42), repo.userID, "identity comes only from authenticated middleware")
			require.Equal(t, int64(9), repo.resultID, "the URL identifies the result; injected body fields cannot replace it")
			require.Equal(t, vote, repo.vote)
			var row service.ScheduledTestVoteResult
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &row))
			require.Equal(t, int64(9), row.Result.ID)
			require.Equal(t, vote, row.Voting.MyVote)
			require.NotContains(t, recorder.Body.String(), "private-account")
			require.NotContains(t, recorder.Body.String(), "private-upstream-error")
		})
	}
}
