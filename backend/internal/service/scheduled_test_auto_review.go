package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const ScheduledTestReviewRubricVersion = "pelican-visual-v1"

type ScheduledTestAutoReviewConfig struct {
	Enabled bool `json:"enabled"`
}

func (r ScheduledTestProtectionRule) UsesAutoReview() bool {
	return r.AutoReview != nil && r.AutoReview.Enabled
}

// The trusted judge is configured once, independently of the tested plans.
type ScheduledTestReviewSettings struct {
	Enabled        bool   `json:"enabled"`
	JudgeAccountID int64  `json:"judge_account_id"`
	JudgeModel     string `json:"judge_model"`
	DailyRequests  int    `json:"daily_requests"`
}

func (s *ScheduledTestReviewSettings) Validate() error {
	if s == nil || !s.Enabled {
		return nil
	}
	if s.JudgeAccountID <= 0 || strings.TrimSpace(s.JudgeModel) == "" || len(s.JudgeModel) > 200 || s.DailyRequests < 1 || s.DailyRequests > 1000000 {
		return fmt.Errorf("automatic review requires a judge account, model (maximum 200 characters), and daily requests 1..1000000")
	}
	return nil
}

type ScheduledTestAutoReviewSummary struct {
	Verdict        string                         `json:"verdict"`
	Reason         string                         `json:"reason"`
	JudgeAccountID int64                          `json:"judge_account_id,omitempty"`
	JudgeModel     string                         `json:"judge_model,omitempty"`
	Attempts       int                            `json:"attempts"`
	ReviewedAt     time.Time                      `json:"reviewed_at"`
	RubricVersion  string                         `json:"rubric_version"`
	Criteria       []ScheduledTestVisualCriterion `json:"criteria,omitempty"`
}

// Reservation is atomic across processes and includes confirmation calls.
// A reserved attempt is never refunded after an ambiguous transport failure.
type ScheduledTestAutoReviewRepository interface {
	LoadReviewSettings(context.Context) (*ScheduledTestReviewSettings, error)
	ReserveReview(context.Context, ScheduledTestReviewSettings) (bool, error)
}

