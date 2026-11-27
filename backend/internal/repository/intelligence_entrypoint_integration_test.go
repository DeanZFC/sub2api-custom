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
	"os"
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

type intelligenceIntegrationUpstream struct {
	mu              sync.Mutex
	calls           map[int64]int
	fingerprint     string
	degradedAccount int64
}

func (up *intelligenceIntegrationUpstream) Do(req *http.Request, _ string, account int64, _ int) (*http.Response, error) {
	up.mu.Lock()
	up.calls[account]++
	stage := up.calls[account]
	up.mu.Unlock()
	header := make(http.Header)
	header.Set("Content-Type", "text/event-stream")
	var answer string
	switch stage {
	case 1:
		answer = up.fingerprint
	case 2, 3:
		answer = "OK"
		marker := "fixture-state"
		if stage == 3 && account == up.degradedAccount {
			marker = "changed-state"
		}
		header.Set("X-Codex-Turn-State", marker)
	case 4:
		answer = "21"
		if account == up.degradedAccount {
			answer = "19"
		}
	case 5:
		answer = "<html><body><svg><circle r=\"10\"/></svg></body></html>"
	default:
		return nil, fmt.Errorf("unexpected probe %d", stage)
	}
	data, err := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": answer})
	if err != nil {
		return nil, err
	}
	events := fmt.Sprintf("data: %s\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":10}}}\n\n", data)
	return &http.Response{Request: req, StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(events))}, nil
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
	both, private := account(), account()
	user := id(`INSERT INTO users(email,password_hash) VALUES($1,'test') RETURNING id`, uuid.NewString()+"@example.invalid")
	t.Cleanup(func() {
		accounts := pq.Array([]int64{both, private})
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
	exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$3),($1,$4),($2,$4)`, both, private, g1, g2)
	exec(`UPDATE groups SET model_allowlist='{"enabled":true,"models":["gpt-6.2-astra","gpt-6.2-sol"]}' WHERE id=$1`, g1)
	exec(`UPDATE groups SET model_allowlist='{"enabled":true,"models":["gpt-6.2-astra"]}' WHERE id=$1`, g2)
	plans := repository.NewScheduledTestPlanRepository(db)
	results := repository.NewScheduledTestResultRepository(db)
	svc := service.ProvideScheduledTestService(plans, results, repository.NewScheduledTestDefinitionRepository(db))
	cfg := &config.Config{}
	accounts := repository.NewAccountRepository(client, db, nil)
	fp, err := os.ReadFile("../pkg/modelfingerprint/testdata/astra-high.txt")
	require.NoError(t, err)
	up := &intelligenceIntegrationUpstream{calls: map[int64]int{}, fingerprint: string(fp), degradedAccount: private}
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
	up.mu.Lock()
	counts := map[int64]int{both: up.calls[both], private: up.calls[private]}
	up.mu.Unlock()
	require.Equal(t, map[int64]int{both: 5, private: 4}, counts, "%+v", overview)
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
	require.EqualValues(t, 9, id(`SELECT count(*) FROM quality_probe_attempts WHERE intelligence_probe AND account_id=ANY($1)`, pq.Array([]int64{both, private})))
}
