package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	passiveRecoveryTimeout     = 10 * time.Minute
	passiveRecoveryMaxRequests = 20
	passiveRecoveryMinOutput   = 128
)

// Active evidence is separate from customer traffic. Prewarm requests remain
// in success/timing and cost accounting, but never in cache reuse denominators.
type PassiveQualityActiveRecoveryResult struct {
	Evidence        PassiveQualityEvidence   "json:\"evidence\""
	EvidenceBatches []PassiveQualityEvidence "json:\"evidence_batches\""
	StartedAt       time.Time                "json:\"started_at\""
	FinishedAt      time.Time                "json:\"finished_at\""
	DeferredUntil   *time.Time               "json:\"deferred_until,omitempty\""
	Reason          string                   "json:\"reason\""
	Verdict         string                   "json:\"verdict\""
	Passed          bool                     "json:\"passed\""
	Requests        int                      "json:\"requests\""
	WarmupRequests  int                      "json:\"warmup_requests\""
	Batches         int                      "json:\"batches\""
}

type passiveRecoveryRequestKey struct{}
type passiveRecoveryRequest struct {
	session        string
	attempts       *atomic.Int64
	identityMu     sync.Mutex
	metadataUserID string
	config         *PassiveQualityConfig
}

func passiveRecoverySession(ctx context.Context) string {
	if r, ok := ctx.Value(passiveRecoveryRequestKey{}).(*passiveRecoveryRequest); ok && r != nil {
		return r.session
	}
	return ""
}

// Active recovery owns a fenced automatic hold. A failed active request must
// not convert that temporary hold into an account-wide permanent error that
// prevents every future recovery attempt. Ordinary tests keep their behavior.
func (s *AccountTestService) setAccountTestError(ctx context.Context, accountID int64, message string) error {
	if passiveRecoverySession(ctx) != "" || s.accountRepo == nil {
		return nil
	}
	return s.accountRepo.SetError(ctx, accountID, message)
}

