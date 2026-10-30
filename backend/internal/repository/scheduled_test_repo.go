package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
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
	protection, err := json.Marshal(plan.Protection)
	if err != nil {
		return nil, err
	}
	schedule, err := json.Marshal(plan.Schedule)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockScheduledTestPlanWrites(ctx, tx); err != nil {
		return nil, err
	}
	if err := validateScheduledTestPlanConflicts(ctx, tx, plan); err != nil {
		return nil, err
	}
	row := tx.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_plans (name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, next_run_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, COALESCE($15::bigint[], '{}'), $16::jsonb, COALESCE($17::bigint[], '{}'), $18, $19::jsonb, COALESCE($20::bigint[], '{}'), COALESCE($21::text[], '{}'), NOW(), NOW())
		RETURNING id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids
	`, plan.Name, plan.SortOrder, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.TargetMode, plan.ModelID, plan.ReasoningEffort, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, pq.Array(plan.TestDefinitionIDs), protection, pq.Array(plan.GroupIDs), plan.MigrationNote, schedule, pq.Array(plan.ExcludedAccountIDs), pq.Array(plan.SelectedModelIDs()))
	created, err := scanPlan(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func (r *scheduledTestPlanRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestPlan, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids
		FROM scheduled_test_plans WHERE id = $1
	`, id)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids
		FROM scheduled_test_plans p WHERE NOT ($1 = ANY(p.excluded_account_ids)) AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=$1 AND ag.group_id=ANY(p.group_ids))
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
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids
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
	if plan != nil {
		plan.MigrationNote = ""
	}
	if err := r.validateTarget(ctx, plan); err != nil {
		return nil, err
	}
	protection, err := json.Marshal(plan.Protection)
	if err != nil {
		return nil, err
	}
	schedule, err := json.Marshal(plan.Schedule)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockScheduledTestPlanWrites(ctx, tx); err != nil {
		return nil, err
	}
	previous, err := scanPlan(tx.QueryRowContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE`, plan.ID))
	if err != nil {
		return nil, err
	}
	if err := validateScheduledTestPlanConflicts(ctx, tx, plan); err != nil {
		return nil, err
	}
	if err := lockScheduledTestLifecycleAccounts(ctx, tx, plan.ID); err != nil {
		return nil, err
	}
	if !scheduledTestSameExecutionPolicy(previous, plan) {
		if err := resetPlanProtectionTx(ctx, tx, plan.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET latest_run_id='' WHERE id=$1`, plan.ID); err != nil {
			return nil, err
		}
	}

	row := tx.QueryRowContext(ctx, `
		UPDATE scheduled_test_plans
		SET name = $2, sort_order = $3, account_id = $4, group_id = $5, test_definition_id = $6, test_type = $7, target_mode = $8, model_id = $9, reasoning_effort = $10, cron_expression = $11, enabled = $12, max_results = $13, auto_recover = $14, next_run_at = $15, test_definition_ids = COALESCE($16::bigint[], '{}'), protection = $17::jsonb, group_ids = COALESCE($18::bigint[], '{}'), migration_note = $19, schedule_config = $20::jsonb, excluded_account_ids = COALESCE($21::bigint[], '{}'), model_ids = COALESCE($22::text[], '{}'), updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids
	`, plan.ID, plan.Name, plan.SortOrder, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.TargetMode, plan.ModelID, plan.ReasoningEffort, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, pq.Array(plan.TestDefinitionIDs), protection, pq.Array(plan.GroupIDs), plan.MigrationNote, schedule, pq.Array(plan.ExcludedAccountIDs), pq.Array(plan.SelectedModelIDs()))
	updated, err := scanPlan(row)
	if err != nil {
		return nil, err
	}
	previousGroups, currentGroups := slices.Clone(previous.GroupIDs), slices.Clone(updated.GroupIDs)
	slices.Sort(previousGroups)
	slices.Sort(currentGroups)
	if err := reconcileScheduledTestPassRates(ctx, tx, updated, !slices.Equal(previousGroups, currentGroups)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}

