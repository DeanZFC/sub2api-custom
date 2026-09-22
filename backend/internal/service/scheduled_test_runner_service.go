package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/robfig/cron/v3"
)

const scheduledTestDefaultMaxWorkers = 10

var (
	// Keep the number grammar deliberately strict. In particular, accepting a
	// number only after an answer marker (or as a standalone line) prevents
	// table values and step numbers in the model's reasoning from becoming the
	// recorded answer.
	scheduledTestNumberPattern      = `[-+]?(?:(?:\d+(?:\.\d*)?)|(?:\.\d+))(?:[eE][-+]?\d+)?`
	scheduledTestNumberRE           = regexp.MustCompile(scheduledTestNumberPattern)
	scheduledTestStandaloneNumberRE = regexp.MustCompile(`^` + scheduledTestNumberPattern + `$`)
	// Keep the marker and number on the same line. Models frequently format
	// the answer as "最少取出 **29个**"; the markdown emphasis and Chinese unit
	// must not make the parser fall through to a step number such as "1.".
	scheduledTestMarkerGap       = "[ \\t*_`~]*"
	scheduledTestStrongNumberRE  = regexp.MustCompile(`(?i)(?:final\s+(?:answer|result)|answer|result|答案|最终\s*(?:答案|结果)|结论)` + scheduledTestMarkerGap + `(?:(?:is|are|为|是)` + scheduledTestMarkerGap + `)?(?:=|:|：)?` + scheduledTestMarkerGap + `(` + scheduledTestNumberPattern + `)`)
	scheduledTestMinimumNumberRE = regexp.MustCompile(`(?i)(?:minimum(?:\s+number)?|最少(?:取出)?)` + scheduledTestMarkerGap + `(?:(?:is|are|为|是)` + scheduledTestMarkerGap + `)?(?:=|:|：)?` + scheduledTestMarkerGap + `(` + scheduledTestNumberPattern + `)`)
	scheduledTestAtLeastNumberRE = regexp.MustCompile(`(?i)至少` + scheduledTestMarkerGap + `(?:(?:is|are|为|是)` + scheduledTestMarkerGap + `)?(?:=|:|：)?` + scheduledTestMarkerGap + `(` + scheduledTestNumberPattern + `)`)
	scheduledTestAnswerLineRE    = regexp.MustCompile(`(?i)(?:final\s+(?:answer|result)|answer|result|答案|最终|结论|minimum|最少|至少)`)
	scheduledTestHTMLRootRE      = regexp.MustCompile(`(?is)<\s*([a-z][a-z0-9:._-]*)(?:\s|/?>)`)
	scheduledTestHTMLDoctypeRE   = regexp.MustCompile(`(?is)<!doctype\s+html\s*>(?:\s|\\[nrt])*$`)
	scheduledTestHTMLAttrRE      = regexp.MustCompile(`=\s*\\"`)
	scheduledTestHTMLSpaceRE     = regexp.MustCompile(`^(?:\s|\\[nrt])*\\[nrt](?:\s|\\[nrt])*<`)
)

const scheduledTestPersistenceTimeout = 15 * time.Second

var ErrScheduledTestAccountRunning = errors.New("this account and test type are already being tested for this plan")
var ErrScheduledTestResultNotFailed = errors.New("test result is no longer failed; refresh the results before retrying")

// ScheduledTestRunnerService periodically scans due test plans and executes them.
type ScheduledTestRunnerService struct {
	planRepo       ScheduledTestPlanRepository
	scheduledSvc   *ScheduledTestService
	accountTestSvc *AccountTestService
	accountRepo    AccountRepository
	rateLimitSvc   *RateLimitService
	cfg            *config.Config

	cron                    *cron.Cron
	startOnce               sync.Once
	stopOnce                sync.Once
	runMu                   sync.Mutex
	lifecycleMu             sync.Mutex
	lifecycleCtx            context.Context
	lifecycleCancel         context.CancelFunc
	stopping                bool
	activeRuns              sync.WaitGroup
	executionTimeout        time.Duration
	automaticRetryBaseDelay time.Duration

	// runningPlans prevents a manual RunNow from racing the cron sweep (and
	// protects against duplicate scheduler ticks in deployments with more than
	// one trigger). workerSem is shared by every plan and every account, so a
	// group plan cannot multiply the configured concurrency by the number of
	// concurrently running plans.
	planRunMu       sync.Mutex
	runningPlans    map[int64]struct{}
	workerMu        sync.Mutex
	workerSem       chan struct{}
	statisticsSem   chan struct{}
	accountRunMu    sync.Mutex
	runningAccounts map[[3]int64]struct{}
}

// NewScheduledTestRunnerService creates a new runner.
func NewScheduledTestRunnerService(
	planRepo ScheduledTestPlanRepository,
	scheduledSvc *ScheduledTestService,
	accountTestSvc *AccountTestService,
	accountRepo AccountRepository,
	rateLimitSvc *RateLimitService,
	cfg *config.Config,
) *ScheduledTestRunnerService {
	return &ScheduledTestRunnerService{
		planRepo:       planRepo,
		scheduledSvc:   scheduledSvc,
		accountTestSvc: accountTestSvc,
		accountRepo:    accountRepo,
		rateLimitSvc:   rateLimitSvc,
		cfg:            cfg,
	}
}

