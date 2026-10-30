//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type strictRequestAccountRepo struct {
	openAIImagesFailoverAccountRepo
}

func (r strictRequestAccountRepo) GetByIDs(ctx context.Context, ids []int64) ([]*service.Account, error) {
	rows := make([]*service.Account, 0, len(ids))
	for _, id := range ids {
		account, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		rows = append(rows, account)
	}
	return rows, nil
}

func (r strictRequestAccountRepo) ListByGroup(_ context.Context, groupID int64) ([]service.Account, error) {
	rows := make([]service.Account, 0, len(r.accounts))
	for _, account := range r.accounts {
		for _, id := range account.GroupIDs {
			if id == groupID {
				rows = append(rows, account)
				break
			}
		}
	}
	return rows, nil
}

type strictRequestUpstream struct {
	service.HTTPUpstream
	mu         sync.Mutex
	accountIDs []int64
	winner     int64
}

func (u *strictRequestUpstream) Do(_ *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	winner := u.winner
	u.mu.Unlock()
	status := http.StatusForbidden
	body := `{"error":{"message":"account unavailable"}}`
	if accountID == 5 {
		status = http.StatusServiceUnavailable
	}
	if accountID == winner {
		status = http.StatusOK
		body = `{"id":"resp_strict_success","object":"response","status":"completed","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(body))}, nil
}

func (u *strictRequestUpstream) reset(winner int64) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.accountIDs = nil
	u.winner = winner
}

func (u *strictRequestUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestStrictFailoverResponsesTraversesTiersWithinOneRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, winner := range []int64{5, 6} {
		t.Run(map[int64]string{5: "second_tier", 6: "implicit_final_tier"}[winner], func(t *testing.T) {
			const groupID int64 = 3131
			group := &service.Group{
				ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true,
				RateMultiplier: 1, ProfitControlEnabled: true, ProfitMinMargin: 0.2, ProfitSafetyBuffer: 0.1,
				StrictFailover: service.StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{1, 2, 3, 4}, {5}}},
			}
			repo := strictRequestAccountRepo{}
			for id := int64(1); id <= 6; id++ {
				rate := 0.5
				repo.accounts = append(repo.accounts, service.Account{
					ID: id, Name: "strict-request-account", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
					Status: service.StatusActive, Schedulable: true, Priority: int(id), RateMultiplier: &rate,
					GroupIDs: []int64{groupID}, AccountGroups: []service.AccountGroup{{AccountID: id, GroupID: groupID}},
					Credentials: map[string]any{"access_token": "test-token", "pool_mode_retry_count": float64(0)},
				})
			}
			upstream := &strictRequestUpstream{winner: winner}
			handler := newOpenAIResponsesFailoverTestHandler(t, upstream, repo)
			handler.maxAccountSwitches = 1 // Four primary accounts must still be exhausted.
			request := func() {
				c, rec := newOpenAIResponsesFailoverTestContext(t, nil)
				key := c.MustGet(string(middleware.ContextKeyAPIKey)).(*service.APIKey)
				key.Group = group
				ctx := context.WithValue(c.Request.Context(), ctxkey.Group, group)
				c.Request = c.Request.WithContext(service.WithAPIKeyGroupFallbackRouting(ctx, key))
				handler.Responses(c)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Equal(t, "completed", gjson.GetBytes(rec.Body.Bytes(), "status").String(), rec.Body.String())
			}
			request()
			calls := upstream.calls()
			require.Len(t, calls, int(winner))
			require.ElementsMatch(t, []int64{1, 2, 3, 4}, calls[:4], "all primary credentials must fail before another tier is attempted")
			require.Equal(t, int64(5), calls[4])
			if winner == 6 {
				require.Equal(t, int64(6), calls[5], "the implicit tier follows the failed second tier")
			}
			// Per-request failure evidence cannot pin the next request to a backup.
			upstream.reset(1)
			request()
			newRequestCalls := upstream.calls()
			require.NotEmpty(t, newRequestCalls)
			require.LessOrEqual(t, len(newRequestCalls), 4)
			for _, id := range newRequestCalls {
				require.Contains(t, []int64{1, 2, 3, 4}, id, "a new request must restart within the primary tier")
			}
			require.Equal(t, int64(1), newRequestCalls[len(newRequestCalls)-1])
		})
	}
}
