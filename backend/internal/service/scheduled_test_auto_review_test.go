package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type autoReviewRepoStub struct {
	ScheduledTestResultRepository
	settings  *ScheduledTestReviewSettings
	remaining int
}

func (r *autoReviewRepoStub) LoadReviewSettings(context.Context) (*ScheduledTestReviewSettings, error) {
	return r.settings, nil
}
func (r *autoReviewRepoStub) ReserveReview(context.Context, ScheduledTestReviewSettings) (bool, error) {
	if r.remaining == 0 {
		return false, nil
	}
	r.remaining--
	return true, nil
}

type autoReviewAccountsStub struct {
	AccountRepository
	accounts map[int64]*Account
}

func (r autoReviewAccountsStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if a := r.accounts[id]; a != nil {
		return a, nil
	}
	return nil, errors.New("missing")
}

type autoReviewRendererStub struct{ err error }

func (r autoReviewRendererStub) Render(context.Context, string) ([]ScheduledTestReviewFrame, error) {
	return make([]ScheduledTestReviewFrame, 4), r.err
}

type autoReviewJudgeStub struct {
	verdicts []ScheduledTestVisualVerdict
	err      error
	calls    int
}

func (j *autoReviewJudgeStub) Judge(context.Context, int64, string, []ScheduledTestReviewFrame) (*ScheduledTestVisualVerdict, error) {
	j.calls++
	if j.err != nil {
		return nil, j.err
	}
	return &j.verdicts[j.calls-1], nil
}
func TestAutomaticVisualReview(t *testing.T) {
	pass := ScheduledTestVisualVerdict{Verdict: "pass", Confidence: .95, Reason: "meets rubric"}
	fail := ScheduledTestVisualVerdict{Verdict: "fail", Confidence: .95, Reason: "missing bicycle", Criteria: []ScheduledTestVisualCriterion{{Key: "bicycle", Passed: false}}}
	for _, tc := range []struct {
		name                string
		verdicts            []ScheduledTestVisualVerdict
		budget              int
		renderErr, judgeErr bool
		verdict             string
		calls               int
	}{
		{name: "pass", verdicts: []ScheduledTestVisualVerdict{pass}, budget: 2, verdict: "pass", calls: 1},
		{name: "confirmed failure", verdicts: []ScheduledTestVisualVerdict{fail, fail}, budget: 2, verdict: "fail", calls: 2},
		{name: "inconsistent failures", verdicts: []ScheduledTestVisualVerdict{fail, {Verdict: "fail", Confidence: .95, Reason: "motion only", Criteria: []ScheduledTestVisualCriterion{{Key: "motion", Passed: false}}}}, budget: 2, verdict: "pending", calls: 2},
		{name: "disagreement", verdicts: []ScheduledTestVisualVerdict{fail, pass}, budget: 2, verdict: "pending", calls: 2},
		{name: "uncertain", verdicts: []ScheduledTestVisualVerdict{{Verdict: "fail", Confidence: .8}}, budget: 2, verdict: "pending", calls: 1},
		{name: "budget exhausted", budget: 0, verdict: "pending"},
		{name: "confirmation budget exhausted", verdicts: []ScheduledTestVisualVerdict{fail}, budget: 1, verdict: "pending", calls: 1},
		{name: "render failure", budget: 2, renderErr: true, verdict: "pending"},
		{name: "judge failure", budget: 2, judgeErr: true, verdict: "pending", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &autoReviewRepoStub{settings: &ScheduledTestReviewSettings{Enabled: true, JudgeAccountID: 2, JudgeModel: "vision", DailyRequests: 20}, remaining: tc.budget}
			judge := &autoReviewJudgeStub{verdicts: tc.verdicts}
			renderer := autoReviewRendererStub{}
			if tc.renderErr {
				renderer.err = errors.New("renderer down")
			}
			if tc.judgeErr {
				judge.err = errors.New("judge down")
			}
			runner := &ScheduledTestRunnerService{scheduledSvc: &ScheduledTestService{resultRepo: repo}, accountRepo: autoReviewAccountsStub{accounts: map[int64]*Account{1: {ID: 1}, 2: {ID: 2}}}, reviewRenderer: renderer, reviewJudge: judge, reviewSem: make(chan struct{}, 1)}
			id, account := int64(10), int64(1)
			plan := &ScheduledTestPlan{Enabled: true, TestDefinitionID: &id, Protection: ScheduledTestProtectionConfig{Enabled: true, Rules: []ScheduledTestProtectionRule{{TestDefinitionID: id, AutoReview: &ScheduledTestAutoReviewConfig{Enabled: true}}}}}
			result := &ScheduledTestResult{Status: "success", OutputKind: "html", OutputHTML: "<svg/>", AccountID: &account, LatencyMs: 1234}
			runner.runAutomaticVisualReview(context.Background(), plan, result)
			require.NotNil(t, result.ExecutionInfo.AutoReview)
			require.Equal(t, tc.verdict, result.ExecutionInfo.AutoReview.Verdict)
			require.Equal(t, tc.calls, judge.calls)
			require.Equal(t, tc.calls, result.ExecutionInfo.AutoReview.Attempts)
			require.False(t, result.ExecutionInfo.AutoReview.ReviewedAt.IsZero())
			require.EqualValues(t, 1234, result.LatencyMs, "review time never alters generation duration")
		})
	}
}