// Start begins the cron ticker (every minute).
func (s *ScheduledTestRunnerService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		loc := time.Local
		if s.cfg != nil {
			if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
				loc = parsed
			}
		}

		c := cron.New(cron.WithParser(scheduledTestCronParser), cron.WithLocation(loc))
		_, err := c.AddFunc("* * * * *", func() { s.runScheduled() })
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] not started (invalid schedule): %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] started (tick=every minute)")
	})
}

// Stop cancels cron, manual runs and retries, then allows result persistence
// to finish before the application closes its database connections.
func (s *ScheduledTestRunnerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopping = true
		if s.lifecycleCancel != nil {
			s.lifecycleCancel()
		}
		s.lifecycleMu.Unlock()
		cronDone := context.Background()
		if s.cron != nil {
			cronDone = s.cron.Stop()
		}
		done := make(chan struct{})
		go func() {
			if s.cron != nil {
				<-cronDone.Done()
			}
			s.activeRuns.Wait()
			close(done)
		}()
		select {
		case <-done:
		// Account completion and advancing its plan each have a bounded
		// persistence context; let both finish before database cleanup.
		case <-time.After(2*scheduledTestPersistenceTimeout + time.Second):
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] shutdown wait timed out")
		}
	})
}

// beginRunContext links work to shutdown without introducing a shared execution
// deadline. Registration and shutdown use the same mutex so Wait cannot race
// a new Add after shutdown has begun.
func (s *ScheduledTestRunnerService) beginRunContext(parent context.Context) (context.Context, func(), error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping {
		return nil, nil, context.Canceled
	}
	if s.lifecycleCtx == nil {
		s.lifecycleCtx, s.lifecycleCancel = context.WithCancel(context.Background())
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(s.lifecycleCtx, cancel)
	s.activeRuns.Add(1)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			stop()
			cancel()
			s.activeRuns.Done()
		})
	}, nil
}

func (s *ScheduledTestRunnerService) runScheduled() {
	ctx, finish, err := s.beginRunContext(context.Background())
	if err != nil {
		return
	}
	defer finish()
	// Only discovery is serialized. Long-running rules own their plan lock and
	// must not stop the next tick from discovering newly due rules.
	if !s.runMu.TryLock() {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] previous sweep still running; skipping tick")
		return
	}
	defer s.runMu.Unlock()

	// Delay 10s so execution lands at ~:10 of each minute instead of :00.
	delay := time.NewTimer(10 * time.Second)
	defer delay.Stop()
	select {
	case <-delay.C:
	case <-ctx.Done():
		return
	}
	s.runDuePlans(ctx)
}

func (s *ScheduledTestRunnerService) runDuePlans(ctx context.Context) {
	now := time.Now()
	queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	plans, err := s.planRepo.ListDue(queryCtx, now)
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] ListDue error: %v", err)
		return
	}
	if len(plans) == 0 {
		return
	}

	logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] found %d due plans", len(plans))

	for _, plan := range plans {
		if ctx.Err() != nil {
			return
		}
		// Register before launching. Repeated ticks cannot accumulate blocked
		// goroutines for the same rule, and shutdown waits for admitted work.
		// The scan context ends when discovery returns; executions instead
		// inherit cancellation from the runner lifecycle.
		runCtx, finish, admitted := s.preparePlanRun(context.Background(), plan)
		if !admitted {
			continue
		}
		go func() {
			defer finish()
			// Only timer-triggered runs receive automatic retries. Manual runs
			// and retries retain their existing single-execution semantics.
			s.executePlan(context.WithValue(runCtx, scheduledTestAutomaticRunKey{}, true), plan)
		}()
	}
}

func (s *ScheduledTestRunnerService) preparePlanRun(ctx context.Context, plan *ScheduledTestPlan) (context.Context, func(), bool) {
	if plan == nil {
		return nil, nil, false
	}
	ctx, finish, err := s.beginRunContext(ctx)
	if err != nil {
		return nil, nil, false
	}
	if !s.beginPlanRun(plan.ID) {
		finish()
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d is already running; skipping overlapping run", plan.ID)
		return nil, nil, false
	}
	return ctx, func() {
		s.endPlanRun(plan.ID)
		finish()
	}, true
}

func (s *ScheduledTestRunnerService) runOnePlan(ctx context.Context, plan *ScheduledTestPlan) {
	ctx, finish, admitted := s.preparePlanRun(ctx, plan)
	if !admitted {
		return
	}
	defer finish()
	s.executePlan(ctx, plan)
}

