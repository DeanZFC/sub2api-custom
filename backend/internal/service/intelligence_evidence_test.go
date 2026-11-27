//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Drive the real scheduler, normal OAuth request builder, SSE reader,
// content contract, result repository and budget boundary using a local transport.
func TestIntelligenceCandyAndArtworkOnWire(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(accountType, func(t *testing.T) {
			completed := func(txt string) *http.Response {
				delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": txt})
				return newJSONResponse(200, fmt.Sprintf("data: %s\n\ndata: {\"type\":\"response.completed\"}\n\n", delta))
			}
			account := &Account{ID: 19, Platform: PlatformOpenAI, Type: accountType, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "example", "api_key": "example"}}
			up := &queuedHTTPUpstream{responses: []*http.Response{completed("21"), completed(contentTestHTML)}}
			budgets := &qualityOperationsStub{}
			sent := 0
			budgets.reserve = func(_ context.Context, id, tokens int64, _ bool, session string) (*QualityProbeReservation, error) {
				require.Equal(t, account.ID, id)
				require.Equal(t, "intelligence:round", session)
				sent++
				return &QualityProbeReservation{ID: int64(sent), AccountID: id}, nil
			}
			results := &retryResultRepoStub{}
			accounts := &modelIdentityAccountRepo{account: account}
			runner := &ScheduledTestRunnerService{accountTestSvc: &AccountTestService{accountRepo: accounts, httpUpstream: up, cfg: &config.Config{}, qualityOperations: budgets}, scheduledSvc: &ScheduledTestService{resultRepo: results}}
			plan := &ScheduledTestPlan{ID: 3, ModelID: "gpt-6.2-astra", ReasoningEffort: "high", Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", CandyPrompt: "candy question", ArtworkPrompt: "鹈鹕骑摩托车"}}}
			pending := &ScheduledTestResult{ID: 91, RunID: "round", StartedAt: time.Now(), AccountID: &account.ID}
			ctx := context.WithValue(context.Background(), qualityRunKey{}, "intelligence:round")
			result := runner.runAccountWithResult(ctx, plan, account.ID, "discarded legacy prompt", "html", pending)
			require.NotNil(t, result)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			e := result.ExecutionInfo.Intelligence
			require.NotNil(t, e)
			require.Equal(t, "pass", e.AutoVerdict)
			require.Equal(t, "pass", e.FinalVerdict(""))
			require.Equal(t, "confirmed", e.FinalVerdict("fail"))
			require.Equal(t, "default", e.Artwork)
			require.Equal(t, []IntelligenceCheck{{"candy", "pass", "答案为 21"}, {"http_429", "pass", "本轮请求成功，连续限流 0/3"}}, e.Checks)
			require.Equal(t, contentTestHTML, result.OutputHTML)
			require.Len(t, result.ExecutionInfo.Attempts, 2)
			require.Len(t, up.requests, 2)
			require.Equal(t, 2, sent)
			require.Len(t, budgets.outcomes, 2)
			for i, prompt := range []string{"candy question", "鹈鹕骑自行车"} {
				body, err := io.ReadAll(up.requests[i].Body)
				require.NoError(t, err)
				require.Equal(t, "gpt-6.2-astra", gjson.GetBytes(body, "model").String())
				require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
				require.Equal(t, prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
			}
		})
	}
}

type intelligenceStreakRepo struct {
	ScheduledTestResultRepository
	prior int
}

func (r intelligenceStreakRepo) PreviousIntelligence429(context.Context, *ScheduledTestResult, string) (int, error) {
	return r.prior, nil
}