func TestAutomaticVisualReviewConfirmationRequiresSharedFailureCriterion(t *testing.T) {
	one := &ScheduledTestVisualVerdict{Verdict: "fail", Criteria: []ScheduledTestVisualCriterion{{Key: "bicycle", Passed: false}}}
	two := &ScheduledTestVisualVerdict{Verdict: "fail", Criteria: []ScheduledTestVisualCriterion{{Key: "motion", Passed: false}}}
	require.False(t, scheduledTestReviewsShareFailure(one, two))
	two.Criteria[0].Key = "bicycle"
	require.True(t, scheduledTestReviewsShareFailure(one, two))
}
func TestAutoReviewAccountIndependence(t *testing.T) {
	parent := int64(3)
	for _, tc := range []struct {
		name          string
		tested, judge *Account
		more          map[int64]*Account
		want          bool
	}{
		{name: "independent", tested: &Account{ID: 1}, judge: &Account{ID: 2}, want: true},
		{name: "same credentials", tested: &Account{ID: 1, Credentials: map[string]any{"api_key": "same"}}, judge: &Account{ID: 2, Credentials: map[string]any{"api_key": "same"}}},
		{name: "same parent", tested: &Account{ID: 1, ParentAccountID: &parent}, judge: &Account{ID: 2, ParentAccountID: &parent}, more: map[int64]*Account{3: {ID: 3}}},
		{name: "missing parent", tested: &Account{ID: 1, ParentAccountID: &parent}, judge: &Account{ID: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := map[int64]*Account{1: tc.tested, 2: tc.judge}
			for k, v := range tc.more {
				accounts[k] = v
			}
			r := &ScheduledTestRunnerService{accountRepo: autoReviewAccountsStub{accounts: accounts}}
			id := int64(1)
			require.Equal(t, tc.want, r.reviewAccountsIndependent(context.Background(), &id, 2))
			require.False(t, r.reviewAccountsIndependent(context.Background(), &id, 1))
		})
	}
}
func TestAutomaticReviewProtectionAndPrivacy(t *testing.T) {
	id := int64(1)
	p := &ScheduledTestPlan{TestDefinitionIDs: []int64{id}, Protection: ScheduledTestProtectionConfig{Enabled: true, Mode: "all_pass", Rules: []ScheduledTestProtectionRule{{TestDefinitionID: id, AutoReview: &ScheduledTestAutoReviewConfig{Enabled: true}}}}}
	require.NoError(t, validateScheduledTestProtection(p))
	rule := p.Protection.Rules[0]
	require.True(t, rule.Vote.Enabled)
	require.False(t, rule.Vote.PublicEnabled)
	require.NoError(t, validateProtectionOutputKind(&rule, "html"))
	require.Error(t, validateProtectionOutputKind(&rule, "number"))
	r := &ScheduledTestResult{Status: "success", OutputKind: "html", LatencyMs: 100, ExecutionInfo: &ScheduledTestExecutionInfo{AutoReview: &ScheduledTestAutoReviewSummary{Verdict: "pass", JudgeAccountID: 42}}}
	verdict, _ := evaluateScheduledTestProtection(rule, r)
	require.Equal(t, "pass", verdict)
	rule.Thresholds = []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 50}}
	verdict, _ = evaluateScheduledTestProtection(rule, r)
	require.Equal(t, "fail", verdict)
	verdict, _ = EvaluateScheduledTestPassRateVerdict(rule, r)
	require.Equal(t, "pass", verdict, "duration is not ability evidence")
	r.ExecutionInfo.AutoReview.Verdict = "pending"
	rule.Thresholds = nil
	verdict, _ = EvaluateScheduledTestPassRateVerdict(rule, r)
	require.Equal(t, "pending", verdict)
	decision, _ := EvaluateScheduledTestAllPass(p.Protection, map[int64]string{id: "pending"}, nil, true)
	require.Equal(t, "pause", decision.Action.Scheduling)
	private := r.ExecutionInfo
	normalizePublicTestResults([]*ScheduledTestResult{r})
	require.Nil(t, r.ExecutionInfo.AutoReview)
	require.NotNil(t, private.AutoReview, "public serialization must not mutate private result info")
}
