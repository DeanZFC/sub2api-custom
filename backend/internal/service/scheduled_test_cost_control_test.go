package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

func costJSONResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func usageInt(n int64) *int64 { return &n }
func TestScheduledTestCostRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		message, kind string
		calls         int
	}{
		{"API returned 401: invalid key", "auth", 1}, {"API returned 403: forbidden", "auth", 1},
		{"API returned 402: payment required", "quota", 1}, {"API returned 429: insufficient_quota", "quota", 1},
		{"API returned 429: too many requests", "rate_limit", 1}, {"API returned 400: bad input", "request", 1},
		{"API returned 503: unavailable", "server", 2}, {"connection reset by peer", "network", 2},
		{"context deadline exceeded", "network", 2}, {"OpenAI response incomplete (max_output_tokens)", "output_limit", 1},
		{"expected HTML/SVG output", "output_invalid", 1}, {"unrecognized error", "unknown", 1},
	} {
		t.Run(tc.kind+tc.message, func(t *testing.T) {
			runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
			calls := 0
			result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
				calls++
				return &ScheduledTestResult{Status: "failed", ErrorMessage: tc.message}
			})
			require.Equal(t, tc.calls, calls)
			require.Equal(t, tc.kind, result.ExecutionInfo.FailureKind)
		})
	}
}
func TestScheduledTestCostRealUpstreamRetryKeepsFailedUsage(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
		costJSONResponse(503, `{"error":{"type":"server_error"},"usage":{"input_tokens":10,"output_tokens":2,"output_tokens_details":{"reasoning_tokens":1}}}`),
		costJSONResponse(200, `{"model":"test-model","status":"completed","usage":{"input_tokens":20,"output_tokens":8,"output_tokens_details":{"reasoning_tokens":3}},"output":[]}`),
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
	result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(ctx context.Context) *ScheduledTestResult {
		r, err := svc.RunTestBackground(ctx, 42, "test-model")
		require.NoError(t, err)
		return r
	})
	require.Equal(t, "success", result.Status)
	require.Equal(t, 2, upstream.requests)
	require.Len(t, result.ExecutionInfo.Attempts, 2)
	require.EqualValues(t, 30, *result.ExecutionInfo.InputTokens)
	require.EqualValues(t, 10, *result.ExecutionInfo.OutputTokens)
	require.EqualValues(t, 4, *result.ExecutionInfo.ReasoningTokens)
	require.Equal(t, "server", result.ExecutionInfo.Attempts[0].FailureKind)
	require.Empty(t, result.ExecutionInfo.FailureKind)
}
func TestScheduledTestCostMetadataStreamingAndUnknown(t *testing.T) {
	cases := []struct {
		name, body               string
		input, output, reasoning int64
	}{
		{"responses", "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":15,\"output_tokens\":7,\"output_tokens_details\":{\"reasoning_tokens\":4}}}}\n\ndata: [DONE]\n", 15, 7, 4},
		{"claude", "event: message_start\ndata: {\"message\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":0}}}\n\ndata: {\"usage\":{\"output_tokens\":3}}\n\ndata: {\"usage\":{\"output_tokens\":8}}\n", 12, 8, -1},
		{"gemini", `{"response":{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":6,"thoughtsTokenCount":4}}}`, 5, 10, 4},
		{"chat", `{"usage":{"prompt_tokens":9,"completion_tokens":4,"completion_tokens_details":{"reasoning_tokens":0}}}`, 9, 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			collector := &accountTestUsageCollector{}
			ctx := context.WithValue(context.Background(), accountTestUsageKey{}, collector)
			resp, err := observeAccountTestResponse(ctx, time.Now(), costJSONResponse(200, tc.body), nil)
			require.NoError(t, err)
			// Force arbitrary read boundaries, including splits within JSON keys.
			chunk := make([]byte, 3)
			for {
				_, e := resp.Body.Read(chunk)
				if e == io.EOF {
					break
				}
				require.NoError(t, e)
			}
			require.NoError(t, resp.Body.Close())
			usage := collector.snapshot()
			require.Equal(t, tc.input, *usage.InputTokens)
			require.Equal(t, tc.output, *usage.OutputTokens)
			if tc.reasoning < 0 {
				require.Nil(t, usage.ReasoningTokens)
			} else {
				require.Equal(t, tc.reasoning, *usage.ReasoningTokens)
			}
		})
	}
	a := &ScheduledTestRequestAttempt{}
	captureAccountTestUsageMetadata(a, map[string]any{"output": []any{map[string]any{"usage": map[string]any{"input_tokens": 999.0}}}})
	require.Nil(t, a.InputTokens, "generated content cannot forge usage")
	info := &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{{ScheduledTestTokenUsage: ScheduledTestTokenUsage{InputTokens: usageInt(4)}}, {}}}
	info.summarize()
	require.Nil(t, info.InputTokens, "partial usage must not be presented as a complete total")
}
func TestScheduledTestCostRetryAfterAndCanceledUsage(t *testing.T) {
	collector := &accountTestUsageCollector{}
	ctx := context.WithValue(context.Background(), accountTestUsageKey{}, collector)
	response := costJSONResponse(429, `{"error":{"type":"rate_limit_error"}}`)
	response.Header.Set("Retry-After", "7200")
	response, err := observeAccountTestResponse(ctx, time.Now(), response, nil)
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	info := collector.snapshot()
	require.WithinDuration(t, time.Now().Add(2*time.Hour), *info.RetryAfter, time.Second)
	require.Nil(t, info.InputTokens)
	runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
	canceled, cancel := context.WithCancel(context.Background())
	result := runner.runWithAutomaticRetries(canceled, &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
		cancel()
		return &ScheduledTestResult{Status: "failed", ExecutionInfo: info}
	})
	require.Len(t, result.ExecutionInfo.Attempts, 1)
	require.Equal(t, "canceled", result.ExecutionInfo.FailureKind)
}
func TestScheduledTestCostCompatibilityResendKeepsBothRequests(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
		costJSONResponse(400, `{"error":{"message":"Unsupported parameter: 'max_output_tokens'"}}`),
		costJSONResponse(200, `{"model":"test-model","status":"completed","usage":{"input_tokens":5,"output_tokens":3},"output":[]}`),
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	result, err := svc.RunTestBackgroundWithPrompt(context.Background(), 42, "test-model", "write text")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, result.ExecutionInfo.Attempts, 2)
	require.Nil(t, result.ExecutionInfo.InputTokens)
	require.EqualValues(t, 5, *result.ExecutionInfo.Attempts[1].InputTokens)
}
func TestScheduledTestCostUsageRequestOption(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}
	upstream := &modelIdentityHTTPUpstream{body: "data: {\"choices\":[{\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\ndata: [DONE]\n", beforeReturn: func(req *http.Request, _ int) {
		body, err := req.GetBody()
		require.NoError(t, err)
		defer func() { _ = body.Close() }()
		raw, _ := io.ReadAll(body)
		require.True(t, strings.Contains(string(raw), `"include_usage":true`))
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	result, err := svc.RunTestBackground(context.Background(), 42, "gpt-4o")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.EqualValues(t, 2, *result.ExecutionInfo.InputTokens)
}

func TestScheduledTestCostChatUsageCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status, requests int
	}{
		{"unsupported", `{"error":{"message":"Unsupported parameter: stream_options"}}`, 400, 2},
		{"unknown nested option", `{"error":{"message":"Unknown parameter: include_usage","param":"stream_options"}}`, 422, 2},
		{"unrelated bad request", `{"error":{"message":"Invalid model"}}`, 400, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: false}}
			upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
				costJSONResponse(tc.status, tc.body),
				costJSONResponse(200, `{"model":"test-model","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`),
			}, beforeReturn: func(req *http.Request, index int) {
				body, err := req.GetBody()
				require.NoError(t, err)
				defer func() { _ = body.Close() }()
				raw, _ := io.ReadAll(body)
				require.Equal(t, index == 1, strings.Contains(string(raw), `"include_usage":true`))
			}}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			result, err := svc.RunTestBackground(context.Background(), 42, "test-model")
			require.NoError(t, err)
			require.Equal(t, tc.requests, upstream.requests)
			require.Len(t, result.ExecutionInfo.Attempts, tc.requests)
			require.Equal(t, "request", result.ExecutionInfo.Attempts[0].FailureKind)
			if tc.requests == 2 {
				require.Equal(t, "success", result.Status)
				require.Nil(t, result.ExecutionInfo.InputTokens, "unknown first-attempt usage is retained")
				require.EqualValues(t, 2, *result.ExecutionInfo.Attempts[1].InputTokens)
			} else {
				require.Equal(t, "failed", result.Status)
			}
		})
	}
}