func (s *ScheduledTestRunnerService) executePlan(ctx context.Context, plan *ScheduledTestPlan) {
	defer s.advancePlan(ctx, plan)
	if s.scheduledSvc != nil {
		if repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestRunRepository); ok {
			s.executePlanSnapshot(ctx, plan, repo)
			return
		}
	}
	if plan.Protection.Enabled && s.scheduledSvc != nil {
		if repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestActionRoundRepository); ok {
			queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
			err := repo.BeginProtectionRun(queryCtx, plan, time.Now().UTC())
			cancel()
			if err != nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d protection round initialization error: %v", plan.ID, err)
				return
			}
		}
	}
	executions := scheduledTestExecutionPlans(plan)
	// Local statistics must not wait behind a potentially fifteen-minute model
	// generation. Display ordering remains a separate definition setting.
	var local, upstream []*ScheduledTestPlan
	for _, execution := range executions {
		queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		_, kind, err := s.resolveDefinition(queryCtx, execution)
		cancel()
		if err == nil && kind == "statistics" {
			local = append(local, execution)
		} else {
			upstream = append(upstream, execution)
		}
	}

	// Each definition has an independent result stream. A disabled definition
	// must not prevent the remaining checks in the same rule from executing.
	for _, execution := range append(local, upstream...) {
		s.runPlanDefinition(ctx, execution)
		if ctx.Err() != nil {
			break
		}
	}
}

func scheduledTestExecutionPlans(plan *ScheduledTestPlan) []*ScheduledTestPlan {
	ids := plan.TestDefinitionIDs
	if len(ids) == 0 {
		return []*ScheduledTestPlan{plan}
	}
	result := make([]*ScheduledTestPlan, 0, len(ids))
	for _, id := range ids {
		execution := *plan
		execution.TestDefinitionID = &id
		execution.TestDefinitionIDs = []int64{id}
		result = append(result, &execution)
	}
	return result
}

func (s *ScheduledTestRunnerService) runPlanDefinition(ctx context.Context, plan *ScheduledTestPlan) {
	if ctx.Err() != nil {
		return
	}
	queryCtx, cancel := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	prompt, outputKind, definitionErr := s.resolveDefinition(queryCtx, plan)
	if definitionErr != nil {
		cancel()
		if ctx.Err() != nil {
			return
		}
		s.savePlanFailure(ctx, plan, "text", definitionErr)
		return
	}
	if outputKind == "statistics" {
		cancel()
		s.runStatisticsDefinition(ctx, plan)
		return
	}

	accountIDs, resolveErr := s.resolveTargetAccounts(queryCtx, plan)
	cancel()
	if ctx.Err() != nil {
		return
	}
	if resolveErr != nil || len(accountIDs) == 0 {
		if resolveErr == nil {
			resolveErr = fmt.Errorf("no schedulable account is available for this group")
		}
		s.savePlanFailure(ctx, plan, outputKind, resolveErr)
		return
	}

	// A group target means every currently schedulable account in that group.
	// Keep each account's result separate so administrators can identify the
	// failing upstream and users only see results for their entitled group.
	sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var wg sync.WaitGroup
dispatchAccounts:
	for _, accountID := range accountIDs {
		if ctx.Err() != nil {
			break
		}
		accountID := accountID
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatchAccounts
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOneAccount(ctx, plan, accountID, prompt, outputKind)
		}()
	}
	wg.Wait()
}

func (s *ScheduledTestRunnerService) resolveDefinition(ctx context.Context, plan *ScheduledTestPlan) (string, string, error) {
	// Legacy account plans predate configurable definitions and intentionally use
	// the existing account probe when no definition is attached.
	if plan.TestDefinitionID == nil {
		return "", "text", nil
	}
	if s.scheduledSvc == nil {
		return "", "", fmt.Errorf("scheduled test service unavailable")
	}
	d, err := s.scheduledSvc.GetDefinition(ctx, *plan.TestDefinitionID)
	if err != nil {
		return "", "", fmt.Errorf("test definition unavailable: %w", err)
	}
	if d == nil || !d.Enabled {
		return "", "", fmt.Errorf("test definition is disabled")
	}
	outputKind := strings.ToLower(strings.TrimSpace(d.OutputKind))
	if outputKind == "" {
		outputKind = "text"
	}
	if outputKind == "model_check" {
		return scheduledTestModelCheckPrompt, outputKind, nil
	}
	return d.Prompt, outputKind, nil
}

func (s *ScheduledTestRunnerService) resolveTargetAccounts(ctx context.Context, plan *ScheduledTestPlan) ([]int64, error) {
	if s.scheduledSvc != nil {
		if repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestTargetAccountRepository); ok {
			return repo.ListPlanTargetAccountIDs(ctx, plan, plan.AccountID)
		}
		if repo, ok := s.scheduledSvc.resultRepo.(ScheduledTestActionRepository); ok {
			return repo.ListPlanDetectionAccountIDs(ctx, plan, plan.AccountID)
		}
	}
	if repo := s.scheduledSvc.protectionRepository(); repo != nil {
		return repo.ListDetectionAccountIDs(ctx, plan.GroupID, plan.AccountID)
	}
	if plan.AccountID != nil && *plan.AccountID > 0 {
		return []int64{*plan.AccountID}, nil
	}
	if plan.GroupID == nil || *plan.GroupID <= 0 {
		return nil, fmt.Errorf("exactly one account or group target is required")
	}
	if s.accountRepo == nil {
		return nil, fmt.Errorf("account repository unavailable")
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, *plan.GroupID)
	if err != nil {
		return nil, fmt.Errorf("list group accounts: %w", err)
	}
	ids := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account.ID > 0 {
			ids = append(ids, account.ID)
		}
	}
	return ids, nil
}

