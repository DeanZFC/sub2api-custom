package service

import (
	"context"
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
)

const scheduledTestPersistenceTimeout = 15 * time.Second

var ErrScheduledTestAccountRunning = errors.New("this account is already being tested for this plan")
var ErrScheduledTestResultNotFailed = errors.New("test result is no longer failed; refresh the results before retrying")

// ScheduledTestRunnerService periodically scans due test plans and executes them.
type ScheduledTestRunnerService struct {
	planRepo       ScheduledTestPlanRepository
	scheduledSvc   *ScheduledTestService
	accountTestSvc *AccountTestService
	accountRepo    AccountRepository
	rateLimitSvc   *RateLimitService
	cfg            *config.Config

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once
	runMu     sync.Mutex

	// runningPlans prevents a manual RunNow from racing the cron sweep (and
	// protects against duplicate scheduler ticks in deployments with more than
	// one trigger). workerSem is shared by every plan and every account, so a
	// group plan cannot multiply the configured concurrency by the number of
	// concurrently running plans.
	planRunMu       sync.Mutex
	runningPlans    map[int64]struct{}
	workerMu        sync.Mutex
	workerSem       chan struct{}
	accountRunMu    sync.Mutex
	runningAccounts map[[2]int64]struct{}
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

// Stop gracefully shuts down the cron scheduler.
func (s *ScheduledTestRunnerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] cron stop timed out")
			}
		}
	})
}

func (s *ScheduledTestRunnerService) runScheduled() {
	// A slow upstream or a large group can outlive the one-minute cron tick.
	// Do not start a second sweep against the same due plans while the previous
	// sweep is still running.
	if !s.runMu.TryLock() {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] previous sweep still running; skipping tick")
		return
	}
	defer s.runMu.Unlock()

	// Delay 10s so execution lands at ~:10 of each minute instead of :00.
	time.Sleep(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), scheduledTestExecutionTimeout)
	defer cancel()

	now := time.Now()
	plans, err := s.planRepo.ListDue(ctx, now)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] ListDue error: %v", err)
		return
	}
	if len(plans) == 0 {
		return
	}

	logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] found %d due plans", len(plans))

	sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var wg sync.WaitGroup

	for _, plan := range plans {
		sem <- struct{}{}
		wg.Add(1)
		go func(p *ScheduledTestPlan) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOnePlan(ctx, p)
		}(plan)
	}

	wg.Wait()
}

func (s *ScheduledTestRunnerService) runOnePlan(ctx context.Context, plan *ScheduledTestPlan) {
	if plan == nil {
		return
	}
	if !s.beginPlanRun(plan.ID) {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d is already running; skipping overlapping run", plan.ID)
		return
	}
	defer s.endPlanRun(plan.ID)

	// Resolve the configured test definition before selecting an account. A
	// disabled/deleted definition is a failed execution and must still advance
	// next_run_at, otherwise the plan remains due forever and silently retries.
	prompt, outputKind, definitionErr := s.resolveDefinition(ctx, plan)
	if definitionErr != nil {
		s.savePlanFailure(ctx, plan, "text", definitionErr)
		s.advancePlan(ctx, plan)
		return
	}

	accountIDs, resolveErr := s.resolveTargetAccounts(ctx, plan)
	if resolveErr != nil || len(accountIDs) == 0 {
		if resolveErr == nil {
			resolveErr = fmt.Errorf("no schedulable account is available for this group")
		}
		s.savePlanFailure(ctx, plan, outputKind, resolveErr)
		s.advancePlan(ctx, plan)
		return
	}

	// A group target means every currently schedulable account in that group.
	// Keep each account's result separate so administrators can identify the
	// failing upstream and users only see results for their entitled group.
	sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
	var wg sync.WaitGroup
	for _, accountID := range accountIDs {
		accountID := accountID
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOneAccount(ctx, plan, accountID, prompt, outputKind)
		}()
	}
	wg.Wait()
	s.advancePlan(ctx, plan)
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
	return d.Prompt, outputKind, nil
}

func (s *ScheduledTestRunnerService) resolveTargetAccounts(ctx context.Context, plan *ScheduledTestPlan) ([]int64, error) {
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
	if !s.beginAccountRun(plan.ID, accountID) {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d is already running; skipping overlapping run", plan.ID, accountID)
		return
	}
	defer s.endAccountRun(plan.ID, accountID)

	persistCtx, cancel := scheduledTestPersistenceContext()
	pending, err := s.startAccountResult(persistCtx, plan, accountID, outputKind)
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d StartResult error: %v", plan.ID, accountID, err)
	}
	s.runAccountWithResult(ctx, plan, accountID, prompt, outputKind, pending)
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
		Status:          "running",
		OutputKind:      outputKind,
		AccountID:       &accountID,
		ModelID:         plan.ModelID,
		ReasoningEffort: plan.ReasoningEffort,
		GroupID:         plan.GroupID,
		StartedAt:       started,
		FinishedAt:      started,
	}
	return s.scheduledSvc.StartResult(ctx, plan.ID, pending)
}

