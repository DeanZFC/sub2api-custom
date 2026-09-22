package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

type scheduledTestExecutionTarget struct {
	plan         *ScheduledTestPlan
	prompt, kind string
	accountID    *int64
	pending      *ScheduledTestResult
	err          error
}

func (s *ScheduledTestRunnerService) executePlanSnapshot(ctx context.Context, plan *ScheduledTestPlan, repo ScheduledTestRunRepository) {
	started := time.Now().UTC()
	queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	accountIDs, targetErr := s.resolveTargetAccounts(queryCtx, plan)
	cancel()
	// Freeze and deduplicate before building any per-type rows. Repositories
	// already use DISTINCT; keeping this boundary explicit protects adapters.
	seenAccounts := make(map[int64]bool, len(accountIDs))
	frozen := make([]int64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if id > 0 && !seenAccounts[id] {
			frozen = append(frozen, id)
			seenAccounts[id] = true
		}
	}
	accountIDs = frozen
	var local, upstream []*scheduledTestExecutionTarget
	for _, execution := range scheduledTestExecutionPlans(plan) {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		prompt, kind, definitionErr := s.resolveDefinition(queryCtx, execution)
		cancel()
		if kind == "" {
			kind = "text"
		}
		if kind == "statistics" {
			executionCopy := *execution
			executionCopy.ReasoningEffort = ""
			execution = &executionCopy
		}
		var targets []*int64
		if len(accountIDs) > 0 && targetErr == nil {
			for _, id := range accountIDs {
				targets = append(targets, &id)
			}
		} else {
			targets = []*int64{plan.AccountID}
			if definitionErr == nil {
				definitionErr = targetErr
				if definitionErr == nil {
					definitionErr = fmt.Errorf("no account belongs to the test target")
				}
			}
		}
		for _, id := range targets {
			target := &scheduledTestExecutionTarget{plan: execution, prompt: prompt, kind: kind, accountID: id, err: definitionErr}
			target.pending = &ScheduledTestResult{
				PlanID: plan.ID, TestDefinitionID: execution.TestDefinitionID, TargetMode: plan.TargetMode,
				AccountID: id, GroupID: plan.GroupID, ModelID: plan.ModelID, ReasoningEffort: execution.ReasoningEffort,
				OutputKind: kind, Status: "pending", StartedAt: started, FinishedAt: started,
			}
			if kind == "statistics" {
				local = append(local, target)
			} else {
				upstream = append(upstream, target)
			}
		}
	}
	targets := append(local, upstream...)
	inputs := make([]*ScheduledTestResult, 0, len(targets))
	for _, target := range targets {
		inputs = append(inputs, target.pending)
	}
	persistCtx, cancel := scheduledTestPersistenceContext()
	results, err := repo.BeginRun(persistCtx, plan, uuid.NewString(), inputs)
	cancel()
	if err != nil || len(results) != len(targets) {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d execution snapshot error: %v", plan.ID, err)
		return
	}
	for i, target := range targets {
		target.pending = results[i]
	}
	if plan.Protection.Enabled {
		if protection, ok := s.scheduledSvc.resultRepo.(ScheduledTestActionRoundRepository); ok {
			persistCtx, cancel := scheduledTestPersistenceContext()
			err = protection.BeginProtectionRun(persistCtx, plan, started)
			cancel()
			if err != nil {
				for _, target := range targets {
					s.failExecutionTarget(target, fmt.Errorf("unable to initialize test protection: %w", err))
				}
				return
			}
		}
	}
	// Keep local statistics ahead of model requests. Each account then advances
	// through its own definitions; one slow account must not hold every other
	// account behind a definition-wide barrier. Each account executes upstream
	// definitions in configured order, regardless of mid-round group changes.
	s.runExecutionTargetQueues(ctx, local, started)
	s.runExecutionTargetQueues(ctx, upstream, started)
}

func (s *ScheduledTestRunnerService) runExecutionTargetQueues(ctx context.Context, targets []*scheduledTestExecutionTarget, started time.Time) {
	var queues [][]*scheduledTestExecutionTarget
	accountQueues := make(map[int64]int)
	for _, target := range targets {
		var accountID int64
		if target.accountID != nil {
			accountID = *target.accountID
		}
		index, ok := accountQueues[accountID]
		if !ok {
			index = len(queues)
			accountQueues[accountID] = index
			queues = append(queues, nil)
		}
		queues[index] = append(queues[index], target)
	}
	// Bound active account queues as well as upstream requests. All pending
	// rows were saved before dispatch, including those cancelled while queued.
	sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var wg sync.WaitGroup
	for _, queue := range queues {
		if ctx.Err() != nil {
			for _, target := range queue {
				s.failExecutionTarget(target, ctx.Err())
			}
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			for _, target := range queue {
				s.failExecutionTarget(target, ctx.Err())
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			for _, target := range queue {
				s.runExecutionTarget(ctx, target, started)
			}
		}()
	}
	wg.Wait()
}

func (s *ScheduledTestRunnerService) failExecutionTarget(target *scheduledTestExecutionTarget, err error) {
	result := target.pending
	result.Status, result.ErrorMessage, result.FinishedAt = "failed", err.Error(), time.Now()
	result.LatencyMs = result.FinishedAt.Sub(result.StartedAt).Milliseconds()
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	if err := s.scheduledSvc.CompleteResult(persistCtx, target.plan.MaxResults, result); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d result=%d failed result persistence error: %v", target.plan.ID, result.ID, err)
		return
	}
	s.recordResultDecision(persistCtx, result, ScheduledTestProtectionDecision{Status: "skipped", Reason: result.ErrorMessage})
}

func (s *ScheduledTestRunnerService) runExecutionTarget(ctx context.Context, target *scheduledTestExecutionTarget, windowEnd time.Time) {
	if target.err != nil {
		s.failExecutionTarget(target, target.err)
		return
	}
	if ctx.Err() != nil {
		s.failExecutionTarget(target, ctx.Err())
		return
	}
	var id int64
	if target.accountID != nil {
		id = *target.accountID
	}
	if !s.beginAccountRun(target.plan.ID, id, target.plan.TestDefinitionID) {
		s.failExecutionTarget(target, fmt.Errorf("检测跳过：该账号的同类型检测正在执行"))
		return
	}
	defer s.endAccountRun(target.plan.ID, id, target.plan.TestDefinitionID)
	persistCtx, cancel := scheduledTestPersistenceContext()
	if target.accountID != nil {
		eligible, err := s.runAccountEligible(persistCtx, target.plan, target.pending, id)
		if err != nil || !eligible {
			cancel()
			if err == nil {
				err = fmt.Errorf("账号未开启调度或当前状态不可检测")
			}
			s.failExecutionTarget(target, fmt.Errorf("检测跳过：%w", err))
			return
		}
	}
	target.pending.Status = "running"
	err := s.scheduledSvc.resultRepo.Update(persistCtx, target.pending)
	if err == nil {
		err = s.beginResultProtection(persistCtx, target.plan, target.pending)
	}
	cancel()
	if err != nil {
		s.failExecutionTarget(target, fmt.Errorf("unable to initialize test result: %w", err))
		return
	}
	if target.kind == "statistics" {
		s.runStatisticsResult(ctx, target.plan, target.accountID, target.pending, windowEnd)
	} else if target.accountID != nil {
		s.runAccountWithResult(ctx, target.plan, id, target.prompt, target.kind, target.pending)
	} else {
		s.failExecutionTarget(target, fmt.Errorf("test account is unavailable"))
	}
}