func (s *ScheduledTestRunnerService) runOneAccount(ctx context.Context, plan *ScheduledTestPlan, accountID int64, prompt, outputKind string) {
	if ctx.Err() != nil {
		return
	}
	if !s.beginAccountRun(plan.ID, accountID, plan.TestDefinitionID) {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d is already running; skipping overlapping run", plan.ID, accountID)
		return
	}
	defer s.endAccountRun(plan.ID, accountID, plan.TestDefinitionID)

	persistCtx, cancel := scheduledTestPersistenceContext()
	eligible, eligibleErr := s.detectionAccountEligible(persistCtx, plan, accountID)
	if eligibleErr != nil || !eligible {
		cancel()
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d skipped: account unavailable for detection (%v)", plan.ID, accountID, eligibleErr)
		s.saveSkippedAccountResult(ctx, plan, accountID, outputKind, eligibleErr)
		return
	}
	pending, err := s.startAccountResult(persistCtx, plan, accountID, outputKind)
	if err == nil {
		err = s.beginResultProtection(persistCtx, plan, pending)
	}
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d StartResult error: %v", plan.ID, accountID, err)
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
	s.runAccountWithResult(ctx, plan, accountID, prompt, outputKind, pending)
}

func (s *ScheduledTestRunnerService) saveSkippedAccountResult(ctx context.Context, plan *ScheduledTestPlan, accountID int64, outputKind string, cause error) {
	if s.scheduledSvc == nil {
		return
	}
	now := time.Now()
	result := &ScheduledTestResult{
		PlanID: plan.ID, TestDefinitionID: plan.TestDefinitionID, TargetMode: plan.TargetMode,
		OutputKind: outputKind, AccountID: &accountID, ModelID: plan.ModelID,
		ReasoningEffort: plan.ReasoningEffort, GroupID: plan.GroupID, Status: "running",
		StartedAt: now, FinishedAt: now,
	}
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	created, err := s.scheduledSvc.StartResult(persistCtx, plan.ID, result)
	if err != nil || created == nil {
		return
	}
	created.Status = "failed"
	created.ErrorMessage = "检测跳过：账号未开启调度或当前状态不可检测"
	if cause != nil {
		created.ErrorMessage += ": " + cause.Error()
	}
	created.FinishedAt = time.Now()
	created.LatencyMs = created.FinishedAt.Sub(created.StartedAt).Milliseconds()
	if err := s.scheduledSvc.CompleteResult(persistCtx, plan.MaxResults, created); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d skipped result error: %v", plan.ID, accountID, err)
	}
}

func (s *ScheduledTestRunnerService) startAccountResult(ctx context.Context, plan *ScheduledTestPlan, accountID int64, outputKind string) (*ScheduledTestResult, error) {
	if s.scheduledSvc == nil {
		return nil, fmt.Errorf("scheduled test service unavailable")
	}
	started := time.Now()
	// Persist the in-progress row before contacting the upstream. This makes a
	// long-running test visible immediately and lets completion update the same
	// row instead of briefly showing no result (or creating duplicate history).
	pending := &ScheduledTestResult{
		TestDefinitionID: plan.TestDefinitionID,
		TargetMode:       plan.TargetMode,
		Status:           "running",
		OutputKind:       outputKind,
		AccountID:        &accountID,
		ModelID:          plan.ModelID,
		ReasoningEffort:  plan.ReasoningEffort,
		GroupID:          plan.GroupID,
		StartedAt:        started,
		FinishedAt:       started,
	}
	return s.scheduledSvc.StartResult(ctx, plan.ID, pending)
}

