package service

import (
	"context"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// A connectivity probe is separate evidence, never a fabricated usage sample.
type ScheduledTestRecoveryProbe struct {
	Status     string    `json:"status"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	LatencyMs  int64     `json:"latency_ms"`
}

type ScheduledTestRecoveryProbeRepository interface {
	StatisticsRecoveryProbeNeeded(context.Context, *ScheduledTestResult) (bool, error)
}

func statisticsRecoverySamplesMissing(rule ScheduledTestProtectionRule, result *ScheduledTestResult) bool {
	if rule.Recovery == nil || !rule.Recovery.Enabled || result == nil || result.Status != "success" ||
		result.OutputKind != "statistics" || result.OutputStatistics == nil {
		return false
	}
	verdict, _ := evaluateScheduledTestProtection(rule, result)
	if verdict != "pending" {
		return false
	}
	// A metric with enough recovery samples may already establish a failure,
	// even when the ordinary hourly minimum is higher. A probe cannot waive it.
	verdict, _ = EvaluateScheduledTestCacheRecovery(rule, result.OutputStatistics)
	return verdict != "fail"
}

// EvaluateScheduledTestRecoveryProbe only substitutes for missing statistics;
// measured failures and unrelated protection rules remain authoritative.
func EvaluateScheduledTestRecoveryProbe(rule ScheduledTestProtectionRule, result *ScheduledTestResult) (string, string, bool) {
	if !statisticsRecoverySamplesMissing(rule, result) {
		return "", "", false
	}
	probe := result.OutputStatistics.RecoveryProbe
	if probe == nil || probe.StartedAt.IsZero() || probe.StartedAt.Before(result.StartedAt) ||
		probe.FinishedAt.Before(probe.StartedAt) || probe.FinishedAt.After(result.FinishedAt) {
		return "", "", false
	}
	switch probe.Status {
	case "success":
		return "pass", "统计样本不足，主动探测成功，解除本规则暂停；后续继续按真实调用统计判断", true
	case "failed":
		return "fail", "统计样本不足，主动探测失败，保留本规则暂停", true
	default:
		return "", "", false
	}
}

// Called once per statistics execution, outside SQL capacity and retry loops.
// The caller already owns the account execution slot shared by test types.
func (s *ScheduledTestRunnerService) probeStatisticsRecovery(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) {
	rule := plan.ProtectionRule(plan.TestDefinitionID)
	if rule == nil || result.AccountID == nil || !statisticsRecoverySamplesMissing(*rule, result) || ctx.Err() != nil {
		return
	}
	repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestRecoveryProbeRepository)
	if !ok || s.accountTestSvc == nil || !s.acquireWorker(ctx) {
		return
	}
	defer s.releaseWorker()
	timeout := time.Minute
	if s.executionTimeout > 0 && s.executionTimeout < timeout {
		timeout = s.executionTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	checkCtx, checkCancel := context.WithTimeout(probeCtx, scheduledTestPersistenceTimeout)
	needed, err := repo.StatisticsRecoveryProbeNeeded(checkCtx, result)
	checkCancel()
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] result=%d recovery probe eligibility error: %v", result.ID, err)
		return
	}
	if !needed || probeCtx.Err() != nil {
		return
	}
	probe := &ScheduledTestRecoveryProbe{Status: "failed", StartedAt: time.Now().UTC()}
	// Use the existing small account connectivity test with the plan's model.
	// It uses the account's provider, model mapping, credentials and proxy route.
	response, err := s.accountTestSvc.RunTestBackground(probeCtx, *result.AccountID, plan.ModelID)
	probe.FinishedAt = time.Now().UTC()
	probe.LatencyMs = probe.FinishedAt.Sub(probe.StartedAt).Milliseconds()
	if ctx.Err() != nil {
		return // Shutdown is not evidence about account quality.
	}
	if err == nil && probeCtx.Err() == nil && response != nil && response.Status == "success" && strings.TrimSpace(response.ResponseText) != "" {
		probe.Status = "success"
	}
	result.OutputStatistics.RecoveryProbe = probe
}
