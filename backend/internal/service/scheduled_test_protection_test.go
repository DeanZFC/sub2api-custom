package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func protectionFloat(v float64) *float64 { return &v }

func protectionPlan() *ScheduledTestPlan {
	id, accountID := int64(1), int64(42)
	return &ScheduledTestPlan{
		ID: 9, Enabled: true, TargetMode: "account", AccountID: &accountID,
		TestDefinitionID: &id, TestDefinitionIDs: []int64{1}, ModelID: "gpt-5.6-sol", MaxResults: 10,
		Protection: ScheduledTestProtectionConfig{Enabled: true, Rules: []ScheduledTestProtectionRule{{TestDefinitionID: 1, PauseOnFailure: true}}},
	}
}

func TestScheduledTestProtectionThresholdsAndSamples(t *testing.T) {
	for _, tc := range []struct {
		name, metric, operator                         string
		threshold, observed                            float64
		samples, firstSamples, inputTokens, minSamples int64
		want                                           string
	}{
		{"success ratio converted to percent", "success_rate", "lt", 85, .9, 10, 0, 0, 0, "pass"},
		{"success equality is not below", "success_rate", "lt", 90, .9, 10, 0, 0, 0, "pass"},
		{"percentage equality resists binary rounding", "success_rate", "lt", 29, .29, 100, 0, 0, 0, "pass"},
		{"decimal percentage equality lt", "success_rate", "lt", 1.1, 11.0 / 1000.0, 1000, 0, 0, 0, "pass"},
		{"decimal percentage equality gt", "success_rate", "gt", 1.1, 11.0 / 1000.0, 1000, 0, 0, 0, "pass"},
		{"near percentage remains strictly below", "success_rate", "lt", 1.1, .01099999999, 1000, 0, 0, 0, "fail"},
		{"near percentage remains strictly above", "success_rate", "gt", 1.1, .01100000001, 1000, 0, 0, 0, "fail"},
		{"success below", "success_rate", "lt", 90, .89, 10, 0, 0, 0, "fail"},
		{"cache ratio converted to percent", "cache_rate", "lt", 85, .9, 10, 0, 100, 0, "pass"},
		{"cache equality is not above", "cache_rate", "gt", 90, .9, 10, 0, 100, 0, "pass"},
		{"cache above", "cache_rate", "gt", 90, .91, 10, 0, 100, 0, "fail"},
		{"latency equality lt", "latency_ms", "lt", 200, 200, 0, 0, 0, 0, "pass"},
		{"latency equality gt", "latency_ms", "gt", 200, 200, 0, 0, 0, 0, "pass"},
		{"latency above", "latency_ms", "gt", 200, 201, 0, 0, 0, 0, "fail"},
		{"numeric below", "output_numeric", "lt", 29, 28, 0, 0, 0, 0, "fail"},
		{"numeric equality", "output_numeric", "gt", 29, 29, 0, 0, 0, 0, "pass"},
		{"zero requests never recover", "success_rate", "lt", 90, 1, 0, 0, 0, 0, "pending"},
		{"too few requests", "success_rate", "lt", 90, 1, 4, 0, 0, 5, "pending"},
		{"minimum request boundary", "success_rate", "lt", 90, 1, 5, 0, 0, 5, "pass"},
		{"cache input absent", "cache_rate", "lt", 90, 1, 10, 0, 0, 0, "pending"},
		{"first token sample count independently enforced", "avg_first_token_ms", "gt", 200, 100, 100, 4, 0, 5, "pending"},
		{"first token exact sample minimum", "avg_first_token_ms", "gt", 200, 100, 100, 5, 0, 5, "pass"},
		{"missing first token samples", "avg_first_token_ms", "gt", 200, 100, 100, 0, 0, 0, "pending"},
		{"bad ratio is not a fabricated failure", "success_rate", "lt", 90, -1, 10, 0, 0, 0, "pending"},
		{"percent stored instead of ratio rejected", "cache_rate", "lt", 90, 90, 10, 0, 100, 0, "pending"},
		{"nonfinite metric rejected", "output_numeric", "gt", 29, math.Inf(1), 0, 0, 0, 0, "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := ScheduledTestProtectionRule{MinSamples: tc.minSamples, Thresholds: []ScheduledTestThreshold{{Metric: tc.metric, Operator: tc.operator, Value: tc.threshold}}}
			result := &ScheduledTestResult{Status: "success", LatencyMs: int64(tc.observed), OutputNumeric: protectionFloat(tc.observed), OutputStatistics: &ScheduledTestStatistics{
				TotalRequests: tc.samples, FirstTokenSamples: tc.firstSamples, CacheInputTokens: tc.inputTokens,
				SuccessRate: protectionFloat(tc.observed), CacheRate: protectionFloat(tc.observed), AvgFirstTokenMs: protectionFloat(tc.observed),
			}}
			verdict, reason := evaluateScheduledTestProtection(rule, result)
			require.Equal(t, tc.want, verdict, reason)
			if tc.want != "pass" {
				require.NotEmpty(t, reason)
			}
		})
	}
	for _, result := range []*ScheduledTestResult{
		{Status: "success"},
		{Status: "success", OutputStatistics: &ScheduledTestStatistics{TotalRequests: 10}},
	} {
		verdict, _ := evaluateScheduledTestProtection(ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: "success_rate", Operator: "lt", Value: 90}}}, result)
		require.Equal(t, "pending", verdict)
	}
}