func (s *ScheduledTestRunnerService) runAutomaticVisualReview(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) {
	rule := plan.ProtectionRule(plan.TestDefinitionID)
	if rule == nil || !rule.UsesAutoReview() || result == nil || result.OutputKind != "html" || result.Status != "success" || ctx.Err() != nil {
		return
	}
	if result.ExecutionInfo == nil {
		result.ExecutionInfo = newScheduledTestExecutionInfo()
	}
	review := &ScheduledTestAutoReviewSummary{Verdict: "pending", Reason: "等待自动视觉评审", RubricVersion: ScheduledTestReviewRubricVersion}
	result.ExecutionInfo.AutoReview = review
	defer func() { review.ReviewedAt = time.Now().UTC() }()
	if s.scheduledSvc == nil {
		review.Reason = "自动评审配置不可用，等待人工复核或下一轮检测"
		return
	}
	repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestAutoReviewRepository)
	if !ok {
		review.Reason = "自动评审配置不可用，等待人工复核或下一轮检测"
		return
	}
	// Keep browser and judge concurrency separate from upstream generation.
	// This deadline includes queueing and both possible judge calls.
	reviewCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if s.reviewSem != nil {
		select {
		case s.reviewSem <- struct{}{}:
			defer func() { <-s.reviewSem }()
		case <-reviewCtx.Done():
			review.Reason = "自动评审排队超时，等待人工复核或下一轮检测"
			return
		}
	}
	settings, err := repo.LoadReviewSettings(reviewCtx)
	if err != nil || settings == nil || !settings.Enabled || settings.Validate() != nil {
		review.Reason = "请先在检测运营中启用并配置可信视觉裁判"
		return
	}
	review.JudgeAccountID, review.JudgeModel = settings.JudgeAccountID, strings.TrimSpace(settings.JudgeModel)
	if !s.reviewAccountsIndependent(reviewCtx, result.AccountID, settings.JudgeAccountID) {
		review.Reason = "被测账号与裁判账号相同或无法确认独立，请选择独立裁判或人工复核"
		return
	}
	if s.reviewRenderer == nil || s.reviewJudge == nil {
		review.Reason = "自动评审服务不可用，等待人工复核或下一轮检测"
		return
	}
	frames, err := s.reviewRenderer.Render(reviewCtx, result.OutputHTML)
	if err != nil || len(frames) != 4 {
		review.Reason = "作品渲染未完成，等待人工复核或下一轮检测"
		return
	}
	judge := func() *ScheduledTestVisualVerdict {
		reserved, err := repo.ReserveReview(reviewCtx, *settings)
		if err != nil || !reserved {
			review.Reason = "自动评审每日预算已用完或配置已变更，等待人工复核或下一轮检测"
			return nil
		}
		review.Attempts++
		verdict, err := s.reviewJudge.Judge(reviewCtx, settings.JudgeAccountID, review.JudgeModel, frames)
		if err != nil || verdict == nil {
			review.Reason = "视觉裁判调用未完成，等待人工复核或下一轮检测"
			return nil
		}
		if verdict.Confidence < .85 || verdict.Verdict != "pass" && verdict.Verdict != "fail" {
			review.Reason = "视觉裁判结论不确定，等待人工复核"
			review.Criteria = verdict.Criteria
			return nil
		}
		return verdict
	}
	first := judge()
	if first == nil {
		return
	}
	review.Criteria = first.Criteria
	if first.Verdict == "pass" {
		review.Verdict, review.Reason = "pass", first.Reason
		return
	}
	// Rejudge the same generated artifact; do not spend another generation or
	// let a successful regeneration erase an observed ability failure.
	second := judge()
	if second == nil {
		return
	}
	if second.Verdict != "fail" || !scheduledTestReviewsShareFailure(first, second) {
		review.Reason = "两次视觉评审结论有分歧，等待人工复核"
		review.Criteria = second.Criteria
		return
	}
	review.Verdict, review.Reason, review.Criteria = "fail", second.Reason, second.Criteria
}

func scheduledTestReviewsShareFailure(first, second *ScheduledTestVisualVerdict) bool {
	if first == nil || second == nil || first.Verdict != "fail" || second.Verdict != "fail" {
		return false
	}
	failed := make(map[string]bool, len(first.Criteria))
	for _, criterion := range first.Criteria {
		if !criterion.Passed {
			failed[criterion.Key] = true
		}
	}
	for _, criterion := range second.Criteria {
		if !criterion.Passed && failed[criterion.Key] {
			return true
		}
	}
	return false
}

func (s *ScheduledTestRunnerService) reviewAccountsIndependent(ctx context.Context, testedID *int64, judgeID int64) bool {
	if testedID == nil || *testedID == judgeID || s.accountRepo == nil {
		return false
	}
	resolve := func(id int64) (*Account, bool) {
		seen := map[int64]bool{}
		for depth := 0; depth < 8; depth++ {
			if seen[id] {
				return nil, false
			}
			seen[id] = true
			account, err := s.accountRepo.GetByID(ctx, id)
			if err != nil || account == nil {
				return nil, false
			}
			if account.ParentAccountID == nil {
				return account, true
			}
			id = *account.ParentAccountID
		}
		return nil, false
	}
	tested, ok := resolve(*testedID)
	if !ok {
		return false
	}
	judge, ok := resolve(judgeID)
	if !ok || tested.ID == judge.ID {
		return false
	}
	for _, key := range []string{"account_id", "chatgpt_account_id", "api_key", "access_token", "refresh_token"} {
		if value := tested.GetCredential(key); value != "" && value == judge.GetCredential(key) {
			return false
		}
	}
	return true
}