func TestScheduledTestCostAntigravityHasNoNestedRetries(t *testing.T) {
	t.Setenv(antigravityForwardBaseURLEnv, "https://antigravity.example.com")
	for _, tc := range []struct{ status, requests int }{{503, 2}, {429, 1}, {401, 1}} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
				costJSONResponse(tc.status, `{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`),
				costJSONResponse(tc.status, `{"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`),
			}}
			gateway := &AntigravityGatewayService{}
			runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
			result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(ctx context.Context) *ScheduledTestResult {
				usage := &accountTestUsageCollector{}
				ctx = context.WithValue(ctx, accountTestUsageKey{}, usage)
				r, err := gateway.antigravityRetryLoop(antigravityRetryLoopParams{
					ctx: ctx, account: &Account{ID: 42, Platform: PlatformAntigravity}, accessToken: "test",
					action: "generateContent", body: []byte(`{}`), httpUpstream: upstream,
				})
				require.NoError(t, err)
				_, err = io.ReadAll(r.resp.Body)
				require.NoError(t, err)
				require.NoError(t, r.resp.Body.Close())
				return &ScheduledTestResult{Status: "failed", ExecutionInfo: usage.snapshot()}
			})
			require.Equal(t, tc.requests, upstream.requests)
			require.Len(t, result.ExecutionInfo.Attempts, tc.requests)
			require.EqualValues(t, 3*tc.requests, *result.ExecutionInfo.InputTokens)
		})
	}
}

