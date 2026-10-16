package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

type recoveryProbeRepoStub struct {
	*protectionRepositoryStub
	needed bool
	err    error
}

func (r *recoveryProbeRepoStub) StatisticsRecoveryProbeNeeded(_ context.Context, result *ScheduledTestResult) (bool, error) {
	r.record("probe_eligibility")
	return r.needed && result.ID > 0, r.err
}

func TestScheduledTestStatisticsRecoveryProbe(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantProbe, wantVerdict                                string
		needed, disabled, cancelled, timeout, queryError, measuredFailure bool
	}{
		{name: "paused account with no usage recovers", needed: true, body: `{"status":"completed","usage":{"input_tokens":8,"output_tokens":2},"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}`, wantProbe: "success", wantVerdict: "pass"},
		{name: "failed probe still records usage", needed: true, body: "data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"failed\"},\"usage\":{\"input_tokens\":8,\"output_tokens\":2}}\n\n", wantProbe: "failed", wantVerdict: "fail"},
		{name: "empty completion cannot recover", needed: true, body: `{"status":"completed","output":[]}`, wantProbe: "failed", wantVerdict: "fail"},
		{name: "incomplete response cannot recover", needed: true, body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"response.incomplete\"}\n\n", wantProbe: "failed", wantVerdict: "fail"},
		{name: "normal account does not probe", body: `{}`, wantVerdict: "pending"},
		{name: "disabled recovery does not probe", needed: true, disabled: true, wantVerdict: "pending"},
		{name: "lookup failure does not recover", needed: true, queryError: true, wantVerdict: "pending"},
		{name: "known recovery failure does not probe", needed: true, measuredFailure: true, wantVerdict: "pending"},
		{name: "probe deadline retains pause", needed: true, timeout: true, body: `{}`, wantProbe: "failed", wantVerdict: "fail"},
		{name: "shutdown is not a failed probe", needed: true, cancelled: true, body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := protectionPlan()
			plan.Protection.Rules[0] = cacheRecoveryRule()
			if tc.disabled {
				plan.Protection.Rules[0].Recovery.Enabled = false
			}
			repo := &recoveryProbeRepoStub{protectionRepositoryStub: &protectionRepositoryStub{eligible: true}, needed: tc.needed}
			if tc.queryError {
				repo.err = errors.New("database unavailable")
			}
			repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
				stats := &ScheduledTestStatistics{WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd}
				if tc.measuredFailure {
					stats.TotalRequests = 10
					stats.CacheSamples, stats.CacheInputTokens, stats.CacheRate = 10, 1000, protectionFloat(.5)
				}
				return stats, nil
			}
			account := &Account{ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusQualityPaused, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{plan.ModelID: "mapped-model"}},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			upstream := &modelIdentityHTTPUpstream{body: tc.body}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream.beforeReturn = func(req *http.Request, _ int) {
				if tc.cancelled {
					cancel()
				}
				if tc.timeout {
					<-req.Context().Done()
				}
			}
			testSvc := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
			runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, repo), testSvc, nil, nil, nil)
			if tc.timeout {
				runner.executionTimeout = 10 * time.Millisecond
			}
			runner.runStatisticsDefinition(ctx, plan)
			require.NotNil(t, repo.completed)
			require.Equal(t, tc.wantVerdict, repo.verdict)
			stats := repo.completed.OutputStatistics
			require.NotNil(t, stats)
			if !tc.measuredFailure {
				require.Zero(t, stats.TotalRequests, "probe must not fabricate usage samples")
			}
			if tc.wantProbe == "" {
				require.Nil(t, stats.RecoveryProbe)
				if !tc.cancelled {
					require.Zero(t, upstream.requests)
				}
				return
			}
			require.Equal(t, 1, upstream.requests, "one probe per execution; no traffic-dependent waiting")
			require.Equal(t, "mapped-model", upstream.requestModel)
			require.NotNil(t, stats.RecoveryProbe)
			require.Equal(t, tc.wantProbe, stats.RecoveryProbe.Status)
			require.Len(t, repo.completed.ExecutionInfo.Attempts, 1, "recovery spending must be attached to the statistics result")
			if tc.name == "paused account with no usage recovers" || tc.name == "failed probe still records usage" {
				require.EqualValues(t, 8, *repo.completed.ExecutionInfo.InputTokens)
				require.EqualValues(t, 2, *repo.completed.ExecutionInfo.OutputTokens)
			}
			var stored ScheduledTestStatistics
			require.NoError(t, json.Unmarshal([]byte(repo.completed.ResponseText), &stored))
			require.Equal(t, tc.wantProbe, stored.RecoveryProbe.Status, "probe evidence must survive result persistence")
			normalizeStoredTestResults([]*ScheduledTestResult{repo.completed})
			require.Equal(t, tc.wantProbe, repo.completed.OutputStatistics.RecoveryProbe.Status)
		})
	}
}

func TestScheduledTestRecoveryProbeCannotWaiveMeasuredFailures(t *testing.T) {
	rule := cacheRecoveryRule()
	rule.Thresholds = append(rule.Thresholds, ScheduledTestThreshold{Metric: "success_rate", Operator: "lt", Value: 90})
	now := time.Now()
	result := &ScheduledTestResult{Status: "success", OutputKind: "statistics", StartedAt: now, FinishedAt: now.Add(time.Second),
		OutputStatistics: &ScheduledTestStatistics{TotalRequests: 10, SuccessRate: protectionFloat(.5),
			RecoveryProbe: &ScheduledTestRecoveryProbe{Status: "success", StartedAt: now, FinishedAt: now.Add(time.Second)}}}
	_, _, accepted := EvaluateScheduledTestRecoveryProbe(rule, result)
	require.False(t, accepted, "enough recovery samples prove a failure even if cache/hourly samples are missing")
	result.OutputStatistics.TotalRequests = 0
	result.OutputStatistics.SuccessRate = nil
	verdict, _, accepted := EvaluateScheduledTestRecoveryProbe(rule, result)
	require.True(t, accepted)
	require.Equal(t, "pass", verdict)
	result.OutputStatistics.RecoveryProbe.StartedAt = now.Add(-time.Second)
	_, _, accepted = EvaluateScheduledTestRecoveryProbe(rule, result)
	require.False(t, accepted, "an older probe cannot be reused for a new statistics execution")
}
