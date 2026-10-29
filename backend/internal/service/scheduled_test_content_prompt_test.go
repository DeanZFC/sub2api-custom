package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const contentTestHTML = `<html><body><svg xmlns="http://www.w3.org/2000/svg"><circle r="10"/></svg></body></html>`

func contentTestClaudeResponse(text string) string {
	encoded, _ := json.Marshal(text)
	return fmt.Sprintf(`{"type":"message","content":[{"type":"text","text":%s}],"stop_reason":"end_turn"}`, encoded)
}

func TestScheduledContentInstructionsOnWire(t *testing.T) {
	const prompt = "Draw a pelican riding a bicycle using inline SVG."
	for _, tc := range []struct {
		name, platform, model, protocol, instructionPath, promptPath, tokenPath, response string
		responses                                                                         bool
	}{
		{"anthropic", PlatformAnthropic, "claude-sonnet-4-6", "", "system.0.text", "messages.0.content.0.text", "max_tokens", outputBudgetClaudeResponse, false},
		{"responses", PlatformOpenAI, "gpt-5.4", "", "instructions", "input.0.content.0.text", "max_output_tokens", `{"status":"completed","output":[]}`, true},
		{"chat", PlatformOpenAI, "gpt-4o", "", "messages.0.content", "messages.1.content", "max_tokens", `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`, false},
		{"deepseek_responses", PlatformDeepseek, "deepseek-chat", APIProtocolResponses, "instructions", "input.0.content.0.text", "max_output_tokens", `{"status":"completed","output":[]}`, true},
		{"deepseek_anthropic", PlatformDeepseek, "deepseek-chat", APIProtocolAnthropic, "system.0.text", "messages.0.content.0.text", "max_tokens", outputBudgetClaudeResponse, false},
		{"deepseek_chat", PlatformDeepseek, "deepseek-chat", APIProtocolChatCompletions, "messages.0.content", "messages.1.content", "max_tokens", `{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`, false},
		{"gemini", PlatformGemini, "gemini-2.5-flash", "", "systemInstruction.parts.0.text", "contents.0.parts.0.text", "generationConfig.maxOutputTokens", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 12, Platform: tc.platform, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "api_protocol": tc.protocol},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: tc.responses}}
			upstream := &outputBudgetUpstream{body: tc.response}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			for _, kind := range []string{"html", "text", "number", "model_check", ""} {
				t.Run(kind, func(t *testing.T) {
					ctx := withAccountTestMaxOutputTokens(context.Background(), 32768)
					if kind != "" {
						ctx = withScheduledTestOutputKind(ctx, kind)
					}
					result, err := svc.RunTestBackgroundWithPromptAndReasoning(ctx, account.ID, tc.model, prompt, "")
					require.NoError(t, err)
					require.Equal(t, "success", result.Status, result.ErrorMessage)
					body := upstream.requests[len(upstream.requests)-1].body
					require.EqualValues(t, 32768, gjson.GetBytes(body, tc.tokenPath).Int())
					if kind == "html" {
						require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(body, tc.instructionPath).String(), string(body))
						require.Equal(t, prompt, gjson.GetBytes(body, tc.promptPath).String())
						require.False(t, gjson.GetBytes(body, "tools").Exists())
					} else {
						require.NotContains(t, string(body), "automated content test")
						switch tc.instructionPath {
						case "instructions":
							require.Contains(t, gjson.GetBytes(body, tc.instructionPath).String(), "Codex")
						case "system.0.text":
							require.Equal(t, claudeCodeSystemPrompt, gjson.GetBytes(body, tc.instructionPath).String())
						case "messages.0.content":
							require.Equal(t, prompt, gjson.GetBytes(body, "messages.0.content").String())
							require.Len(t, gjson.GetBytes(body, "messages").Array(), 1)
						}
					}
				})
			}
		})
	}
}

func TestScheduledContentInstructionsContextScope(t *testing.T) {
	base := context.Background()
	html := withScheduledTestOutputKind(base, " HTML ")
	require.Equal(t, scheduledTestHTMLInstructions, scheduledTestContentInstructions(html))
	require.Empty(t, scheduledTestContentInstructions(base))
	require.Empty(t, scheduledTestContentInstructions(nil)) //nolint:staticcheck // Deliberately exercise the helper's nil-context guard.
	for _, kind := range []string{"text", "number", "model_check", "statistics", ""} {
		require.Empty(t, scheduledTestContentInstructions(withScheduledTestOutputKind(html, kind)))
	}
}

