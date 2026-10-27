package service

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestPassRateBoundariesAndRecovery(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	config := ScheduledTestPassRateConfig{Enabled: true, WindowSize: 10, WindowSeconds: 604800, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 2}
	observations := func(verdicts string) []ScheduledTestPassRateObservation {
		out := make([]ScheduledTestPassRateObservation, 0, len(verdicts))
		for i, verdict := range verdicts {
			v := "pass"
			if verdict == 'F' {
				v = "fail"
			}
			out = append(out, ScheduledTestPassRateObservation{Verdict: v, StartedAt: now.Add(-time.Duration(i+1) * time.Minute)})
		}
		return out
	}
	for _, test := range []struct {
		name, verdicts             string
		wasBlocked, blocked, ready bool
		passed, consecutive        int
	}{
		{"exactly eighty is allowed", "PPPPPPPPFF", false, false, true, 8, 8},
		{"seventy pauses", "PPPPPPPFFF", false, true, true, 7, 7},
		{"ninety and two latest passes recovers", "PPFPPPPPPP", true, false, true, 9, 2},
		{"ninety but latest failure stays held", "FPPPPPPPPP", true, true, true, 9, 0},
		{"ninety but only latest one pass stays held", "PFPPPPPPPP", true, true, true, 9, 1},
		{"eighty does not recover", "PPPPPPPPFF", true, true, true, 8, 8},
		{"insufficient does not newly pause", "FFFFFFFFF", false, false, false, 0, 0},
		{"insufficient does not recover", "PPPPPPPPP", true, true, false, 9, 9},
		{"most recent ten only", "PPPPPPPPPPFFFFFFFFFF", false, false, true, 10, 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := SummarizeScheduledTestPassRate(config, observations(test.verdicts), test.wasBlocked, now)
			require.Equal(t, test.blocked, s.Blocked)
			require.Equal(t, test.ready, s.Ready)
			require.Equal(t, test.passed, s.Passed)
			require.Equal(t, test.consecutive, s.ConsecutivePasses)
			require.False(t, s.Stale)
			require.NotNil(t, s.PassRate)
		})
	}
	t.Run("single failure can pause before full window", func(t *testing.T) {
		config.PauseOnSingleFailure = true
		s := SummarizeScheduledTestPassRate(config, observations("F"), false, now)
		require.True(t, s.Blocked)
		require.False(t, s.Ready)
	})
}

func TestScheduledTestPassRateExcludesExpiredFutureAndUnknown(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	c := ScheduledTestPassRateConfig{Enabled: true, WindowSize: 2, WindowSeconds: 3600, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 2}
	input := []ScheduledTestPassRateObservation{
		{Verdict: "pass", StartedAt: now.Add(time.Minute)},
		{Verdict: "pending", StartedAt: now.Add(-time.Minute)},
		{Verdict: "fail", StartedAt: now.Add(-2 * time.Minute)},
		{Verdict: "pass", StartedAt: now.Add(-time.Hour)},
		{Verdict: "pass", StartedAt: now.Add(-time.Hour - time.Nanosecond)},
	}
	s := SummarizeScheduledTestPassRate(c, input, false, now)
	require.Equal(t, 2, s.Samples)
	require.Equal(t, 1, s.Passed)
	require.Equal(t, "fail", s.LatestVerdict)
	require.Equal(t, now.Add(-2*time.Minute), *s.LatestAt)
	require.True(t, s.Blocked)
	s = SummarizeScheduledTestPassRate(c, input, true, now.Add(2*time.Hour))
	require.True(t, s.Stale)
	require.True(t, s.Blocked)
	require.Nil(t, s.PassRate)
	require.Nil(t, s.LatestAt)
	require.Zero(t, s.Samples)
}

func TestScheduledTestPassRateValidation(t *testing.T) {
	valid := ScheduledTestPassRateConfig{Enabled: true, WindowSize: 10, WindowSeconds: 604800, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 2}
	require.NoError(t, valid.Validate())
	for _, mutate := range []func(*ScheduledTestPassRateConfig){
		func(c *ScheduledTestPassRateConfig) { c.WindowSize = 1 },
		func(c *ScheduledTestPassRateConfig) { c.WindowSize = 1001 },
		func(c *ScheduledTestPassRateConfig) { c.WindowSeconds = 3599 },
		func(c *ScheduledTestPassRateConfig) { c.WindowSeconds = 2592001 },
		func(c *ScheduledTestPassRateConfig) { c.PauseBelow = math.NaN() },
		func(c *ScheduledTestPassRateConfig) { c.RecoverAtLeast = math.Inf(1) },
		func(c *ScheduledTestPassRateConfig) { c.RecoverAtLeast = 79 },
		func(c *ScheduledTestPassRateConfig) { c.RecoverConsecutive = 11 },
	} {
		c := valid
		mutate(&c)
		require.Error(t, c.Validate())
	}
	defaults := ScheduledTestPassRateConfig{Enabled: true, PauseBelow: 80, RecoverAtLeast: 90}
	require.NoError(t, defaults.Validate())
	require.Equal(t, 10, defaults.WindowSize)
	require.Equal(t, 604800, defaults.WindowSeconds)
	require.Equal(t, 2, defaults.RecoverConsecutive)
}

