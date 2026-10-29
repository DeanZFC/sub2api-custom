package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestScheduledGrokCapabilityPromptOnWire(t *testing.T) {
	const prompt = "你的知识库截止什么时间？不能联网，如果有答案，输出yyyy-MM-dd格式"
	account := &Account{ID: 12, Platform: PlatformGrok, Type: AccountTypeAPIKey, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test"}}
	upstream := &outputBudgetUpstream{body: `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"2025-01-31"}]}]}`}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	ctx := withScheduledTestOutputKind(withAccountTestMaxOutputTokens(context.Background(), 4096), "capability")
	result, err := svc.RunTestBackgroundWithPromptAndReasoning(ctx, account.ID, "grok-4.5", prompt, "high")
	require.NoError(t, err)
	require.Equal(t, "success", result.Status, result.ErrorMessage)
	require.Equal(t, "2025-01-31", result.ResponseText)
	require.Len(t, upstream.requests, 1)
	body := upstream.requests[0].body
	require.Equal(t, prompt, gjson.GetBytes(body, "input.0.content.0.text").String())
	require.EqualValues(t, 4096, gjson.GetBytes(body, "max_output_tokens").Int())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	require.False(t, gjson.GetBytes(body, "tools").Exists())
	require.False(t, qualityCapabilityAnswerMatches(QualityCapabilityQuestion{Match: "not_date"}, result.ResponseText))
}

func TestScheduledBedrockCapabilityUsesOnlyActualModelText(t *testing.T) {
	const prompt = "你的知识库截止什么时间？不能联网，如果有答案，输出yyyy-MM-dd格式"
	for _, tc := range []struct {
		name, content, expected string
	}{
		{"empty", `[]`, ""},
		{"empty text", `[{"type":"text","text":""}]`, ""},
		{"date in later block", `[{"type":"text","text":"知识截止时间："},{"type":"text","text":"2025-01-31"}]`, "知识截止时间：2025-01-31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive, Concurrency: 1,
				Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test", "aws_region": "us-east-1"}}
			upstream := &outputBudgetUpstream{body: `{"content":` + tc.content + `,"stop_reason":"end_turn"}`}
			svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			ctx := withScheduledTestOutputKind(context.Background(), "capability")
			result, err := svc.RunTestBackgroundWithPromptAndReasoning(ctx, account.ID, "claude-sonnet-4-6", prompt, "")
			require.NoError(t, err)
			require.Equal(t, "success", result.Status, result.ErrorMessage)
			require.Equal(t, tc.expected, result.ResponseText)
			require.NotContains(t, result.ResponseText, "(empty response)")
			require.Len(t, upstream.requests, 1)
			body := upstream.requests[0].body
			require.Equal(t, prompt, gjson.GetBytes(body, "messages.0.content.0.text").String())
			require.False(t, gjson.GetBytes(body, "tools").Exists())
			require.False(t, qualityCapabilityAnswerMatches(QualityCapabilityQuestion{Match: "not_date"}, result.ResponseText))
		})
	}
}

func TestBedrockInteractiveEmptyResponseKeepsPlaceholder(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeBedrock, Status: StatusActive, Concurrency: 1,
		Credentials: map[string]any{"auth_mode": "apikey", "api_key": "test", "aws_region": "us-east-1"}}
	upstream := &outputBudgetUpstream{body: `{"content":[],"stop_reason":"end_turn"}`}
	svc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault))
	text, errText := parseTestSSEOutput(recorder.Body.String())
	require.Empty(t, errText)
	require.Equal(t, "(empty response)", text)
}