func (s *ScheduledTestRunnerService) runAccountWithResult(ctx context.Context, plan *ScheduledTestPlan, accountID int64, prompt, outputKind string, pending *ScheduledTestResult) {
	if outputKind == "statistics" {
		s.runStatisticsResult(ctx, plan, &accountID, pending, time.Now().UTC())
		return
	}
	started := time.Now()
	if pending != nil {
		started = pending.StartedAt
	}
	result := s.runWithAutomaticRetries(ctx, plan, &accountID, func(attemptCtx context.Context) *ScheduledTestResult {
		if !s.acquireWorker(attemptCtx) {
			return scheduledTestFailedAttempt(started, attemptCtx.Err())
		}
		// Release the worker before retry backoff so other accounts can run.
		defer s.releaseWorker()
		timeout := s.executionTimeout
		if timeout <= 0 {
			timeout = scheduledTestExecutionTimeout
		}
		// Each attempt starts a fresh budget after obtaining a worker; a
		// previous attempt's deadline must not poison a retry or later type.
		executionCtx, cancel := context.WithTimeout(attemptCtx, timeout)
		defer cancel()
		attemptStarted := time.Now()
		checkCtx, checkCancel := context.WithTimeout(executionCtx, scheduledTestPersistenceTimeout)
		eligible, err := s.detectionAccountEligible(checkCtx, plan, accountID)
		checkCancel()
		if err != nil {
			return scheduledTestFailedAttempt(attemptStarted, err)
		}
		if !eligible {
			return scheduledTestFailedAttempt(attemptStarted, fmt.Errorf("account is no longer enabled for detection"))
		}
		if s.accountTestSvc == nil {
			return scheduledTestFailedAttempt(attemptStarted, fmt.Errorf("account test service unavailable"))
		}
		result, err := s.accountTestSvc.RunTestBackgroundWithPromptAndReasoning(executionCtx, accountID, plan.ModelID, prompt, plan.ReasoningEffort)
		if deadlineErr := executionCtx.Err(); deadlineErr != nil {
			return scheduledTestFailedAttempt(attemptStarted, deadlineErr)
		}
		if err != nil {
			return scheduledTestFailedAttempt(attemptStarted, err)
		}
		if result == nil {
			return scheduledTestFailedAttempt(attemptStarted, fmt.Errorf("account test returned no result"))
		}
		// Background tests encode upstream errors in Status, often with a nil
		// Go error. Output validation can also turn a response into a failure.
		s.applyOutputContract(result, outputKind)
		return result
	})
	result.AccountID = &accountID
	result.PlanID = plan.ID
	if pending != nil {
		result.ID = pending.ID
		result.StartedAt = pending.StartedAt
		result.CreatedAt = pending.CreatedAt
	}
	result.ModelID = plan.ModelID
	result.ReasoningEffort = plan.ReasoningEffort
	result.GroupID = plan.GroupID
	result.TestDefinitionID = plan.TestDefinitionID
	result.TargetMode = plan.TargetMode
	result.OutputKind = outputKind
	if outputKind == "model_check" {
		applyScheduledTestModelCheck(result, plan)
	}
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	if s.scheduledSvc == nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d SaveResult skipped: scheduled test service unavailable", plan.ID, accountID)
		return
	}
	if pending != nil && pending.ID > 0 {
		if err := s.scheduledSvc.CompleteResult(persistCtx, plan.MaxResults, result); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d CompleteResult error: %v", plan.ID, accountID, err)
			return
		}
	} else if err := s.scheduledSvc.SaveResult(persistCtx, plan.ID, plan.MaxResults, result); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d SaveResult error: %v", plan.ID, accountID, err)
		return
	}
	// Shutdown/cancellation is not an account-quality observation.
	if ctx.Err() == nil {
		s.completeResultProtection(persistCtx, plan, result)
	}
	modelCheckPassed := outputKind != "model_check" || (result.OutputModelCheck != nil && result.OutputModelCheck.Verdict == "pass")
	if result.Status == "success" && modelCheckPassed && plan.AutoRecover && !plan.Protection.Enabled {
		recoveryCtx, cancelRecovery := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
		s.tryRecoverAccount(recoveryCtx, accountID, plan.ID)
		cancelRecovery()
	}
}

func (s *ScheduledTestRunnerService) savePlanFailure(ctx context.Context, plan *ScheduledTestPlan, outputKind string, cause error) {
	if plan == nil || s.scheduledSvc == nil {
		return
	}
	now := time.Now()
	if strings.TrimSpace(outputKind) == "" {
		outputKind = "text"
	}
	errorMessage := "scheduled test failed"
	if cause != nil {
		errorMessage = cause.Error()
	}
	result := &ScheduledTestResult{Status: "running", TestDefinitionID: plan.TestDefinitionID, TargetMode: plan.TargetMode, OutputKind: outputKind, GroupID: plan.GroupID, ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, StartedAt: now, FinishedAt: now}
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	created, err := s.scheduledSvc.StartResult(persistCtx, plan.ID, result)
	if err == nil && created != nil {
		created.Status = "failed"
		created.ErrorMessage = errorMessage
		created.FinishedAt = time.Now()
		created.LatencyMs = created.FinishedAt.Sub(created.StartedAt).Milliseconds()
		err = s.scheduledSvc.CompleteResult(persistCtx, plan.MaxResults, created)
	}
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d failure result error: %v", plan.ID, err)
	}
}

func (s *ScheduledTestRunnerService) advancePlan(ctx context.Context, plan *ScheduledTestPlan) {
	if plan == nil || s.planRepo == nil {
		return
	}
	now := time.Now()
	nextRun, err := computeNextRun(plan.CronExpression, now)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d computeNextRun error: %v", plan.ID, err)
		return
	}
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	if err := s.planRepo.UpdateAfterRun(persistCtx, plan.ID, now, nextRun); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d UpdateAfterRun error: %v", plan.ID, err)
	}
}