func TestScheduledContentInstructionsOnOpenAIOAuthCompatibilityRetry(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test"}}
	upstream := &outputBudgetUpstream{respond: func(_ int64, body []byte) *http.Response {
		if gjson.GetBytes(body, "max_output_tokens").Exists() {
			return costJSONResponse(400, `{"error":{"message":"Unsupported parameter: max_output_tokens"}}`)
		}
		return costJSONResponse(200, `{"status":"completed","output":[]}`)
	}}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	ctx := withScheduledTestOutputKind(context.Background(), "html")
	result, err := svc.RunTestBackgroundWithPromptAndReasoning(ctx, account.ID, "gpt-5.4", "pelican", "high")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Len(t, upstream.requests, 2)
	for _, request := range upstream.requests {
		require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(request.body, "instructions").String())
		require.Equal(t, "pelican", gjson.GetBytes(request.body, "input.0.content.0.text").String())
		require.Equal(t, "high", gjson.GetBytes(request.body, "reasoning.effort").String())
		require.False(t, gjson.GetBytes(request.body, "store").Bool())
	}
}

func TestScheduledContentInstructionsOnGrokPreserveConnectivityProbe(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test", "refresh_token": "test", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}}
	repo := &modelIdentityAccountRepo{account: account}
	upstream := &outputBudgetUpstream{body: `{"status":"completed","output":[]}`}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, grokTokenProvider: NewGrokTokenProvider(repo, nil), cfg: &config.Config{}}
	ctx := withScheduledTestOutputKind(withAccountTestMaxOutputTokens(context.Background(), 32768), "html")
	result, err := svc.RunTestBackgroundWithPromptAndReasoning(ctx, account.ID, "grok-4.5", "pelican", "high")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Len(t, upstream.requests, 1)
	body := upstream.requests[0].body
	require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(body, "instructions").String())
	require.Equal(t, "pelican", gjson.GetBytes(body, "input.0.content.0.text").String())
	require.EqualValues(t, 32768, gjson.GetBytes(body, "max_output_tokens").Int())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())

	result, err = svc.RunTestBackgroundWithPromptAndReasoning(withScheduledTestOutputKind(context.Background(), "capability"), account.ID, "grok-4.5", "Reply with seven.", "")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "Reply with seven.", gjson.GetBytes(upstream.requests[1].body, "input.0.content.0.text").String())

	result, err = svc.RunTestBackgroundWithPromptAndReasoning(context.Background(), account.ID, "grok-4.5", "", "")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Len(t, upstream.requests, 3)
	probe, err := buildGrokQuotaProbeBody("grok-4.5")
	require.NoError(t, err)
	require.JSONEq(t, string(probe), string(upstream.requests[2].body))
}

func TestScheduledBedrockCustomPromptUsesOnlyActualModelText(t *testing.T) {
	const prompt = "Return two short words."
	for _, tc := range []struct {
		name, content, want string
	}{
		{"empty", `[]`, ""},
		{"multiple text blocks", `[{"type":"text","text":"hello "},{"type":"text","text":"world"}]`, "hello world"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test", "aws_region": "us-east-1"}}
			upstream := &outputBudgetUpstream{body: `{"content":` + tc.content + `,"stop_reason":"end_turn"}`}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			result, err := svc.RunTestBackgroundWithPromptAndReasoning(withScheduledTestOutputKind(context.Background(), "capability"), account.ID, "claude-sonnet-4-6", prompt, "")
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, tc.want, result.ResponseText)
			require.Equal(t, prompt, gjson.GetBytes(upstream.requests[0].body, "messages.0.content.0.text").String())
			if tc.want == "" {
				require.NotContains(t, result.ResponseText, "(empty response)")
			}
		})
	}
}

func TestBedrockInteractiveEmptyResponseKeepsPlaceholder(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test", "aws_region": "us-east-1"}}
	upstream := &outputBudgetUpstream{body: "{\"content\":[],\"stop_reason\":\"end_turn\"}"}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault))
	text, errText := parseTestSSEOutput(recorder.Body.String())
	require.Empty(t, errText)
	require.Equal(t, "(empty response)", text)
}

func TestScheduledContentInstructionsOnAntigravityTransport(t *testing.T) {
	for _, model := range []string{"gemini-2.5-flash", "claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			account := &Account{ID: 12, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"access_token": "test", "project_id": "project", "model_mapping": map[string]any{model: model}}}
			upstream := &outputBudgetUpstream{body: `{"response":{"candidates":[{"content":{"parts":[{"text":"<svg></svg>"}]},"finishReason":"STOP"}]}}`}
			svc := &AntigravityGatewayService{tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
			ctx := withScheduledTestOutputKind(withAccountTestMaxOutputTokens(context.Background(), 32768), "html")
			result, err := svc.TestConnectionWithPrompt(ctx, account, model, "pelican")
			require.NoError(t, err)
			require.Equal(t, "<svg></svg>", result.Text)
			require.Len(t, upstream.requests, 1)
			body := upstream.requests[0].body
			require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(body, "request.systemInstruction.parts.0.text").String())
			require.Equal(t, "pelican", gjson.GetBytes(body, "request.contents.0.parts.0.text").String())
			require.EqualValues(t, 32768, gjson.GetBytes(body, "request.generationConfig.maxOutputTokens").Int())
		})
	}
}

