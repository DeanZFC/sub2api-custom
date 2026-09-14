package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// --- Plan Repository ---

type scheduledTestPlanRepository struct {
	db *sql.DB
}

func NewScheduledTestPlanRepository(db *sql.DB) service.ScheduledTestPlanRepository {
	return &scheduledTestPlanRepository{db: db}
}

func (r *scheduledTestPlanRepository) Create(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	if err := r.validateTarget(ctx, plan); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_plans (name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, next_run_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		RETURNING id, name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
	`, plan.Name, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans WHERE id = $1
	`, id)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans WHERE account_id = $1
		ORDER BY created_at DESC
	`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlans(rows)
}

func (r *scheduledTestPlanRepository) ListDue(ctx context.Context, now time.Time) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans
		WHERE enabled = true AND next_run_at <= $1
		ORDER BY next_run_at ASC
	`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPlans(rows)
}

func (r *scheduledTestPlanRepository) Update(ctx context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	if err := r.validateTarget(ctx, plan); err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `
		UPDATE scheduled_test_plans
		SET name = $2, account_id = $3, group_id = $4, test_definition_id = $5, test_type = $6, model_id = $7, cron_expression = $8, enabled = $9, max_results = $10, auto_recover = $11, next_run_at = $12, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
	`, plan.ID, plan.Name, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.ModelID, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) validateTarget(ctx context.Context, plan *service.ScheduledTestPlan) error {
	if plan == nil {
		return fmt.Errorf("test plan is required")
	}
	if plan.AccountID != nil && *plan.AccountID > 0 {
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND deleted_at IS NULL)`, *plan.AccountID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("account %d not found", *plan.AccountID)
		}
	}
	if plan.GroupID != nil && *plan.GroupID > 0 {
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM groups WHERE id = $1 AND deleted_at IS NULL)`, *plan.GroupID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("group %d not found", *plan.GroupID)
		}
	}
	return nil
}

func (r *scheduledTestPlanRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id = $1`, id)
	return err
}

func (r *scheduledTestPlanRepository) UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_plans SET last_run_at = $2, next_run_at = $3, updated_at = NOW() WHERE id = $1
	`, id, lastRunAt, nextRunAt)
	return err
}

func (r *scheduledTestPlanRepository) List(ctx context.Context) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, account_id, group_id, test_definition_id, test_type, model_id, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at FROM scheduled_test_plans ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPlans(rows)
}

// --- Result Repository ---

type scheduledTestResultRepository struct {
	db *sql.DB
}

func NewScheduledTestResultRepository(db *sql.DB) service.ScheduledTestResultRepository {
	return &scheduledTestResultRepository{db: db}
}

type scheduledTestDefinitionRepository struct{ db *sql.DB }

