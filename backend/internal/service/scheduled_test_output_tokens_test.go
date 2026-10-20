package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type outputBudgetRequest struct {
	accountID int64
	body      []byte
}

type outputBudgetUpstream struct {
	mu       sync.Mutex
	requests []outputBudgetRequest
	body     string
	respond  func(int64, []byte) *http.Response
}

func (u *outputBudgetUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.requests = append(u.requests, outputBudgetRequest{id, body})
	if u.respond != nil {
		return u.respond(id, body), nil
	}
	return costJSONResponse(200, u.body), nil
}

func (u *outputBudgetUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

const outputBudgetClaudeResponse = `{"type":"message","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`

func TestAccountTestConfiguredOutputLimitOnWire(t *testing.T) {
	for _, tc := range []struct {
		name, platform, model, field, response string
		responses                              bool
		legacy                                 int64
	}{
		{"claude", PlatformAnthropic, "claude-sonnet-4-6", "max_tokens", outputBudgetClaudeResponse, false, 8192},
		{"responses", PlatformOpenAI, "gpt-5.4", "max_output_tokens", `{"status":"completed","output":[]}`, true, 8192},
		{"chat", PlatformOpenAI, "gpt-4o", "max_tokens", `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`, false, 0},
		{"reasoning_chat", PlatformOpenAI, "gpt-5.4", "max_completion_tokens", `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`, false, 0},
		{"gemini", PlatformGemini, "gemini-2.5-flash", "generationConfig.maxOutputTokens", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 12, Platform: tc.platform, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: tc.responses}}
			upstream := &outputBudgetUpstream{body: tc.response}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			for _, limit := range []int{32768, 49152, 0} {
				ctx := withAccountTestMaxOutputTokens(context.Background(), limit)
				result, err := svc.RunTestBackgroundWithPromptAndReasoning(ctx, account.ID, tc.model, "Draw a pelican", "")
				require.NoError(t, err)
				require.Equal(t, "success", result.Status, result.ErrorMessage)
				want := int64(limit)
				if limit == 0 {
					want = tc.legacy
				}
				body := upstream.requests[len(upstream.requests)-1].body
				require.Equal(t, want, gjson.GetBytes(body, tc.field).Int(), string(body))
				if want == 0 {
					require.False(t, gjson.GetBytes(body, tc.field).Exists())
				}
			}
		})
	}
}

func TestScheduledTestOutputLimitsSurviveSnapshotsAndAutomaticRetries(t *testing.T) {
	for _, snapshot := range []bool{false, true} {
		t.Run(fmt.Sprintf("snapshot=%v", snapshot), func(t *testing.T) {
			results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 20)}
			var repo ScheduledTestResultRepository = results
			if snapshot {
				repo = &snapshotResultRepoStub{retryResultRepoStub: results, targets: []int64{12, 13}}
			}
			accounts := multiDefinitionAccountRepoStub{accounts: []Account{
				{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}},
				{ID: 13, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}},
			}}
			attempts := map[int64]int{}
			upstream := &outputBudgetUpstream{respond: func(id int64, body []byte) *http.Response {
				if gjson.GetBytes(body, "messages.0.content.0.text").String() == "pelican" {
					attempts[id]++
					if attempts[id] == 1 {
						return costJSONResponse(503, `{"error":{"message":"temporary server error"}}`)
					}
				}
				return costJSONResponse(200, outputBudgetClaudeResponse)
			}}
			runner := newScheduledTestExecutionRunner(repo, accounts)
			defer runner.Stop()
			runner.executionTimeout = time.Second
			runner.accountTestSvc.httpUpstream = upstream
			runner.accountTestSvc.cfg = &config.Config{}
			runner.scheduledSvc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, Prompt: "pelican", OutputKind: "text", MaxOutputTokens: 32768},
				2: {ID: 2, Enabled: true, Prompt: "candy", OutputKind: "text", MaxOutputTokens: 0},
			}})
			plan := scheduledTestExecutionPlan()
			plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			runner.RunPlanNow(ctx, plan)
			require.NoError(t, ctx.Err())
			require.Len(t, upstream.requests, 6, "two accounts, each with one retried Pelican call and one Candy call")
			for _, request := range upstream.requests {
				want := int64(8192)
				if gjson.GetBytes(request.body, "messages.0.content.0.text").String() == "pelican" {
					want = 32768
				}
				require.Equal(t, want, gjson.GetBytes(request.body, "max_tokens").Int())
			}
			completed := 0
			for len(results.updated) > 0 {
				result := <-results.updated
				if result.Status == "running" {
					continue
				}
				require.Equal(t, "success", result.Status, result.ErrorMessage)
				completed++
			}
			require.Equal(t, 4, completed)
		})
	}
}

