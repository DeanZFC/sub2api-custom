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
	plan            *ScheduledTestPlan
	prompt, kind    string
	maxOutputTokens int
	accountID       *int64
	pending         *ScheduledTestResult
	err             error
}

type scheduledTestAccountProgress struct {
	verdicts map[int64]string
	stopped  bool
}

func (p *scheduledTestAccountProgress) allowsNext(plan *ScheduledTestPlan, result *ScheduledTestResult) bool {
	if p.stopped {
		return false
	}
	if plan == nil || !plan.Protection.Enabled || !plan.Protection.UsesCombinations() {
		p.stopped = !scheduledTestResultSatisfies(plan, result)
		return !p.stopped
	}
	if result == nil {
		p.stopped = true
		return false
	}
	if plan.TestDefinitionID != nil {
		if rule := plan.ProtectionRule(plan.TestDefinitionID); rule != nil {
			verdict, _ := evaluateScheduledTestProtection(*rule, result)
			if probeVerdict, _, ok := EvaluateScheduledTestRecoveryProbe(*rule, result); ok {
				verdict = probeVerdict
			}
			if p.verdicts == nil {
				p.verdicts = make(map[int64]string)
			}
			p.verdicts[*plan.TestDefinitionID] = verdict
		}
	}
	// In a combined rule, one failed test may still leave an OR branch that
	// can pass. Stop only when no configured action can still match.
	if len(plan.Protection.Combinations) == 0 {
		p.stopped = result == nil
		return !p.stopped
	}
	for _, combination := range plan.Protection.Combinations {
		if EvaluateScheduledTestCombinationCondition(combination.Condition, p.verdicts) != "fail" {
			return true
		}
	}
	p.stopped = true
	return false
}

// scheduledTestResultSatisfies is the gate between two checks for one
// account. A successful HTTP/upstream response is not enough when the plan
// has a configured answer, threshold, or model-consistency rule: the quality
// verdict must also be pass. A completed statistics snapshot with insufficient
// samples may continue the queue, but stays pending for quality actions.
// Unknown model identity and failed automatic observations still stop the
// account queue; display-only and manual-review checks remain informational
// in the all-pass strategy.
func scheduledTestResultSatisfies(plan *ScheduledTestPlan, result *ScheduledTestResult) bool {
	if result == nil {
		return false
	}
	if plan != nil && plan.Protection.Enabled {
		if plan.Protection.UsesCombinations() {
			// Combined rules need the account's accumulated verdicts; the
			// snapshot queue uses scheduledTestAccountProgress instead.
			return true
		}
		if rule := plan.ProtectionRule(plan.TestDefinitionID); rule == nil ||
			(plan.Protection.UsesAllPass() && rule.Vote != nil && rule.Vote.Enabled) {
			if rule != nil && rule.HasDurationThreshold() {
				verdict, _ := EvaluateScheduledTestDuration(*rule, result)
				return verdict == "pass"
			}
			// A successfully executed display-only or administrator-review
			// check is not an automatic quality gate in the all-pass strategy.
			return true
		}
	}
	if result.Status != "success" && result.Status != "passed" {
		return false
	}
	if result.OutputKind == "model_check" && (result.OutputModelCheck == nil || result.OutputModelCheck.Verdict != "pass") {
		return false
	}
	if result.OutputKind == "capability" && (result.ExecutionInfo == nil || result.ExecutionInfo.Capability == nil || result.ExecutionInfo.Capability.Verdict != "pass") {
		return false
	}
	if plan == nil {
		return true
	}
	rule := plan.ProtectionRule(plan.TestDefinitionID)
	if rule == nil {
		return true
	}
	verdict, _ := evaluateScheduledTestProtection(*rule, result)
	if probeVerdict, _, ok := EvaluateScheduledTestRecoveryProbe(*rule, result); ok {
		verdict = probeVerdict
	}
	return verdict == "pass" ||
		(verdict == "pending" && result.OutputKind == "statistics" && result.OutputStatistics != nil)
}