func TestScheduledTestProtectionObservedViolationWinsOverMissingMetric(t *testing.T) {
	rule := ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{
		{Metric: "cache_rate", Operator: "lt", Value: 90},
		{Metric: "success_rate", Operator: "lt", Value: 80},
	}}
	verdict, reason := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputStatistics: &ScheduledTestStatistics{TotalRequests: 10, SuccessRate: protectionFloat(.5)}})
	require.Equal(t, "fail", verdict)
	require.Contains(t, reason, "成功率小于80%")
}

func TestScheduledTestProtectionAnswersAndExecutionFailure(t *testing.T) {
	for _, tc := range []struct {
		name, match, expected, actual, status, want string
		number                                      *float64
		voting, pause                               bool
	}{
		{name: "exact trims outer whitespace", match: "exact", expected: "PASS", actual: " \nPASS\n ", status: "success", want: "pass"},
		{name: "exact rejects other answer", match: "exact", expected: "PASS", actual: "FAIL", status: "success", want: "fail"},
		{name: "exact case sensitive", match: "exact", expected: "PASS", actual: "pass", status: "success", want: "fail"},
		{name: "contains", match: "contains", expected: "鹈鹕", actual: "这是一只鹈鹕。", status: "success", want: "pass"},
		{name: "contains mismatch", match: "contains", expected: "鹈鹕", actual: "海鸥", status: "success", want: "fail"},
		{name: "numeric field", match: "numeric", expected: "29.0", actual: "reasoning", number: protectionFloat(29), status: "passed", want: "pass"},
		{name: "numeric text fallback", match: "numeric", expected: "29", actual: "step 1: reason\nfinal answer: 29", status: "success", want: "pass"},
		{name: "numeric decimal fallback", match: "numeric", expected: "-0.25", actual: "答案 = -0.25", status: "success", want: "pass"},
		{name: "numeric mismatch", match: "numeric", expected: "29", actual: "final answer: 28", status: "success", want: "fail"},
		{name: "numeric unmarked reasoning rejected", match: "numeric", expected: "1", actual: "step 1: inspect the input\nrow 2 contains 7 values", status: "success", want: "fail"},
		{name: "vote reference is not auto compared", match: "exact", expected: "reference picture", actual: "different model output", status: "success", voting: true, want: "pass"},
		{name: "vote numeric reference is not auto parsed", match: "numeric", expected: "for humans only", actual: "no number", status: "success", voting: true, want: "pass"},
		{name: "execution failure pauses when configured", status: "failed", pause: true, want: "fail"},
		{name: "execution failure is inconclusive otherwise", status: "failed", want: "pending"},
		{name: "not completed does not recover", status: "running", want: "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := ScheduledTestProtectionRule{ExpectedAnswer: tc.expected, AnswerMatch: tc.match, PauseOnFailure: tc.pause, Vote: &ScheduledTestVoteConfig{Enabled: tc.voting}}
			verdict, reason := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: tc.status, ResponseText: tc.actual, OutputNumeric: tc.number})
			require.Equal(t, tc.want, verdict, reason)
		})
	}
	verdict, _ := evaluateScheduledTestProtection(ScheduledTestProtectionRule{}, nil)
	require.Equal(t, "pending", verdict)
}

func TestScheduledTestProtectionValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ScheduledTestPlan)
	}{
		{"group target", func(p *ScheduledTestPlan) { p.TargetMode = "group" }},
		{"no rules", func(p *ScheduledTestPlan) { p.Protection.Rules = nil }},
		{"too many rules", func(p *ScheduledTestPlan) { p.Protection.Rules = make([]ScheduledTestProtectionRule, 33) }},
		{"unselected definition", func(p *ScheduledTestPlan) { p.Protection.Rules[0].TestDefinitionID = 2 }},
		{"duplicate definition", func(p *ScheduledTestPlan) { p.Protection.Rules = append(p.Protection.Rules, p.Protection.Rules[0]) }},
		{"negative sample minimum", func(p *ScheduledTestPlan) { p.Protection.Rules[0].MinSamples = -1 }},
		{"too large sample minimum", func(p *ScheduledTestPlan) { p.Protection.Rules[0].MinSamples = 1000000001 }},
		{"unknown answer match", func(p *ScheduledTestPlan) { p.Protection.Rules[0].AnswerMatch = "regex" }},
		{"numeric answer invalid", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].ExpectedAnswer = "NaN"
			p.Protection.Rules[0].AnswerMatch = "numeric"
		}},
		{"numeric answer infinite", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].ExpectedAnswer = "Inf"
			p.Protection.Rules[0].AnswerMatch = "numeric"
		}},
		{"answer too long", func(p *ScheduledTestPlan) { p.Protection.Rules[0].ExpectedAnswer = strings.Repeat("答", 10001) }},
		{"negative votes", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Vote = &ScheduledTestVoteConfig{Enabled: true, RejectAbove: -1, PassAtLeast: 1}
		}},
		{"zero pass threshold", func(p *ScheduledTestPlan) { p.Protection.Rules[0].Vote = &ScheduledTestVoteConfig{Enabled: true} }},
		{"huge vote threshold", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Vote = &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1000001}
		}},
		{"empty rule", func(p *ScheduledTestPlan) { p.Protection.Rules[0].PauseOnFailure = false }},
		{"unknown metric", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "credentials", Operator: "lt"}}
		}},
		{"unknown operator", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "lte", Value: 1}}
		}},
		{"percentage above 100", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "success_rate", Operator: "lt", Value: 101}}
		}},
		{"negative percentage", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: -1}}
		}},
		{"negative latency", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: -1}}
		}},
		{"nonfinite threshold", func(p *ScheduledTestPlan) {
			p.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "output_numeric", Operator: "gt", Value: math.NaN()}}
		}},
		{"too many thresholds", func(p *ScheduledTestPlan) { p.Protection.Rules[0].Thresholds = make([]ScheduledTestThreshold, 21) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := protectionPlan()
			tc.mutate(p)
			require.Error(t, validateScheduledTestProtection(p))
		})
	}
	p := protectionPlan()
	p.Protection.Rules[0].ExpectedAnswer = "  正确答案  "
	require.NoError(t, validateScheduledTestProtection(p))
	require.Equal(t, "正确答案", p.Protection.Rules[0].ExpectedAnswer)
	require.Equal(t, "exact", p.Protection.Rules[0].AnswerMatch)
	p.Protection.Rules[0].Vote = &ScheduledTestVoteConfig{Enabled: true, RejectAbove: 0, PassAtLeast: 1}
	p.Protection.Rules[0].AnswerMatch = "numeric"
	require.NoError(t, validateScheduledTestProtection(p), "human reference answer is not constrained to a numeric machine answer")
	p.Protection.Enabled = false
	p.Protection.Rules = nil
	require.NoError(t, validateScheduledTestProtection(p))
}