func (r *scheduledTestPlanRepository) validateTarget(ctx context.Context, plan *service.ScheduledTestPlan) error {
	if plan == nil {
		return fmt.Errorf("test plan is required")
	}
	if plan.TargetMode != "all_accounts" || plan.AccountID != nil {
		return fmt.Errorf("test policies require all_accounts across selected groups")
	}
	if len(plan.GroupIDs) == 0 {
		return fmt.Errorf("select at least one detection group")
	}
	if plan.GroupID == nil || *plan.GroupID != plan.GroupIDs[0] {
		return fmt.Errorf("policy group anchor must match the first selected group")
	}
	var count, platforms int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT platform) FROM groups WHERE id=ANY($1) AND deleted_at IS NULL AND status='active' AND platform<>'composite'`, pq.Array(plan.GroupIDs)).Scan(&count, &platforms); err != nil {
		return err
	}
	if count != len(plan.GroupIDs) || platforms != 1 {
		return fmt.Errorf("selected detection groups must be distinct, active and use one concrete platform")
	}
	return r.validateProtectionGroups(ctx, plan)
}

func (r *scheduledTestPlanRepository) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockScheduledTestPlanWrites(ctx, tx); err != nil {
		return err
	}
	if err := lockScheduledTestLifecycleAccounts(ctx, tx, id); err != nil {
		return err
	}
	if err := clearPlanProtectionTx(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scheduledTestPlanRepository) UpdateAfterRun(ctx context.Context, plan *service.ScheduledTestPlan, lastRunAt time.Time, nextRunAt time.Time) error {
	schedule, err := json.Marshal(plan.Schedule)
	if err != nil {
		return err
	}
	// A long-running round must not replace a newer schedule's next_run_at.
	// Legacy SQL NULL and explicit JSON null both mean cron mode.
	_, err = r.db.ExecContext(ctx, `
		UPDATE scheduled_test_plans SET last_run_at = $2,
		next_run_at = CASE WHEN cron_expression = $4 AND COALESCE(schedule_config, 'null'::jsonb) = $5::jsonb THEN $3 ELSE next_run_at END,
		updated_at = NOW() WHERE id = $1
	`, plan.ID, lastRunAt, nextRunAt, plan.CronExpression, schedule)
	return err
}

func (r *scheduledTestPlanRepository) UpdateNextRun(ctx context.Context, plan *service.ScheduledTestPlan, nextRunAt time.Time) error {
	schedule, err := json.Marshal(plan.Schedule)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE scheduled_test_plans SET next_run_at=$2, updated_at=NOW()
		WHERE id=$1 AND cron_expression=$3 AND COALESCE(schedule_config,'null'::jsonb)=$4::jsonb
		AND next_run_at IS NOT DISTINCT FROM $5`, plan.ID, nextRunAt, plan.CronExpression, string(schedule), plan.NextRunAt)
	return err
}

func (r *scheduledTestPlanRepository) List(ctx context.Context) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note, schedule_config, excluded_account_ids, model_ids FROM scheduled_test_plans ORDER BY created_at DESC`)
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
		SELECT r.id, r.plan_id, p.name, COALESCE(d.name, ''), COALESCE(d.sort_order, 0), COALESCE(g.name, ''), COALESCE(p.sort_order, 2147483647), r.target_mode, r.status, r.response_text, r.output_kind, r.output_html, r.output_numeric, r.account_id, r.model_id, r.reasoning_effort, r.group_id, r.error_message, r.latency_ms, r.started_at, r.finished_at, r.created_at, r.test_definition_id, COALESCE(a.name, ''),r.run_id
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		LEFT JOIN scheduled_test_definitions d ON d.id = r.test_definition_id
		LEFT JOIN groups g ON g.id = r.group_id
		LEFT JOIN accounts a ON a.id = r.account_id
		WHERE r.id = $1
	`, id).Scan(&out.ID, &out.PlanID, &out.PlanName, &out.TestName, &out.TestOrder, &out.GroupName, &out.PlanOrder, &out.TargetMode, &out.Status, &out.ResponseText, &out.OutputKind, &out.OutputHTML, &out.OutputNumeric, &out.AccountID, &out.ModelID, &out.ReasoningEffort, &out.GroupID, &out.ErrorMessage, &out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt, &out.TestDefinitionID, &out.AccountName, &out.RunID)
	if err != nil {
		return nil, err
	}
	if err := r.hydrateProtectionDecisions(ctx, []*service.ScheduledTestResult{out}); err != nil {
		return nil, err
	}
	return out, nil
}

type scheduledTestDefinitionRepository struct{ db *sql.DB }