func TestScheduledTestPassRateVerdictSeparatesCapabilityFromTransportAndLatency(t *testing.T) {
	rule := ScheduledTestProtectionRule{
		ExpectedAnswer: "21", AnswerMatch: "numeric", PauseOnFailure: true,
		OnFail:     &ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"},
		Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 100}},
	}
	for _, test := range []struct {
		name, status, kind, response, want string
	}{
		{"slow correct answer is a capability pass", "success", "number", "21", "pass"},
		{"wrong answer is a capability failure", "success", "number", "20", "fail"},
		{"transport failure stays incomplete despite failure action", "failed", "number", "20", "pending"},
		{"running is incomplete", "running", "number", "21", "pending"},
		{"passive statistics are not capability evidence", "success", "statistics", "21", "pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
			verdict, _ := EvaluateScheduledTestPassRateVerdict(rule, &ScheduledTestResult{
				Status: test.status, OutputKind: test.kind, ResponseText: test.response, LatencyMs: 10000,
			})
			require.Equal(t, test.want, verdict)
		})
	}
	verdict, _ := EvaluateScheduledTestPassRateVerdict(rule, nil)
	require.Equal(t, "pending", verdict)
	require.Len(t, rule.Thresholds, 1, "independent evaluation must preserve the existing latency guard")
	require.NotNil(t, rule.OnFail)
}

func TestScheduledTestPassRateVerdictNormalizesPersistedModelEvidence(t *testing.T) {
	for _, test := range []struct {
		name, upstream, returned, want string
	}{
		{"matching model", "model-a", "model-a", "pass"},
		{"mismatching model cannot trust a stored pass", "model-a", "model-b", "fail"},
		{"missing returned model is incomplete", "model-a", "", "pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
			returned := []string{}
			if test.returned != "" {
				returned = append(returned, test.returned)
			}
			stored, err := json.Marshal(ScheduledTestModelCheck{RequestedModel: "stale-model", UpstreamModel: test.upstream, ReturnedModels: returned, MatchMode: "exact", Verdict: "pass", Reason: "match"})
			require.NoError(t, err)
			result := &ScheduledTestResult{Status: "success", OutputKind: "model_check", ModelID: "model-a", ResponseText: string(stored)}
			verdict, _ := EvaluateScheduledTestPassRateVerdict(ScheduledTestProtectionRule{ModelMatch: "exact"}, result)
			require.Equal(t, test.want, verdict)
			require.Equal(t, string(stored), result.ResponseText, "normalization must preserve the persisted snapshot")
		})
	}
}

func TestScheduledTestPassRateOutputContractFailureIsCapabilityEvidence(t *testing.T) {
	runner := &ScheduledTestRunnerService{}
	for _, kind := range []string{"number", "html"} {
		t.Run(kind, func(t *testing.T) {
			for _, status := range []string{"success", "failed"} {
				result := &ScheduledTestResult{Status: status, OutputKind: kind, ResponseText: "no structured answer"}
				runner.applyOutputContract(result, kind)
				verdict, _ := EvaluateScheduledTestPassRateVerdict(ScheduledTestProtectionRule{}, result)
				if status == "success" {
					require.Equal(t, "failed", result.Status)
					require.NotNil(t, result.ExecutionInfo)
					require.True(t, result.ExecutionInfo.OutputContractFailed)
					require.Equal(t, "fail", verdict)
				} else {
					require.True(t, result.ExecutionInfo == nil || !result.ExecutionInfo.OutputContractFailed)
					require.Equal(t, "pending", verdict, "an upstream failure must not become a capability failure")
				}
			}
		})
	}
}

func TestScheduledTestPassRateRequiresCapabilityCondition(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		rule       ScheduledTestProtectionRule
		allowed    bool
	}{
		{"pause on execution failure is not capability", "number", ScheduledTestProtectionRule{PauseOnFailure: true}, false},
		{"latency alone is not capability", "html", ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 100}}}, false},
		{"statistics use passive protection", "statistics", ScheduledTestProtectionRule{}, false},
		{"expected answer", "number", ScheduledTestProtectionRule{ExpectedAnswer: "21", AnswerMatch: "numeric"}, true},
		{"numeric threshold", "number", ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: "output_numeric", Operator: "lt", Value: 21}}}, true},
		{"reviewed html", "html", ScheduledTestProtectionRule{Vote: &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}}, true},
		{"builtin capability verdict", "capability", ScheduledTestProtectionRule{}, true},
		{"model metadata verdict", "model_check", ScheduledTestProtectionRule{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.rule.PassRate = &ScheduledTestPassRateConfig{Enabled: true}
			err := validateProtectionOutputKind(&test.rule, test.kind)
			if test.allowed {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "pass_rate")
			}
		})
	}
}