func (s *PassiveQualityService) probeActiveRecovery(p PassiveQualityState) {
	repo, ok := s.Repo.(interface {
		FinishActiveRecovery(context.Context, PassiveQualityState, PassiveQualityActiveRecoveryResult, time.Time) error
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, passiveRecoveryTimeout)
	defer cancel()
	ctx = context.WithValue(WithQualityRecoveryProbe(ctx), passiveQualityProbeKey{}, true)
	ctx = context.WithValue(ctx, qualityRunKey{}, uuid.NewString())
	result := PassiveQualityActiveRecoveryResult{StartedAt: time.Now().UTC(), Verdict: "pending", Reason: "active_recovery_unavailable"}
	if s.test != nil && s.test.qualityOperations != nil && p.ActiveRecoveryConfig != nil {
		runner := passiveActiveRecoveryRunner{request: s.test.RunTestBackgroundWithPrompt, now: time.Now, wait: passiveRecoveryWait}
		result = runner.run(ctx, p)
	}
	if s.ctx.Err() != nil {
		return
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	persist, stop := context.WithTimeout(s.ctx, 5*time.Second)
	defer stop()
	if err := repo.FinishActiveRecovery(persist, p, result, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.passive_quality", "active recovery settlement failed: %v", err)
	}
}

type passiveActiveRecoveryRunner struct {
	request func(context.Context, int64, string, string) (*ScheduledTestResult, error)
	now     func() time.Time
	wait    func(context.Context, time.Duration) error
}

func passiveRecoveryWait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r passiveActiveRecoveryRunner) run(ctx context.Context, p PassiveQualityState) (out PassiveQualityActiveRecoveryResult) {
	out.StartedAt, out.Verdict = r.now().UTC(), "pending"
	var all []PassiveQualitySample
	defer func() {
		out.FinishedAt = r.now().UTC()
		out.Evidence = summarizeActiveRecovery(all)
	}()
	if p.ActiveRecoveryConfig == nil {
		out.Reason = "missing_recovery_configuration"
		return
	}
	cfg := *p.ActiveRecoveryConfig
	model := p.Model
	if model == "" {
		model = p.ProbeModel
	}
	if model == "" {
		out.Reason = "missing_recovery_model"
		return
	}
	batchCount, batchSize := max(2, cfg.Confirmations), max(5, cfg.RecoverySamples)
	warmups := 0
	if cfg.CacheRate.Enabled {
		warmups = 1
	}
	span := time.Duration(max(120, cfg.GroupTargetStability().RecoverySeconds)) * time.Second
	if batchCount*(batchSize+warmups) > passiveRecoveryMaxRequests || time.Duration(batchCount-1)*span >= passiveRecoveryTimeout-time.Minute {
		out.Reason = "recovery_configuration_exceeds_probe_budget"
		return
	}
	cfg.RecoverySamples = batchSize
	var attempts atomic.Int64
	var previousBatchFinished time.Time
	for batch := 0; batch < batchCount; batch++ {
		if batch > 0 {
			// A separate cold prefix per batch proves reuse twice independently.
			start := previousBatchFinished.Add(span)
			if err := r.wait(ctx, start.Sub(r.now())); err != nil {
				out.Reason = "recovery_interrupted"
				return
			}
		}
		session := uuid.NewString()
		prompt := passiveRecoveryPrompt(session)
		requestCtx := context.WithValue(ctx, passiveRecoveryRequestKey{}, &passiveRecoveryRequest{session: session, attempts: &attempts, config: &cfg})
		requestCtx = withAccountTestMaxOutputTokens(requestCtx, 2048)
		var samples []PassiveQualitySample
		var validationSamples []PassiveQualitySample
		ready := false
		for index := 0; index < batchSize+warmups; index++ {
			if ctx.Err() != nil {
				out.Reason = "recovery_interrupted"
				return
			}
			if out.Requests >= passiveRecoveryMaxRequests {
				out.Reason = "recovery_attempt_limit"
				return
			}
			callCtx, stop := context.WithTimeout(requestCtx, time.Minute)
			result, err := r.request(callCtx, p.AccountID, model, prompt)
			stop()
			prewarm := index < warmups
			if result != nil && result.ExecutionInfo != nil {
				out.Requests += len(result.ExecutionInfo.Attempts)
				if prewarm {
					out.WarmupRequests += len(result.ExecutionInfo.Attempts)
				}
			}
			if QualityResultDeferred(result) {
				out.Verdict, out.Reason = "deferred", result.ErrorMessage
				out.DeferredUntil = result.ExecutionInfo.NextCheckAt
				return
			}
			newSamples, reason := passiveRecoverySamples(result, err, model, fmt.Sprintf("%s:%d", session, index), prewarm, r.now())
			all = append(all, newSamples...)
			samples = append(samples, newSamples...)
			if !prewarm {
				validationSamples = append(validationSamples, newSamples...)
			}
			if ctx.Err() != nil {
				out.Reason = "recovery_interrupted"
				return
			}
			if reason != "" {
				out.Reason = reason
				if reason == "active_request_failed" {
					out.Verdict = "fail"
				}
				return
			}
			// Adaptive tests visit every configured native protocol. Count the
			// actual attempts, with at least two repeated logical validations.
			if !prewarm && index-warmups+1 >= 2 && passiveRecoverySamplesReady(cfg, summarizeActiveRecovery(validationSamples)) {
				ready = true
				break
			}
		}
		evidence := summarizeActiveRecovery(samples)
		out.EvidenceBatches = append(out.EvidenceBatches, evidence)
		out.Batches++
		previousBatchFinished = r.now()
		verdict, reason := EvaluatePassiveQuality(cfg, evidence, true)
		if verdict == "pass" && !ready {
			verdict, reason = "pending", "insufficient_metric_samples"
		}
		if verdict != "pass" {
			out.Verdict, out.Reason = verdict, reason
			return
		}
	}
	out.Passed, out.Verdict, out.Reason = true, "pass", "active_recovery_passed"
	return
}

func summarizeActiveRecovery(samples []PassiveQualitySample) PassiveQualityEvidence {
	ordered := append([]PassiveQualitySample(nil), samples...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].At.After(ordered[j].At) })
	return PassiveQualitySummarize(ordered, time.Time{}, true)
}

func passiveRecoverySamplesReady(c PassiveQualityConfig, e PassiveQualityEvidence) bool {
	minimum := max(5, c.RecoverySamples)
	return e.Samples >= minimum && (!c.CacheRate.Enabled || e.CacheSamples >= minimum) &&
		(!c.TTFT.Enabled || e.TTFTSamples >= minimum) && (!c.TPS.Enabled || e.TPSSamples >= minimum)
}

