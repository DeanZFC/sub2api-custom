package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The response in the reported screenshot previously displayed 8: the old
// fallback chose the proof line containing "至少一颗" after missing both 21s.
const scheduledTestCandyScreenshotResponse = `最少取 **21 颗**，方法是：**利用手感，取 9 颗圆形糖和 12 颗五角星形糖。**

### 为什么一定能成功？

- **12 颗五角星形糖中，必定同时有苹果味和桃子味。**
  不含苹果味的五角星形糖只有 \(6+4=10\) 颗；不含桃子味的只有 \(7+4=11\) 颗。因此取 12 颗，两种口味必定都有。
- **9 颗圆形糖中，必有苹果味或桃子味。**
  圆形西瓜味只有 8 颗，因此至少有一颗是苹果味或桃子味，它就能与五角星形糖中另一种口味配对。

### 为什么 20 颗不够？

设取出的圆形糖为 \(r\) 颗，五角星形糖为 \(s\) 颗，且 \(r+s=20\)：

- 若 \(r\le 8\)，圆形糖可能全是西瓜味，无法配对。
- 若 \(s\le 4\)，五角星形糖可能全是西瓜味，无法配对。
- 否则 \(r\ge9,\ s\ge5\)，于是 \(r\le15,\ s\le11\)。而苹果味加西瓜味的糖，圆形共有 \(7+8=15\) 颗，五角星形共有 \(7+4=11\) 颗，所以取出的糖仍可能完全没有桃子味，无法配对。

因此，20 颗不能保证，最少需要 **\(\boxed{21}\) 颗**。`

func TestExtractScheduledTestNumberFormattedConclusions(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       float64
	}{
		{"reported candy response", scheduledTestCandyScreenshotResponse, 21},
		{"take without out", "最少取 **21 颗**，其中取 9 颗和 12 颗。", 21},
		{"need with counterexample", "因此，20 颗不能保证，最少需要 **21 颗**。", 21},
		{"need to take", "最少需取出 **22 颗**，此前的 21 颗不够。", 22},
		{"boxed answer", `答案是 \(\boxed{22}\) 颗，而不是 21 颗。`, 22},
		{"math answer", `最终答案：\(21\)。中间结果是 8。`, 21},
		{"display math", `最终结果：\[21\]`, 21},
		{"dollar math", `最少需要 $21$ 颗。`, 21},
		{"boxed only", `\boxed{21}`, 21},
		{"last boxed conclusion", "中间共有 8 颗。\n因此，" + `\boxed{21}` + " 颗。", 21},
		{"final outranks intermediate", "Final answer: 21\nAn intermediate result is 8.", 21},
		{"minimum outranks proof", "最少取 **21 颗**。\n其中第二步计算结果是 8 颗。", 21},
		{"minimum outranks English proof", "The minimum number is 21.\nAn intermediate result is 8.", 21},
		{"markdown bare scalar", "**22**", 22},
		{"numeric fence", "```text\n22\n```", 22},
		{"boxed scientific", `答案：\boxed{-1.25e-3}`, -0.00125},
		{"english unit", "Final answer: 21 candies.", 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractScheduledTestNumber(tc.text)
			require.True(t, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestExtractScheduledTestNumberDoesNotGuessFromProof(t *testing.T) {
	for _, text := range []string{
		"圆形西瓜味只有 8 颗，因此至少有一颗是苹果味或桃子味。",
		"最少需要多少颗？\n已知圆形糖有 8 颗。",
		`最终答案：\boxed{9+12}`,
		`答案：\(9+12\)`,
		"答案：21 + 1",
		"答案：21 × 2",
		"答案：21 ÷ 2",
		`因此，\boxed{21}/x`,
		`因此，\boxed{21}+x`,
		"答案：21/2",
		"答案：1,000",
		"答案：1.2.3",
		"答案：1e999",
		"答案：21oops",
		"subresult: 8",
		"1. 第一步\n2. 第二步",
	} {
		t.Run(text, func(t *testing.T) {
			_, ok := extractScheduledTestNumber(text)
			require.False(t, ok, "reasoning or a partial expression must not become an answer")
		})
	}
}

func TestScheduledTestNumericProtectionReparsesLegacyValue(t *testing.T) {
	result := &ScheduledTestResult{Status: "success", OutputKind: "number", OutputNumeric: protectionFloat(8), ResponseText: scheduledTestCandyScreenshotResponse}
	rule := strategyPlan().Protection.Rules[0]
	verdict, _ := evaluateScheduledTestProtection(rule, result)
	require.Equal(t, "pass", verdict)
	value, ok := scheduledTestMetric(result, "output_numeric", 0)
	require.True(t, ok)
	require.Equal(t, 21.0, value, "numeric thresholds and answer matching must agree")

	result.ResponseText = "圆形糖有 8 颗，因此至少有一颗符合要求。"
	_, ok = scheduledTestMetric(result, "output_numeric", 0)
	require.False(t, ok, "ambiguous source must not fall back to the persisted number")
	result.ResponseText = ""
	value, ok = scheduledTestMetric(result, "output_numeric", 0)
	require.True(t, ok)
	require.Equal(t, 8.0, value, "legacy rows without source text retain their stored value")
}

func TestScheduledTestCandyOutputContractUsesExtractedConclusion(t *testing.T) {
	result := &ScheduledTestResult{Status: "success", OutputKind: "number", ResponseText: scheduledTestCandyScreenshotResponse}
	(&ScheduledTestRunnerService{}).applyOutputContract(result, "number")
	require.Equal(t, "success", result.Status)
	require.Equal(t, 21.0, *result.OutputNumeric)
	rule := strategyPlan().Protection.Rules[0]
	verdict, _ := evaluateScheduledTestProtection(rule, result)
	require.Equal(t, "pass", verdict, "group switching must use the conclusion, not the proof's 8")

	result.ResponseText = "圆形西瓜味只有 8 颗，因此至少有一颗符合要求。"
	(&ScheduledTestRunnerService{}).applyOutputContract(result, "number")
	require.Equal(t, "failed", result.Status)
	require.Nil(t, result.OutputNumeric, "failed extraction must clear any older derived value")
}