func (s *ScheduledTestRunnerService) executePlanSnapshot(ctx context.Context, plan *ScheduledTestPlan, repo ScheduledTestRunRepository) {
	started := time.Now().UTC()
	queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	accountIDs, targetErr := s.resolveTargetAccounts(queryCtx, plan)
	cancel()
	// Freeze and deduplicate before building any per-type rows. Repositories
	// already use DISTINCT; keeping this boundary explicit protects adapters.
	accountIDs = scheduledTestUniqueAccountIDs(accountIDs)
	var local, upstream []*scheduledTestExecutionTarget
	for _, execution := range scheduledTestExecutionPlans(plan) {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		prompt, kind, maxOutputTokens, definitionErr := s.resolveDefinition(queryCtx, execution)
		cancel()
		if kind == "" {
			kind = "text"
		}
		if kind == "statistics" {
			executionCopy := *execution
			executionCopy.ReasoningEffort = ""
			execution = &executionCopy
		}
		var accountTargets []*int64
		if len(accountIDs) > 0 && targetErr == nil {
			for _, id := range accountIDs {
				accountTargets = append(accountTargets, &id)
			}
		} else {
			accountTargets = []*int64{plan.AccountID}
			if definitionErr == nil {
				definitionErr = targetErr
				if definitionErr == nil {
					definitionErr = fmt.Errorf("no account belongs to the test target")
				}
			}
		}
		for _, id := range accountTargets {
			target := &scheduledTestExecutionTarget{plan: execution, prompt: prompt, kind: kind, maxOutputTokens: maxOutputTokens, accountID: id, err: definitionErr}
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
	// Put low-cost statistics first within each account queue. A fast account
	// can advance to upstream work while another account's statistics are slow.
	progress := make(map[int64]*scheduledTestAccountProgress, len(accountIDs))
	s.runExecutionTargetQueues(ctx, targets, started, progress)
}

func (s *ScheduledTestRunnerService) runExecutionTargetQueues(ctx context.Context, targets []*scheduledTestExecutionTarget, started time.Time, progress map[int64]*scheduledTestAccountProgress) {
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
	// Keep the account order from resolveTargetAccounts while allowing different
	// accounts to use independent workers. All pending rows were saved before
	// dispatch, including rows that are completed as skipped after the first
	// unsatisfied check for their account.
	sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var wg sync.WaitGroup
	var progressMu sync.Mutex
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
		go func(queue []*scheduledTestExecutionTarget) {
			ctx := context.WithValue(ctx, qualityRunKey{}, uuid.NewString())
			defer wg.Done()
			defer func() { <-sem }()
			var accountID int64
			if len(queue) > 0 && queue[0].accountID != nil {
				accountID = *queue[0].accountID
			}
			progressMu.Lock()
			accountProgress := progress[accountID]
			if accountProgress == nil {
				accountProgress = &scheduledTestAccountProgress{}
				progress[accountID] = accountProgress
			}
			stopped := accountProgress.stopped
			progressMu.Unlock()
			if stopped {
				for _, skipped := range queue {
					s.skipExecutionTarget(skipped)
				}
				return
			}
			policy := scheduledTestQueuePolicy(queue)
			if s.deferAccountQueue(ctx, queue, policy) {
				return
			}
			var observations []*ScheduledTestResult
			complete := false
			defer func() {
				s.recordAccountQueueBackoff(ctx, queue, policy, observations, accountProgress.stopped, complete)
			}()
			dynamicChecked := false
			for targetIndex, target := range queue {
				if target.kind != "statistics" && !dynamicChecked {
					dynamicChecked = true
					localHealthy := true
					for i, observation := range observations {
						if !scheduledTestResultSatisfies(queue[i].plan, observation) {
							localHealthy = false
						}
					}
					if localHealthy && s.deferHealthyAccountQueue(ctx, queue[targetIndex:], policy) {
						return
					}
				}
				result, ignoreGate := s.runExecutionTarget(ctx, target, started)
				observations = append(observations, result)
				if QualityResultDeferred(result) {
					for _, remaining := range queue[targetIndex+1:] {
						s.failExecutionTarget(remaining, fmt.Errorf("检测跳过：本轮检测暂缓"))
					}
					return
				}
				if ctx.Err() != nil {
					for _, remaining := range queue[targetIndex+1:] {
						s.failExecutionTarget(remaining, ctx.Err())
					}
					return
				}
				progressMu.Lock()
				continueQueue := true
				if !ignoreGate {
					continueQueue = accountProgress.allowsNext(target.plan, result)
				}
				progressMu.Unlock()
				if !continueQueue {
					for _, skipped := range queue[targetIndex+1:] {
						s.skipExecutionTarget(skipped)
					}
					return
				}
			}
			complete = true
		}(queue)
	}
	wg.Wait()
}

func (s *ScheduledTestRunnerService) skipExecutionTarget(target *scheduledTestExecutionTarget) {
	if target == nil || target.pending == nil {
		return
	}
	s.failExecutionTarget(target, fmt.Errorf("检测跳过：前一项检测未满足条件"))
}

func (s *ScheduledTestRunnerService) failExecutionTarget(target *scheduledTestExecutionTarget, err error) {
	result := target.pending
	if result.ExecutionInfo == nil {
		result.ExecutionInfo = newScheduledTestExecutionInfo()
		result.ExecutionInfo.summarize()
	}
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

func (s *ScheduledTestRunnerService) runExecutionTarget(ctx context.Context, target *scheduledTestExecutionTarget, windowEnd time.Time) (*ScheduledTestResult, bool) {
	if target.err != nil {
		s.failExecutionTarget(target, target.err)
		// A missing/disabled definition is not an account-quality observation;
		// retain the existing behavior of continuing with later definitions.
		return nil, true
	}
	if ctx.Err() != nil {
		s.failExecutionTarget(target, ctx.Err())
		return nil, false
	}
	var id int64
	if target.accountID != nil {
		id = *target.accountID
	}
	if !s.beginAccountRun(target.plan.ID, id, target.plan.TestDefinitionID) {
		s.failExecutionTarget(target, fmt.Errorf("检测跳过：该账号的同类型检测正在执行"))
		return nil, false
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
			return nil, false
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
		return nil, false
	}
	var result *ScheduledTestResult
	if target.kind == "statistics" {
		result = s.runStatisticsResult(ctx, target.plan, target.accountID, target.pending, windowEnd)
	} else if target.accountID != nil {
		result = s.runAccountWithResult(withAccountTestMaxOutputTokens(ctx, target.maxOutputTokens), target.plan, id, target.prompt, target.kind, target.pending)
	} else {
		s.failExecutionTarget(target, fmt.Errorf("test account is unavailable"))
		return nil, false
	}
	return result, false
}
