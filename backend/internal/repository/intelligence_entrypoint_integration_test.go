//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type intelligenceTestReply struct {
	status int
	text   string
}

type intelligenceIntegrationUpstream struct {
	mu      sync.Mutex
	calls   map[int64]int
	replies map[int64][]intelligenceTestReply
}

func (up *intelligenceIntegrationUpstream) Do(req *http.Request, _ string, account int64, _ int) (*http.Response, error) {
	up.mu.Lock()
	index := up.calls[account]
	up.calls[account]++
	up.mu.Unlock()
	if index >= len(up.replies[account]) {
		return nil, fmt.Errorf("unexpected probe %d for account %d", index+1, account)
	}
	reply := up.replies[account][index]
	header := make(http.Header)
	body := reply.text
	if reply.status == http.StatusOK {
		header.Set("Content-Type", "text/event-stream")
		data, err := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": reply.text})
		if err != nil {
			return nil, err
		}
		body = fmt.Sprintf("data: %s\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":10}}}\n\n", data)
	} else {
		header.Set("Content-Type", "application/json")
	}
	return &http.Response{Request: req, StatusCode: reply.status, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}
func (up *intelligenceIntegrationUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrent int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return up.Do(req, proxy, account, concurrent)
}

// Exercise the minimal administrator request through real production services and
// PostgreSQL. Only upstream HTTP responses are simulated.
func TestIntelligenceStandardAdminCreateAndRun(t *testing.T) {
	ctx := context.Background()
	db, client := repository.IntegrationTestDatabase()
	id := func(q string, args ...any) int64 {
		t.Helper()
		var v int64
		require.NoError(t, db.QueryRowContext(ctx, q, args...).Scan(&v))
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err)
	}
	group := func(exclusive bool) int64 {
		return id(`INSERT INTO groups(name,is_exclusive,platform) VALUES($1,$2,'openai') RETURNING id`, uuid.NewString(), exclusive)
	}
	account := func() int64 {
		return id(`INSERT INTO accounts(name,type,platform,credentials) VALUES($1,'oauth','openai','{"access_token":"fixture-only"}') RETURNING id`, uuid.NewString())
	}
	g1, g2 := group(false), group(true)
	both, private, imageOnly := account(), account(), account()
	user := id(`INSERT INTO users(email,password_hash) VALUES($1,'test') RETURNING id`, uuid.NewString()+"@example.invalid")
	t.Cleanup(func() {
		accounts := pq.Array([]int64{both, private, imageOnly})
		exec(`WITH used AS (
 DELETE FROM quality_budget_days WHERE account_id=ANY($1) RETURNING day,requests,tokens,unknown_requests
), totals AS (SELECT day,sum(requests) requests,sum(tokens) tokens,sum(unknown_requests) unknown_requests FROM used GROUP BY day)
 UPDATE quality_budget_days d SET requests=d.requests-t.requests,tokens=d.tokens-t.tokens,unknown_requests=d.unknown_requests-t.unknown_requests
 FROM totals t WHERE d.day=t.day AND d.account_id=0`, accounts)
		exec(`DELETE FROM quality_probe_attempts WHERE account_id=ANY($1)`, accounts)
		exec(`DELETE FROM users WHERE id=$1`, user)
		exec(`DELETE FROM accounts WHERE id=ANY($1)`, accounts)
		exec(`DELETE FROM groups WHERE id=ANY($1)`, pq.Array([]int64{g1, g2}))
	})
	exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$4),($1,$5),($2,$5),($3,$4),($3,$5)`, both, private, imageOnly, g1, g2)
	// Shared API-key accounts must pass the same two checks as direct OAuth accounts.
	exec(`UPDATE accounts SET type='apikey', credentials='{"api_key":"fixture-only"}' WHERE id=$1`, both)
	exec(`UPDATE accounts SET credentials = credentials || '{"model_mapping":{"gpt-image-1":"gpt-image-1"}}'::jsonb WHERE id=$1`, imageOnly)
	exec(`UPDATE groups SET model_allowlist='{"enabled":true,"models":["gpt-6.2-astra","gpt-6.2-sol"]}' WHERE id=$1`, g1)
	exec(`UPDATE groups SET model_allowlist='{"enabled":true,"models":["gpt-6.2-astra"]}' WHERE id=$1`, g2)
	plans := repository.NewScheduledTestPlanRepository(db)
	results := repository.NewScheduledTestResultRepository(db)
	svc := service.ProvideScheduledTestService(plans, results, repository.NewScheduledTestDefinitionRepository(db))
	cfg := &config.Config{}
	accounts := repository.NewAccountRepository(client, db, nil)
	correct := intelligenceTestReply{http.StatusOK, "21"}
	wrong := intelligenceTestReply{http.StatusOK, "19"}
	art := intelligenceTestReply{http.StatusOK, `<html><body><svg><circle r="10"/></svg></body></html>`}
	limited := intelligenceTestReply{http.StatusTooManyRequests, `{"error":{"message":"rate limited"}}`}
	up := &intelligenceIntegrationUpstream{calls: map[int64]int{}, replies: map[int64][]intelligenceTestReply{
		both:    {correct, art, limited, limited, limited},
		private: {wrong, correct, art, correct, art},
	}}
	at := service.ProvideAccountTestService(accounts, repository.NewQualityOperationsRepository(db), nil, nil, nil, nil, up, cfg, nil, nil, service.NewSettingService(repository.NewSettingRepository(client), cfg), nil)
	t.Cleanup(at.StopAstraScheduling)
	runner := service.NewScheduledTestRunnerService(plans, svc, at, accounts, nil, cfg)
	t.Cleanup(runner.Stop)
	ran := make(chan struct{})
	svc.SetRunFunc(func(ctx context.Context, plan *service.ScheduledTestPlan) { runner.RunPlanNow(ctx, plan); close(ran) })
	router := gin.New()
	handler := admin.NewScheduledTestHandler(svc)
	router.POST("/admin/scheduled-test-plans", handler.Create)
	create := func(model string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"group_ids":[%d,%d],"model_ids":[%q],"protection":{"intelligence":{"auto_isolate":true}}}`, g1, g2, model)
		req := httptest.NewRequest(http.MethodPost, "/admin/scheduled-test-plans", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, http.StatusBadRequest, create("gpt-6.2-sol").Code)
	w := create("gpt-6.2-astra")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var plan service.ScheduledTestPlan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &plan))
	require.Positive(t, plan.ID)
	t.Cleanup(func() { exec(`DELETE FROM scheduled_test_plans WHERE id=$1`, plan.ID) })
	require.NotNil(t, plan.Protection.Intelligence)
	require.NotEmpty(t, plan.Protection.Intelligence.Version)
	require.Equal(t, 60, plan.Protection.Intelligence.IntervalMinutes)
	require.Equal(t, 2, plan.Protection.Intelligence.RecoveryPasses)
	require.Equal(t, "high", plan.ReasoningEffort)
	require.NoError(t, svc.RunNow(ctx, plan.ID))
	select {
	case <-ran:
	case <-time.After(30 * time.Second):
		t.Fatal("runner did not finish")
	}
	overview, err := svc.IntelligenceOverview(ctx, plan.ID)
	require.NoError(t, err)
	require.Len(t, overview.Results, 2, "%+v", overview)
	require.Len(t, overview.States, 1, "%+v", overview)
	require.Equal(t, private, overview.States[0].AccountID)
	require.True(t, overview.States[0].Blocked)
	for _, row := range overview.Results {
		require.NotNil(t, row.Evidence)
		require.Len(t, row.Evidence.Checks, 2)
		require.Equal(t, "candy", row.Evidence.Checks[0].Key)
		require.Equal(t, "http_429", row.Evidence.Checks[1].Key)
	}
	up.mu.Lock()
	counts := map[int64]int{both: up.calls[both], private: up.calls[private], imageOnly: up.calls[imageOnly]}
	up.mu.Unlock()
	require.Equal(t, map[int64]int{both: 2, private: 1, imageOnly: 0}, counts, "%+v", overview)
	pub, err := svc.PublicIntelligence()
	require.NoError(t, err)
	publicRound, err := pub.PublicIntelligenceRounds(ctx, user, g1, "")
	require.NoError(t, err)
	require.Len(t, publicRound, 1)
	require.Equal(t, "pass", publicRound[0].Verdict)
	require.Equal(t, 1, publicRound[0].Accounts)
	hidden, err := pub.PublicIntelligenceRounds(ctx, user, g2, "")
	require.NoError(t, err)
	require.Empty(t, hidden)
	exec(`INSERT INTO user_allowed_groups(user_id,group_id) VALUES($1,$2)`, user, g2)
	merged, err := pub.PublicIntelligenceRounds(ctx, user, g2, "")
	require.NoError(t, err)
	require.Len(t, merged, 1)
	require.Equal(t, 2, merged[0].Accounts)
	require.Equal(t, 1, merged[0].Counts.Pass)
	require.Equal(t, 1, merged[0].Counts.Degraded)
	require.Equal(t, "degraded", merged[0].Verdict)
	var artwork int64
	for _, r := range overview.Results {
		if r.AccountID == both {
			artwork = r.ResultID
		}
	}
	_, err = pub.PublicIntelligenceArtwork(ctx, user, g2, artwork)
	require.NoError(t, err)
	exec(`DELETE FROM user_allowed_groups WHERE user_id=$1 AND group_id=$2`, user, g2)
	_, err = pub.PublicIntelligenceArtwork(ctx, user, g2, artwork)
	require.ErrorIs(t, err, sql.ErrNoRows)
	// An administrator can correct the automatically accepted artwork. Both
	// the same public round and this target's scheduling reflect the decision.
	require.NoError(t, svc.ReviewIntelligenceArt(ctx, artwork, user, "fail", "invalid drawing"))
	reviewed, err := pub.PublicIntelligenceRounds(ctx, user, g1, "")
	require.NoError(t, err)
	require.Equal(t, "degraded", reviewed[0].Verdict)
	require.EqualValues(t, 1, id(`SELECT count(*) FROM accounts WHERE id=$1 AND status='quality_paused'`, both))
	require.NoError(t, svc.ReviewIntelligenceArt(ctx, artwork, user, "pass", "corrected review"))
	corrected, err := pub.PublicIntelligenceRounds(ctx, user, g1, "")
	require.NoError(t, err)
	require.Equal(t, "pass", corrected[0].Verdict)
	require.EqualValues(t, 1, id(`SELECT count(*) FROM accounts WHERE id=$1 AND status='active'`, both))
	require.EqualValues(t, 1, id(`SELECT count(*) FROM accounts WHERE id=$1 AND status='quality_paused'`, private))
	require.EqualValues(t, 3, id(`SELECT count(*) FROM quality_probe_attempts WHERE intelligence_probe AND account_id=ANY($1)`, pq.Array([]int64{both, private, imageOnly})))

	// Advance only the persisted due time to exercise the real runner without
	// a 30-minute wait. Both the delay and account status are verified first.
	checkState := func(account int64, verdict string, passes, minutes int, status string) {
		t.Helper()
		var actualVerdict, actualStatus string
		var actualPasses int
		var delay sql.NullFloat64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT s.verdict,s.passes,a.status,extract(epoch FROM s.next_check-o.finished_at)
 FROM intelligence_model_states s JOIN accounts a ON a.id=s.account_id JOIN intelligence_observations o ON o.result_id=s.last_result_id
 WHERE s.plan_id=$1 AND s.account_id=$2 AND s.model_key=$3`, plan.ID, account, plan.ModelID).Scan(&actualVerdict, &actualPasses, &actualStatus, &delay))
		require.Equal(t, verdict, actualVerdict)
		require.Equal(t, passes, actualPasses)
		require.Equal(t, status, actualStatus)
		require.Equal(t, minutes > 0, delay.Valid)
		if delay.Valid {
			require.InDelta(t, minutes*60, delay.Float64, 0.01)
		}
	}
	advance := func() {
		exec(`UPDATE intelligence_model_states SET next_check=clock_timestamp() WHERE plan_id=$1 AND next_check IS NOT NULL`, plan.ID)
	}
	run := func(retest bool, want ...service.IntelligenceTarget) {
		t.Helper()
		current, err := svc.GetPlan(ctx, plan.ID)
		require.NoError(t, err)
		if retest {
			current.IntelligenceRetest = true
			current.IntelligenceTargets, err = plans.(service.IntelligenceRetestRepository).DueIntelligenceTargets(ctx, plan.ID, time.Now())
			require.NoError(t, err)
			require.ElementsMatch(t, want, current.IntelligenceTargets)
		}
		runner.RunPlanNow(ctx, current)
	}

	checkState(private, "confirmed", 0, 30, "quality_paused")
	advance()
	run(false)
	checkState(private, "pass", 1, 5, "quality_paused")
	checkState(both, "suspect", 0, 5, "active")
	advance()
	run(true, service.IntelligenceTarget{AccountID: private, Model: plan.ModelID}, service.IntelligenceTarget{AccountID: both, Model: plan.ModelID})
	checkState(private, "pass", 2, 0, "active")
	checkState(both, "suspect", 0, 5, "active")
	advance()
	run(true, service.IntelligenceTarget{AccountID: both, Model: plan.ModelID})
	checkState(both, "confirmed", 0, 30, "quality_paused")
	require.EqualValues(t, 3, id(`SELECT count(DISTINCT run_id) FROM scheduled_test_results
 WHERE plan_id=$1 AND account_id=$2 AND execution_info->'intelligence'->>'candy_http_status'='429'`, plan.ID, both))
}