func (s *ScheduledTestRunnerService) applyOutputContract(result *ScheduledTestResult, outputKind string) {
	if result == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(outputKind)) {
	case "html":
		html := extractScheduledTestHTML(result.ResponseText)
		if html == "" {
			if result.Status == "success" {
				result.Status = "failed"
			}
			if result.ErrorMessage == "" {
				result.ErrorMessage = "expected HTML/SVG output"
			}
			return
		}
		result.OutputHTML = html
	case "number":
		if n, ok := extractScheduledTestNumber(result.ResponseText); ok {
			result.OutputNumeric = &n
			return
		}
		if result.Status == "success" {
			result.Status = "failed"
		}
		if result.ErrorMessage == "" {
			result.ErrorMessage = "expected numeric output"
		}
	}
}

func extractScheduledTestHTML(text string) string {
	// Some upstreams include tool-call transcripts in their text response. The
	// document can then be inside a JSON command string. Decode that string
	// once, without executing the command or unescaping ordinary HTML/scripts.
	if root := scheduledTestHTMLRootRE.FindStringIndex(text); root != nil {
		for i := root[0] - 1; i >= 0; i-- {
			if text[i] != '"' {
				continue
			}
			backslashes := 0
			for j := i - 1; j >= 0 && text[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 != 0 {
				continue
			}
			var decoded string
			if err := json.NewDecoder(strings.NewReader(text[i:])).Decode(&decoded); err == nil {
				if html := extractScheduledTestHTMLDocument(decoded); html != "" {
					return html
				}
			}
			break
		}
	}
	html := extractScheduledTestHTMLDocument(text)
	return decodeScheduledTestEscapedHTML(html)
}

func extractScheduledTestHTMLDocument(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	trimmed = unwrapScheduledTestMarkdownFence(trimmed)
	root := scheduledTestHTMLRootRE.FindStringSubmatchIndex(trimmed)
	if len(root) < 4 {
		// Plain text (including a model's explanatory sentence) is not an HTML
		// result. The output contract must fail closed instead of displaying it
		// as if it were a document.
		return ""
	}
	start := root[0]
	rootName := strings.ToLower(trimmed[root[2]:root[3]])
	end := scheduledTestHTMLRootEnd(trimmed, start, rootName)
	if end <= start {
		return ""
	}

	// Keep a doctype when the model emitted one immediately before the root,
	// while dropping prose such as "Here is the HTML:" before it.
	prefix := strings.TrimSpace(trimmed[:start])
	if prefix != "" {
		if loc := scheduledTestHTMLDoctypeRE.FindStringIndex(prefix); loc != nil {
			start = loc[0]
		}
	}
	return strings.TrimSpace(trimmed[start:end])
}

// Older results may contain only the escaped document, without its enclosing
// JSON string. Require escapes in markup itself, not just inside JavaScript,
// and a valid JSON encoding before decoding exactly one layer. Normal HTML
// and malformed/mixed encodings are left untouched.
func decodeScheduledTestEscapedHTML(html string) string {
	root := scheduledTestHTMLRootRE.FindStringIndex(html)
	if root == nil {
		return html
	}
	end := scheduledTestTagEnd(html, root[0]+1)
	if end < 0 || (!scheduledTestHTMLAttrRE.MatchString(html[root[0]:end]) && !scheduledTestHTMLSpaceRE.MatchString(html[end+1:])) {
		return html
	}
	var decoded string
	if err := json.Unmarshal([]byte(`"`+html+`"`), &decoded); err == nil {
		if document := extractScheduledTestHTMLDocument(decoded); document != "" {
			return document
		}
	}
	return html
}

func unwrapScheduledTestMarkdownFence(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "```") {
		return text
	}
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		return ""
	}
	header := strings.TrimSpace(text[3:lineEnd])
	if header != "" && !strings.EqualFold(header, "html") && !strings.EqualFold(header, "svg") {
		return text
	}
	body := text[lineEnd+1:]
	if close := strings.LastIndex(body, "```"); close >= 0 {
		body = body[:close]
	}
	return strings.TrimSpace(body)
}

// scheduledTestHTMLRootEnd returns the end offset of the first complete root
// element. It intentionally performs a small, quote-aware tag scan instead of
// taking everything after the opening tag, so a model's trailing explanation
// never leaks into the rendered result. It accepts normal HTML/SVG and custom
// element names, which keeps future output definitions extensible.
func scheduledTestHTMLRootEnd(text string, start int, rootName string) int {
	depth := 0
	for i := start; i < len(text); {
		open := strings.IndexByte(text[i:], '<')
		if open < 0 {
			return 0
		}
		i += open
		if strings.HasPrefix(text[i:], "<!--") {
			if close := strings.Index(text[i+4:], "-->"); close >= 0 {
				i += close + 7
				continue
			}
			return 0
		}
		end := scheduledTestTagEnd(text, i+1)
		if end < 0 {
			return 0
		}
		inside := strings.TrimSpace(text[i+1 : end])
		if inside == "" || strings.HasPrefix(inside, "!") || strings.HasPrefix(inside, "?") {
			i = end + 1
			continue
		}
		closing := strings.HasPrefix(inside, "/")
		if closing {
			inside = strings.TrimSpace(strings.TrimPrefix(inside, "/"))
		}
		nameEnd := 0
		for nameEnd < len(inside) {
			c := inside[nameEnd]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == ':' || c == '.' || c == '_' || c == '-' {
				nameEnd++
				continue
			}
			break
		}
		name := strings.ToLower(inside[:nameEnd])
		if name != rootName {
			i = end + 1
			continue
		}
		if closing {
			depth--
			if depth == 0 {
				return end + 1
			}
		} else if strings.HasSuffix(strings.TrimSpace(inside), "/") {
			if depth == 0 {
				return end + 1
			}
		} else {
			depth++
		}
		i = end + 1
	}
	return 0
}

