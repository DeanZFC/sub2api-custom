package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type autoReviewSequenceRenderer struct {
	errors        []error
	calls         int
	html          []string
	invalidFrames bool
	cancel        context.CancelFunc
}

func (r *autoReviewSequenceRenderer) Render(_ context.Context, html string) ([]ScheduledTestReviewFrame, error) {
	r.calls++
	r.html = append(r.html, html)
	if r.cancel != nil {
		r.cancel()
	}
	if r.calls <= len(r.errors) && r.errors[r.calls-1] != nil {
		return nil, r.errors[r.calls-1]
	}
	if r.invalidFrames {
		return make([]ScheduledTestReviewFrame, 3), nil
	}
	return make([]ScheduledTestReviewFrame, 4), nil
}

func TestAutomaticReviewRenderRecovery(t *testing.T) {
	transient := func(code string) error { return &scheduledTestRenderError{code: code, diagnostic: "http_status"} }
	for _, tc := range []struct {
		name          string
		errors        []error
		invalidFrames bool
		wantCalls     int
		wantCode      string
		retryable     bool
	}{
		{name: "busy recovers", errors: []error{transient("renderer_busy")}, wantCalls: 2},
		{name: "connection recovers", errors: []error{transient("renderer_unavailable")}, wantCalls: 2},
		{name: "timeout recovers", errors: []error{context.DeadlineExceeded}, wantCalls: 2},
		{name: "retry exhausted", errors: []error{transient("renderer_busy"), transient("renderer_busy")}, wantCalls: 2, wantCode: "renderer_busy", retryable: true},
		{name: "browser deployment failure", errors: []error{transient("renderer_browser_unavailable")}, wantCalls: 1, wantCode: "renderer_browser_unavailable"},
		{name: "invalid content", errors: []error{transient("renderer_invalid_input")}, wantCalls: 1, wantCode: "renderer_invalid_input"},
		{name: "incomplete frames", invalidFrames: true, wantCalls: 1, wantCode: "renderer_invalid_response"},
		{name: "permission is permanent", errors: []error{os.ErrPermission}, wantCalls: 1, wantCode: "renderer_unavailable"},
		{name: "untrusted diagnostic", errors: []error{errors.New("secret HTML /private/path token=private")}, wantCalls: 1, wantCode: "renderer_render_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			renderer := &autoReviewSequenceRenderer{errors: tc.errors, invalidFrames: tc.invalidFrames}
			runner := &ScheduledTestRunnerService{reviewRenderer: renderer}
			review := &ScheduledTestAutoReviewSummary{Verdict: "pending"}
			result := &ScheduledTestResult{OutputHTML: "<svg>same artwork</svg>", LatencyMs: 1234}
			frames := runner.renderAutomaticReview(context.Background(), 17, result, review, 0)
			require.Equal(t, tc.wantCalls, renderer.calls)
			require.Equal(t, tc.wantCalls, review.RenderAttempts)
			require.Equal(t, tc.wantCode, review.ErrorCode)
			require.Equal(t, tc.retryable, review.RenderRetryable)
			require.Zero(t, review.Attempts, "render retry never reserves paid judge calls")
			require.EqualValues(t, 1234, result.LatencyMs)
			for _, html := range renderer.html {
				require.Equal(t, result.OutputHTML, html)
			}
			if tc.wantCode == "" {
				require.Len(t, frames, 4)
				require.Empty(t, review.FailureStage)
			} else {
				require.Nil(t, frames)
				require.Equal(t, "render", review.FailureStage)
				require.Equal(t, "pending", review.Verdict)
				require.NotContains(t, review.Reason, "private")
			}
		})
	}
}

func TestAutomaticReviewRenderCancellation(t *testing.T) {
	for _, cancelBefore := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		renderer := &autoReviewSequenceRenderer{errors: []error{&scheduledTestRenderError{code: "renderer_busy"}}, cancel: cancel}
		if cancelBefore {
			cancel()
		}
		runner := &ScheduledTestRunnerService{reviewRenderer: renderer}
		review := &ScheduledTestAutoReviewSummary{Verdict: "pending"}
		started := time.Now()
		frames := runner.renderAutomaticReview(ctx, 1, &ScheduledTestResult{OutputHTML: "<svg/>"}, review, time.Hour)
		require.Nil(t, frames)
		require.Less(t, time.Since(started), time.Second, "cancelled renders must not wait for retry")
		require.Equal(t, "renderer_cancelled", review.ErrorCode)
		require.False(t, review.RenderRetryable)
		if cancelBefore {
			require.Zero(t, renderer.calls)
		} else {
			require.Equal(t, 1, renderer.calls)
		}
	}
}

func TestAutomaticVisualReviewRecoversBeforeCallingJudge(t *testing.T) {
	repo := &autoReviewRepoStub{settings: &ScheduledTestReviewSettings{Enabled: true, JudgeAccountID: 2, JudgeModel: "vision", DailyRequests: 20}, remaining: 2}
	renderer := &autoReviewSequenceRenderer{errors: []error{&scheduledTestRenderError{code: "renderer_busy"}}}
	judge := &autoReviewJudgeStub{verdicts: []ScheduledTestVisualVerdict{{Verdict: "pass", Confidence: .95}}}
	runner := &ScheduledTestRunnerService{scheduledSvc: &ScheduledTestService{resultRepo: repo}, accountRepo: autoReviewAccountsStub{accounts: map[int64]*Account{1: {ID: 1}, 2: {ID: 2}}}, reviewRenderer: renderer, reviewJudge: judge}
	id, account := int64(10), int64(1)
	plan := &ScheduledTestPlan{Enabled: true, TestDefinitionID: &id, Protection: ScheduledTestProtectionConfig{Enabled: true, Rules: []ScheduledTestProtectionRule{{TestDefinitionID: id, AutoReview: &ScheduledTestAutoReviewConfig{Enabled: true}}}}}
	result := &ScheduledTestResult{Status: "success", OutputKind: "html", OutputHTML: "<svg/>", AccountID: &account, LatencyMs: 1234}
	runner.runAutomaticVisualReview(context.Background(), plan, result)
	require.NotNil(t, result.ExecutionInfo)
	require.NotNil(t, result.ExecutionInfo.AutoReview)
	review := result.ExecutionInfo.AutoReview
	require.Equal(t, "pass", review.Verdict)
	require.Equal(t, 2, review.RenderAttempts)
	require.Equal(t, 1, review.Attempts)
	require.Equal(t, 1, judge.calls)
	require.Equal(t, 1, repo.remaining)
	require.Empty(t, review.ErrorCode)
	require.Empty(t, review.FailureStage)
	require.GreaterOrEqual(t, review.RenderDurationMs, int64(1900))
	require.EqualValues(t, 1234, result.LatencyMs)
}
