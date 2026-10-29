//go:build unit

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func knowledgeCutoffTestConfig(t *testing.T) string {
	t.Helper()
	raw, err := migrations.FS.ReadFile("319_scheduled_test_knowledge_cutoff.sql")
	require.NoError(t, err)
	parts := strings.Split(string(raw), "$knowledge_cutoff$")
	require.Len(t, parts, 3)
	return parts[1]
}

func TestQualityCapabilityKnowledgeCutoffDefinition(t *testing.T) {
	raw := knowledgeCutoffTestConfig(t)
	definition := &ScheduledTestDefinition{Key: "knowledge_cutoff", Name: "知识截止日期检测", Prompt: raw, OutputKind: "capability"}
	require.NoError(t, ValidateScheduledTestDefinitionInput(definition))
	config, err := ParseQualityCapabilityConfig(definition.Prompt)
	require.NoError(t, err)
	require.Equal(t, 1, config.Samples)
	require.Equal(t, 1, config.RequiredPasses)
	require.Empty(t, config.Families)
	require.Len(t, config.Questions, 1)
	question := config.Questions[0]
	require.Equal(t, "你的知识库截止什么时间？不能联网，如果有答案，输出yyyy-MM-dd格式", question.Prompt)
	require.Empty(t, question.Expected)
	require.Equal(t, "not_date", question.Match)
	for _, mode := range []string{"exact", "number", "unknown"} {
		_, err := ParseQualityCapabilityConfig(strings.Replace(raw, `"not_date"`, `"`+mode+`"`, 1))
		require.Error(t, err, "only not_date can omit a reference answer")
	}
}

func TestQualityCapabilityKnowledgeCutoffVerdicts(t *testing.T) {
	raw := knowledgeCutoffTestConfig(t)
	for _, tc := range []struct {
		name, answer, status, verdict, answerVerdict string
	}{
		{"date only", "2025-01-01", "success", "fail", "fail"},
		{"date in explanation", "我的知识库截止到 2025-01-01。", "success", "fail", "fail"},
		{"markdown date", "```text\n2025-01-01\n```", "success", "fail", "fail"},
		{"quoted date", `"2025-01-01"`, "passed", "fail", "fail"},
		{"format rather than calendar", "2025-99-99", "success", "fail", "fail"},
		{"cannot confirm", "我无法确认知识库的具体截止日期。", "success", "pass", "pass"},
		{"other date format", "2025年1月", "success", "pass", "pass"},
		{"unmatched widths", "2025-1-1", "success", "pass", "pass"},
		{"format placeholder", "yyyy-MM-dd", "success", "pass", "pass"},
		{"other text", "不知道", "passed", "pass", "pass"},
		{"empty", "", "success", "pending", "unknown"},
		{"whitespace", " \n\t", "success", "pending", "unknown"},
		{"upstream failure with no date", "服务异常", "failed", "pending", "unknown"},
		{"upstream failure with date", "2025-01-01", "failed", "pending", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &ScheduledTestRunnerService{}
			calls := 0
			result := runner.runCapabilitySuite(context.Background(), &ScheduledTestPlan{ID: 1}, 1, raw, func(_ context.Context, prompt string) *ScheduledTestResult {
				calls++
				require.Equal(t, "你的知识库截止什么时间？不能联网，如果有答案，输出yyyy-MM-dd格式", prompt)
				return &ScheduledTestResult{Status: tc.status, ResponseText: tc.answer}
			})
			require.Equal(t, 1, calls)
			result.OutputKind = "capability"
			suite := result.ExecutionInfo.Capability
			require.Equal(t, tc.verdict, suite.Verdict)
			require.Len(t, suite.Answers, 1)
			require.Equal(t, tc.answer, suite.Answers[0].Answer)
			require.Equal(t, tc.answerVerdict, suite.Answers[0].Verdict)
			rule := ScheduledTestProtectionRule{}
			verdict, _ := evaluateScheduledTestProtection(rule, result)
			require.Equal(t, tc.verdict, verdict)
			verdict, _ = EvaluateScheduledTestPassRateVerdict(rule, result)
			require.Equal(t, tc.verdict, verdict)
			require.Equal(t, tc.verdict == "pass", scheduledTestResultSatisfies(nil, result))
			if tc.verdict == "pending" {
				require.Equal(t, "failed", result.Status)
			}
		})
	}
}