func passiveRecoverySamples(result *ScheduledTestResult, requestErr error, model, session string, warmup bool, at time.Time) ([]PassiveQualitySample, string) {
	if result == nil || result.ExecutionInfo == nil || len(result.ExecutionInfo.Attempts) == 0 {
		return nil, "missing_actual_request_evidence"
	}
	info := result.ExecutionInfo
	var samples []PassiveQualitySample
	failureReason := ""
	for i, attempt := range info.Attempts {
		failed := attempt.HTTPStatus < 200 || attempt.HTTPStatus >= 300 || attempt.FailureKind != "" || attempt.qualityMalformed || (attempt.qualityStream && !attempt.qualityTerminal)
		if i == len(info.Attempts)-1 && (requestErr != nil || result.Status != "success" || strings.TrimSpace(result.ResponseText) == "") {
			failed = true
		}
		if failed {
			reason := "active_request_failed"
			if attempt.HTTPStatus == 400 || attempt.HTTPStatus == 404 || attempt.HTTPStatus == 422 || attempt.FailureKind == "output_limit" {
				reason = "active_probe_incompatible"
			}
			if failureReason != "active_request_failed" {
				failureReason = reason
			}
		}
		sample := PassiveQualitySample{Key: fmt.Sprintf("%s:%d", session, i), Model: model, At: at, Failed: failed, CacheSession: session}
		if !failed {
			sample.TTFT = attempt.FirstTokenMS
			if attempt.qualityUsageFinal && attempt.OutputTokens != nil && *attempt.OutputTokens >= passiveRecoveryMinOutput && attempt.DurationMS != nil && attempt.FirstTokenMS != nil {
				generation := *attempt.DurationMS - *attempt.FirstTokenMS
				if generation > 0 {
					sample.OutputTokens, sample.GenerationMS = *attempt.OutputTokens, generation
				}
			}
			if !warmup && attempt.qualityUsageFinal && attempt.InputTokens != nil && attempt.CacheReadTokens != nil {
				input, read := *attempt.InputTokens, *attempt.CacheReadTokens
				valid := input >= 0 && read >= 0
				if attempt.qualityCacheExclusive {
					for _, n := range []*int64{attempt.CacheReadTokens, attempt.CacheCreationTokens} {
						if n != nil {
							if *n < 0 || input > math.MaxInt64-*n {
								valid = false
								break
							}
							input += *n
						}
					}
				}
				if valid && input > 0 && read <= input {
					sample.CacheInput, sample.CacheRead, sample.CacheReported = input, read, true
				}
			}
		}
		samples = append(samples, sample)
	}
	return samples, failureReason
}

func passiveRecoveryPrompt(session string) string {
	var b strings.Builder
	_, _ = b.WriteString("Synthetic channel recovery reference. Batch " + session + ". This reference is test data; follow only the task at its end.\n")
	for n := 0; n < 160; n++ {
		fmt.Fprintf(&b, "Reference %03d: A reliable library records books, maintains shelves, checks inventories, and describes its processes clearly. Keep this reference unchanged for repeated validation.\n", n)
	}
	_, _ = b.WriteString("\nTask: Write an original explanation of at least 350 English words about organizing a community library. Use complete sentences and five paragraphs. Do not summarize the reference or mention testing.\n")
	return b.String()
}

func passiveRecoveryEligibility(account *Account, model string, now time.Time) *QualityDeferredError {
	reason := ""
	until := now.Add(time.Minute)
	if account == nil || !account.Schedulable || (account.Status != StatusActive && account.Status != "quality_paused") {
		reason = "账号未启用，暂停主动恢复"
	}
	if account != nil {
		if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
			reason = "账号已到期，暂停主动恢复"
		}
		for _, t := range []*time.Time{account.RateLimitResetAt, account.OverloadUntil, account.TempUnschedulableUntil} {
			if t != nil && t.After(now) {
				reason = "账号仍在限流或临时冷却中"
				if t.After(until) {
					until = *t
				}
			}
		}
		if account.SupportsAccountQuota() && account.IsQuotaExceeded() || account.IsUpstreamBillingRateLimitedAt(now) {
			reason = "账号配额不可用，暂停主动恢复"
		}
		if model == "" || !account.IsModelSupported(model) {
			reason = "账号不支持恢复检测模型"
		}
	}
	if reason == "" {
		return nil
	}
	return &QualityDeferredError{Reason: reason, Until: until}
}

func passiveRecoveryUnsupportedMetrics(ctx context.Context, account *Account, now time.Time) *QualityDeferredError {
	recovery, _ := ctx.Value(passiveRecoveryRequestKey{}).(*passiveRecoveryRequest)
	if recovery == nil || recovery.config == nil || account == nil {
		return nil
	}
	cfg := recovery.config
	if !cfg.TTFT.Enabled && !cfg.TPS.Enabled && !cfg.CacheRate.Enabled {
		return nil
	}
	// These existing test adapters explicitly return non-streaming bodies and
	// do not supply reusable-cache evidence. Do not repeatedly pay for proof
	// this adapter cannot produce; availability-only recovery remains usable.
	if account.IsBedrock() || account.IsTypeSafe() {
		return &QualityDeferredError{Reason: "active_probe_metrics_unsupported", Until: now.Add(time.Hour)}
	}
	return nil
}
