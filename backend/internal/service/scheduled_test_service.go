package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Scheduled tests may generate large HTML/SVG responses (for example the
// pelican animation). Keep their background context independent from the
// short-lived admin HTTP request and allow enough time for streamed output.
const scheduledTestExecutionTimeout = 15 * time.Minute

var scheduledTestDefinitionKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,99}$`)

// ValidateDefinitionInput keeps definitions predictable while leaving room for
// new renderers. output_kind is metadata consumed by clients, so new kinds can
// be introduced without a backend migration as long as they use a safe key.
func ValidateScheduledTestDefinitionInput(d *ScheduledTestDefinition) error {
	if d == nil {
		return fmt.Errorf("test definition is required")
	}
	d.Key = strings.ToLower(strings.TrimSpace(d.Key))
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	d.Prompt = strings.TrimSpace(d.Prompt)
	d.OutputKind = strings.ToLower(strings.TrimSpace(d.OutputKind))
	if d.SortOrder < 0 {
		return fmt.Errorf("sort_order must be non-negative")
	}
	if d.Key == "" || !scheduledTestDefinitionKeyPattern.MatchString(d.Key) {
		return fmt.Errorf("key must contain only lowercase letters, numbers, '.', '_' or '-' and be at most 100 characters")
	}
	if d.Name == "" || len([]rune(d.Name)) > 200 {
		return fmt.Errorf("name is required and must be at most 200 characters")
	}
	if d.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if d.OutputKind == "" {
		d.OutputKind = "text"
	}
	if len(d.OutputKind) > 20 || !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,19}$`).MatchString(d.OutputKind) {
		return fmt.Errorf("output_kind must be a lowercase renderer key of at most 20 characters")
	}
	return nil
}

func validateScheduledTestPlan(plan *ScheduledTestPlan) error {
	if plan == nil {
		return fmt.Errorf("test plan is required")
	}
	plan.Name = strings.TrimSpace(plan.Name)
	if len([]rune(plan.Name)) > 200 {
		return fmt.Errorf("name must be at most 200 characters")
	}
	if plan.SortOrder < 0 {
		return fmt.Errorf("sort_order must be non-negative")
	}
	if (plan.AccountID == nil || *plan.AccountID <= 0) && (plan.GroupID == nil || *plan.GroupID <= 0) {
		return fmt.Errorf("group_id or account_id is required")
	}
	plan.TargetMode = strings.ToLower(strings.TrimSpace(plan.TargetMode))
	if plan.TargetMode == "" {
		if plan.AccountID != nil && *plan.AccountID > 0 {
			plan.TargetMode = "account"
		} else {
			plan.TargetMode = "all_accounts"
		}
	}
	switch plan.TargetMode {
	case "group":
		if plan.GroupID == nil || *plan.GroupID <= 0 {
			return fmt.Errorf("group target requires group_id")
		}
		if plan.AccountID != nil && *plan.AccountID > 0 {
			return fmt.Errorf("group target cannot include account_id")
		}
	case "all_accounts":
		if plan.GroupID == nil || *plan.GroupID <= 0 {
			return fmt.Errorf("all_accounts target requires group_id")
		}
		if plan.AccountID != nil && *plan.AccountID > 0 {
			return fmt.Errorf("all_accounts target cannot include account_id")
		}
	case "account":
		if plan.AccountID == nil || *plan.AccountID <= 0 {
			return fmt.Errorf("account target requires account_id")
		}
	default:
		return fmt.Errorf("target_mode must be group, all_accounts, or account")
	}
	if plan.GroupID != nil && *plan.GroupID > 0 && plan.TestDefinitionID == nil {
		return fmt.Errorf("group targets require a test_definition_id")
	}
	plan.ModelID = strings.TrimSpace(plan.ModelID)
	if plan.ModelID == "" {
		return fmt.Errorf("model_id is required")
	}
	plan.ReasoningEffort = strings.ToLower(strings.TrimSpace(plan.ReasoningEffort))
	if plan.ReasoningEffort != "" {
		if !scheduledTestReasoningEffortAllowed(plan.ModelID, plan.ReasoningEffort) {
			return fmt.Errorf("reasoning_effort %q is not supported by model %q", plan.ReasoningEffort, plan.ModelID)
		}
	}
	plan.CronExpression = strings.TrimSpace(plan.CronExpression)
	if plan.CronExpression == "" {
		return fmt.Errorf("cron_expression is required")
	}
	if plan.MaxResults < 0 {
		return fmt.Errorf("max_results cannot be negative")
	}
	return nil
}

