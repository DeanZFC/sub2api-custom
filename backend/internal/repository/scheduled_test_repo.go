package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
		INSERT INTO scheduled_test_plans (name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, next_run_at, test_definition_ids, protection, group_ids, migration_note, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, COALESCE($15::bigint[], '{}'), $16::jsonb, COALESCE($17::bigint[], '{}'), $18, NOW(), NOW())
		RETURNING id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note
	`, plan.Name, plan.SortOrder, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.TargetMode, plan.ModelID, plan.ReasoningEffort, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, pq.Array(plan.TestDefinitionIDs), protection, pq.Array(plan.GroupIDs), plan.MigrationNote)
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
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note
		FROM scheduled_test_plans WHERE id = $1
	`, id)
	return scanPlan(row)
}

func (r *scheduledTestPlanRepository) ListByAccountID(ctx context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note
		FROM scheduled_test_plans p WHERE EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=$1 AND ag.group_id=ANY(p.group_ids))
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
		SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note
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
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockScheduledTestPlanWrites(ctx, tx); err != nil {
		return nil, err
	}
	previous, err := scanPlan(tx.QueryRowContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE`, plan.ID))
	if err != nil {
		return nil, err
	}
	if err := validateScheduledTestPlanConflicts(ctx, tx, plan); err != nil {
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
		SET name = $2, sort_order = $3, account_id = $4, group_id = $5, test_definition_id = $6, test_type = $7, target_mode = $8, model_id = $9, reasoning_effort = $10, cron_expression = $11, enabled = $12, max_results = $13, auto_recover = $14, next_run_at = $15, test_definition_ids = COALESCE($16::bigint[], '{}'), protection = $17::jsonb, group_ids = COALESCE($18::bigint[], '{}'), migration_note = $19, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note
	`, plan.ID, plan.Name, plan.SortOrder, plan.AccountID, plan.GroupID, plan.TestDefinitionID, plan.TestType, plan.TargetMode, plan.ModelID, plan.ReasoningEffort, plan.CronExpression, plan.Enabled, plan.MaxResults, plan.AutoRecover, plan.NextRunAt, pq.Array(plan.TestDefinitionIDs), protection, pq.Array(plan.GroupIDs), plan.MigrationNote)
	updated, err := scanPlan(row)
	if err != nil {
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
	if err := clearPlanProtectionTx(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scheduledTestPlanRepository) UpdateAfterRun(ctx context.Context, id int64, lastRunAt time.Time, nextRunAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scheduled_test_plans SET last_run_at = $2, next_run_at = $3, updated_at = NOW() WHERE id = $1
	`, id, lastRunAt, nextRunAt)
	return err
}

func (r *scheduledTestPlanRepository) List(ctx context.Context) ([]*service.ScheduledTestPlan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note FROM scheduled_test_plans ORDER BY created_at DESC`)
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
	e := row.Scan(&d.ID, &d.Key, &d.Name, &d.Description, &d.Prompt, &d.OutputKind, &d.Enabled, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt)
	return d, e
}

func (r *scheduledTestResultRepository) Create(ctx context.Context, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	return createScheduledTestResult(ctx, r.db, result)
}