func scheduledTestTagEnd(text string, start int) int {
	var quote byte
	for i := start; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '>' {
			return i
		}
	}
	return -1
}

func extractScheduledTestNumber(text string) (float64, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	// A bare numeric response is unambiguous, including decimals, signs and
	// scientific notation.
	if scheduledTestStandaloneNumberRE.MatchString(trimmed) {
		if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return n, true
		}
	}

	// Prefer an explicit final-answer marker. A later phrase such as
	// "至少 1 个" often appears in the proof and must not overwrite the
	// actual answer "最少取出 29 个".
	for _, markerRE := range []*regexp.Regexp{scheduledTestStrongNumberRE, scheduledTestMinimumNumberRE, scheduledTestAtLeastNumberRE} {
		matches := markerRE.FindAllStringSubmatch(trimmed, -1)
		for i := len(matches) - 1; i >= 0; i-- {
			if len(matches[i]) > 1 {
				if n, err := strconv.ParseFloat(matches[i][1], 64); err == nil {
					return n, true
				}
			}
		}
	}

	// A final non-empty line may contain an answer marker that was not matched
	// above because the model used punctuation or markdown formatting. Search
	// lines from the end but still require exactly one numeric token and a
	// marker-like word; this avoids returning an unrelated line number.
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(strings.Trim(lines[i], "`*_ \\t"))
		if line == "" || !scheduledTestAnswerLineRE.MatchString(line) {
			continue
		}
		matches := scheduledTestNumberRE.FindAllString(line, -1)
		if len(matches) != 1 {
			continue
		}
		if n, err := strconv.ParseFloat(matches[0], 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func (s *ScheduledTestRunnerService) RunPlanNow(ctx context.Context, plan *ScheduledTestPlan) {
	s.runOnePlan(ctx, plan)
}

// RetryAccount reuses the failed row for the chosen account and returns its
// running snapshot. It does not advance the plan's cron schedule. The account
// guard is also used by normal plan runs, so the retry can run while other
// members of the group are still generating without duplicating this account.
func (s *ScheduledTestRunnerService) RetryAccount(ctx context.Context, plan *ScheduledTestPlan, previous *ScheduledTestResult) (*ScheduledTestResult, error) {
	if plan == nil || plan.ID <= 0 || previous == nil || previous.ID <= 0 || previous.PlanID != plan.ID || previous.AccountID == nil || *previous.AccountID <= 0 {
		return nil, fmt.Errorf("matching test plan, result and account are required")
	}
	if previous.Status != "failed" {
		return nil, ErrScheduledTestResultNotFailed
	}
	bg, finish, err := s.beginRunContext(context.Background())
	if err != nil {
		return nil, err
	}
	// Validation still respects the HTTP caller, but shutdown must also
	// interrupt it before this request can enqueue a new background retry.
	validationCtx, cancelValidation := context.WithTimeout(ctx, scheduledTestPersistenceTimeout)
	stopValidation := context.AfterFunc(bg, cancelValidation)
	defer func() {
		stopValidation()
		cancelValidation()
	}()
	ctx = validationCtx
	queued := false
	defer func() {
		if !queued {
			finish()
		}
	}()
	// Retry the failed execution's type and target snapshot, even when its
	// parent rule has subsequently been edited to select different checks.
	execution := *plan
	execution.TestDefinitionID = previous.TestDefinitionID
	execution.TestDefinitionIDs = nil
	if previous.TargetMode != "" {
		execution.TargetMode = previous.TargetMode
		execution.GroupID = previous.GroupID
		execution.ModelID = previous.ModelID
		execution.ReasoningEffort = previous.ReasoningEffort
	}
	plan = &execution
	accountID := *previous.AccountID
	if !s.beginAccountRun(plan.ID, accountID, plan.TestDefinitionID) {
		return nil, ErrScheduledTestAccountRunning
	}
	defer func() {
		if !queued {
			s.endAccountRun(plan.ID, accountID, plan.TestDefinitionID)
		}
	}()
	if plan.AccountID != nil && *plan.AccountID != accountID {
		return nil, fmt.Errorf("the plan now targets a different account")
	}
	if s.accountRepo == nil {
		return nil, fmt.Errorf("account repository unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("test account unavailable: %w", err)
	}
	if account == nil {
		return nil, fmt.Errorf("test account unavailable")
	}
	if eligible, err := s.detectionAccountEligible(ctx, plan, accountID); err != nil || !eligible {
		return nil, fmt.Errorf("account is not enabled for detection")
	}
	followsMoves := false
	if s.scheduledSvc != nil {
		_, followsMoves = s.scheduledSvc.resultRepo.(ScheduledTestActionRepository)
	}
	if plan.GroupID != nil && !followsMoves {
		linked := false
		for _, groupID := range account.GroupIDs {
			if groupID == *plan.GroupID {
				linked = true
				break
			}
		}
		if !linked {
			return nil, fmt.Errorf("account %d is no longer assigned to group %d", accountID, *plan.GroupID)
		}
	}
	prompt, outputKind, err := s.resolveDefinition(ctx, plan)
	if err != nil {
		return nil, err
	}
	if outputKind == "statistics" {
		plan.ReasoningEffort = ""
	}
	if s.scheduledSvc == nil {
		return nil, fmt.Errorf("scheduled test service unavailable")
	}
	started := time.Now()
	pending := &ScheduledTestResult{
		ID: previous.ID, PlanID: plan.ID, CreatedAt: previous.CreatedAt,
		TestDefinitionID: previous.TestDefinitionID, TargetMode: plan.TargetMode,
		Status: "running", OutputKind: outputKind, AccountID: &accountID,
		ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, GroupID: plan.GroupID,
		StartedAt: started, FinishedAt: started,
	}
	if err := s.scheduledSvc.RestartFailedResult(ctx, pending); err != nil {
		return nil, err
	}
	if err := s.beginResultProtection(ctx, plan, pending); err != nil {
		pending.Status, pending.ErrorMessage = "failed", "unable to initialize test protection"
		cleanupCtx, cleanupCancel := scheduledTestPersistenceContext()
		_ = s.scheduledSvc.CompleteResult(cleanupCtx, plan.MaxResults, pending)
		cleanupCancel()
		return nil, err
	}
	// Return an immutable snapshot to the HTTP handler while the background
	// worker updates the stored row independently of the request lifecycle.
	response := *pending
	queued = true
	go func() {
		defer finish()
		defer s.endAccountRun(plan.ID, accountID, plan.TestDefinitionID)
		s.runAccountWithResult(bg, plan, accountID, prompt, outputKind, pending)
	}()
	return &response, nil
}

func scheduledTestPersistenceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), scheduledTestPersistenceTimeout)
}

