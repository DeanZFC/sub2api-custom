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
		INSERT INTO scheduled_test_plans (name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, next_run_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW(), NOW())
		RETURNING id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
	`, plan.Name, plan.SortOrder, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.TargetMode, plan.ModelID, plan.ReasoningEffort, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
		FROM scheduled_test_plans WHERE id = $1
	`, id)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
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
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
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
		SET name = $2, sort_order = $3, account_id = $4, group_id = $5, test_definition_id = $6, test_type = $7, target_mode = $8, model_id = $9, reasoning_effort = $10, cron_expression = $11, enabled = $12, max_results = $13, auto_recover = $14, next_run_at = $15, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at
	`, plan.ID, plan.Name, plan.SortOrder, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.TargetMode, plan.ModelID, plan.ReasoningEffort, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt)
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
	// New plans select a group first and may optionally narrow execution to one
	// account in that group. Keep account-only plans valid for older installs,
	// but reject a mismatched account/group pair before persisting it.
	if plan.AccountID != nil && *plan.AccountID > 0 && plan.GroupID != nil && *plan.GroupID > 0 {
		var linked bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM account_groups WHERE account_id = $1 AND group_id = $2)`, *plan.AccountID, *plan.GroupID).Scan(&linked); err != nil {
			return err
		}
		if !linked {
			return fmt.Errorf("account %d is not assigned to group %d", *plan.AccountID, *plan.GroupID)
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
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at FROM scheduled_test_plans ORDER BY created_at DESC`)
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

func (r *scheduledTestResultRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestResult, error) {
	out := &service.ScheduledTestResult{}
	err := r.db.QueryRowContext(ctx, `
		SELECT r.id, r.plan_id, p.name, COALESCE(d.name, ''), COALESCE(d.sort_order, 0), COALESCE(g.name, ''), COALESCE(p.sort_order, 2147483647), p.target_mode, r.status, r.response_text, r.output_kind, r.output_html, r.output_numeric, r.account_id, r.model_id, r.reasoning_effort, r.group_id, r.error_message, r.latency_ms, r.started_at, r.finished_at, r.created_at
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		LEFT JOIN scheduled_test_definitions d ON d.id = p.test_definition_id
		LEFT JOIN groups g ON g.id = r.group_id
		WHERE r.id = $1
	`, id).Scan(&out.ID, &out.PlanID, &out.PlanName, &out.TestName, &out.TestOrder, &out.GroupName, &out.PlanOrder, &out.TargetMode, &out.Status, &out.ResponseText, &out.OutputKind, &out.OutputHTML, &out.OutputNumeric, &out.AccountID, &out.ModelID, &out.ReasoningEffort, &out.GroupID, &out.ErrorMessage, &out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type scheduledTestDefinitionRepository struct{ db *sql.DB }

func NewScheduledTestDefinitionRepository(db *sql.DB) service.ScheduledTestDefinitionRepository {
	return &scheduledTestDefinitionRepository{db: db}
}
func (r *scheduledTestDefinitionRepository) Create(ctx context.Context, d *service.ScheduledTestDefinition) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `INSERT INTO scheduled_test_definitions (key,name,description,prompt,output_kind,enabled,sort_order) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id,key,name,description,prompt,output_kind,enabled,sort_order,created_at,updated_at`, d.Key, d.Name, d.Description, d.Prompt, d.OutputKind, d.Enabled, d.SortOrder))
}
func (r *scheduledTestDefinitionRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT id,key,name,description,prompt,output_kind,enabled,sort_order,created_at,updated_at FROM scheduled_test_definitions WHERE id=$1`, id))
}
func (r *scheduledTestDefinitionRepository) GetByKey(ctx context.Context, key string) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT id,key,name,description,prompt,output_kind,enabled,sort_order,created_at,updated_at FROM scheduled_test_definitions WHERE key=$1`, key))
}
func (r *scheduledTestDefinitionRepository) List(ctx context.Context, enabledOnly bool) ([]*service.ScheduledTestDefinition, error) {
	q := `SELECT id,key,name,description,prompt,output_kind,enabled,sort_order,created_at,updated_at FROM scheduled_test_definitions`
	if enabledOnly {
		q += ` WHERE enabled=true`
	}
	q += ` ORDER BY sort_order, id`
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
	return r.scan(r.db.QueryRowContext(ctx, `UPDATE scheduled_test_definitions SET key=$2,name=$3,description=$4,prompt=$5,output_kind=$6,enabled=$7,sort_order=$8,updated_at=NOW() WHERE id=$1 RETURNING id,key,name,description,prompt,output_kind,enabled,sort_order,created_at,updated_at`, d.ID, d.Key, d.Name, d.Description, d.Prompt, d.OutputKind, d.Enabled, d.SortOrder))
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
	e := row.Scan(&d.ID, &d.Key, &d.Name, &d.Description, &d.Prompt, &d.OutputKind, &d.Enabled, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt)
	return d, e
}

func (r *scheduledTestResultRepository) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_results (plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, reasoning_effort, group_id, error_message, latency_ms, started_at, finished_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NOW())
		RETURNING id, plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, reasoning_effort, group_id, error_message, latency_ms, started_at, finished_at, created_at
	`, result.PlanID, result.Status, result.ResponseText, result.OutputKind, result.OutputHTML, result.OutputNumeric, result.AccountID, result.ModelID, result.ReasoningEffort, result.GroupID, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt)

	out := &service.ScheduledTestResult{}
	if err := row.Scan(
		&out.ID, &out.PlanID, &out.Status, &out.ResponseText, &out.OutputKind, &out.OutputHTML, &out.OutputNumeric, &out.AccountID, &out.ModelID, &out.ReasoningEffort, &out.GroupID, &out.ErrorMessage,
		&out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *scheduledTestResultRepository) Update(ctx context.Context, result *service.ScheduledTestResult) error {
	if result == nil || result.ID <= 0 {
		return fmt.Errorf("test result id is required")
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_results
		SET status = $2, response_text = $3, output_kind = $4, output_html = $5,
		    output_numeric = $6, account_id = $7, model_id = $8, reasoning_effort = $9, group_id = $10,
		    error_message = $11, latency_ms = $12, started_at = $13, finished_at = $14
		WHERE id = $1
	`, result.ID, result.Status, result.ResponseText, result.OutputKind, result.OutputHTML, result.OutputNumeric,
		result.AccountID, result.ModelID, result.ReasoningEffort, result.GroupID, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RestartFailed atomically reuses an existing failed result row for a manual
// account retry. The compare-and-swap predicate prevents a stale retry request
// from overwriting a result that has already been retried or completed. The
// row identity and ownership columns are intentionally left unchanged.
func (r *scheduledTestResultRepository) RestartFailed(ctx context.Context, result *service.ScheduledTestResult) error {
	if result == nil || result.ID <= 0 {
		return fmt.Errorf("test result id is required")
	}
	if result.PlanID <= 0 {
		return fmt.Errorf("test result plan id is required")
	}
	if result.AccountID == nil || *result.AccountID <= 0 {
		return fmt.Errorf("test result account id is required")
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_results
		SET status = 'running', response_text = '', output_kind = $2, output_html = '',
		    output_numeric = NULL, model_id = $3, reasoning_effort = $4, group_id = $5,
		    error_message = '', latency_ms = 0, started_at = $6, finished_at = $7
		WHERE id = $1 AND plan_id = $8 AND account_id = $9 AND status = 'failed'
	`, result.ID, result.OutputKind, result.ModelID, result.ReasoningEffort, result.GroupID, result.StartedAt, result.FinishedAt, result.PlanID, *result.AccountID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrScheduledTestResultNotFailed
	}
	return nil
}

