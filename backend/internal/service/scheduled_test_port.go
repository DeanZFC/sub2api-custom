package service

import (
	"context"
	"time"
)

// ScheduledTestPlan represents a scheduled test plan domain model.
type ScheduledTestPlan struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// SortOrder controls the order in which this test rule's group is shown
	// on the user-facing test-results page. It is intentionally stored on the
	// rule/plan rather than on groups, because the same group can participate
	// in multiple test rules with different display positions.
	SortOrder        int        `json:"sort_order"`
	AccountID        *int64     `json:"account_id,omitempty"`
	GroupID          *int64     `json:"group_id,omitempty"`
	TestDefinitionID *int64     `json:"test_definition_id,omitempty"`
	TestType         string     `json:"test_type"`
	TargetMode       string     `json:"target_mode"`
	ModelID          string     `json:"model_id"`
	ReasoningEffort  string     `json:"reasoning_effort,omitempty"`
	CronExpression   string     `json:"cron_expression"`
	Enabled          bool       `json:"enabled"`
	MaxResults       int        `json:"max_results"`
	AutoRecover      bool       `json:"auto_recover"`
	LastRunAt        *time.Time `json:"last_run_at"`
	NextRunAt        *time.Time `json:"next_run_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// ScheduledTestResult represents a single test execution result.
type ScheduledTestResult struct {
	ID        int64  `json:"id"`
	PlanID    int64  `json:"plan_id"`
	PlanName  string `json:"plan_name"`
	TestName  string `json:"test_name"`
	TestOrder int    `json:"test_order"`
	GroupName string `json:"group_name"`
	// PlanOrder is the administrator-configured order of the test rule/plan
	// that produced this result. The user-facing page sorts by this value and
	// derives its group list from that ordered result stream.
	PlanOrder       int       `json:"plan_order"`
	TargetMode      string    `json:"target_mode,omitempty"`
	Status          string    `json:"status"`
	ResponseText    string    `json:"response_text"`
	OutputKind      string    `json:"output_kind"`
	OutputHTML      string    `json:"output_html,omitempty"`
	OutputNumeric   *float64  `json:"output_numeric,omitempty"`
	AccountID       *int64    `json:"account_id,omitempty"`
	ModelID         string    `json:"model_id"`
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	GroupID         *int64    `json:"group_id,omitempty"`
	ErrorMessage    string    `json:"error_message"`
	LatencyMs       int64     `json:"latency_ms"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type ScheduledTestDefinition struct {
	ID          int64     `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Prompt      string    `json:"prompt"`
	OutputKind  string    `json:"output_kind"`
	Enabled     bool      `json:"enabled"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ScheduledTestPlanRepository defines the data access interface for test plans.
type ScheduledTestPlanRepository interface {
	Create(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	GetByID(ctx context.Context, id int64) (*ScheduledTestPlan, error)
	ListByAccountID(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error)
	List(ctx context.Context) ([]*ScheduledTestPlan, error)
	ListDue(ctx context.Context, now time.Time) ([]*ScheduledTestPlan, error)
	Update(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error)
	Delete(ctx context.Context, id int64) error
	UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error
}

type ScheduledTestDefinitionRepository interface {
	Create(context.Context, *ScheduledTestDefinition) (*ScheduledTestDefinition, error)
	GetByID(context.Context, int64) (*ScheduledTestDefinition, error)
	GetByKey(context.Context, string) (*ScheduledTestDefinition, error)
	List(context.Context, bool) ([]*ScheduledTestDefinition, error)
	Update(context.Context, *ScheduledTestDefinition) (*ScheduledTestDefinition, error)
	Delete(context.Context, int64) error
}

// ScheduledTestResultRepository defines the data access interface for test results.
type ScheduledTestResultRepository interface {
	GetByID(ctx context.Context, id int64) (*ScheduledTestResult, error)
	Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error)
	Update(ctx context.Context, result *ScheduledTestResult) error
	RestartFailed(ctx context.Context, result *ScheduledTestResult) error
	ListByPlanID(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error)
	ListVisible(ctx context.Context, userID int64, limit int) ([]*ScheduledTestResult, error)
	Delete(ctx context.Context, id int64) error
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}