func NewScheduledTestDefinitionRepository(db *sql.DB) service.ScheduledTestDefinitionRepository {
	return &scheduledTestDefinitionRepository{db: db}
}
func (r *scheduledTestDefinitionRepository) Create(ctx context.Context, d *service.ScheduledTestDefinition) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `INSERT INTO scheduled_test_definitions (key,name,description,prompt,output_kind,enabled,sort_order,max_output_tokens) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,key,name,description,prompt,output_kind,enabled,sort_order,max_output_tokens,created_at,updated_at`, d.Key, d.Name, d.Description, d.Prompt, d.OutputKind, d.Enabled, d.SortOrder, d.MaxOutputTokens))
}
func (r *scheduledTestDefinitionRepository) GetByID(ctx context.Context, id int64) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT id,key,name,description,prompt,output_kind,enabled,sort_order,max_output_tokens,created_at,updated_at FROM scheduled_test_definitions WHERE id=$1`, id))
}
func (r *scheduledTestDefinitionRepository) GetByKey(ctx context.Context, key string) (*service.ScheduledTestDefinition, error) {
	return r.scan(r.db.QueryRowContext(ctx, `SELECT id,key,name,description,prompt,output_kind,enabled,sort_order,max_output_tokens,created_at,updated_at FROM scheduled_test_definitions WHERE key=$1`, key))
}
func (r *scheduledTestDefinitionRepository) List(ctx context.Context, enabledOnly bool) ([]*service.ScheduledTestDefinition, error) {
	q := `SELECT id,key,name,description,prompt,output_kind,enabled,sort_order,max_output_tokens,created_at,updated_at FROM scheduled_test_definitions`
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockScheduledTestPlanWrites(ctx, tx); err != nil {
		return nil, err
	}
	plans, err := lockScheduledTestPassRateDefinitionPlans(ctx, tx, d.ID)
	if err != nil {
		return nil, err
	}
	updated, err := r.scan(tx.QueryRowContext(ctx, `UPDATE scheduled_test_definitions SET key=$2,name=$3,description=$4,prompt=$5,output_kind=$6,enabled=$7,sort_order=$8,max_output_tokens=$9,updated_at=NOW() WHERE id=$1 RETURNING id,key,name,description,prompt,output_kind,enabled,sort_order,max_output_tokens,created_at,updated_at`, d.ID, d.Key, d.Name, d.Description, d.Prompt, d.OutputKind, d.Enabled, d.SortOrder, d.MaxOutputTokens))
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		if err := reconcileScheduledTestPassRates(ctx, tx, plan, false); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return updated, nil
}
func (r *scheduledTestDefinitionRepository) Delete(ctx context.Context, id int64) error {
	result, e := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_definitions
		WHERE id=$1 AND NOT EXISTS (
			SELECT 1 FROM scheduled_test_plans WHERE test_definition_id=$1 OR $1=ANY(test_definition_ids)
		) AND NOT EXISTS (SELECT 1 FROM scheduled_test_results WHERE test_definition_id=$1)`, id)
	if e != nil {
		return e
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr == nil && affected == 0 {
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduled_test_definitions WHERE id=$1)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("test definition is used by a scheduled test plan or result; disable it instead")
		}
	}
	return nil
}
func (r *scheduledTestDefinitionRepository) scan(row scannable) (*service.ScheduledTestDefinition, error) {
	d := &service.ScheduledTestDefinition{}
	e := row.Scan(&d.ID, &d.Key, &d.Name, &d.Description, &d.Prompt, &d.OutputKind, &d.Enabled, &d.SortOrder, &d.MaxOutputTokens, &d.CreatedAt, &d.UpdatedAt)
	return d, e
}

func (r *scheduledTestResultRepository) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	return createScheduledTestResult(ctx, r.db, result)
}

