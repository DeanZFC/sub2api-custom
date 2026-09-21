package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const scheduledTestAutomaticRetries = 3

// Private provenance marker: only the cron dispatch path sets this value.
type scheduledTestAutomaticRunKey struct{}

// runWithAutomaticRetries keeps persistence outside the retry loop. The same
// running row and account/type lock cover all attempts, with one final result.
func (s *ScheduledTestRunnerService) runWithAutomaticRetries(ctx context.Context, plan *ScheduledTestPlan, accountID *int64, execute func(context.Context) *ScheduledTestResult) *ScheduledTestResult {
	retryLimit := 0
	if automatic, _ := ctx.Value(scheduledTestAutomaticRunKey{}).(bool); automatic {
		retryLimit = scheduledTestAutomaticRetries
	}
	baseDelay := s.automaticRetryBaseDelay
	if baseDelay <= 0 {
		baseDelay = time.Second
	}
	var account, definition int64
	if accountID != nil {
		account = *accountID
	}
	if plan.TestDefinitionID != nil {
		definition = *plan.TestDefinitionID
	}
	started := time.Now()
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return scheduledTestFailedAttempt(started, err)
		}
		result := execute(ctx)
		if err := ctx.Err(); err != nil {
			return scheduledTestFailedAttempt(started, err)
		}
		if result == nil {
			result = scheduledTestFailedAttempt(started, fmt.Errorf("test returned no result"))
		}
		if result.Status == "success" {
			if attempt > 0 {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d definition=%d automatic_retry_succeeded retries=%d", plan.ID, account, definition, attempt)
			}
			return result
		}
		if result.Status != "failed" {
			result.Status = "failed"
			if result.ErrorMessage == "" {
				result.ErrorMessage = "test did not complete successfully"
			}
		}
		if attempt >= retryLimit {
			if retryLimit > 0 {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d definition=%d automatic_retry_exhausted retries=%d", plan.ID, account, definition, retryLimit)
			}
			return result
		}
		delay := baseDelay * time.Duration(1<<attempt)
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d definition=%d automatic_retry=%d/%d delay=%s", plan.ID, account, definition, attempt+1, retryLimit, delay)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return scheduledTestFailedAttempt(started, ctx.Err())
		}
	}
}

func scheduledTestFailedAttempt(started time.Time, err error) *ScheduledTestResult {
	if err == nil {
		err = context.Canceled
	}
	finished := time.Now()
	return &ScheduledTestResult{
		Status: "failed", ErrorMessage: err.Error(), StartedAt: started,
		FinishedAt: finished, LatencyMs: finished.Sub(started).Milliseconds(),
	}
}
