package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

func (s *ScheduledTestRunnerService) statisticsRepository() (ScheduledTestStatisticsRepository, error) {
	if s.scheduledSvc != nil {
		if repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestStatisticsRepository); ok {
			return repo, nil
		}
	}
	return nil, fmt.Errorf("local statistics repository unavailable")
}

func (s *ScheduledTestRunnerService) runStatisticsDefinition(ctx context.Context, plan *ScheduledTestPlan) {
	execution := *plan
	execution.ReasoningEffort = ""
	plan = &execution
	repo, err := s.statisticsRepository()
	if err != nil {
		s.savePlanFailure(ctx, plan, "statistics", err)
		return
	}
	windowEnd := time.Now().UTC()
	if plan.TargetMode == "group" {
		if plan.GroupID == nil || *plan.GroupID <= 0 {
			s.savePlanFailure(ctx, plan, "statistics", fmt.Errorf("statistics group is required"))
			return
		}
		s.startStatisticsResult(ctx, plan, nil, windowEnd)
		return
	}
	var ids []int64
	if targetRepo, ok := s.scheduledSvc.resultRepo.(ScheduledTestTargetAccountRepository); ok {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		ids, err = targetRepo.ListPlanTargetAccountIDs(queryCtx, plan, plan.AccountID)
		cancel()
	} else if actionRepo, ok := s.scheduledSvc.resultRepo.(ScheduledTestActionRepository); ok {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		ids, err = actionRepo.ListPlanDetectionAccountIDs(queryCtx, plan, plan.AccountID)
		cancel()
	} else if protectionRepo := s.scheduledSvc.protectionRepository(); protectionRepo != nil {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		ids, err = protectionRepo.ListDetectionAccountIDs(queryCtx, plan.GroupID, plan.AccountID)
		cancel()
	} else if plan.AccountID != nil && *plan.AccountID > 0 {
		ids = []int64{*plan.AccountID}
	} else if plan.GroupID != nil && *plan.GroupID > 0 {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		ids, err = repo.ListStatisticsAccountIDs(queryCtx, *plan.GroupID)
		cancel()
	} else {
		err = fmt.Errorf("statistics account or group is required")
	}
	if err != nil || len(ids) == 0 {
		if err == nil {
			err = fmt.Errorf("no account belongs to the statistics group")
		}
		s.savePlanFailure(ctx, plan, "statistics", err)
		return
	}
	sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var wg sync.WaitGroup
dispatch:
	for _, id := range ids {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.startStatisticsResult(ctx, plan, &id, windowEnd)
		}()
	}
	wg.Wait()
}

func (s *ScheduledTestRunnerService) startStatisticsResult(ctx context.Context, plan *ScheduledTestPlan, accountID *int64, windowEnd time.Time) {
	if ctx.Err() != nil {
		return
	}
	var id int64
	if accountID != nil {
		id = *accountID
	}
	if !s.beginAccountRun(plan.ID, id, plan.TestDefinitionID) {
		return
	}
	defer s.endAccountRun(plan.ID, id, plan.TestDefinitionID)
	pending := &ScheduledTestResult{
		TestDefinitionID: plan.TestDefinitionID, TargetMode: plan.TargetMode, Status: "running",
		OutputKind: "statistics", AccountID: accountID, ModelID: plan.ModelID, GroupID: plan.GroupID,
		StartedAt: windowEnd, FinishedAt: windowEnd,
	}
	persistCtx, cancel := scheduledTestPersistenceContext()
	if accountID != nil {
		eligible, err := s.detectionAccountEligible(persistCtx, plan, *accountID)
		if err != nil || !eligible {
			cancel()
			s.saveSkippedAccountResult(ctx, plan, *accountID, "statistics", err)
			return
		}
	}
	pending, err := s.scheduledSvc.StartResult(persistCtx, plan.ID, pending)
	if err == nil {
		err = s.beginResultProtection(persistCtx, plan, pending)
	}
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d statistics StartResult error: %v", plan.ID, err)
		if plan.ProtectionRule(plan.TestDefinitionID) != nil {
			if pending != nil {
				pending.Status, pending.ErrorMessage = "failed", "unable to initialize test protection"
				cleanupCtx, cleanupCancel := scheduledTestPersistenceContext()
				_ = s.scheduledSvc.CompleteResult(cleanupCtx, plan.MaxResults, pending)
				cleanupCancel()
			}
			return
		}
	}
	s.runStatisticsResult(ctx, plan, accountID, pending, windowEnd)
}