func TestScheduledContentInstructionsInWrappedGeminiRequests(t *testing.T) {
	svc := &AntigravityGatewayService{}
	claude, err := svc.buildClaudeTestRequest("project", "claude-sonnet-4-6", "pelican", 32768)
	require.NoError(t, err)
	gemini, err := svc.buildGeminiTestRequestWithPrompt("project", "gemini-2.5-flash", "pelican", 32768)
	require.NoError(t, err)
	for name, body := range map[string][]byte{"claude": claude, "gemini": gemini} {
		t.Run(name, func(t *testing.T) {
			unchanged, err := applyScheduledTestGeminiContentInstructions(context.Background(), body)
			require.NoError(t, err)
			require.Equal(t, body, unchanged)
			patched, err := applyScheduledTestGeminiContentInstructions(withScheduledTestOutputKind(context.Background(), "html"), body)
			require.NoError(t, err)
			require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(patched, "request.systemInstruction.parts.0.text").String())
			require.Len(t, gjson.GetBytes(patched, "request.systemInstruction.parts").Array(), 1)
			for _, path := range []string{"project", "model", "request.contents", "request.generationConfig"} {
				require.Equal(t, gjson.GetBytes(body, path).Value(), gjson.GetBytes(patched, path).Value(), path)
			}
			require.NotContains(t, string(patched), "pair programming")
		})
	}
}

func TestScheduledContentInstructionsSurviveSnapshotsAndAutomaticRetries(t *testing.T) {
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
					return costJSONResponse(200, contentTestClaudeResponse(contentTestHTML))
				}
				return costJSONResponse(200, contentTestClaudeResponse("21"))
			}}
			runner := newScheduledTestExecutionRunner(repo, accounts)
			defer runner.Stop()
			runner.executionTimeout = time.Second
			runner.accountTestSvc.httpUpstream = upstream
			runner.accountTestSvc.cfg = &config.Config{}
			runner.scheduledSvc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, Prompt: "pelican", OutputKind: "html", MaxOutputTokens: 32768},
				2: {ID: 2, Enabled: true, Prompt: "candy", OutputKind: "number"},
			}})
			plan := scheduledTestExecutionPlan()
			plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			runner.RunPlanNow(ctx, plan)
			require.NoError(t, ctx.Err())
			require.Len(t, upstream.requests, 6)
			for _, request := range upstream.requests {
				want := claudeCodeSystemPrompt
				if gjson.GetBytes(request.body, "messages.0.content.0.text").String() == "pelican" {
					want = scheduledTestHTMLInstructions
					require.EqualValues(t, 32768, gjson.GetBytes(request.body, "max_tokens").Int())
				}
				require.Equal(t, want, gjson.GetBytes(request.body, "system.0.text").String())
			}
			completed := 0
			for len(results.updated) > 0 {
				result := <-results.updated
				if result.Status == "running" {
					continue
				}
				require.Equal(t, "success", result.Status, result.ErrorMessage)
				if result.OutputKind == "html" {
					require.Equal(t, contentTestHTML, result.OutputHTML)
				} else {
					require.NotNil(t, result.OutputNumeric)
					require.EqualValues(t, 21, *result.OutputNumeric)
				}
				completed++
			}
			require.Equal(t, 4, completed)
		})
	}
}

func TestScheduledContentInstructionsSurviveDetachedManualRetry(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}}
	previous := &ScheduledTestResult{ID: 9, PlanID: 7, TestDefinitionID: scheduledTestPtrInt64(1), AccountID: &account.ID, Status: "failed"}
	results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 4)}
	runner := newScheduledTestExecutionRunner(results, &modelIdentityAccountRepo{account: account})
	defer runner.Stop()
	runner.executionTimeout = time.Second
	runner.scheduledSvc.SetDefinitionRepository(scheduledTestDefinitionRepoStub{definition: &ScheduledTestDefinition{ID: 1, Enabled: true, Prompt: "pelican", OutputKind: "html", MaxOutputTokens: 49152}})
	upstream := &outputBudgetUpstream{body: contentTestClaudeResponse(contentTestHTML)}
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
		require.Equal(t, contentTestHTML, completed.OutputHTML)
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not finish")
	}
	require.Len(t, upstream.requests, 1)
	body := upstream.requests[0].body
	require.Equal(t, scheduledTestHTMLInstructions, gjson.GetBytes(body, "system.0.text").String())
	require.Equal(t, "pelican", gjson.GetBytes(body, "messages.0.content.0.text").String())
	require.EqualValues(t, 49152, gjson.GetBytes(body, "max_tokens").Int())
}