func NewScheduledTestDefinitionRepository(db *sql.DB) service.ScheduledTestDefinitionRepository {
	return &scheduledTestDefinitionRepository{db: db}
}
func (r *scheduledTestDefinitionRepository) Create(ctx context.Context, d *service.ScheduledTestDefinition) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `INSERT INTO scheduled_test_definitions (key,name,description,prompt,output_kind,enabled) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id,key,name,description,prompt,output_kind,enabled,created_at,updated_at`, d.Key, d.Name, d.Description, d.Prompt, d.OutputKind, d.Enabled))
}
func (r *scheduledTestDefinitionRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT id,key,name,description,prompt,output_kind,enabled,created_at,updated_at FROM scheduled_test_definitions WHERE id=$1`, id))
}
func (r *scheduledTestDefinitionRepository) GetByKey(ctx context.Context, key string) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT id,key,name,description,prompt,output_kind,enabled,created_at,updated_at FROM scheduled_test_definitions WHERE key=$1`, key))
}
func (r *scheduledTestDefinitionRepository) List(ctx context.Context, enabledOnly bool) ([]*service.ScheduledTestDefinition, error) {
	q := `SELECT id,key,name,description,prompt,output_kind,enabled,created_at,updated_at FROM scheduled_test_definitions`
	if enabledOnly {
		q += ` WHERE enabled=true`
	}
	q += ` ORDER BY id`
	rows, e := r.db.QueryContext(ctx, q)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []*service.ScheduledTestDefinition
	for rows.Next() {
		d, e := r.scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (r *scheduledTestDefinitionRepository) Update(ctx context.Context, d *service.ScheduledTestDefinition) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `UPDATE scheduled_test_definitions SET key=$2,name=$3,description=$4,prompt=$5,output_kind=$6,enabled=$7,updated_at=NOW() WHERE id=$1 RETURNING id,key,name,description,prompt,output_kind,enabled,created_at,updated_at`, d.ID, d.Key, d.Name, d.Description, d.Prompt, d.OutputKind, d.Enabled))
}
func (r *scheduledTestDefinitionRepository) Delete(ctx context.Context, id int64) error {
	result, e := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_definitions
		WHERE id=$1 AND NOT EXISTS (
			SELECT 1 FROM scheduled_test_plans WHERE test_definition_id=$1
		)`, id)
	if e != nil {
		return e
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduled_test_definitions WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("test definition is used by a scheduled test plan; disable it instead")
		}
	}
	return nil
}
func (r *scheduledTestDefinitionRepository) scan(row scannable) (*service.ScheduledTestDefinition, error) {
	d := &service.ScheduledTestDefinition{}
	e := row.Scan(&d.ID, &d.Key, &d.Name, &d.Description, &d.Prompt, &d.OutputKind, &d.Enabled, &d.CreatedAt, &d.UpdatedAt)
	return d, e
}

func (r *scheduledTestResultRepository) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_results (plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, group_id, error_message, latency_ms, started_at, finished_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
		RETURNING id, plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, group_id, error_message, latency_ms, started_at, finished_at, created_at
	`, result.PlanID, result.Status, result.ResponseText, result.OutputKind, result.OutputHTML, result.OutputNumeric, result.AccountID, result.ModelID, result.GroupID, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt)

	out := &service.ScheduledTestResult{}
	if err := row.Scan(
		&out.ID, &out.PlanID, &out.Status, &out.ResponseText, &out.OutputKind, &out.OutputHTML, &out.OutputNumeric, &out.AccountID, &out.ModelID, &out.GroupID, &out.ErrorMessage,
		&out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *scheduledTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.ScheduledTestResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.plan_id, p.name, COALESCE(d.name, ''), COALESCE(g.name, ''), r.status, r.response_text, r.output_kind, r.output_html, r.output_numeric, r.account_id, r.model_id, r.group_id, r.error_message, r.latency_ms, r.started_at, r.finished_at, r.created_at
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		LEFT JOIN scheduled_test_definitions d ON d.id = p.test_definition_id
		LEFT JOIN groups g ON g.id = r.group_id
		WHERE plan_id = $1
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $2
	`, planID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*service.ScheduledTestResult
	for rows.Next() {
		r := &service.ScheduledTestResult{}
		if err := rows.Scan(
			&r.ID, &r.PlanID, &r.PlanName, &r.TestName, &r.GroupName, &r.Status, &r.ResponseText, &r.OutputKind, &r.OutputHTML, &r.OutputNumeric, &r.AccountID, &r.ModelID, &r.GroupID, &r.ErrorMessage,
			&r.LatencyMs, &r.StartedAt, &r.FinishedAt, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (r *scheduledTestResultRepository) ListVisible(ctx context.Context, userID int64, limit int) ([]*service.ScheduledTestResult, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Group plans carry p.group_id directly. Legacy account plans carry the
	// tested account and resolve its account_groups bindings. An ungrouped
	// account plan is private until it is attached to an entitled group.
	rows, err := r.db.QueryContext(ctx, `SELECT r.id,r.plan_id,p.name,COALESCE(d.name, ''),COALESCE(g.name, ''),r.status,r.response_text,r.output_kind,r.output_html,r.output_numeric,r.account_id,r.model_id,r.group_id,r.error_message,r.latency_ms,r.started_at,r.finished_at,r.created_at
FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
LEFT JOIN scheduled_test_definitions d ON d.id=p.test_definition_id
LEFT JOIN groups g ON g.id=r.group_id
WHERE EXISTS (
    SELECT 1
    FROM (
        SELECT uag.group_id
        FROM user_allowed_groups uag
        WHERE uag.user_id = $1
        UNION
        SELECT g.id
        FROM groups g
        WHERE g.status = 'active'
          AND g.deleted_at IS NULL
          AND g.is_exclusive = false
          AND g.is_shared_pool = false
          AND g.subscription_type <> 'subscription'
        UNION
        SELECT us.group_id
        FROM user_subscriptions us
        WHERE us.user_id = $1
          AND us.deleted_at IS NULL
          AND us.status = 'active'
          AND us.starts_at <= NOW()
          AND us.expires_at > NOW()
    ) entitled
    WHERE entitled.group_id = r.group_id
       OR (p.group_id IS NULL AND r.group_id IS NULL AND EXISTS (
            SELECT 1 FROM account_groups ag
            WHERE ag.account_id = r.account_id
              AND ag.group_id = entitled.group_id
       ))
)
ORDER BY r.created_at DESC, r.id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*service.ScheduledTestResult
	for rows.Next() {
		v := &service.ScheduledTestResult{}
		if err := rows.Scan(&v.ID, &v.PlanID, &v.PlanName, &v.TestName, &v.GroupName, &v.Status, &v.ResponseText, &v.OutputKind, &v.OutputHTML, &v.OutputNumeric, &v.AccountID, &v.ModelID, &v.GroupID, &v.ErrorMessage, &v.LatencyMs, &v.StartedAt, &v.FinishedAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *scheduledTestResultRepository) PruneOldResults(ctx context.Context, planID int64, keepCount int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM scheduled_test_results
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY plan_id ORDER BY created_at DESC, id DESC) AS rn
				FROM scheduled_test_results
				WHERE plan_id = $1
			) ranked
			WHERE rn > $2
		)
	`, planID, keepCount)
	return err
}

// --- scan helpers ---

type scannable interface {
	Scan(dest ...any) error
}

func scanPlan(row scannable) (*service.ScheduledTestPlan, error) {
	p := &service.ScheduledTestPlan{}
	if err := row.Scan(
		&p.ID, &p.Name, &p.AccountID, &p.GroupID, &p.TestDefinitionID, &p.TestType, &p.ModelID, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover,
		&p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return p, nil
}

func scanPlans(rows *sql.Rows) ([]*service.ScheduledTestPlan, error) {
	var plans []*service.ScheduledTestPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}