func createScheduledTestResult(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	execution, err := json.Marshal(result.ExecutionInfo)
	if err != nil {
		return nil, err
	}
	row := db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_results (plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, reasoning_effort, group_id, error_message, latency_ms, started_at, finished_at, test_definition_id, target_mode, run_id, execution_info, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, NOW())
		RETURNING id, plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, reasoning_effort, group_id, error_message, latency_ms, started_at, finished_at, created_at, test_definition_id, target_mode
	`, result.PlanID, result.Status, result.ResponseText, result.OutputKind, result.OutputHTML, result.OutputNumeric, result.AccountID, result.ModelID, result.ReasoningEffort, result.GroupID, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt, result.TestDefinitionID, result.TargetMode, result.RunID, execution)

	out := &service.ScheduledTestResult{RunID: result.RunID, ExecutionInfo: result.ExecutionInfo}
	if err := row.Scan(
		&out.ID, &out.PlanID, &out.Status, &out.ResponseText, &out.OutputKind, &out.OutputHTML, &out.OutputNumeric, &out.AccountID, &out.ModelID, &out.ReasoningEffort, &out.GroupID, &out.ErrorMessage,
		&out.LatencyMs, &out.StartedAt, &out.FinishedAt, &out.CreatedAt, &out.TestDefinitionID, &out.TargetMode,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *scheduledTestResultRepository) Update(ctx context.Context, result *service.ScheduledTestResult) error {
	if result == nil || result.ID <= 0 {
		return fmt.Errorf("test result id is required")
	}
	execution, err := json.Marshal(result.ExecutionInfo)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_results
		SET status = $2, response_text = $3, output_kind = $4, output_html = $5,
		    output_numeric = $6, account_id = $7, model_id = $8, reasoning_effort = $9, group_id = $10,
		    error_message = $11, latency_ms = $12, started_at = $13, finished_at = $14, execution_info = $15
		WHERE id = $1
	`, result.ID, result.Status, result.ResponseText, result.OutputKind, result.OutputHTML, result.OutputNumeric,
		result.AccountID, result.ModelID, result.ReasoningEffort, result.GroupID, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt, execution)
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
		    error_message = '', latency_ms = 0, started_at = $6, finished_at = $7, protection_decision = NULL
		WHERE id = $1 AND plan_id = $8 AND account_id = $9 AND status = 'failed'
		  AND test_definition_id IS NOT DISTINCT FROM $10 AND target_mode = $11
	`, result.ID, result.OutputKind, result.ModelID, result.ReasoningEffort, result.GroupID, result.StartedAt, result.FinishedAt, result.PlanID, *result.AccountID, result.TestDefinitionID, result.TargetMode)
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
// its original ID/created_at while refreshing started_at. Apply the history
// limit to each account/type series, not the entire plan: otherwise a later
// test type can hide every result of earlier types. Latest view is restricted
// to the last full execution snapshot when one has been recorded, excluding
// deleted accounts. History retains their results and display names for audit.
func (r *scheduledTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.ScheduledTestResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH ranked_results AS (
			SELECT id, ROW_NUMBER() OVER (
				-- Keep each model and reasoning effort's history independent.
				-- The latest-run filter below still excludes older snapshots.
				PARTITION BY test_definition_id, account_id, model_id, reasoning_effort
				ORDER BY started_at DESC, id DESC
			) AS history_rank
			FROM scheduled_test_results
			WHERE plan_id = $1 AND ($2 > 1 OR run_id = (
				SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1
			) OR (SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1) = '')
		)
		SELECT r.id, r.plan_id, p.name, COALESCE(d.name, ''), COALESCE(d.sort_order, 0), COALESCE(g.name, ''), COALESCE(p.sort_order, 2147483647), r.target_mode, r.status, r.response_text, r.output_kind, r.output_html, r.output_numeric, r.account_id, r.model_id, r.reasoning_effort, r.group_id, r.error_message, r.latency_ms, r.started_at, r.finished_at, r.created_at, r.test_definition_id, COALESCE(a.name, '')
		FROM scheduled_test_results r
		JOIN ranked_results ranked ON ranked.id = r.id
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		LEFT JOIN scheduled_test_definitions d ON d.id = r.test_definition_id
		LEFT JOIN groups g ON g.id = r.group_id
		LEFT JOIN accounts a ON a.id = r.account_id
		WHERE ranked.history_rank <= $2
		  AND ($2 > 1 OR r.account_id IS NULL OR (a.id IS NOT NULL AND a.deleted_at IS NULL))
		ORDER BY r.started_at DESC, r.id DESC
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
			&r.LatencyMs, &r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.TestDefinitionID, &r.AccountName,
		); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := r.hydrateProtectionDecisions(ctx, results); err != nil {
		return nil, err
	}
	return results, nil
}

func (r *scheduledTestResultRepository) hydrateProtectionDecisions(ctx context.Context, results []*service.ScheduledTestResult) error {
	ids := make([]int64, 0, len(results))
	for _, result := range results {
		if result != nil && result.ID > 0 {
			ids = append(ids, result.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT r.id,r.protection_decision,r.execution_info,
 CASE WHEN r.account_id IS NOT NULL THEN COALESCE(a.status='active' AND a.schedulable AND COALESCE(a.extra->>'active_test_all_models_paused','false')<>'true',FALSE) END,
 CASE WHEN r.account_id IS NOT NULL THEN COALESCE(a.status='active' AND a.schedulable
 AND COALESCE(a.extra->>'active_test_all_models_paused','false')<>'true'
 AND NOT ((COALESCE(a.extra->'active_test_model_holds','{}'::jsonb) || COALESCE(a.extra->'active_pass_rate_holds','{}'::jsonb)) ?| ARRAY[r.model_id,'']),FALSE) END,
 `+scheduledTestModelDisplayOrderSQL+`
FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id LEFT JOIN accounts a ON a.id=r.account_id
WHERE r.id = ANY($1)`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[int64]*service.ScheduledTestResult, len(results))
	for _, result := range results {
		if result != nil {
			byID[result.ID] = result
		}
	}
	for rows.Next() {
		var id int64
		var raw, execution []byte
		var accountSchedulable, modelSchedulable *bool
		var modelOrder int
		if err := rows.Scan(&id, &raw, &execution, &accountSchedulable, &modelSchedulable, &modelOrder); err != nil {
			return err
		}
		result := byID[id]
		if result == nil {
			continue
		}
		result.AccountSchedulable, result.ModelSchedulable = accountSchedulable, modelSchedulable
		result.ModelOrder = modelOrder
		if len(execution) > 0 && string(execution) != "null" {
			if err := json.Unmarshal(execution, &result.ExecutionInfo); err != nil {
				return err
			}
		}
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		decision := &service.ScheduledTestProtectionDecision{}
		if err := json.Unmarshal(raw, decision); err != nil {
			return err
		}
		result.ProtectionDecision = decision
		if decision.Status == "error" {
			result.QualityVerdict = "pending"
		} else if decision.Verdict == "pass" || decision.Verdict == "fail" || decision.Verdict == "pending" {
			result.QualityVerdict = decision.Verdict
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	r.hydrateDisplayEvidence(ctx, results)
	return nil
}

// Display position belongs to the current group, independently of the rule
// that produced a historical result. Retain disabled rules' display settings;
// disabling execution does not remove their groups' configured positions.
const scheduledTestGroupDisplayOrdersSQL = `SELECT group_id,
 (DENSE_RANK() OVER (ORDER BY sort_order, position)-1)::integer AS sort_order
 FROM (SELECT DISTINCT ON (selected.group_id) selected.group_id,p.sort_order,selected.position
       FROM scheduled_test_plans p CROSS JOIN LATERAL unnest(p.group_ids) WITH ORDINALITY selected(group_id,position)
       ORDER BY selected.group_id,p.sort_order,selected.position,p.id) configured_groups`

// Re-read the current selection so editing its order updates existing results.
// Preserve the single-model fallback used by ScheduledTestPlan.SelectedModelIDs.
const scheduledTestModelDisplayOrderSQL = `COALESCE(array_position(
 CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END,
 r.model_id)-1,2147483647)`

// A completion error records the preliminary evaluator result for diagnostics.
// It has not committed the final vote, administrator and duration decision.
const scheduledTestPublicQualityVerdictSQL = `CASE
 WHEN r.protection_decision->>'status'='error' THEN 'pending'
 WHEN r.protection_decision->>'verdict' IN ('pass','fail','pending')
 THEN r.protection_decision->>'verdict' ELSE '' END`

const scheduledTestExecutionSkippedSQL = `(COALESCE(r.protection_decision->>'status'='skipped',false)
 OR COALESCE(r.execution_info->>'failure_kind'='quality_deferred',false)
 OR (r.status='failed' AND r.error_message LIKE '检测跳过：%'))`

// Both public queries authorize the projected current group. The account is
// projected only as an ID and hidden for group tests.
// Read its current status and scheduling switch, even for group-mode model
// tests whose executing account ID is hidden. Account-free group statistics
// remain visible; orphaned or manually disabled account results do not.
// Quality-paused accounts remain visible while automatic recovery is pending.
const scheduledTestVisibleProjectionCTE = `WITH projected_results AS NOT MATERIALIZED (
    SELECT r.id,r.plan_id,p.name AS plan_name,COALESCE(d.name, '') AS test_name,COALESCE(d.sort_order, 2147483647) AS test_order,
           COALESCE(projected_group.name, source_group.name, '') AS group_name,COALESCE(p.sort_order, 2147483647) AS plan_order,r.target_mode,r.status,
           r.response_text,r.output_kind,r.output_html,r.output_numeric,
           COALESCE(d.key, '') AS test_key,
           ` + scheduledTestPublicQualityVerdictSQL + ` AS quality_verdict,
           ` + scheduledTestExecutionSkippedSQL + ` AS execution_skipped,
           CASE WHEN r.target_mode IN ('account', 'all_accounts') THEN r.account_id ELSE NULL END AS visible_account_id,
           r.model_id,r.reasoning_effort,` + scheduledTestModelDisplayOrderSQL + ` AS model_order,
           CASE WHEN r.account_id IS NULL THEN r.group_id ELSE projected_group.group_id END AS group_id,
           r.error_message,r.latency_ms,r.started_at,r.finished_at,r.created_at,r.test_definition_id,
           COALESCE(group_display_order.sort_order, 2147483647) AS group_order,
           CASE WHEN r.target_mode IN ('account', 'all_accounts')
                THEN COALESCE(r.account_id::text, '')
                ELSE 'group'
           END AS result_target_key,
           r.account_id,
           CASE WHEN r.account_id IS NOT NULL THEN COALESCE(visible_account.status = 'active' AND visible_account.schedulable AND COALESCE(visible_account.extra->>'active_test_all_models_paused','false')<>'true', FALSE) END AS account_schedulable,
           CASE WHEN r.account_id IS NOT NULL THEN COALESCE(visible_account.status = 'active' AND visible_account.schedulable
                AND COALESCE(visible_account.extra->>'active_test_all_models_paused','false')<>'true'
                AND NOT ((COALESCE(visible_account.extra->'active_test_model_holds','{}'::jsonb) || COALESCE(visible_account.extra->'active_pass_rate_holds','{}'::jsonb)) ?| ARRAY[r.model_id,'']), FALSE) END AS model_schedulable,
           visible_account.id IS NOT NULL AND visible_account.deleted_at IS NULL
               AND visible_account.schedulable AND visible_account.status IN ('active','quality_paused') AS account_enabled
    FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
    LEFT JOIN accounts visible_account ON visible_account.id=r.account_id
    LEFT JOIN scheduled_test_definitions d ON d.id=r.test_definition_id
    LEFT JOIN groups source_group ON source_group.id=r.group_id
    -- Account results follow the account's current group memberships. Prefer
    -- the original source group when it is still assigned so an unchanged
    -- result keeps its existing grouping; otherwise choose a stable current
    -- membership. A group aggregate (account_id NULL) remains on its source
    -- group and is never projected through account memberships.
    LEFT JOIN LATERAL (
        SELECT ag.group_id, cg.name
        FROM account_groups ag
        JOIN groups cg ON cg.id=ag.group_id
        WHERE r.account_id IS NOT NULL
          AND ag.account_id = r.account_id
          AND cg.deleted_at IS NULL
          AND cg.status = 'active'
        ORDER BY CASE WHEN ag.group_id = r.group_id THEN 0 ELSE 1 END, ag.group_id
`

const scheduledTestVisibleAccessCTE = `    ) projected_group ON TRUE
    LEFT JOIN (` + scheduledTestGroupDisplayOrdersSQL + `) group_display_order
      ON group_display_order.group_id = CASE WHEN r.account_id IS NULL THEN r.group_id ELSE projected_group.group_id END
), visible_results AS NOT MATERIALIZED (
    SELECT r.id,r.plan_id,r.plan_name,r.test_name,r.test_order,r.group_name,r.plan_order,r.target_mode,r.status,
           r.response_text,r.output_kind,r.output_html,r.output_numeric,r.visible_account_id,r.model_id,r.reasoning_effort,
           r.group_id,r.error_message,r.latency_ms,r.started_at,r.finished_at,r.created_at,r.test_definition_id,r.result_target_key,r.group_order,r.account_schedulable,r.test_key,r.quality_verdict,r.model_schedulable,r.model_order,r.execution_skipped
    FROM projected_results r
    WHERE (
        (r.account_id IS NULL AND r.target_mode = 'group')
        OR (
            r.account_id IS NOT NULL
            AND r.group_id IS NOT NULL
            AND r.account_enabled
        )
    ) AND EXISTS (
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
    )
)`

const scheduledTestVisibleResultsCTE = scheduledTestVisibleProjectionCTE + "        LIMIT 1\n" + scheduledTestVisibleAccessCTE

const scheduledTestVisibleResultColumns = `vr.id,vr.plan_id,vr.plan_name,vr.test_name,vr.test_order,vr.group_name,vr.plan_order,vr.target_mode,vr.status,vr.response_text,vr.output_kind,vr.output_html,vr.output_numeric,vr.visible_account_id,vr.model_id,vr.reasoning_effort,vr.group_id,vr.error_message,vr.latency_ms,vr.started_at,vr.finished_at,vr.created_at,vr.test_definition_id,vr.group_order,vr.account_schedulable,vr.test_key,vr.quality_verdict,vr.model_schedulable,vr.model_order,vr.execution_skipped`

// GetAdminResultAccountNames loads current names in one batch, independently of
// review eligibility. Only administrator handlers may request this enrichment.
func (r *scheduledTestResultRepository) GetAdminResultAccountNames(ctx context.Context, accountIDs []int64) (map[int64]string, error) {
	names := make(map[int64]string, len(accountIDs))
	if len(accountIDs) == 0 {
		return names, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, COALESCE(name, '') FROM accounts WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		names[id] = name
	}
	return names, rows.Err()
}

// The user list shows completed results, including failures, from each plan's
// current execution. Rank first so a failed or running check cannot fall back
// to an older success. History uses its own query.
func (r *scheduledTestResultRepository) ListVisible(ctx context.Context, userID int64, _ int) ([]*service.ScheduledTestResult, error) {
	return r.listVisible(ctx, userID, scheduledTestVisibleResultsCTE, 1, false)
}

// Recent artwork supplements the current execution; it never supplies a
// replacement when the current check is still running or no longer visible.
func (r *scheduledTestResultRepository) ListVisibleWithRecentHTML(ctx context.Context, userID int64, total int) ([]*service.ScheduledTestResult, error) {
	if total < 1 || total > 5 {
		total = 3
	}
	return r.listVisible(ctx, userID, scheduledTestVisibleResultsCTE, total, false)
}

// Recent results include numeric checks and artwork from the current series.
// They supplement the current execution without replacing its result.
func (r *scheduledTestResultRepository) ListVisibleWithRecentResults(ctx context.Context, userID int64, total int) ([]*service.ScheduledTestResult, error) {
	if total < 1 || total > 5 {
		total = 4
	}
	return r.listVisible(ctx, userID, scheduledTestVisibleResultsCTE, total, true)
}

// Monitor cards need every current group membership, with the same per-group
// entitlement checks. The ordinary result list keeps its single-group layout.
func (r *scheduledTestResultRepository) ListVisibleByGroup(ctx context.Context, userID int64) ([]*service.ScheduledTestResult, error) {
	return r.listVisible(ctx, userID, scheduledTestVisibleProjectionCTE+scheduledTestVisibleAccessCTE, 1, false)
}

func (r *scheduledTestResultRepository) listVisible(ctx context.Context, userID int64, visibilityCTE string, recentTotal int, includeNumeric bool) ([]*service.ScheduledTestResult, error) {
	query := visibilityCTE + `, ranked_results AS (
    SELECT vr.*, row_number() OVER (
        PARTITION BY vr.group_id, vr.result_target_key, vr.test_definition_id, vr.model_id, vr.reasoning_effort
        ORDER BY vr.started_at DESC, vr.id DESC
    ) AS history_rank
    FROM visible_results vr
    JOIN scheduled_test_results execution ON execution.id = vr.id
    JOIN scheduled_test_plans plan ON plan.id = vr.plan_id
    WHERE execution.run_id = plan.latest_run_id
), current_results AS (
    SELECT * FROM ranked_results
    WHERE history_rank = 1 AND status IN ('success', 'passed', 'failed', 'error')
)`
	args := []any{userID}
	resultSource := "current_results"
	if recentTotal > 1 {
		// Reuse the authorized projection for every historical row, and match
		// the same series as the history endpoint. Only completed output with
		// an earlier execution time can appear beside the current result.
		eligibleOutput := "anchor.output_kind = 'html' AND COALESCE(historical.output_html, '') ~ '[^[:space:]]'"
		if includeNumeric {
			eligibleOutput = "((anchor.output_kind = 'html' AND COALESCE(historical.output_html, '') ~ '[^[:space:]]') OR (anchor.output_kind = 'number' AND historical.output_numeric IS NOT NULL))"
		}
		query += `, recent_results AS (
    SELECT * FROM current_results
    UNION ALL
    SELECT history.*, 1::bigint AS history_rank
    FROM current_results anchor
    JOIN LATERAL (
        SELECT historical.* FROM visible_results historical
        WHERE ` + eligibleOutput + `
          AND historical.group_id IS NOT DISTINCT FROM anchor.group_id
          AND historical.result_target_key = anchor.result_target_key
          AND historical.test_definition_id IS NOT DISTINCT FROM anchor.test_definition_id
          AND historical.model_id = anchor.model_id
          AND historical.reasoning_effort = anchor.reasoning_effort
          AND historical.output_kind = anchor.output_kind
          AND historical.status IN ('success', 'passed')
          AND (historical.started_at, historical.id) < (anchor.started_at, anchor.id)
        ORDER BY historical.started_at DESC, historical.id DESC
        LIMIT $2
    ) history ON TRUE
)`
		args = append(args, recentTotal-1)
		resultSource = "recent_results"
	}
	query += `
SELECT ` + scheduledTestVisibleResultColumns + `
FROM ` + resultSource + ` vr
ORDER BY vr.account_schedulable DESC NULLS LAST, (vr.status IN ('success', 'passed')) DESC, vr.started_at DESC, vr.id DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results, err := scanVisibleTestResults(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	r.hydrateDisplayEvidence(ctx, results)
	return results, nil
}

// A completed, authorized result determines the entire series. Cursor IDs
// must belong to that same series; callers cannot choose another group/target.
func (r *scheduledTestResultRepository) ListVisibleHistory(ctx context.Context, userID, resultID, beforeID int64, limit int) ([]*service.ScheduledTestResult, error) {
	if limit <= 0 || limit > 51 {
		limit = 51
	}
	const seriesCTE = `, history_anchor AS (
    SELECT * FROM visible_results
    WHERE id = $2 AND status IN ('success', 'passed', 'failed', 'error')
), history_series AS (
    SELECT vr.*
    FROM visible_results vr
    JOIN history_anchor anchor ON vr.group_id IS NOT DISTINCT FROM anchor.group_id
       AND vr.result_target_key = anchor.result_target_key
       AND vr.test_definition_id IS NOT DISTINCT FROM anchor.test_definition_id
       AND vr.model_id = anchor.model_id
       AND vr.reasoning_effort = anchor.reasoning_effort
    WHERE vr.status IN ('success', 'passed')
), cursor_result AS (
    SELECT id, started_at FROM history_series WHERE id = $3
)`
	var anchorVisible, cursorValid bool
	err := r.db.QueryRowContext(ctx, scheduledTestVisibleResultsCTE+seriesCTE+`
SELECT EXISTS (SELECT 1 FROM history_anchor),
       ($3 = 0 OR EXISTS (SELECT 1 FROM cursor_result))`, userID, resultID, beforeID).Scan(&anchorVisible, &cursorValid)
	if err != nil {
		return nil, err
	}
	if !anchorVisible || !cursorValid {
		return nil, sql.ErrNoRows
	}
	// Reapply visibility and series checks for the page itself, including when
	// permissions or stored rows changed after validating the cursor.
	rows, err := r.db.QueryContext(ctx, scheduledTestVisibleResultsCTE+seriesCTE+`
SELECT `+scheduledTestVisibleResultColumns+`
FROM history_series vr
WHERE $3 = 0 OR (vr.started_at, vr.id) < (SELECT started_at, id FROM cursor_result)
ORDER BY vr.started_at DESC, vr.id DESC
LIMIT $4`, userID, resultID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results, err := scanVisibleTestResults(rows)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	r.hydrateDisplayEvidence(ctx, results)
	return results, nil
}

func scanVisibleTestResults(rows *sql.Rows) ([]*service.ScheduledTestResult, error) {
	var out []*service.ScheduledTestResult
	for rows.Next() {
		v := &service.ScheduledTestResult{}
		if err := rows.Scan(&v.ID, &v.PlanID, &v.PlanName, &v.TestName, &v.TestOrder, &v.GroupName, &v.PlanOrder, &v.TargetMode, &v.Status, &v.ResponseText, &v.OutputKind, &v.OutputHTML, &v.OutputNumeric, &v.AccountID, &v.ModelID, &v.ReasoningEffort, &v.GroupID, &v.ErrorMessage, &v.LatencyMs, &v.StartedAt, &v.FinishedAt, &v.CreatedAt, &v.TestDefinitionID, &v.GroupOrder, &v.AccountSchedulable, &v.TestKey, &v.QualityVerdict, &v.ModelSchedulable, &v.ModelOrder, &v.ExecutionSkipped); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *scheduledTestResultRepository) Delete(ctx context.Context, id int64) error {
	// Follow protection's plan/account lock order before ON DELETE SET NULL
	// touches the active state. The hold survives removing its visible output.
	var planID int64
	var accountID sql.NullInt64
	if err := r.db.QueryRowContext(ctx, `SELECT plan_id,account_id FROM scheduled_test_results WHERE id=$1`, id).Scan(&planID, &accountID); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockProtectionPlan(ctx, tx, planID); err != nil {
		return err
	}
	if accountID.Valid {
		if _, err := lockProtectionAccount(ctx, tx, accountID.Int64); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_results WHERE id = $1`, id)
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
	return tx.Commit()
}