func TestScheduledTestCostManualRetryRetainsPreviousUsage(t *testing.T) {
	plan := automaticRetryTestPlan()
	previous := &ScheduledTestResult{ID: 90, PlanID: plan.ID, AccountID: plan.AccountID, TestDefinitionID: scheduledTestPtrInt64(1), Status: "failed", ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{
		{ScheduledTestTokenUsage: ScheduledTestTokenUsage{InputTokens: usageInt(7), OutputTokens: usageInt(3)}},
	}}}
	results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 5)}
	account := &Account{ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
	accounts := &modelIdentityAccountRepo{account: account}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.accountTestSvc.httpUpstream = &modelIdentityHTTPUpstream{responses: []*http.Response{
		costJSONResponse(200, `{"model":"model","status":"completed","usage":{"input_tokens":5,"output_tokens":2},"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`),
	}}
	runner.accountTestSvc.cfg = &config.Config{}
	_, err := runner.RetryAccount(context.Background(), plan, previous)
	require.NoError(t, err)
	completed := automaticRetryCompleted(t, results)
	runner.activeRuns.Wait()
	require.Equal(t, "success", completed.Status)
	require.Equal(t, previous.ID, completed.ID)
	require.Zero(t, results.countCreated())
	require.Len(t, completed.ExecutionInfo.Attempts, 2)
	require.EqualValues(t, 12, *completed.ExecutionInfo.InputTokens)
	require.EqualValues(t, 5, *completed.ExecutionInfo.OutputTokens)
}

func TestScheduledTestCostServerRetryAfterDefersInsteadOfResending(t *testing.T) {
	next := time.Now().Add(time.Hour)
	calls := 0
	runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
	result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
		calls++
		return &ScheduledTestResult{Status: "failed", ExecutionInfo: &ScheduledTestExecutionInfo{FailureKind: "server", RetryAfter: &next}}
	})
	require.Equal(t, 1, calls)
	require.Equal(t, &next, result.ExecutionInfo.RetryAfter)
}