// Sort and retain results by the latest execution time: a manual retry keeps
// its original ID/created_at while refreshing started_at.
func (r *scheduledTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.ScheduledTestResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.plan_id, p.name, COALESCE(d.name, ''), COALESCE(d.sort_order, 0), COALESCE(g.name, ''), COALESCE(p.sort_order, 2147483647), p.target_mode, r.status, r.response_text, r.output_kind, r.output_html, r.output_numeric, r.account_id, r.model_id, r.reasoning_effort, r.group_id, r.error_message, r.latency_ms, r.started_at, r.finished_at, r.created_at
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		LEFT JOIN scheduled_test_definitions d ON d.id = p.test_definition_id
		LEFT JOIN groups g ON g.id = r.group_id
		WHERE plan_id = $1
		ORDER BY r.started_at DESC, r.id DESC
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
			&r.ID, &r.PlanID, &r.PlanName, &r.TestName, &r.TestOrder, &r.GroupName, &r.PlanOrder, &r.TargetMode, &r.Status, &r.ResponseText, &r.OutputKind, &r.OutputHTML, &r.OutputNumeric, &r.AccountID, &r.ModelID, &r.ReasoningEffort, &r.GroupID, &r.ErrorMessage,
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
	rows, err := r.db.QueryContext(ctx, `WITH visible_results AS (
    SELECT r.id,r.plan_id,p.name AS plan_name,COALESCE(d.name, '') AS test_name,COALESCE(d.sort_order, 2147483647) AS test_order,COALESCE(g.name, '') AS group_name,COALESCE(p.sort_order, 2147483647) AS plan_order,p.target_mode,r.status,r.response_text,r.output_kind,r.output_html,r.output_numeric,
           CASE WHEN COALESCE(p.target_mode, '') IN ('account', 'all_accounts') THEN r.account_id ELSE NULL END AS visible_account_id,
           r.model_id,r.reasoning_effort,r.group_id,r.error_message,r.latency_ms,r.started_at,r.finished_at,r.created_at,
           CASE WHEN COALESCE(p.target_mode, '') IN ('account', 'all_accounts')
                THEN COALESCE(r.account_id::text, '')
                ELSE 'group'
           END AS result_target_key
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
)
SELECT vr.id,vr.plan_id,vr.plan_name,vr.test_name,vr.test_order,vr.group_name,vr.plan_order,vr.target_mode,vr.status,vr.response_text,vr.output_kind,vr.output_html,vr.output_numeric,vr.visible_account_id,vr.model_id,vr.reasoning_effort,vr.group_id,vr.error_message,vr.latency_ms,vr.started_at,vr.finished_at,vr.created_at
FROM visible_results vr
-- Never expose failed executions. Show initial progress only until the target
-- has a successful result, then keep that result visible while tests rerun.
WHERE vr.status IN ('success', 'passed')
   OR (vr.status IN ('pending', 'running') AND NOT EXISTS (
       SELECT 1
       FROM visible_results successful
       WHERE successful.plan_id = vr.plan_id
         AND successful.result_target_key = vr.result_target_key
         AND successful.status IN ('success', 'passed')
   ))
ORDER BY vr.started_at DESC, vr.id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*service.ScheduledTestResult
	for rows.Next() {
		v := &service.ScheduledTestResult{}
		if err := rows.Scan(&v.ID, &v.PlanID, &v.PlanName, &v.TestName, &v.TestOrder, &v.GroupName, &v.PlanOrder, &v.TargetMode, &v.Status, &v.ResponseText, &v.OutputKind, &v.OutputHTML, &v.OutputNumeric, &v.AccountID, &v.ModelID, &v.ReasoningEffort, &v.GroupID, &v.ErrorMessage, &v.LatencyMs, &v.StartedAt, &v.FinishedAt, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *scheduledTestResultRepository) Delete(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_results WHERE id = $1`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *scheduledTestResultRepository) PruneOldResults(ctx context.Context, planID int64, keepCount int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM scheduled_test_results
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY plan_id ORDER BY started_at DESC, id DESC) AS rn
				FROM scheduled_test_results
				WHERE plan_id = $1 AND status <> 'running'
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
		&p.ID, &p.Name, &p.SortOrder, &p.AccountID, &p.GroupID, &p.TestDefinitionID, &p.TestType, &p.TargetMode, &p.ModelID, &p.ReasoningEffort, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover,
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
