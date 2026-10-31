package service

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const scheduledTestAutomaticRetries = 3

type scheduledTestRetryPendingKey struct{}

var scheduledTestHTTPError = regexp.MustCompile(`(?i)(?:returned|status[=: ]+)\s*([45][0-9]{2})`)

// Classify failures for execution history and scheduler cooldowns. Every
// execution error may spend up to three additional requests, regardless of category.
func scheduledTestFailureKind(result *ScheduledTestResult) string {
	if result == nil {
		return "unknown"
	}
	if QualityResultDeferred(result) {
		return "quality_deferred"
	}
	if result.Status == "success" || result.Status == "passed" {
		return ""
	}
	info := result.ExecutionInfo
	code := 0
	if info != nil && len(info.Attempts) > 0 {
		code = info.Attempts[len(info.Attempts)-1].HTTPStatus
	}
	message := strings.ToLower(result.ErrorMessage)
	if strings.Contains(message, "context canceled") {
		return "canceled"
	}
	if code == 0 {
		if match := scheduledTestHTTPError.FindStringSubmatch(message); len(match) > 1 {
			code, _ = strconv.Atoi(match[1])
		}
	}
	// Quota exhaustion is often reported as HTTP 429, but is not rate limiting.
	for _, text := range []string{"insufficient_quota", "insufficient balance", "quota exhausted", "credit balance", "余额不足", "billing_hard_limit"} {
		if strings.Contains(message, text) {
			return "quota"
		}
	}
	if info != nil && info.FailureKind == "quota" {
		return "quota"
	}
	switch {
	case code == 401 || code == 403:
		return "auth"
	case code == 402:
		return "quota"
	case code == 429:
		return "rate_limit"
	case code == 408:
		return "network"
	case code >= 400 && code < 500:
		return "request"
	}
	if info != nil && info.FailureKind != "" && info.FailureKind != "unknown" {
		return info.FailureKind
	}
	if code >= 500 && code <= 599 {
		return "server"
	}
	if strings.Contains(message, "overloaded") {
		return "server"
	}
	for _, text := range []string{"truncated", "response incomplete", "max_output_tokens", "max_tokens", "finish_reason=length"} {
		if strings.Contains(message, text) {
			return "output_limit"
		}
	}
	for _, text := range []string{"expected html", "expected numeric", "expected number", "invalid response"} {
		if strings.Contains(message, text) {
			return "output_invalid"
		}
	}
	for _, text := range []string{"context deadline exceeded", "timeout", "connection reset", "connection refused", "unexpected eof", "stream ended before", "stream read error", "temporary"} {
		if strings.Contains(message, text) {
			return "network"
		}
	}
	return "unknown"
}
func (s *ScheduledTestRunnerService) runWithAutomaticRetries(ctx context.Context, plan *ScheduledTestPlan, accountID *int64, execute func(context.Context) *ScheduledTestResult) *ScheduledTestResult {
	delay := s.automaticRetryBaseDelay
	if delay <= 0 {
		delay = time.Second
	}
	started := time.Now()
	var totalMs int64
	var history *ScheduledTestExecutionInfo
	finishCanceled := func() *ScheduledTestResult {
		r := scheduledTestFailedAttempt(started, ctx.Err())
		r.LatencyMs = totalMs
		r.ExecutionInfo = history
		if r.ExecutionInfo != nil {
			r.ExecutionInfo.FailureKind = "canceled"
		}
		return r
	}
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return finishCanceled()
		}
		attemptStarted := time.Now()
		attemptCtx := context.WithValue(ctx, scheduledTestRetryPendingKey{}, attempt < scheduledTestAutomaticRetries)
		result := execute(attemptCtx)
		if result == nil {
			result = scheduledTestFailedAttempt(attemptStarted, fmt.Errorf("test returned no result"))
		}
		// Each attempt measures execution after obtaining a worker. Include all
		// attempts and retry backoff, but never charge local worker queues to the
		// upstream. Manual retries start a new measurement, even when reusing a row.
		if result.LatencyMs < 0 {
			totalMs = -1 // Missing timing must not turn into a passing zero duration.
		} else if totalMs >= 0 {
			totalMs += result.LatencyMs
		}
		result.LatencyMs = totalMs
		kind := scheduledTestFailureKind(result)
		if result.ExecutionInfo == nil {
			result.ExecutionInfo = newScheduledTestExecutionInfo()
		}
		result.ExecutionInfo.FailureKind = kind
		if n := len(result.ExecutionInfo.Attempts); n > 0 {
			result.ExecutionInfo.Attempts[n-1].FailureKind = kind
		}
		history = mergeScheduledTestExecution(history, result.ExecutionInfo)
		result.ExecutionInfo = history
		if ctx.Err() != nil {
			return finishCanceled()
		}
		// A deferred result means admission blocked the request (for example,
		// because the detection budget is exhausted), rather than an execution
		// error. Actual cancellation is handled by ctx.Err() above.
		if kind == "" || attempt >= scheduledTestAutomaticRetries || QualityResultDeferred(result) {
			return result
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d automatic_retry=%d/%d kind=%s", plan.ID, attempt+1, scheduledTestAutomaticRetries, kind)
		waitStarted := time.Now()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			if totalMs >= 0 {
				totalMs += time.Since(waitStarted).Milliseconds()
			}
			return finishCanceled()
		}
		if totalMs >= 0 {
			totalMs += time.Since(waitStarted).Milliseconds()
		}
	}
}
func scheduledTestFailedAttempt(started time.Time, err error) *ScheduledTestResult {
	if err == nil {
		err = context.Canceled
	}
	finished := time.Now()
	return &ScheduledTestResult{Status: "failed", ErrorMessage: err.Error(), StartedAt: started, FinishedAt: finished, LatencyMs: finished.Sub(started).Milliseconds()}
}
