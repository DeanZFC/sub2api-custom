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
	SortOrder         int                           `json:"sort_order"`
	AccountID         *int64                        `json:"account_id,omitempty"`
	GroupID           *int64                        `json:"group_id,omitempty"`
	TestDefinitionID  *int64                        `json:"test_definition_id,omitempty"`
	TestDefinitionIDs []int64                       `json:"test_definition_ids"`
	TestType          string                        `json:"test_type"`
	TargetMode        string                        `json:"target_mode"`
	ModelID           string                        `json:"model_id"`
	ReasoningEffort   string                        `json:"reasoning_effort,omitempty"`
	CronExpression    string                        `json:"cron_expression"`
	Enabled           bool                          `json:"enabled"`
	MaxResults        int                           `json:"max_results"`
	AutoRecover       bool                          `json:"auto_recover"`
	Protection        ScheduledTestProtectionConfig `json:"protection"`
	LastRunAt         *time.Time                    `json:"last_run_at"`
	NextRunAt         *time.Time                    `json:"next_run_at"`
	CreatedAt         time.Time                     `json:"created_at"`
	UpdatedAt         time.Time                     `json:"updated_at"`
}

// ScheduledTestResult represents a single test execution result.
type ScheduledTestResult struct {
	RunID            string `json:"-"`
	ID               int64  `json:"id"`
	PlanID           int64  `json:"plan_id"`
	TestDefinitionID *int64 `json:"test_definition_id,omitempty"`
	PlanName         string `json:"plan_name"`
	TestName         string `json:"test_name"`
	TestOrder        int    `json:"test_order"`
	GroupName        string `json:"group_name"`
	// PlanOrder is the administrator-configured order of the test rule/plan
	// that produced this result. The user-facing page sorts by this value and
	// derives its group list from that ordered result stream.
	PlanOrder            int                              `json:"plan_order"`
	TargetMode           string                           `json:"target_mode,omitempty"`
	Status               string                           `json:"status"`
	ResponseText         string                           `json:"response_text"`
	OutputKind           string                           `json:"output_kind"`
	OutputHTML           string                           `json:"output_html,omitempty"`
	OutputNumeric        *float64                         `json:"output_numeric,omitempty"`
	OutputStatistics     *ScheduledTestStatistics         `json:"output_statistics,omitempty"`
	OutputModelCheck     *ScheduledTestModelCheck         `json:"output_model_check,omitempty"`
	ProtectionDecision   *ScheduledTestProtectionDecision `json:"protection_decision,omitempty"`
	UpstreamModel        string                           `json:"-"`
	ReturnedModels       []string                         `json:"-"`
	ModelEvidenceInvalid bool                             `json:"-"`
	AccountID            *int64                           `json:"account_id,omitempty"`
	AccountName          string                           `json:"account_name,omitempty"` // populated by admin queries only
	ModelID              string                           `json:"model_id"`
	ReasoningEffort      string                           `json:"reasoning_effort,omitempty"`
	GroupID              *int64                           `json:"group_id,omitempty"`
	ErrorMessage         string                           `json:"error_message"`
	LatencyMs            int64                            `json:"latency_ms"`
	StartedAt            time.Time                        `json:"started_at"`
	FinishedAt           time.Time                        `json:"finished_at"`
	CreatedAt            time.Time                        `json:"created_at"`
}

// ScheduledTestProtectionDecision is populated for administrator result views.
// It records the verdict and actual account changes in the same transaction;
// public result endpoints deliberately do not expose it.
type ScheduledTestProtectionDecision struct {
	Status          string  `json:"status,omitempty"`
	Verdict         string  `json:"verdict"`
	Reason          string  `json:"reason,omitempty"`
	Scheduling      string  `json:"scheduling,omitempty"`
	GroupIDs        []int64 `json:"group_ids,omitempty"`
	AddedGroupIDs   []int64 `json:"added_group_ids,omitempty"`
	RemovedGroupIDs []int64 `json:"removed_group_ids,omitempty"`
}

// BeginRun atomically publishes the complete account/type snapshot before any
// upstream work begins, including records waiting for a worker.
type ScheduledTestRunRepository interface {
	BeginRun(context.Context, int64, string, []*ScheduledTestResult) ([]*ScheduledTestResult, error)
}

type ScheduledTestDecisionRepository interface {
	RecordDecision(context.Context, *ScheduledTestResult, ScheduledTestProtectionDecision) error
}

// ScheduledTestStatistics is a local snapshot, never an upstream model output.
type ScheduledTestStatistics struct {
	WindowStart       time.Time                    `json:"window_start"`
	WindowEnd         time.Time                    `json:"window_end"`
	TotalRequests     int64                        `json:"total_requests"`
	SuccessRequests   int64                        `json:"success_requests"`
	FailedRequests    int64                        `json:"failed_requests"`
	SuccessRate       *float64                     `json:"success_rate"`
	CacheRate         *float64                     `json:"cache_rate"`
	AvgFirstTokenMs   *float64                     `json:"avg_first_token_ms"`
	FirstTokenSamples int64                        `json:"first_token_samples"`
	CacheReadTokens   int64                        `json:"cache_read_tokens"`
	CacheInputTokens  int64                        `json:"cache_input_tokens"`
	RecentRequests    []ScheduledTestRecentRequest `json:"recent_requests"`
}

// ScheduledTestRecentRequest deliberately contains no request or account details.
type ScheduledTestRecentRequest struct {
	Success   bool      `json:"success"`
	CreatedAt time.Time `json:"created_at"`
}

type ScheduledTestStatisticsFilter struct {
	GroupID     *int64
	AccountID   *int64
	Model       string
	WindowStart time.Time
	WindowEnd   time.Time
}

// Optional capability implemented by the SQL result repository. Existing test
// adapters and upstream execution wiring do not need a new dependency.
type ScheduledTestStatisticsRepository interface {
	CollectStatistics(context.Context, ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error)
	ListStatisticsAccountIDs(context.Context, int64) ([]int64, error)
}

type ScheduledTestResultHistory struct {
	Items        []*ScheduledTestResult `json:"items"`
	NextBeforeID *int64                 `json:"next_before_id,omitempty"`
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
	ListVisibleHistory(ctx context.Context, userID, resultID, beforeID int64, limit int) ([]*ScheduledTestResult, error)
	Delete(ctx context.Context, id int64) error
	PruneOldResults(ctx context.Context, planID int64, keepCount int) error
}

// ScheduledTestTargetAccountRepository returns all configured target accounts
// for an execution. Eligibility is checked again immediately before upstream
// work, allowing the runner to persist a visible skipped result for an account
// that was manually stopped or otherwise unavailable.
type ScheduledTestTargetAccountRepository interface {
	ListPlanTargetAccountIDs(context.Context, *ScheduledTestPlan, *int64) ([]int64, error)
}