func TestScheduledTestProtectionOutputKindAndSelectedDefinition(t *testing.T) {
	for _, tc := range []struct {
		metric, kind string
		valid        bool
	}{
		{"success_rate", "statistics", true}, {"cache_rate", "text", false}, {"success_rate", "number", false},
		{"avg_first_token_ms", "statistics", true}, {"avg_first_token_ms", "html", false},
		{"output_numeric", "number", true}, {"output_numeric", "text", false},
		{"latency_ms", "statistics", false}, {"latency_ms", "html", true},
	} {
		t.Run(tc.metric+"/"+tc.kind, func(t *testing.T) {
			rule := &ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: tc.metric, Operator: "lt", Value: 1}}}
			if tc.valid {
				require.NoError(t, validateProtectionOutputKind(rule, tc.kind))
			} else {
				require.Error(t, validateProtectionOutputKind(rule, tc.kind))
			}
		})
	}
	require.Error(t, validateProtectionOutputKind(&ScheduledTestProtectionRule{ExpectedAnswer: "42"}, "statistics"))
	require.Error(t, validateProtectionOutputKind(&ScheduledTestProtectionRule{Vote: &ScheduledTestVoteConfig{Enabled: true}}, "statistics"))
	require.NoError(t, validateProtectionOutputKind(nil, "text"))
	p := protectionPlan()
	require.NotNil(t, p.ProtectionRule(p.TestDefinitionID))
	require.Nil(t, p.ProtectionRule(nil))
	unknown := int64(2)
	require.Nil(t, p.ProtectionRule(&unknown))
	p.Enabled = false
	require.Nil(t, p.ProtectionRule(p.TestDefinitionID))
	svc := NewScheduledTestService(nil, nil)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{1: {ID: 1, Enabled: false, OutputKind: "text"}}})
	require.ErrorContains(t, svc.validateDefinitionForPlan(context.Background(), protectionPlan()), "disabled")
}

type protectionRepositoryStub struct {
	ScheduledTestResultRepository
	ScheduledTestProtectionRepository
	mu                 sync.Mutex
	events             []string
	eligible           bool
	eligibilityErr     error
	beginErr           error
	created, completed *ScheduledTestResult
	verdict            string
	collect            func(context.Context, ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error)
	rows               []*ScheduledTestVoteResult
	voteErr            error
	userID, resultID   int64
	vote               string
}

func (r *protectionRepositoryStub) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}
func (r *protectionRepositoryStub) ListDetectionAccountIDs(_ context.Context, _, id *int64) ([]int64, error) {
	if r.eligibilityErr != nil {
		return nil, r.eligibilityErr
	}
	if !r.eligible {
		return nil, nil
	}
	if id != nil {
		return []int64{*id}, nil
	}
	return []int64{42}, nil
}
func (r *protectionRepositoryStub) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	r.record("create")
	copy := *result
	copy.ID = 71
	r.created = &copy
	return &copy, nil
}
func (r *protectionRepositoryStub) Update(_ context.Context, result *ScheduledTestResult) error {
	r.record("update")
	copy := *result
	r.completed = &copy
	return nil
}
func (r *protectionRepositoryStub) PruneOldResults(context.Context, int64, int) error { return nil }
func (r *protectionRepositoryStub) BeginProtection(_ context.Context, result *ScheduledTestResult, _ ScheduledTestProtectionRule) error {
	r.record("begin")
	if result.ID != 71 {
		return errors.New("missing persisted row")
	}
	return r.beginErr
}
func (r *protectionRepositoryStub) CompleteProtection(_ context.Context, result *ScheduledTestResult, verdict, _ string) error {
	r.record("complete")
	if result.ID != 71 {
		return errors.New("result identity changed")
	}
	r.verdict = verdict
	return nil
}
func (r *protectionRepositoryStub) CollectStatistics(ctx context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
	r.record("collect")
	return r.collect(ctx, filter)
}
func (r *protectionRepositoryStub) ListStatisticsAccountIDs(context.Context, int64) ([]int64, error) {
	panic("must use detection eligibility, including quality-paused accounts")
}
func (r *protectionRepositoryStub) ListVotingResults(_ context.Context, userID int64) ([]*ScheduledTestVoteResult, error) {
	r.userID = userID
	return r.rows, r.voteErr
}
func (r *protectionRepositoryStub) CastTestVote(_ context.Context, userID, resultID int64, vote string) (*ScheduledTestVoteResult, error) {
	r.userID, r.resultID, r.vote = userID, resultID, vote
	if r.voteErr != nil {
		return nil, r.voteErr
	}
	return r.rows[0], nil
}