func createScheduledTestResult(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, result *service.ScheduledTestResult) (*service.ScheduledTestResult, error) {
	row := db.QueryRowContext(ctx, `
		INSERT INTO scheduled_test_results (plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, reasoning_effort, group_id, error_message, latency_ms, started_at, finished_at, test_definition_id, target_mode, run_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, NOW())
		RETURNING id, plan_id, status, response_text, output_kind, output_html, output_numeric, account_id, model_id, reasoning_effort, group_id, error_message, latency_ms, started_at, finished_at, created_at, test_definition_id, target_mode
	`, result.PlanID, result.Status, result.ResponseText, result.OutputKind, result.OutputHTML, result.OutputNumeric, result.AccountID, result.ModelID, result.ReasoningEffort, result.GroupID, result.ErrorMessage, result.LatencyMs, result.StartedAt, result.FinishedAt, result.TestDefinitionID, result.TargetMode, result.RunID)

	out := &service.ScheduledTestResult{RunID: result.RunID}
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
// to the last full execution snapshot when one has been recorded.
func (r *scheduledTestResultRepository) ListByPlanID(ctx context.Context, planID int64, limit int) ([]*service.ScheduledTestResult, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH ranked_results AS (
			SELECT id, ROW_NUMBER() OVER (
				-- The administrator's latest view is one row per account and
				-- configured test type. Model/effort belong to that execution
				-- snapshot and must not make an older run visible beside it.
				PARTITION BY test_definition_id, account_id
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
	rows, err := r.db.QueryContext(ctx, `SELECT id, protection_decision
FROM scheduled_test_results
WHERE id = ANY($1) AND protection_decision IS NOT NULL`, pq.Array(ids))
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
		var raw []byte
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		result := byID[id]
		if result == nil {
			continue
		}
		decision := &service.ScheduledTestProtectionDecision{}
		if err := json.Unmarshal(raw, decision); err != nil {
			return err
		}
		result.ProtectionDecision = decision
	}
	return rows.Err()
}

// Display position belongs to the current group, independently of the rule
// that produced a historical result. Retain disabled rules' display settings;
// disabling execution does not remove their groups' configured positions.
const scheduledTestGroupDisplayOrdersSQL = `SELECT group_id,
 (DENSE_RANK() OVER (ORDER BY sort_order, position)-1)::integer AS sort_order
 FROM (SELECT DISTINCT ON (selected.group_id) selected.group_id,p.sort_order,selected.position
       FROM scheduled_test_plans p CROSS JOIN LATERAL unnest(p.group_ids) WITH ORDINALITY selected(group_id,position)
       ORDER BY selected.group_id,p.sort_order,selected.position,p.id) configured_groups`

// Both public queries authorize the projected current group. The account is
// projected only as an ID and hidden for group tests.
// Read its current status and scheduling switch, even for group-mode model
// tests whose executing account ID is hidden. Account-free group statistics
// remain visible; orphaned account results do not.
const scheduledTestVisibleResultsCTE = `WITH projected_results AS NOT MATERIALIZED (
    SELECT r.id,r.plan_id,p.name AS plan_name,COALESCE(d.name, '') AS test_name,COALESCE(d.sort_order, 2147483647) AS test_order,
           COALESCE(projected_group.name, source_group.name, '') AS group_name,COALESCE(p.sort_order, 2147483647) AS plan_order,r.target_mode,r.status,
           r.response_text,r.output_kind,r.output_html,r.output_numeric,
           CASE WHEN r.target_mode IN ('account', 'all_accounts') THEN r.account_id ELSE NULL END AS visible_account_id,
           r.model_id,r.reasoning_effort,
           CASE WHEN r.account_id IS NULL THEN r.group_id ELSE projected_group.group_id END AS group_id,
           r.error_message,r.latency_ms,r.started_at,r.finished_at,r.created_at,r.test_definition_id,
           COALESCE(group_display_order.sort_order, 2147483647) AS group_order,
           CASE WHEN r.target_mode IN ('account', 'all_accounts')
                THEN COALESCE(r.account_id::text, '')
                ELSE 'group'
           END AS result_target_key,
           r.account_id
    FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
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
        LIMIT 1
    ) projected_group ON TRUE
    LEFT JOIN (` + scheduledTestGroupDisplayOrdersSQL + `) group_display_order
      ON group_display_order.group_id = CASE WHEN r.account_id IS NULL THEN r.group_id ELSE projected_group.group_id END
), visible_results AS NOT MATERIALIZED (
    SELECT r.id,r.plan_id,r.plan_name,r.test_name,r.test_order,r.group_name,r.plan_order,r.target_mode,r.status,
           r.response_text,r.output_kind,r.output_html,r.output_numeric,r.visible_account_id,r.model_id,r.reasoning_effort,
           r.group_id,r.error_message,r.latency_ms,r.started_at,r.finished_at,r.created_at,r.test_definition_id,r.result_target_key,r.group_order
    FROM projected_results r
    WHERE (
        (r.account_id IS NULL AND r.target_mode = 'group')
        OR (
            r.account_id IS NOT NULL
            AND r.group_id IS NOT NULL
            AND EXISTS (
                SELECT 1 FROM accounts visible_account
                WHERE visible_account.id = r.account_id
                  AND visible_account.deleted_at IS NULL
                  AND visible_account.status = 'active'
                  AND visible_account.schedulable = TRUE
            )
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

const scheduledTestVisibleResultColumns = `vr.id,vr.plan_id,vr.plan_name,vr.test_name,vr.test_order,vr.group_name,vr.plan_order,vr.target_mode,vr.status,vr.response_text,vr.output_kind,vr.output_html,vr.output_numeric,vr.visible_account_id,vr.model_id,vr.reasoning_effort,vr.group_id,vr.error_message,vr.latency_ms,vr.started_at,vr.finished_at,vr.created_at,vr.test_definition_id,vr.group_order`

func (r *scheduledTestResultRepository) ListVisible(ctx context.Context, userID int64, limit int) ([]*service.ScheduledTestResult, error) {
	if limit <= 0 || limit > 3 {
		limit = 3
	}
	rows, err := r.db.QueryContext(ctx, scheduledTestVisibleResultsCTE+`, success_state AS (
    SELECT vr.*, bool_or(status IN ('success', 'passed')) OVER (
        PARTITION BY group_id, result_target_key, test_definition_id, model_id, reasoning_effort
    ) AS has_success
    FROM visible_results vr
), ranked_results AS (
    SELECT vr.*, row_number() OVER (
        PARTITION BY group_id, result_target_key, test_definition_id, model_id, reasoning_effort
        ORDER BY started_at DESC, id DESC
    ) AS history_rank
    FROM success_state vr
    WHERE status IN ('success', 'passed')
       OR (status IN ('pending', 'running') AND NOT has_success)
)
SELECT `+scheduledTestVisibleResultColumns+`
FROM ranked_results vr
WHERE history_rank <= $2
ORDER BY vr.started_at DESC, vr.id DESC`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVisibleTestResults(rows)
}

// A successful, authorized result determines the entire series. Cursor IDs
// must belong to that same series; callers cannot choose another group/target.
func (r *scheduledTestResultRepository) ListVisibleHistory(ctx context.Context, userID, resultID, beforeID int64, limit int) ([]*service.ScheduledTestResult, error) {
	if limit <= 0 || limit > 51 {
		limit = 51
	}
	const seriesCTE = `, history_series AS (
    SELECT vr.*
    FROM visible_results vr
    JOIN visible_results anchor ON anchor.id = $2
       AND anchor.status IN ('success', 'passed')
       AND vr.group_id IS NOT DISTINCT FROM anchor.group_id
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
SELECT EXISTS (SELECT 1 FROM history_series),
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
	return scanVisibleTestResults(rows)
}

func scanVisibleTestResults(rows *sql.Rows) ([]*service.ScheduledTestResult, error) {
	var out []*service.ScheduledTestResult
	for rows.Next() {
		v := &service.ScheduledTestResult{}
		if err := rows.Scan(&v.ID, &v.PlanID, &v.PlanName, &v.TestName, &v.TestOrder, &v.GroupName, &v.PlanOrder, &v.TargetMode, &v.Status, &v.ResponseText, &v.OutputKind, &v.OutputHTML, &v.OutputNumeric, &v.AccountID, &v.ModelID, &v.ReasoningEffort, &v.GroupID, &v.ErrorMessage, &v.LatencyMs, &v.StartedAt, &v.FinishedAt, &v.CreatedAt, &v.TestDefinitionID, &v.GroupOrder); err != nil {
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
	defer tx.Rollback()
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
	var protection []byte
	if err := row.Scan(
		&p.ID, &p.Name, &p.SortOrder, &p.AccountID, &p.GroupID, &p.TestDefinitionID, &p.TestType, &p.TargetMode, &p.ModelID, &p.ReasoningEffort, &p.CronExpression, &p.Enabled, &p.MaxResults, &p.AutoRecover,
		&p.LastRunAt, &p.NextRunAt, &p.CreatedAt, &p.UpdatedAt, pq.Array(&p.TestDefinitionIDs), &protection, pq.Array(&p.GroupIDs), &p.MigrationNote,
	); err != nil {
		return nil, err
	}
	if len(protection) > 0 {
		if err := json.Unmarshal(protection, &p.Protection); err != nil {
			return nil, fmt.Errorf("decode test protection: %w", err)
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