func TestIntelligenceCandyVerdictAnd429Rounds(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		prior, http                      int
		candy                            string
		wantVerdict, wantCandy, wantRate string
	}{
		{"first limited round", 0, 429, "", "suspect", "unknown", "unknown"},
		{"second limited round", 1, 429, "", "suspect", "unknown", "unknown"},
		{"third limited round", 2, 429, "", "confirmed", "unknown", "fail"},
		{"one wrong answer", 2, 200, "29", "confirmed", "fail", "pass"},
		{"malformed answer", 2, 200, "unparseable", "unknown", "unknown", "pass"},
		{"upstream error", 2, 503, "", "unknown", "unknown", "unknown"},
		{"missing HTTP evidence", 2, 0, "29", "unknown", "unknown", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &ScheduledTestRunnerService{scheduledSvc: &ScheduledTestService{resultRepo: intelligenceStreakRepo{prior: tc.prior}}}
			plan := &ScheduledTestPlan{ModelID: "model", Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", CandyPrompt: "Q"}}}
			res := runner.runIntelligence(context.Background(), plan, &ScheduledTestResult{ID: 11}, "art", func(_ context.Context, prompt, kind string, _ int) *ScheduledTestResult {
				require.Equal(t, "Q", prompt)
				require.Equal(t, "number", kind, "no artwork is due")
				status := "failed"
				if tc.http == 200 || tc.http == 0 {
					status = "success"
				}
				result := &ScheduledTestResult{Status: status, ResponseText: tc.candy, ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{{HTTPStatus: 429}, {HTTPStatus: 429}, {HTTPStatus: tc.http}}}}
				runner.applyOutputContract(result, kind)
				return result
			})
			ev := res.ExecutionInfo.Intelligence
			require.Equal(t, tc.wantVerdict, ev.AutoVerdict)
			require.Equal(t, tc.wantCandy, ev.Checks[0].Status)
			require.Equal(t, tc.wantRate, ev.Checks[1].Status)
			if tc.http == 429 {
				require.Equal(t, tc.prior+1, ev.Consecutive429)
				require.Contains(t, ev.Checks[1].Detail, fmt.Sprintf("%d/3", ev.Consecutive429))
			} else {
				require.Zero(t, ev.Consecutive429)
			}
			require.Equal(t, "skipped", ev.Artwork)
		})
	}
}