func TestScheduledTestManualRetryUsesSavedOutputLimitAfterRequestCancellation(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}}
	previous := &ScheduledTestResult{ID: 9, PlanID: 7, TestDefinitionID: scheduledTestPtrInt64(1), AccountID: &account.ID, Status: "failed"}
	results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 4)}
	runner := newScheduledTestExecutionRunner(results, &modelIdentityAccountRepo{account: account})
	defer runner.Stop()
	runner.executionTimeout = time.Second
	runner.scheduledSvc.SetDefinitionRepository(scheduledTestDefinitionRepoStub{definition: &ScheduledTestDefinition{ID: 1, Enabled: true, Prompt: "pelican", OutputKind: "text", MaxOutputTokens: 49152}})
	upstream := &outputBudgetUpstream{body: outputBudgetClaudeResponse}
	runner.accountTestSvc.httpUpstream = upstream
	runner.accountTestSvc.cfg = &config.Config{}
	runner.workerSem = make(chan struct{}, 1)
	runner.workerSem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := runner.RetryAccount(ctx, scheduledTestExecutionPlan(), previous)
	require.NoError(t, err)
	cancel()
	<-runner.workerSem
	select {
	case completed := <-results.updated:
		require.Equal(t, "success", completed.Status, completed.ErrorMessage)
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not finish")
	}
	require.Len(t, upstream.requests, 1)
	require.EqualValues(t, 49152, gjson.GetBytes(upstream.requests[0].body, "max_tokens").Int())
}

func TestAccountTestExplicitLimitIsNotSilentlyRemoved(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	upstream := &outputBudgetUpstream{respond: func(int64, []byte) *http.Response {
		return costJSONResponse(400, `{"error":{"message":"Unsupported parameter: max_output_tokens"}}`)
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	result, err := svc.RunTestBackgroundWithPrompt(withAccountTestMaxOutputTokens(context.Background(), 32768), 12, "gpt-5.4", "pelican")
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, "max_output_tokens")
	require.Len(t, upstream.requests, 1)
}

func TestAntigravityAndGeminiConfiguredOutputLimits(t *testing.T) {
	svc := &AntigravityGatewayService{}
	claude, err := svc.buildClaudeTestRequest("project", "claude-sonnet-4-6", "pelican", 32768)
	require.NoError(t, err)
	gemini, err := svc.buildGeminiTestRequestWithPrompt("project", "gemini-2.5-flash", "pelican", 49152)
	require.NoError(t, err)
	require.EqualValues(t, 32768, gjson.GetBytes(claude, "request.generationConfig.maxOutputTokens").Int())
	require.EqualValues(t, 49152, gjson.GetBytes(gemini, "request.generationConfig.maxOutputTokens").Int())
	var imagePayload map[string]any
	require.NoError(t, json.Unmarshal(createGeminiTestPayload("gemini-2.5-flash-image", "draw", 32768), &imagePayload))
	require.NotContains(t, imagePayload["generationConfig"], "maxOutputTokens", "text budgets must not change image probes")
}

func TestGeminiOutputTruncationRemainsAFailure(t *testing.T) {
	c, _ := modelIdentityContext(t)
	svc := &AccountTestService{}
	err := svc.processGeminiStream(c, strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]},\"finishReason\":\"MAX_TOKENS\"}]}\n\n"))
	require.ErrorContains(t, err, "truncated")
}
