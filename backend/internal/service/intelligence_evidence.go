package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type IntelligenceCheck struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type IntelligenceEvidence struct {
	Checks          []IntelligenceCheck `json:"checks"`
	PolicyVersion   string              `json:"policy_version"`
	AutoVerdict     string              `json:"auto_verdict"`
	ArtworkAttempts int                 `json:"artwork_attempts,omitempty"`
	ArtworkRetryAt  *time.Time          `json:"artwork_retry_at,omitempty"`
	Artwork         string              `json:"artwork"`
	Consecutive429  int                 `json:"consecutive_429"`
	CandyHTTPStatus int                 `json:"candy_http_status"`
}

func (e *IntelligenceEvidence) FinalVerdict(review string) string {
	if e == nil {
		return "unknown"
	}
	if e.AutoVerdict == "pass" {
		if e.Artwork != "default" {
			return "unknown"
		}
		if review == "fail" || review == "veto" || review == "rejected" {
			return "confirmed"
		}
	}
	return e.AutoVerdict
}

type intelligenceHistoryRepository interface {
	PreviousIntelligence429(context.Context, *ScheduledTestResult, string) (int, error)
}

func isIntelligenceAccountTest(ctx context.Context) bool {
	plan, _ := ctx.Value(scheduledTestPlanContextKey{}).(*ScheduledTestPlan)
	return plan != nil && plan.Protection.Intelligence != nil
}

func emptyIntelligenceEvidence(version string) *IntelligenceEvidence {
	return &IntelligenceEvidence{
		PolicyVersion: version,
		Artwork:       "skipped",
		AutoVerdict:   "unknown",
		Checks: []IntelligenceCheck{
			{"candy", "unknown", "未取得可判定的答案"},
			{"http_429", "unknown", "请求未完成"},
		},
	}
}

func retryableIntelligenceArtwork(result *ScheduledTestResult, version string) bool {
	if result == nil || result.ExecutionInfo == nil || result.ExecutionInfo.Intelligence == nil {
		return false
	}
	ev := result.ExecutionInfo.Intelligence
	return ev.PolicyVersion == version && ev.AutoVerdict == "pass" && ev.Artwork == "missing"
}

type intelligenceRequest func(context.Context, string, string, int) *ScheduledTestResult

func (s *ScheduledTestRunnerService) runIntelligence(ctx context.Context, plan *ScheduledTestPlan, pending *ScheduledTestResult, pelican string, execute intelligenceRequest) *ScheduledTestResult {
	started := time.Now()
	e := emptyIntelligenceEvidence(plan.Protection.Intelligence.Version)
	info := newScheduledTestExecutionInfo()
	// Each transport attempt uses the same account-test budget and accounting
	// as a scheduled test. Only the designated candy outcome contributes to 429.
	request := func(c context.Context, prompt, kind string, tokens int) *ScheduledTestResult {
		if c.Err() != nil || info.FailureKind == "quality_deferred" {
			return nil
		}
		r := execute(c, prompt, kind, tokens)
		if r != nil && r.ExecutionInfo != nil {
			info = mergeScheduledTestExecution(info, r.ExecutionInfo)
		}
		return r
	}
	if retryableIntelligenceArtwork(pending, e.PolicyVersion) {
		prev := *pending.ExecutionInfo.Intelligence
		e = &prev
	} else {
		s.intelligenceChecks(ctx, plan, pending, e, request)
	}
	out := &ScheduledTestResult{Status: "success", StartedAt: started}
	if e.AutoVerdict == "pass" {
		e.Artwork = "missing"
		budget := accountTestOutputTokenLimit(ctx)
		if policy := plan.Protection.Intelligence; policy.ArtworkPrompt != "" {
			pelican = policy.ArtworkPrompt
			budget = policy.ArtworkMaxOutputTokens
		}
		art := request(ctx, strings.ReplaceAll(pelican, "摩托车", "自行车"), "html", budget)
		e.ArtworkRetryAt = nil
		if ctx.Err() == nil && art != nil && !QualityResultDeferred(art) {
			e.ArtworkAttempts++
		}
		if art != nil && art.Status == "success" && art.OutputHTML != "" {
			e.Artwork = "default"
			out.OutputHTML = art.OutputHTML
		} else {
			out.Status = "failed"
			out.ErrorMessage = "鹈鹕作品生成失败，待补测"
			if e.ArtworkAttempts < 3 {
				next := time.Now().Add(5 * time.Minute)
				if e.ArtworkAttempts >= 2 {
					next = time.Now().Add(15 * time.Minute)
				}
				for _, until := range []*time.Time{info.NextCheckAt, info.RetryAfter} {
					if until != nil && until.After(next) {
						next = *until
					}
				}
				e.ArtworkRetryAt = &next
			}
		}
	}
	if e.AutoVerdict == "unknown" {
		out.Status = "failed"
		out.ErrorMessage = "本轮证据不足"
	}
	if ctx.Err() != nil {
		out.Status = "failed"
		out.ErrorMessage = ctx.Err().Error()
		out.OutputHTML = ""
		// A cancelled artwork retry may still be retried against the original checks.
		if e.AutoVerdict == "pass" {
			e.Artwork = "missing"
		} else {
			e = emptyIntelligenceEvidence(e.PolicyVersion)
		}
	}
	out.FinishedAt = time.Now()
	out.LatencyMs = out.FinishedAt.Sub(started).Milliseconds()
	info.Intelligence = e
	out.ExecutionInfo = info
	return out
}

