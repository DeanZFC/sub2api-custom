package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestDurationIncludesRetriesAndWait(t *testing.T) {
	runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: 5 * time.Millisecond}
	calls := 0
	result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
		calls++
		if calls == 1 {
			return &ScheduledTestResult{Status: "failed", ErrorMessage: "API returned 503", LatencyMs: 400}
		}
		return &ScheduledTestResult{Status: "success", OutputKind: "html", OutputHTML: "<svg/>", LatencyMs: 200}
	})
	require.Equal(t, 2, calls)
	require.Equal(t, "success", result.Status)
	require.GreaterOrEqual(t, result.LatencyMs, int64(605))
	rule := ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 500}}}
	verdict, reason := evaluateScheduledTestProtection(rule, result)
	require.Equal(t, "fail", verdict, "the final 200ms response alone would incorrectly pass")
	require.Contains(t, reason, "检测总耗时")
	require.Equal(t, "<svg/>", result.OutputHTML)
	// A manual retry is a new invocation, even when persistence reuses the row.
	again := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
		return &ScheduledTestResult{Status: "success", LatencyMs: 100}
	})
	require.EqualValues(t, 100, again.LatencyMs)
}

func TestScheduledTestDurationExcludesWorkerWait(t *testing.T) {
	runner := &ScheduledTestRunnerService{}
	result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
		// execute includes worker acquisition, but the returned latency begins
		// after acquisition. Only that measured duration belongs to the upstream.
		time.Sleep(10 * time.Millisecond)
		return &ScheduledTestResult{Status: "success", LatencyMs: 2}
	})
	require.EqualValues(t, 2, result.LatencyMs)
}

func TestScheduledTestDurationAndReviewConditions(t *testing.T) {
	rule := ScheduledTestProtectionRule{TestDefinitionID: 1, Vote: &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}, Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 60000}}}
	plan := &ScheduledTestPlan{Enabled: true, TestDefinitionID: scheduledTestPtrInt64(1), TestDefinitionIDs: []int64{1}, Protection: ScheduledTestProtectionConfig{Enabled: true, Mode: "all_pass", Rules: []ScheduledTestProtectionRule{rule}}}
	require.NoError(t, validateScheduledTestProtection(plan), "duration is an automatic condition even when content uses review")
	for _, tc := range []struct {
		name, status, want string
		ms                 int64
	}{{"under", "success", "pass", 59999}, {"equal", "success", "pass", 60000}, {"over", "success", "fail", 60001}, {"missing", "success", "pending", -1}, {"unfinished", "running", "pending", 60001}, {"fast failure", "failed", "fail", 100}} {
		t.Run(tc.name, func(t *testing.T) {
			result := &ScheduledTestResult{Status: tc.status, OutputKind: "html", LatencyMs: tc.ms}
			verdict, _ := EvaluateScheduledTestDuration(rule, result)
			require.Equal(t, tc.want, verdict)
			require.Equal(t, tc.want == "pass", scheduledTestResultSatisfies(plan, result))
		})
	}
	decision, _ := EvaluateScheduledTestAllPass(plan.Protection, nil, nil, true)
	require.Equal(t, "pause", decision.Action.Scheduling, "pending timed review cannot release an existing hold")
	decision, _ = EvaluateScheduledTestAllPass(plan.Protection, map[int64]string{1: "fail"}, nil, false)
	require.Equal(t, "pause", decision.Action.Scheduling)
	decision, _ = EvaluateScheduledTestAllPass(plan.Protection, map[int64]string{1: "pass"}, nil, true)
	require.Equal(t, "resume", decision.Action.Scheduling)
	plan.Protection.Rules[0].Thresholds = nil
	require.True(t, scheduledTestResultSatisfies(plan, &ScheduledTestResult{Status: "failed"}), "untimed review retains its existing informational behavior")
	require.ErrorContains(t, validateScheduledTestProtection(plan), "at least one automatic check")
}

func TestScheduledTestDurationDoesNotFabricateMissingTiming(t *testing.T) {
	runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
	calls := 0
	result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
		calls++
		if calls == 1 {
			return &ScheduledTestResult{Status: "failed", ErrorMessage: "API returned 503", LatencyMs: -1}
		}
		return &ScheduledTestResult{Status: "success", LatencyMs: 100}
	})
	require.EqualValues(t, -1, result.LatencyMs)
	verdict, _ := EvaluateScheduledTestDuration(ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 60000}}}, result)
	require.Equal(t, "pending", verdict)
}
