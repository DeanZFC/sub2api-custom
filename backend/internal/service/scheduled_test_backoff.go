package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type scheduledTestAutomaticRunKey struct{}
type ScheduledTestBackoff struct {
	ConsecutiveFailures int       `json:"consecutive_failures"`
	NextCheckAt         time.Time `json:"next_check_at"`
}
type ScheduledTestBackoffRepository interface {
	GetTestBackoff(context.Context, int64, int64, string) (*ScheduledTestBackoff, error)
	RecordTestBackoff(context.Context, *ScheduledTestPlan, *ScheduledTestResult, string, bool, *time.Time) error
}

func ScheduledTestBackoffDelay(failures int) time.Duration {
	switch {
	case failures <= 1:
		return 5 * time.Minute
	case failures == 2:
		return 15 * time.Minute
	case failures == 3:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

// Changing model, prompts, rules or targeting immediately invalidates old cooldowns.
func scheduledTestQueuePolicy(queue []*scheduledTestExecutionTarget) string {
	if len(queue) == 0 {
		return ""
	}
	plan := *queue[0].plan
	plan.TestDefinitionID = nil
	plan.LastRunAt = nil
	plan.NextRunAt = nil
	plan.CreatedAt = time.Time{}
	plan.UpdatedAt = time.Time{}
	plan.SortOrder = 0
	type definition struct {
		ID           *int64
		Prompt, Kind string
	}
	definitions := []definition{}
	for _, target := range queue {
		definitions = append(definitions, definition{target.plan.TestDefinitionID, target.prompt, target.kind})
	}
	raw, _ := json.Marshal(struct {
		Plan        ScheduledTestPlan
		Definitions []definition
	}{plan, definitions})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
func (s *ScheduledTestRunnerService) deferAccountQueue(ctx context.Context, queue []*scheduledTestExecutionTarget, policy string) bool {
	automatic, _ := ctx.Value(scheduledTestAutomaticRunKey{}).(bool)
	if !automatic || len(queue) == 0 || queue[0].accountID == nil {
		return false
	}
	repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestBackoffRepository)
	if !ok {
		return false
	}
	queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	defer cancel()
	state, err := repo.GetTestBackoff(queryCtx, queue[0].plan.ID, *queue[0].accountID, policy)
	if err != nil { // Avoid sending paid probes when their cooldown cannot be checked.
		for _, target := range queue {
			s.failExecutionTarget(target, fmt.Errorf("检测跳过：无法读取复测等待状态"))
		}
		return true
	}
	if state == nil || !state.NextCheckAt.After(time.Now()) {
		return false
	}
	for _, target := range queue {
		info := newScheduledTestExecutionInfo()
		info.summarize()
		info.NextCheckAt = &state.NextCheckAt
		info.ConsecutiveFailures = state.ConsecutiveFailures
		target.pending.ExecutionInfo = info
		s.failExecutionTarget(target, fmt.Errorf("检测跳过：异常账号复测等待至 %s", state.NextCheckAt.Format(time.RFC3339)))
	}
	return true
}
func (s *ScheduledTestRunnerService) recordAccountQueueBackoff(ctx context.Context, queue []*scheduledTestExecutionTarget, policy string, observations []*ScheduledTestResult, stopped, complete bool) {
	if ctx.Err() != nil || len(queue) == 0 || len(observations) == 0 || queue[0].accountID == nil {
		return
	}
	repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestBackoffRepository)
	if !ok {
		return
	}
	failed := false
	conclusive := complete
	var retryAfter *time.Time
	var last *ScheduledTestResult
	verdicts := make(map[int64]string)
	for i, result := range observations {
		if result == nil || QualityResultDeferred(result) {
			return
		}
		last = result
		if result.ExecutionInfo != nil && result.ExecutionInfo.RetryAfter != nil {
			if retryAfter == nil || retryAfter.Before(*result.ExecutionInfo.RetryAfter) {
				retryAfter = result.ExecutionInfo.RetryAfter
			}
		}
		if result.Status == "failed" {
			if result.OutputKind == "statistics" {
				return
			} // A failed SQL query is not an account failure.
			failed = true
		}
		if result.OutputKind == "statistics" && result.OutputStatistics != nil && result.OutputStatistics.TotalRequests == 0 && result.OutputStatistics.RecoveryProbe == nil {
			conclusive = false
		}
		if i < len(queue) {
			plan := queue[i].plan
			if plan.Protection.Enabled {
				if rule := plan.ProtectionRule(plan.TestDefinitionID); rule != nil &&
					(!plan.Protection.UsesAllPass() || rule.Vote == nil || !rule.Vote.Enabled || rule.HasDurationThreshold()) {
					verdict, _ := evaluateScheduledTestProtection(*rule, result)
					if plan.Protection.UsesAllPass() && rule.Vote != nil && rule.Vote.Enabled {
						verdict, _ = EvaluateScheduledTestDuration(*rule, result)
					}
					if probeVerdict, _, ok := EvaluateScheduledTestRecoveryProbe(*rule, result); ok {
						verdict = probeVerdict
					}
					if plan.TestDefinitionID != nil {
						verdicts[*plan.TestDefinitionID] = verdict
					}
					if verdict == "pending" {
						conclusive = false
					}
					if verdict == "fail" {
						failed = true
					}
				}
			}
		}
	}
	if last == nil {
		return
	}
	failed = failed || stopped
	// A positive OR branch can establish health despite another failed branch.
	// Matching a failure condition or a selected pause never claims recovery.
	plan := queue[0].plan
	if complete && plan.Protection.Enabled && plan.Protection.UsesCombinations() {
		decision := EvaluateScheduledTestCombinations(plan.Protection, verdicts)
		if decision.Action != nil && decision.Action.Scheduling == "pause" {
			failed = true
		} else {
			passing := make(map[int64]string)
			for id, verdict := range verdicts {
				if verdict == "pass" {
					passing[id] = verdict
				}
			}
			for _, combination := range plan.Protection.Combinations {
				if slices.Contains(decision.RuleIDs, combination.ID) && EvaluateScheduledTestCombinationCondition(combination.Condition, passing) == "pass" {
					failed, conclusive = false, true
					break
				}
			}
		}
	}
	if !failed && !conclusive {
		return
	}
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	if err := repo.RecordTestBackoff(persistCtx, queue[0].plan, last, policy, failed, retryAfter); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d backoff persistence error: %v", queue[0].plan.ID, *queue[0].accountID, err)
		return
	}
	if s.accountTestSvc != nil && s.accountTestSvc.qualityOperations != nil {
		if err := s.accountTestSvc.qualityOperations.RecordQualityFrequency(persistCtx, queue[0].plan, last, policy, !failed && conclusive); err != nil {
			logger.LegacyPrintf("service.quality_operations", "frequency persistence failed: %v", err)
		}
	}
}

func (s *ScheduledTestRunnerService) deferHealthyAccountQueue(ctx context.Context, queue []*scheduledTestExecutionTarget, policy string) bool {
	automatic, _ := ctx.Value(scheduledTestAutomaticRunKey{}).(bool)
	if !automatic || len(queue) == 0 || queue[0].accountID == nil {
		return false
	}
	if s.accountTestSvc == nil || s.accountTestSvc.qualityOperations == nil {
		return false
	}
	query, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	defer cancel()
	next, err := s.accountTestSvc.qualityOperations.QualityFrequencyDue(query, queue[0].plan.ID, *queue[0].accountID, policy)
	if next == nil && err == nil {
		return false
	}
	for _, target := range queue {
		reason := "检测跳过：无法检查动态检测频率"
		if next != nil {
			reason = fmt.Sprintf("检测跳过：连续健康，复测等待至 %s", next.Format(time.RFC3339))
			info := newScheduledTestExecutionInfo()
			info.NextCheckAt = next
			target.pending.ExecutionInfo = info
		}
		s.failExecutionTarget(target, fmt.Errorf("%s", reason))
	}
	return true
}