func TestScheduledTestProtectionRunnerStatisticsWiring(t *testing.T) {
	repo := &protectionRepositoryStub{eligible: true}
	repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
		require.Equal(t, []string{"create", "begin", "collect"}, repo.events)
		require.Equal(t, int64(42), *filter.AccountID)
		return &ScheduledTestStatistics{WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd, TotalRequests: 10, CacheInputTokens: 100, CacheReadTokens: 90, CacheRate: protectionFloat(.9)}, nil
	}
	plan := protectionPlan()
	plan.TargetMode = "all_accounts"
	plan.AccountID = nil
	plan.GroupID = scheduledTestPtrInt64(8)
	plan.Protection.Rules[0].Thresholds = []ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 90}}
	runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, repo), nil, nil, nil, nil)
	runner.runStatisticsDefinition(context.Background(), plan)
	require.Equal(t, []string{"create", "begin", "collect", "update", "complete"}, repo.events)
	require.Equal(t, "pass", repo.verdict)
	require.Equal(t, repo.created.ID, repo.completed.ID)
	require.Equal(t, "running", repo.created.Status)
	require.Equal(t, "success", repo.completed.Status)
}

func TestScheduledTestProtectionRunnerModelWiringAndEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		eligible bool
		beginErr error
	}{
		{"quality paused account returned by detection lookup", true, nil},
		{"manually stopped account excluded", false, nil},
		{"protection initialization failure blocks execution", true, errors.New("database failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &protectionRepositoryStub{eligible: tc.eligible, beginErr: tc.beginErr}
			calls := 0
			accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
				calls++
				repo.record("model")
				require.Equal(t, []string{"create", "begin", "model"}, repo.events)
				return &Account{ID: id, Status: StatusQualityPaused, Schedulable: true, Extra: map[string]any{"synthetic_ui_test": true}}, nil
			}}
			runner := newScheduledTestExecutionRunner(repo, accounts)
			runner.runOneAccount(context.Background(), protectionPlan(), 42, "hello", "text")
			if !tc.eligible {
				require.Zero(t, calls)
				require.Empty(t, repo.events)
				return
			}
			if tc.beginErr != nil {
				require.Zero(t, calls)
				require.Equal(t, "failed", repo.completed.Status)
				require.NotContains(t, repo.events, "complete")
				return
			}
			require.Equal(t, 1, calls)
			require.Equal(t, []string{"create", "begin", "model", "update", "complete"}, repo.events)
			require.Equal(t, repo.created.ID, repo.completed.ID)
			require.Equal(t, "pass", repo.verdict)
		})
	}
}

func TestScheduledTestProtectionVotingServicePrivacyAndValidation(t *testing.T) {
	ctx := context.Background()
	row := &ScheduledTestVoteResult{Result: &ScheduledTestResult{ID: 71, AccountID: scheduledTestPtrInt64(42), AccountName: "private-account", ErrorMessage: "private-upstream-error", Status: "success", OutputKind: "number", ResponseText: "final answer: 29"}}
	repo := &protectionRepositoryStub{rows: []*ScheduledTestVoteResult{row}}
	svc := NewScheduledTestService(nil, repo)
	results, err := svc.ListVotingResults(ctx, 7)
	require.NoError(t, err)
	require.Equal(t, int64(7), repo.userID)
	require.Len(t, results, 1)
	payload, err := json.Marshal(results)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "account_name")
	require.NotContains(t, string(payload), "private-")
	require.Equal(t, 29.0, *results[0].Result.OutputNumeric)
	_, err = svc.CastTestVote(ctx, 7, 71, "fail")
	require.NoError(t, err)
	require.Equal(t, int64(7), repo.userID)
	require.Equal(t, int64(71), repo.resultID)
	require.Equal(t, "fail", repo.vote)
	_, err = svc.CastTestVote(ctx, 7, 71, "PASS")
	require.ErrorIs(t, err, ErrScheduledTestVoteInvalid)
	_, err = svc.CastTestVote(ctx, 0, 71, "pass")
	require.ErrorIs(t, err, ErrScheduledTestVoteUnavailable)
	_, err = svc.CastTestVote(ctx, 7, 0, "pass")
	require.ErrorIs(t, err, ErrScheduledTestVoteUnavailable)
	repo.rows = nil
	results, err = svc.ListVotingResults(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, results)
	require.Empty(t, results)
	_, err = NewScheduledTestService(nil, nil).ListVotingResults(ctx, 7)
	require.ErrorIs(t, err, ErrScheduledTestVoteUnavailable)
}