// ScheduledTestReasoningEfforts returns the reasoning levels that may be sent
// for a model. Known Codex models use the same catalog as the Codex models
// endpoint; unknown/custom upstream models retain the complete standard set so
// the upstream metadata can decide their exact capabilities.
func ScheduledTestReasoningEfforts(modelID string) []string {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	descriptor := newConfiguredCodexModelDescriptor(modelID)
	levels := make([]string, 0, len(descriptor.SupportedReasoningLevels))
	for _, level := range descriptor.SupportedReasoningLevels {
		if effort := strings.ToLower(strings.TrimSpace(level.Effort)); effort != "" && effort != "none" {
			levels = append(levels, effort)
		}
	}
	// The generic descriptor intentionally advertises only `none`; custom
	// models can still expose reasoning through their upstream catalog.
	if len(levels) == 0 && descriptor.SupportedReasoningLevels != nil && len(descriptor.SupportedReasoningLevels) == 1 && descriptor.SupportedReasoningLevels[0].Effort == "none" {
		known := strings.ToLower(modelID)
		if !strings.HasPrefix(known, "gpt-") && !strings.HasPrefix(known, "claude-") && !strings.HasPrefix(known, "grok-") && !strings.Contains(known, "deepseek") {
			return []string{"minimal", "low", "medium", "high", "xhigh", "max", "ultra"}
		}
	}
	return levels
}

func scheduledTestReasoningEffortAllowed(modelID, effort string) bool {
	for _, supported := range ScheduledTestReasoningEfforts(modelID) {
		if supported == effort {
			return true
		}
	}
	return false
}

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo       ScheduledTestPlanRepository
	resultRepo     ScheduledTestResultRepository
	definitionRepo ScheduledTestDefinitionRepository
	runFunc        func(context.Context, *ScheduledTestPlan)
	retryFunc      func(context.Context, *ScheduledTestPlan, *ScheduledTestResult) (*ScheduledTestResult, error)
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// SetDefinitionRepository wires optional configurable test definitions without
// breaking legacy constructors used by integrations and tests.
func (s *ScheduledTestService) SetDefinitionRepository(repo ScheduledTestDefinitionRepository) {
	s.definitionRepo = repo
}
func (s *ScheduledTestService) SetRunFunc(f func(context.Context, *ScheduledTestPlan)) { s.runFunc = f }
func (s *ScheduledTestService) SetRetryFunc(f func(context.Context, *ScheduledTestPlan, *ScheduledTestResult) (*ScheduledTestResult, error)) {
	s.retryFunc = f
}

// RetryResult reruns the failed result's account in the same result row.
// The parent plan and its next scheduled execution are unchanged.
func (s *ScheduledTestService) RetryResult(ctx context.Context, id int64) (*ScheduledTestResult, error) {
	if id <= 0 {
		return nil, fmt.Errorf("invalid test result id")
	}
	previous, err := s.resultRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if previous != nil && (previous.Status == "running" || previous.Status == "pending") {
		return nil, ErrScheduledTestAccountRunning
	}
	if previous == nil || previous.Status != "failed" {
		return nil, fmt.Errorf("only failed test results can be retried")
	}
	if previous.AccountID == nil || *previous.AccountID <= 0 {
		return nil, fmt.Errorf("test result has no account to retry; run the plan again")
	}
	plan, err := s.GetPlan(ctx, previous.PlanID)
	if err != nil {
		return nil, err
	}
	if s.retryFunc == nil {
		return nil, fmt.Errorf("test runner unavailable")
	}
	result, err := s.retryFunc(ctx, plan, previous)
	if err != nil {
		return nil, err
	}
	result.PlanName = plan.Name
	result.TestName = previous.TestName
	result.GroupName = previous.GroupName
	result.TargetMode = plan.TargetMode
	return result, nil
}

// RestartFailedResult atomically reuses a failed row and clears its old output
// before the upstream request is queued. A stale retry cannot replace a result
// that has already started or completed elsewhere.
func (s *ScheduledTestService) RestartFailedResult(ctx context.Context, result *ScheduledTestResult) error {
	return s.resultRepo.RestartFailed(ctx, result)
}