func (r *scheduledTestResultRepository) PruneOldResults(ctx context.Context, planID int64, keepCount int) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM scheduled_test_results
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (
					PARTITION BY plan_id, test_definition_id, group_id, account_id,
					model_id, reasoning_effort,
					CASE WHEN status IN ('success', 'passed') THEN 'success' ELSE 'failed' END
					ORDER BY started_at DESC, id DESC
				) AS rn
				FROM scheduled_test_results
				WHERE plan_id = $1 AND status NOT IN ('running', 'pending')
				  AND NOT EXISTS (SELECT 1 FROM scheduled_test_protection_states protection WHERE protection.result_id = scheduled_test_results.id)
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
	var protection, schedule []byte
	if err := row.Scan(
		&p.ID, &p.Name, &p.SortOrder, &p.AccountID, &p.GroupID, &p.TestDefinitionID, &p.TestType, &p.TargetMode, &p.ModelID, &p.ReasoningEffort, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover,
		&p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt, pq.Array(&p.TestDefinitionIDs), &protection, pq.Array(&p.GroupIDs), &p.MigrationNote, &schedule, pq.Array(&p.ExcludedAccountIDs), pq.Array(&p.ModelIDs),
	); err != nil {
		return nil, err
	}
	if len(p.ModelIDs) == 0 && p.ModelID != "" {
		p.ModelIDs = []string{p.ModelID}
	}
	if len(protection) > 0 {
		if err := json.Unmarshal(protection, &p.Protection); err != nil {
			return nil, fmt.Errorf("decode test protection: %w", err)
		}
	}
	if len(schedule) > 0 {
		if err := json.Unmarshal(schedule, &p.Schedule); err != nil {
			return nil, fmt.Errorf("decode test schedule: %w", err)
		}
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
