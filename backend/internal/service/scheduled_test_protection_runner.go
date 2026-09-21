package service

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *ScheduledTestRunnerService) detectionAccountEligible(ctx context.Context, plan *ScheduledTestPlan, accountID int64) (bool, error) {
	if s.scheduledSvc != nil {
		if repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestActionRepository); ok {
			ids, err := repo.ListPlanDetectionAccountIDs(ctx, plan, &accountID)
			return len(ids) > 0, err
		}
	}
	if repo := s.scheduledSvc.protectionRepository(); repo != nil {
		ids, err := repo.ListDetectionAccountIDs(ctx, plan.GroupID, &accountID)
		return len(ids) > 0, err
	}
	return true, nil // Legacy adapters do not expose the database capability.
}

func (s *ScheduledTestRunnerService) beginResultProtection(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) error {
	rule := plan.ProtectionRule(plan.TestDefinitionID)
	if rule == nil {
		return nil
	}
	if result == nil || result.ID <= 0 {
		return fmt.Errorf("protection requires a persisted test result")
	}
	repo := s.scheduledSvc.protectionRepository()
	if repo == nil {
		return fmt.Errorf("test protection repository unavailable")
	}
	return repo.BeginProtection(ctx, result, *rule)
}

func (s *ScheduledTestRunnerService) completeResultProtection(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) {
	rule := plan.ProtectionRule(plan.TestDefinitionID)
	if rule == nil || result == nil || result.ID <= 0 {
		return
	}
	repo := s.scheduledSvc.protectionRepository()
	if repo == nil {
		return
	}
	verdict, reason := evaluateScheduledTestProtection(*rule, result)
	if err := repo.CompleteProtection(ctx, result, verdict, reason); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d result=%d protection completion error: %v", plan.ID, result.ID, err)
	}
}