func (s *ScheduledTestService) RunNow(ctx context.Context, id int64) error {
	p, e := s.GetPlan(ctx, id)
	if e != nil {
		return e
	}
	if s.runFunc == nil {
		return fmt.Errorf("test runner unavailable")
	}
	// The HTTP request context is cancelled as soon as the 202 response is
	// returned; background execution must therefore use its own bounded context.
	bg, cancel := context.WithTimeout(context.Background(), scheduledTestExecutionTimeout)
	go func() { defer cancel(); s.runFunc(bg, p) }()
	return nil
}
func (s *ScheduledTestService) ListPlans(ctx context.Context) ([]*ScheduledTestPlan, error) {
	return s.planRepo.List(ctx)
}
func (s *ScheduledTestService) ListDefinitions(ctx context.Context, enabledOnly bool) ([]*ScheduledTestDefinition, error) {
	if s.definitionRepo == nil {
		return nil, fmt.Errorf("test definitions unavailable")
	}
	return s.definitionRepo.List(ctx, enabledOnly)
}
func (s *ScheduledTestService) GetDefinition(ctx context.Context, id int64) (*ScheduledTestDefinition, error) {
	if s.definitionRepo == nil {
		return nil, fmt.Errorf("test definitions unavailable")
	}
	return s.definitionRepo.GetByID(ctx, id)
}
func (s *ScheduledTestService) CreateDefinition(ctx context.Context, d *ScheduledTestDefinition) (*ScheduledTestDefinition, error) {
	if s.definitionRepo == nil {
		return nil, fmt.Errorf("test definitions unavailable")
	}
	if err := ValidateScheduledTestDefinitionInput(d); err != nil {
		return nil, err
	}
	return s.definitionRepo.Create(ctx, d)
}
func (s *ScheduledTestService) UpdateDefinition(ctx context.Context, d *ScheduledTestDefinition) (*ScheduledTestDefinition, error) {
	if s.definitionRepo == nil {
		return nil, fmt.Errorf("test definitions unavailable")
	}
	if err := ValidateScheduledTestDefinitionInput(d); err != nil {
		return nil, err
	}
	return s.definitionRepo.Update(ctx, d)
}
func (s *ScheduledTestService) DeleteDefinition(ctx context.Context, id int64) error {
	if s.definitionRepo == nil {
		return fmt.Errorf("test definitions unavailable")
	}
	return s.definitionRepo.Delete(ctx, id)
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := validateScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	if err := s.validateDefinitionForPlan(ctx, plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := validateScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	if err := s.validateDefinitionForPlan(ctx, plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun
	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Update(ctx, plan)
}

func (s *ScheduledTestService) validateDefinitionForPlan(ctx context.Context, plan *ScheduledTestPlan) error {
	if plan == nil || plan.TestDefinitionID == nil {
		return nil
	}
	if s.definitionRepo == nil {
		return fmt.Errorf("test definitions unavailable")
	}
	d, err := s.definitionRepo.GetByID(ctx, *plan.TestDefinitionID)
	if err != nil {
		return fmt.Errorf("test definition not found: %w", err)
	}
	if d == nil {
		return fmt.Errorf("test definition is disabled")
	}
	if !d.Enabled && plan.Enabled {
		return fmt.Errorf("test definition is disabled")
	}
	return nil
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	results, err := s.resultRepo.ListByPlanID(ctx, planID, limit)
	if err != nil {
		return nil, err
	}
	normalizeStoredTestResults(results)
	return results, nil
}
func (s *ScheduledTestService) ListVisibleResults(ctx context.Context, userID int64, limit int) ([]*ScheduledTestResult, error) {
	results, err := s.resultRepo.ListVisible(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	normalizeStoredTestResults(results)
	return results, nil
}

// normalizeStoredTestResults repairs derived output from older parsers when
// results are read. Keep the response and status unchanged so historical
// records remain available for diagnosis without rewriting persisted data.
func normalizeStoredTestResults(results []*ScheduledTestResult) {
	for _, result := range results {
		if result == nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(result.OutputKind)) {
		case "html":
			// Prefer the original response; older output_html values may have
			// lost the doctype or retained JSON string escapes from tool output.
			if html := extractScheduledTestHTML(result.ResponseText); html != "" {
				result.OutputHTML = html
			} else if html := extractScheduledTestHTML(result.OutputHTML); html != "" {
				result.OutputHTML = html
			}
		case "number":
			if strings.TrimSpace(result.ResponseText) == "" {
				continue
			}
			if numeric, ok := extractScheduledTestNumber(result.ResponseText); ok {
				result.OutputNumeric = &numeric
			} else {
				// If the original text has no unambiguous answer, show that text
				// rather than retain a number selected by an older parser.
				result.OutputNumeric = nil
			}
		}
	}
}

// DeleteResult removes one test execution result by its ID.
func (s *ScheduledTestService) DeleteResult(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("invalid test result id")
	}
	return s.resultRepo.Delete(ctx, id)
}

// SaveResult inserts a result and prunes old entries beyond maxResults.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	if result == nil {
		return fmt.Errorf("test result is required")
	}
	if maxResults <= 0 {
		maxResults = 50
	}
	result.PlanID = planID
	if _, err := s.resultRepo.Create(ctx, result); err != nil {
		return err
	}
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

// StartResult persists a running result before any upstream work begins. The
// caller can then update the same row when the test finishes, allowing clients
// to render an in-progress execution immediately.
func (s *ScheduledTestService) StartResult(ctx context.Context, planID int64, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	if result == nil {
		return nil, fmt.Errorf("test result is required")
	}
	result.PlanID = planID
	if strings.TrimSpace(result.Status) == "" {
		result.Status = "running"
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = time.Now()
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = result.StartedAt
	}
	return s.resultRepo.Create(ctx, result)
}

// CompleteResult updates a previously-created running result and prunes old
// rows after the final state is visible.
func (s *ScheduledTestService) CompleteResult(ctx context.Context, maxResults int, result *ScheduledTestResult) error {
	if result == nil || result.ID <= 0 {
		return fmt.Errorf("test result id is required")
	}
	if err := s.resultRepo.Update(ctx, result); err != nil {
		return err
	}
	if maxResults <= 0 {
		maxResults = 50
	}
	return s.resultRepo.PruneOldResults(ctx, result.PlanID, maxResults)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}
