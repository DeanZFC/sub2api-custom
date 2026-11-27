package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/modelfingerprint"
	"github.com/google/uuid"
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

func intelligenceAutoVerdict(checks []IntelligenceCheck) string {
	failures, passes := 0, 0
	for _, c := range checks {
		switch c.Status {
		case "pass":
			passes++
		case "fail":
			failures++
		}
	}
	switch {
	case failures >= 2:
		return "confirmed"
	case failures == 1:
		return "suspect"
	case passes == 4:
		return "pass"
	default:
		return "unknown"
	}
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

// The state marker is opaque. Keep it in this one account's in-memory probe,
// bound to one conversation and never serialize it to results or logs.
type intelligenceTurnKey struct{}
type intelligenceTurn struct {
	session                        string
	sent, received, previousAnswer string
	observed                       bool
}

const intelligenceTurnPrompt = "Reply with the single word OK."

func (t *intelligenceTurn) prepare(req *http.Request) {
	req.Header.Set("session_id", t.session)
	if t.sent != "" {
		req.Header.Set(openAICodexTurnStateHeader, t.sent)
	}
}
func (t *intelligenceTurn) continueInput(payload map[string]any) {
	if t.sent == "" {
		return
	}
	payload["input"] = []map[string]any{
		{"role": "user", "content": intelligenceTurnPrompt},
		{"role": "assistant", "content": t.previousAnswer},
		{"role": "user", "content": "Reply with the single word OK again."},
	}
}
func (t *intelligenceTurn) observe(req *http.Request, resp *http.Response) {
	if resp != nil && resp.Request != nil {
		req = resp.Request
	}
	t.observed = req != nil && resp != nil && resp.StatusCode == http.StatusOK && req.Header.Get(openAICodexTurnStateHeader) == t.sent && req.Header.Get("session_id") == t.session
	if t.observed {
		t.received = extractOpenAICodexTurnState(resp.Header)
	}
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
			{"fingerprint", "unknown", "未取得有效指纹样本"},
			{"codex_state", "unknown", "未取得有效续接状态"},
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
		// A cancelled artwork retry may still be retried against the original four checks.
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
	checks := e.Checks
	if modelfingerprint.Supports(plan.ModelID, plan.ReasoningEffort) {
		r := request(ctx, modelfingerprint.Prompt, "text", 0)
		if r != nil && r.Status == "success" {
			checks[0].Status, checks[0].Detail = modelfingerprint.Evaluate(plan.ModelID, plan.ReasoningEffort, r.ResponseText)
		}
	} else {
		checks[0].Detail = "该模型和推理档位缺少适用的指纹参考样本"
	}
	turn := &intelligenceTurn{session: uuid.NewString()}
	first := request(context.WithValue(ctx, intelligenceTurnKey{}, turn), intelligenceTurnPrompt, "text", 0)
	if first != nil && first.Status == "success" && turn.observed && turn.received != "" && len(turn.received) <= 16384 {
		follow := &intelligenceTurn{session: turn.session, sent: turn.received, previousAnswer: first.ResponseText}
		second := request(context.WithValue(ctx, intelligenceTurnKey{}, follow), "Reply with the single word OK again.", "text", 0)
		if second != nil && second.Status == "success" && follow.observed {
			if follow.received == "" || follow.received == follow.sent {
				checks[1] = IntelligenceCheck{"codex_state", "pass", "续接状态一致"}
			} else {
				checks[1] = IntelligenceCheck{"codex_state", "fail", "续接时返回了不同的状态标记"}
			}
		}
	}
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
	if candy != nil && !QualityResultDeferred(candy) {
		if candy.ExecutionInfo != nil && len(candy.ExecutionInfo.Attempts) > 0 {
			e.CandyHTTPStatus = candy.ExecutionInfo.Attempts[len(candy.ExecutionInfo.Attempts)-1].HTTPStatus
		}
		if candy.Status == "success" && candy.OutputNumeric != nil {
			if *candy.OutputNumeric == 21 {
				checks[2] = IntelligenceCheck{"candy", "pass", "答案为 21"}
			} else {
				checks[2] = IntelligenceCheck{"candy", "fail", fmt.Sprintf("回答 %g，参考答案 21", *candy.OutputNumeric)}
			}
		}
	}
	if e.CandyHTTPStatus >= 200 && e.CandyHTTPStatus < 300 && candy != nil && (candy.Status == "success" || (candy.ExecutionInfo != nil && candy.ExecutionInfo.OutputContractFailed)) {
		checks[3] = IntelligenceCheck{"http_429", "pass", "本轮请求成功，连续限流 0/3"}
	} else if e.CandyHTTPStatus == 429 && pending != nil && s.scheduledSvc != nil {
		if repo, ok := s.scheduledSvc.resultRepo.(intelligenceHistoryRepository); ok {
			prev, err := repo.PreviousIntelligence429(ctx, pending, e.PolicyVersion)
			if err == nil {
				e.Consecutive429 = min(3, prev+1)
				checks[3].Detail = fmt.Sprintf("连续限流 %d/3", e.Consecutive429)
				if e.Consecutive429 == 3 {
					checks[3].Status = "fail"
				}
			}
		}
	}
	e.Checks = checks
	e.AutoVerdict = intelligenceAutoVerdict(checks)
}