func TestIntelligenceMissingArtworkRetriesOnlyOriginalWork(t *testing.T) {
	p := &ScheduledTestPlan{Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", ArtworkPrompt: "frozen art"}}}
	ev := &IntelligenceEvidence{PolicyVersion: "v", Checks: []IntelligenceCheck{
		{Key: "fingerprint"}, {Key: "codex_state"}, {Key: "candy"}, {Key: "http_429"},
	}}
	for i := range ev.Checks {
		ev.Checks[i].Status = "pass"
	}
	ev.AutoVerdict = "pass"
	ev.Artwork = "missing"
	ev.ArtworkAttempts = 1
	old := &ScheduledTestResult{ID: 2, ExecutionInfo: &ScheduledTestExecutionInfo{Intelligence: ev}}
	runner := &ScheduledTestRunnerService{}
	for _, ok := range []bool{false, true} {
		n := 0
		r := runner.runIntelligence(context.Background(), p, old, "legacy", func(_ context.Context, q, kind string, _ int) *ScheduledTestResult {
			n++
			require.Equal(t, "frozen art", q)
			require.Equal(t, "html", kind)
			if ok {
				return &ScheduledTestResult{Status: "success", OutputHTML: contentTestHTML}
			}
			return &ScheduledTestResult{Status: "failed"}
		})
		require.Equal(t, 1, n)
		require.Equal(t, "missing", ev.Artwork)
		fresh := r.ExecutionInfo.Intelligence
		require.Equal(t, ev.Checks, fresh.Checks)
		require.Equal(t, 2, fresh.ArtworkAttempts)
		if ok {
			require.Equal(t, "pass", fresh.FinalVerdict(""))
			require.Nil(t, fresh.ArtworkRetryAt)
		} else {
			require.Equal(t, "unknown", fresh.FinalVerdict(""))
			require.NotNil(t, fresh.ArtworkRetryAt)
		}
	}
	require.False(t, retryableIntelligenceArtwork(old, "new-policy"))
}

type intelligenceRetryQueueStub struct {
	*retryResultRepoStub
	deferred map[int64]time.Time
}

func (q intelligenceRetryQueueStub) DueIntelligenceArtwork(context.Context, time.Time) ([]int64, error) {
	return []int64{q.previous.ID}, nil
}

func (q intelligenceRetryQueueStub) DeferIntelligenceArtwork(_ context.Context, id int64, _ time.Time, next time.Time) error {
	q.deferred[id] = next
	return nil
}

func TestIntelligenceDueArtworkRunsInOriginalRow(t *testing.T) {
	id := int64(12)
	account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "example"}}
	start := time.Now().Add(-time.Hour)
	old := &ScheduledTestResult{ID: 42, PlanID: 4, RunID: "original", ModelID: "gpt-6.2-astra", ReasoningEffort: "high", AccountID: &id, Status: "failed", TargetMode: "all_accounts", StartedAt: start, FinishedAt: start.Add(time.Minute), ExecutionInfo: &ScheduledTestExecutionInfo{Intelligence: &IntelligenceEvidence{PolicyVersion: "v", AutoVerdict: "pass", Artwork: "missing", ArtworkAttempts: 1}, Attempts: []ScheduledTestRequestAttempt{{HTTPStatus: 503}}}}
	q := intelligenceRetryQueueStub{retryResultRepoStub: &retryResultRepoStub{previous: old, updated: make(chan *ScheduledTestResult, 1)}, deferred: map[int64]time.Time{}}
	plan := &ScheduledTestPlan{ID: 4, ModelID: old.ModelID, ModelIDs: []string{old.ModelID}, Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", ArtworkPrompt: "生成自行车上的鹈鹕"}}}
	svc := NewScheduledTestService(retryPlanRepoStub{plan: plan}, q)
	accounts := &modelIdentityAccountRepo{account: account}
	up := &outputBudgetUpstream{body: `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"<svg><circle/></svg>"}]}]}`}
	ops := &qualityOperationsStub{reserve: func(_ context.Context, _, _ int64, automatic bool, session string) (*QualityProbeReservation, error) {
		if !automatic || !strings.HasPrefix(session, "intelligence-retest:") {
			return nil, fmt.Errorf("wrong retry reservation context")
		}
		return &QualityProbeReservation{ID: 5, AccountID: id}, nil
	}}
	runner := NewScheduledTestRunnerService(nil, svc, &AccountTestService{accountRepo: accounts, httpUpstream: up, qualityOperations: ops, cfg: &config.Config{}}, accounts, nil, nil)
	svc.SetRetryFunc(runner.RetryAccount)
	now := time.Now()
	account.Credentials["model_mapping"] = map[string]any{"gpt-image-2": "gpt-image-2"}
	_, err := svc.RetryResult(context.Background(), old.ID)
	require.ErrorIs(t, err, errIntelligenceTargetUnavailable)
	require.Empty(t, q.deferred, "manual validation leaves the queue untouched")
	runner.retryDueIntelligenceArt(context.Background(), now)
	require.True(t, q.deferred[old.ID].After(now))
	require.Empty(t, up.requests)
	delete(account.Credentials, "model_mapping")
	runner.retryDueIntelligenceArt(context.Background(), time.Now())
	select {
	case completed := <-q.updated:
		require.Equal(t, "success", completed.Status, completed.ErrorMessage)
		require.Equal(t, old.ID, completed.ID)
		require.Equal(t, old.RunID, completed.RunID)
		require.Equal(t, old.StartedAt, completed.StartedAt)
		require.Len(t, completed.ExecutionInfo.Attempts, 2)
		require.Equal(t, 2, completed.ExecutionInfo.Intelligence.ArtworkAttempts)
		require.Len(t, up.requests, 1)
		require.Len(t, ops.outcomes, 1)
	case <-time.After(2 * time.Second):
		t.Fatal("artwork retry did not finish")
	}
}

func TestIntelligenceCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	plan := &ScheduledTestPlan{Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", CandyPrompt: "candy"}}, ModelID: "unregistered"}
	runner := &ScheduledTestRunnerService{}
	r := runner.runIntelligence(ctx, plan, nil, "art", func(c context.Context, _, kind string, _ int) *ScheduledTestResult {
		cancel()
		wrong := 19.0
		return &ScheduledTestResult{Status: "success", OutputNumeric: &wrong, ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{{HTTPStatus: 200}}}}
	})
	require.Equal(t, "failed", r.Status)
	require.Equal(t, "unknown", r.ExecutionInfo.Intelligence.FinalVerdict(""))
	for _, check := range r.ExecutionInfo.Intelligence.Checks {
		require.Equal(t, "unknown", check.Status)
	}
}