func (s *ScheduledTestRunnerService) beginPlanRun(planID int64) bool {
	s.planRunMu.Lock()
	defer s.planRunMu.Unlock()
	if s.runningPlans == nil {
		s.runningPlans = make(map[int64]struct{})
	}
	if _, ok := s.runningPlans[planID]; ok {
		return false
	}
	s.runningPlans[planID] = struct{}{}
	return true
}

func (s *ScheduledTestRunnerService) endPlanRun(planID int64) {
	s.planRunMu.Lock()
	defer s.planRunMu.Unlock()
	delete(s.runningPlans, planID)
}

// A retry suppresses duplicate executions of its check, never another type.
func scheduledTestAccountRunKey(planID, accountID int64, definitionID *int64) [3]int64 {
	key := [3]int64{planID, accountID, 0}
	if definitionID != nil {
		key[2] = *definitionID
	}
	return key
}

func (s *ScheduledTestRunnerService) beginAccountRun(planID, accountID int64, definitionID *int64) bool {
	s.accountRunMu.Lock()
	defer s.accountRunMu.Unlock()
	if s.runningAccounts == nil {
		s.runningAccounts = make(map[[3]int64]struct{})
	}
	key := scheduledTestAccountRunKey(planID, accountID, definitionID)
	if _, running := s.runningAccounts[key]; running {
		return false
	}
	s.runningAccounts[key] = struct{}{}
	return true
}

func (s *ScheduledTestRunnerService) endAccountRun(planID, accountID int64, definitionID *int64) {
	s.accountRunMu.Lock()
	defer s.accountRunMu.Unlock()
	delete(s.runningAccounts, scheduledTestAccountRunKey(planID, accountID, definitionID))
}

func (s *ScheduledTestRunnerService) acquireWorker(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	s.workerMu.Lock()
	if s.workerSem == nil {
		s.workerSem = make(chan struct{}, scheduledTestDefaultMaxWorkers)
	}
	sem := s.workerSem
	s.workerMu.Unlock()
	select {
	case sem <- struct{}{}:
		if ctx.Err() != nil {
			<-sem
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *ScheduledTestRunnerService) releaseWorker() {
	s.workerMu.Lock()
	sem := s.workerSem
	s.workerMu.Unlock()
	if sem != nil {
		<-sem
	}
}

// tryRecoverAccount attempts to recover an account from recoverable runtime state.
func (s *ScheduledTestRunnerService) tryRecoverAccount(ctx context.Context, accountID int64, planID int64) {
	if s.rateLimitSvc == nil {
		return
	}

	recovery, err := s.rateLimitSvc.RecoverAccountAfterSuccessfulTest(ctx, accountID)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover failed: %v", planID, err)
		return
	}
	if recovery == nil {
		return
	}

	if recovery.ClearedError {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover: account=%d recovered from error status", planID, accountID)
	}
	if recovery.ClearedRateLimit {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover: account=%d cleared rate-limit/runtime state", planID, accountID)
	}
}