func (s *ScheduledTestRunnerService) intelligenceChecks(ctx context.Context, plan *ScheduledTestPlan, pending *ScheduledTestResult, e *IntelligenceEvidence, request intelligenceRequest) {
	var candy *ScheduledTestResult
	if policy := plan.Protection.Intelligence; policy.CandyPrompt != "" {
		candy = request(ctx, policy.CandyPrompt, "number", policy.CandyMaxOutputTokens)
	} else if s.scheduledSvc != nil && s.scheduledSvc.definitionRepo != nil {
		def, err := s.scheduledSvc.definitionRepo.GetByKey(ctx, "candy")
		if err == nil && def != nil && def.Enabled && def.OutputKind == "number" {
			// Only one logical request per round; configured compatibility retries
			// inside the transport are reduced to the final upstream HTTP status.
			candy = request(ctx, def.Prompt, "number", def.MaxOutputTokens)
		}
	}
	if candy == nil || QualityResultDeferred(candy) {
		return
	}
	if candy.ExecutionInfo != nil && len(candy.ExecutionInfo.Attempts) > 0 {
		e.CandyHTTPStatus = candy.ExecutionInfo.Attempts[len(candy.ExecutionInfo.Attempts)-1].HTTPStatus
	}
	if e.CandyHTTPStatus == 429 {
		if pending == nil || s.scheduledSvc == nil {
			return
		}
		repo, ok := s.scheduledSvc.resultRepo.(intelligenceHistoryRepository)
		if !ok {
			return
		}
		prev, err := repo.PreviousIntelligence429(ctx, pending, e.PolicyVersion)
		if err != nil {
			return
		}
		e.Consecutive429 = min(3, prev+1)
		e.Checks[1].Detail = fmt.Sprintf("连续限流 %d/3", e.Consecutive429)
		if e.Consecutive429 == 3 {
			e.Checks[1].Status = "fail"
			e.AutoVerdict = "confirmed"
		} else {
			e.AutoVerdict = "suspect"
		}
		return
	}
	if e.CandyHTTPStatus < 200 || e.CandyHTTPStatus >= 300 {
		return
	}
	if candy.Status != "success" && (candy.ExecutionInfo == nil || !candy.ExecutionInfo.OutputContractFailed) {
		return
	}
	e.Checks[1].Status, e.Checks[1].Detail = "pass", "本轮请求成功，连续限流 0/3"
	if candy.Status != "success" || candy.OutputNumeric == nil {
		return
	}
	if *candy.OutputNumeric == 21 {
		e.Checks[0].Status, e.Checks[0].Detail = "pass", "答案为 21"
		e.AutoVerdict = "pass"
	} else {
		e.Checks[0].Status, e.Checks[0].Detail = "fail", fmt.Sprintf("回答 %g，参考答案 21", *candy.OutputNumeric)
		e.AutoVerdict = "confirmed"
	}
}