func (s *ScheduledTestRunnerService) runStatisticsResult(ctx context.Context, plan *ScheduledTestPlan, accountID *int64, pending *ScheduledTestResult, windowEnd time.Time) {
	started := time.Now()
	s.workerMu.Lock()
	if s.statisticsSem == nil {
		s.statisticsSem = make(chan struct{}, scheduledTestDefaultMaxWorkers)
	}
	sem := s.statisticsSem
	s.workerMu.Unlock()
	result := s.runWithAutomaticRetries(ctx, plan, accountID, func(attemptCtx context.Context) *ScheduledTestResult {
		attempt := &ScheduledTestResult{Status: "success"}
		repo, err := s.statisticsRepository()
		if err == nil {
			select {
			case sem <- struct{}{}:
				func() {
					// Release SQL capacity before retry backoff so other snapshots
					// can continue while this request waits to try again.
					defer func() { <-sem }()
					if err = attemptCtx.Err(); err != nil {
						return
					}
					queryCtx, cancel := context.WithTimeout(attemptCtx, scheduledTestPersistenceTimeout)
					defer cancel()
					statisticsGroupID := plan.GroupID
					if accountID != nil && plan.HasGroupActions() {
						// Track this account across its quality tiers, including after a move.
						statisticsGroupID = nil
					}
					attempt.OutputStatistics, err = repo.CollectStatistics(queryCtx, ScheduledTestStatisticsFilter{
						GroupID: statisticsGroupID, AccountID: accountID, Model: plan.ModelID,
						WindowStart: windowEnd.Add(-time.Hour), WindowEnd: windowEnd,
					})
				}()
			case <-attemptCtx.Done():
				err = attemptCtx.Err()
			}
		}
		if err == nil {
			err = attemptCtx.Err()
		}
		if err == nil && attempt.OutputStatistics == nil {
			err = fmt.Errorf("local statistics returned no snapshot")
		}
		if err == nil {
			var encoded []byte
			encoded, err = json.Marshal(attempt.OutputStatistics)
			attempt.ResponseText = string(encoded)
		}
		if err != nil {
			attempt.Status, attempt.ErrorMessage = "failed", err.Error()
			attempt.OutputStatistics, attempt.ResponseText = nil, ""
		}
		return attempt
	})
	result.PlanID, result.TestDefinitionID = plan.ID, plan.TestDefinitionID
	result.TargetMode, result.OutputKind = plan.TargetMode, "statistics"
	result.AccountID, result.ModelID, result.GroupID = accountID, plan.ModelID, plan.GroupID
	result.StartedAt = started
	if pending != nil {
		result.ID, result.StartedAt, result.CreatedAt = pending.ID, pending.StartedAt, pending.CreatedAt
	}
	result.FinishedAt, result.LatencyMs = time.Now(), time.Since(started).Milliseconds()
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	var err error
	if result.ID > 0 {
		err = s.scheduledSvc.CompleteResult(persistCtx, plan.MaxResults, result)
	} else {
		err = s.scheduledSvc.SaveResult(persistCtx, plan.ID, plan.MaxResults, result)
	}
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d statistics completion error: %v", plan.ID, err)
		return
	}
	// Only explicitly configured metrics may affect scheduling; generating a
	// database snapshot alone must never invoke legacy upstream-test recovery.
	if ctx.Err() == nil {
		s.completeResultProtection(persistCtx, plan, result)
	}
}
