//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQualityCapabilityGeneratedReferences(t *testing.T) {
	// Solve from the presented question, independently of the generator's state.
	for _, family := range []string{"candy", "state", "instructions"} {
		for seed := int64(0); seed < 100; seed++ {
			c := QualityCapabilityConfig{Version: 1, Samples: 3, RequiredPasses: 2, Families: []string{family}}
			qs := qualityCapabilityQuestions(c, seed)
			require.Equal(t, qs, qualityCapabilityQuestions(c, seed))
			keys := map[string]bool{}
			for _, q := range qs {
				require.False(t, keys[q.Key])
				keys[q.Key] = true
				var expected string
				switch family {
				case "candy":
					matches := regexp.MustCompile(`1/(\d+).*得到 (\d+) 颗，现在有 (\d+)`).FindStringSubmatch(q.Prompt)
					require.Len(t, matches, 4)
					b, _ := strconv.Atoi(matches[1])
					extra, _ := strconv.Atoi(matches[2])
					final, _ := strconv.Atoi(matches[3])
					expected = strconv.Itoa((final - extra) * 3 * b / (b - 1))
				case "state":
					var x, y int
					_, err := fmt.Sscanf(q.Prompt, "初始 x=%d，y=%d。", &x, &y)
					require.NoError(t, err)
					steps := strings.Split(strings.Split(strings.Split(q.Prompt, "每步立即更新变量：")[1], "。最终")[0], "；")
					require.Len(t, steps, 8)
					for _, step := range steps {
						switch step {
						case "交换 x 与 y":
							x, y = y, x
						case "x = x + y":
							x = x + y
						case "y = x - y":
							y = x - y
						default:
							t.Fatal(step)
						}
					}
					expected = strconv.Itoa(x - y)
				case "instructions":
					seq := strings.Split(strings.Split(q.Prompt, "[")[1], "]")[0]
					out := []string{}
					for _, v := range strings.Fields(seq) {
						n, err := strconv.Atoi(v)
						require.NoError(t, err)
						if n%2 == 0 {
							out = append([]string{strconv.Itoa(n * 3)}, out...)
						}
					}
					expected = strings.Join(out, ",")
					if len(out) == 0 {
						expected = "EMPTY"
					}
				}
				require.Equal(t, expected, q.Expected, "%s", q.Prompt)
			}
		}
	}
}

func TestQualityCapabilitySuiteDecisionsAndUsage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		answers       []string
		fail          bool
		verdict       string
		calls, passed int
	}{
		{"early pass", []string{"7", "7"}, false, "pass", 2, 2},
		{"early fail", []string{"8", "8"}, false, "fail", 2, 0},
		{"mixed", []string{"8", "7", "7"}, false, "pass", 3, 2},
		{"transport unknown", []string{"7", ""}, true, "pending", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := QualityCapabilityConfig{Version: 1, Samples: 3, RequiredPasses: 2, Questions: []QualityCapabilityQuestion{{Key: "a", Prompt: "a", Expected: "7", Match: "number"}, {Key: "b", Prompt: "b", Expected: "7", Match: "number"}, {Key: "c", Prompt: "c", Expected: "7", Match: "number"}}}
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
			calls := 0
			r := runner.runCapabilitySuite(context.Background(), &ScheduledTestPlan{ID: 1}, 1, string(raw), func(context.Context, string) *ScheduledTestResult {
				value := int64(10)
				status := "success"
				code := 200
				if tc.fail && calls == 1 {
					status = "failed"
					code = 401
				}
				answer := tc.answers[calls]
				calls++
				return &ScheduledTestResult{Status: status, ResponseText: answer, ExecutionInfo: &ScheduledTestExecutionInfo{Attempts: []ScheduledTestRequestAttempt{{HTTPStatus: code, ScheduledTestTokenUsage: ScheduledTestTokenUsage{InputTokens: &value}}}}}
			})
			require.Equal(t, tc.calls, calls)
			require.Equal(t, tc.verdict, r.ExecutionInfo.Capability.Verdict)
			require.Equal(t, tc.passed, r.ExecutionInfo.Capability.Passed)
			require.Len(t, r.ExecutionInfo.Attempts, tc.calls)
			require.EqualValues(t, tc.calls*10, *r.ExecutionInfo.InputTokens)
			require.Nil(t, r.ExecutionInfo.OutputTokens)
			r.OutputKind = "capability"
			verdict, _ := evaluateScheduledTestProtection(ScheduledTestProtectionRule{}, r)
			require.Equal(t, tc.verdict, verdict)
			if tc.fail {
				require.Equal(t, "unknown", r.ExecutionInfo.Capability.Answers[1].Verdict)
				require.Equal(t, "failed", r.Status)
			}
		})
	}
}

func TestQualityCapabilityValidationAndProtection(t *testing.T) {
	for _, raw := range []string{`{}`, `{"version":1,"samples":3,"required_passes":2,"families":["unknown"]}`, `{"version":1,"samples":2,"required_passes":3,"families":["candy"]}`, `{"version":1,"samples":1,"required_passes":1,"families":["candy","candy"]}`} {
		_, err := ParseQualityCapabilityConfig(raw)
		require.Error(t, err)
	}
	rule := ScheduledTestProtectionRule{}
	require.NoError(t, validateProtectionOutputKind(&rule, "capability"))
	rule.ExpectedAnswer = "7"
	require.Error(t, validateProtectionOutputKind(&rule, "capability"))
	require.True(t, qualityCapabilityAnswerMatches(QualityCapabilityQuestion{Expected: "7", Match: "number"}, " +007\n"))
	require.False(t, qualityCapabilityAnswerMatches(QualityCapabilityQuestion{Expected: "7", Match: "number"}, "答案是 7"))
	require.False(t, qualityCapabilityAnswerMatches(QualityCapabilityQuestion{Expected: "7", Match: "exact"}, "007"))
	verdict, _ := evaluateScheduledTestProtection(ScheduledTestProtectionRule{}, &ScheduledTestResult{Status: "success", OutputKind: "capability"})
	require.Equal(t, "pending", verdict)
}