func (s *ScheduledTestRunnerService) runAccountWithResult(ctx context.Context, plan *ScheduledTestPlan, accountID int64, prompt, outputKind string, pending *ScheduledTestResult) {
	started := time.Now()
	if pending != nil {
		started = pending.StartedAt
	}
	var result *ScheduledTestResult
	var err error
	if !s.acquireWorker(ctx) {
		err = ctx.Err()
		if err == nil {
			err = context.Canceled
		}
	} else {
		if s.accountTestSvc == nil {
			err = fmt.Errorf("account test service unavailable")
		} else {
			result, err = s.accountTestSvc.RunTestBackgroundWithPromptAndReasoning(ctx, accountID, plan.ModelID, prompt, plan.ReasoningEffort)
		}
		s.releaseWorker()
	}
	if err != nil {
		result = &ScheduledTestResult{Status: "failed", ErrorMessage: err.Error(), StartedAt: started, FinishedAt: time.Now(), LatencyMs: time.Since(started).Milliseconds()}
	}
	if result == nil {
		result = &ScheduledTestResult{Status: "failed", ErrorMessage: "account test returned no result", StartedAt: started, FinishedAt: time.Now(), LatencyMs: time.Since(started).Milliseconds()}
	}
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
	result.OutputKind = outputKind
	s.applyOutputContract(result, outputKind)
	persistCtx, cancel := scheduledTestPersistenceContext()
	defer cancel()
	if s.scheduledSvc == nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d SaveResult skipped: scheduled test service unavailable", plan.ID, accountID)
		return
	}
	if pending != nil && pending.ID > 0 {
		if err := s.scheduledSvc.CompleteResult(persistCtx, plan.MaxResults, result); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d CompleteResult error: %v", plan.ID, accountID, err)
		}
	} else if err := s.scheduledSvc.SaveResult(persistCtx, plan.ID, plan.MaxResults, result); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d SaveResult error: %v", plan.ID, accountID, err)
	}
	if result.Status == "success" && plan.AutoRecover {
		s.tryRecoverAccount(ctx, accountID, plan.ID)
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
	result := &ScheduledTestResult{Status: "running", OutputKind: outputKind, GroupID: plan.GroupID, ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, StartedAt: now, FinishedAt: now}
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
		doctypeRE := regexp.MustCompile(`(?is)<!doctype\s+html\s*>\s*$`)
		if loc := doctypeRE.FindStringIndex(prefix); loc != nil {
			start = loc[0]
		}
	}
	return strings.TrimSpace(trimmed[start:end])
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
	accountID := *previous.AccountID
	if !s.beginAccountRun(plan.ID, accountID) {
		return nil, ErrScheduledTestAccountRunning
	}
	queued := false
	defer func() {
		if !queued {
			s.endAccountRun(plan.ID, accountID)
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
	if plan.GroupID != nil {
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
	if s.scheduledSvc == nil {
		return nil, fmt.Errorf("scheduled test service unavailable")
	}
	started := time.Now()
	pending := &ScheduledTestResult{
		ID: previous.ID, PlanID: plan.ID, CreatedAt: previous.CreatedAt,
		Status: "running", OutputKind: outputKind, AccountID: &accountID,
		ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, GroupID: plan.GroupID,
		StartedAt: started, FinishedAt: started,
	}
	if err := s.scheduledSvc.RestartFailedResult(ctx, pending); err != nil {
		return nil, err
	}
	// Return an immutable snapshot to the HTTP handler while the background
	// worker updates the stored row independently of the request lifecycle.
	response := *pending
	bg, cancel := context.WithTimeout(context.Background(), scheduledTestExecutionTimeout)
	queued = true
	go func() {
		defer cancel()
		defer s.endAccountRun(plan.ID, accountID)
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

func (s *ScheduledTestRunnerService) beginAccountRun(planID, accountID int64) bool {
	s.accountRunMu.Lock()
	defer s.accountRunMu.Unlock()
	if s.runningAccounts == nil {
		s.runningAccounts = make(map[[2]int64]struct{})
	}
	key := [2]int64{planID, accountID}
	if _, running := s.runningAccounts[key]; running {
		return false
	}
	s.runningAccounts[key] = struct{}{}
	return true
}

func (s *ScheduledTestRunnerService) endAccountRun(planID, accountID int64) {
	s.accountRunMu.Lock()
	defer s.accountRunMu.Unlock()
	delete(s.runningAccounts, [2]int64{planID, accountID})
}

func (s *ScheduledTestRunnerService) acquireWorker(ctx context.Context) bool {
	s.workerMu.Lock()
	if s.workerSem == nil {
		s.workerSem = make(chan struct{}, scheduledTestDefaultMaxWorkers)
	}
	sem := s.workerSem
	s.workerMu.Unlock()
	select {
	case sem <- struct{}{}:
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
