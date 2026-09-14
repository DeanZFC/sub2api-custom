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
	if (plan.AccountID == nil || *plan.AccountID <= 0) == (plan.GroupID == nil || *plan.GroupID <= 0) {
		return fmt.Errorf("exactly one of account_id or group_id must be provided")
	}
	if plan.GroupID != nil && *plan.GroupID > 0 && plan.TestDefinitionID == nil {
		return fmt.Errorf("group targets require a test_definition_id")
	}
	plan.ModelID = strings.TrimSpace(plan.ModelID)
	if plan.ModelID == "" {
		return fmt.Errorf("model_id is required")
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

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo       ScheduledTestPlanRepository
	resultRepo     ScheduledTestResultRepository
	definitionRepo ScheduledTestDefinitionRepository
	runFunc        func(context.Context, *ScheduledTestPlan)
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
	bg, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}
func (s *ScheduledTestService) ListVisibleResults(ctx context.Context, userID int64, limit int) ([]*ScheduledTestResult, error) {
	return s.resultRepo.ListVisible(ctx, userID, limit)
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

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}
