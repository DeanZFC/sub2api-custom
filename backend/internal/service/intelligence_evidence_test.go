//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Drive the real scheduler, normal OAuth request builder, SSE reader,
// content contract, result repository and budget boundary using a local transport.
func TestIntelligenceFourChecksAndArtworkOnWire(t *testing.T) {
	const marker = "opaque-value-should-never-be-persisted"
	raw, err := os.ReadFile("../pkg/modelfingerprint/testdata/astra-high.txt")
	require.NoError(t, err)
	completed := func(txt string, turn bool) *http.Response {
		delta, _ := json.Marshal(map[string]string{"type": "response.output_text.delta", "delta": txt})
		resp := newJSONResponse(200, fmt.Sprintf("data: %s\n\ndata: {\"type\":\"response.completed\"}\n\n", delta))
		if turn {
			resp.Header.Set(openAICodexTurnStateHeader, marker)
		}
		return resp
	}
	account := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1, Credentials: map[string]any{"access_token": "example"}}
	up := &queuedHTTPUpstream{responses: []*http.Response{completed(string(raw), false), completed("OK", true), completed("OK", true), completed("21", false), completed(contentTestHTML, false)}}
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
	require.Len(t, e.Checks, 4)
	for _, c := range e.Checks {
		require.Equal(t, "pass", c.Status, c.Key)
	}
	require.Equal(t, contentTestHTML, result.OutputHTML)
	require.Len(t, result.ExecutionInfo.Attempts, 5)
	require.Len(t, up.requests, 5)
	require.Equal(t, 5, sent)
	require.Len(t, budgets.outcomes, 5)
	require.NotEmpty(t, up.requests[1].Header.Get("session_id"))
	require.Equal(t, up.requests[1].Header.Get("session_id"), up.requests[2].Header.Get("session_id"))
	require.Empty(t, up.requests[1].Header.Get(openAICodexTurnStateHeader))
	require.Equal(t, marker, up.requests[2].Header.Get(openAICodexTurnStateHeader))
	require.Empty(t, up.requests[3].Header.Get(openAICodexTurnStateHeader))
	for i, r := range up.requests {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, "gpt-6.2-astra", gjson.GetBytes(body, "model").String())
		require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
		if i == 2 {
			require.Equal(t, "OK", gjson.GetBytes(body, "input.1.content").String())
		}
		if i == 4 {
			require.Equal(t, "鹈鹕骑自行车", gjson.GetBytes(body, "input.0.content.0.text").String())
		}
	}
	serialized, err := json.Marshal(results.previous)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), marker)
}

type intelligenceStreakRepo struct {
	ScheduledTestResultRepository
	prior int
}

func (r intelligenceStreakRepo) PreviousIntelligence429(context.Context, *ScheduledTestResult, string) (int, error) {
	return r.prior, nil
}

func TestIntelligenceDistinctFailuresAnd429Rounds(t *testing.T) {
	for _, tc := range []struct {
		prior      int
		marker     string
		http       int
		candy      float64
		want, rate string
	}{
		{0, "", 429, 0, "unknown", "unknown"}, {1, "", 429, 0, "unknown", "unknown"}, {2, "", 429, 0, "suspect", "fail"},
		{2, "changed", 429, 0, "confirmed", "fail"}, {2, "changed", 200, 19, "confirmed", "pass"},
		{2, "", 200, 19, "suspect", "pass"}, {2, "", 503, 0, "unknown", "unknown"},
	} {
		runner := &ScheduledTestRunnerService{scheduledSvc: &ScheduledTestService{resultRepo: intelligenceStreakRepo{prior: tc.prior}}}
		plan := &ScheduledTestPlan{ModelID: "unsupported", Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", CandyPrompt: "Q"}}}
		res := runner.runIntelligence(context.Background(), plan, &ScheduledTestResult{ID: 11}, "art", func(ctx context.Context, prompt, kind string, _ int) *ScheduledTestResult {
			if turn, _ := ctx.Value(intelligenceTurnKey{}).(*intelligenceTurn); turn != nil {
				turn.observed = true
				turn.received = "start"
				if turn.sent != "" {
					turn.received = tc.marker
				}
				return &ScheduledTestResult{Status: "success", ResponseText: "OK"}
			}
			require.Equal(t, "number", kind, "no artwork is due")
			status := "failed"
			if tc.http == 200 {
				status = "success"
			}
			return &ScheduledTestResult{Status: status, OutputNumeric: &tc.candy, ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{{HTTPStatus: 429}, {HTTPStatus: 429}, {HTTPStatus: tc.http}}}}
		})
		ev := res.ExecutionInfo.Intelligence
		require.Equal(t, tc.want, ev.AutoVerdict)
		require.Equal(t, tc.rate, ev.Checks[3].Status)
		if tc.marker == "" {
			require.Equal(t, "pass", ev.Checks[1].Status, "successful continuation may omit a new marker")
		}
		require.Equal(t, "skipped", ev.Artwork)
	}
}

func TestIntelligenceMissingArtworkRetriesOnlyOriginalWork(t *testing.T) {
	p := &ScheduledTestPlan{Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", ArtworkPrompt: "frozen art"}}}
	ev := emptyIntelligenceEvidence("v")
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
}

func (q intelligenceRetryQueueStub) DueIntelligenceArtwork(context.Context, time.Time) ([]int64, error) {
	return []int64{q.previous.ID}, nil
}

func TestIntelligenceDueArtworkRunsInOriginalRow(t *testing.T) {
	id := int64(12)
	account := &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1, Credentials: map[string]any{"access_token": "example"}}
	start := time.Now().Add(-time.Hour)
	old := &ScheduledTestResult{ID: 42, PlanID: 4, RunID: "original", ModelID: "gpt-6.2-astra", ReasoningEffort: "high", AccountID: &id, Status: "failed", TargetMode: "all_accounts", StartedAt: start, FinishedAt: start.Add(time.Minute), ExecutionInfo: &ScheduledTestExecutionInfo{Intelligence: &IntelligenceEvidence{PolicyVersion: "v", AutoVerdict: "pass", Artwork: "missing", ArtworkAttempts: 1}, Attempts: []ScheduledTestRequestAttempt{{HTTPStatus: 503}}}}
	q := intelligenceRetryQueueStub{&retryResultRepoStub{previous: old, updated: make(chan *ScheduledTestResult, 1)}}
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

func TestIntelligenceCancellationAndMissingContinuation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	plan := &ScheduledTestPlan{Protection: ScheduledTestProtectionConfig{Intelligence: &IntelligencePolicy{Version: "v", CandyPrompt: "candy"}}, ModelID: "unregistered"}
	runner := &ScheduledTestRunnerService{}
	r := runner.runIntelligence(ctx, plan, nil, "art", func(c context.Context, _, kind string, _ int) *ScheduledTestResult {
		if t, _ := c.Value(intelligenceTurnKey{}).(*intelligenceTurn); t != nil {
			t.observed = true
			t.received = ""
			return &ScheduledTestResult{Status: "success"}
		}
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
