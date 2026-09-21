package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestProtectionRepository = (*scheduledTestResultRepository)(nil)

// Lock order is always plan -> account -> protection state. Account locking
// serializes independent rules/plans before deciding whether any hold remains.
func lockProtectionPlan(ctx context.Context, tx *sql.Tx, planID int64) (*service.ScheduledTestProtectionConfig, error) {
	var enabled bool
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT enabled, protection FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE`, planID).Scan(&enabled, &raw); err != nil {
		return nil, err
	}
	var config service.ScheduledTestProtectionConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	if !enabled {
		config.Enabled = false
	}
	return &config, nil
}

func protectionRuleCurrent(config *service.ScheduledTestProtectionConfig, rule service.ScheduledTestProtectionRule) bool {
	if config == nil || !config.Enabled {
		return false
	}
	for _, current := range config.Rules {
		if current.TestDefinitionID == rule.TestDefinitionID {
			return reflect.DeepEqual(current, rule)
		}
	}
	return false
}

func protectionResultCurrent(ctx context.Context, tx *sql.Tx, resultID int64) (bool, error) {
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
	 SELECT 1 FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
	 WHERE r.id=$1 AND r.group_id IS NOT DISTINCT FROM p.group_id AND r.model_id=p.model_id
	 AND r.target_mode=p.target_mode AND (p.account_id IS NULL OR p.account_id=r.account_id)
	 AND (r.output_kind='statistics' OR r.reasoning_effort=p.reasoning_effort)
	 AND (r.test_definition_id=ANY(p.test_definition_ids) OR r.test_definition_id=p.test_definition_id)
	 AND `+protectionSourceMembershipSQL+`
	)`, resultID).Scan(&current)
	return current, err
}

func lockProtectionAccount(ctx context.Context, tx *sql.Tx, accountID int64) (bool, error) {
	var status string
	var schedulable bool
	var deleted sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT status, schedulable, deleted_at FROM accounts WHERE id=$1 FOR NO KEY UPDATE`, accountID).Scan(&status, &schedulable, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return !deleted.Valid && schedulable && (status == "active" || status == "quality_paused"), err
}

func (r *scheduledTestResultRepository) ListDetectionAccountIDs(ctx context.Context, groupID, accountID *int64) ([]int64, error) {
	if groupID == nil && accountID == nil {
		return []int64{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
		WHERE a.deleted_at IS NULL AND a.schedulable=TRUE AND a.status IN ('active','quality_paused')
		  AND ($1::bigint IS NULL OR a.id=$1)
		  AND ($2::bigint IS NULL OR EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$2))
		ORDER BY a.id`, accountID, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *scheduledTestResultRepository) BeginProtection(ctx context.Context, result *service.ScheduledTestResult, rule service.ScheduledTestProtectionRule) error {
	if result == nil || result.ID <= 0 || result.AccountID == nil || result.TestDefinitionID == nil || *result.TestDefinitionID != rule.TestDefinitionID {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	config, err := lockProtectionPlan(ctx, tx, result.PlanID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !protectionRuleCurrent(config, rule) {
		return nil
	}
	current, err := protectionResultCurrent(ctx, tx, result.ID)
	if err != nil || !current {
		return err
	}
	eligible, err := lockProtectionAccount(ctx, tx, *result.AccountID)
	if err != nil || !eligible {
		return err
	}
	if err := initializeProtectionActionStates(ctx, tx, result.PlanID, *result.AccountID, config); err != nil {
		return err
	}
	raw, err := json.Marshal(rule)
	if err != nil {
		return err
	}
	// A delayed Begin from an older execution cannot invalidate a newer round.
	// A repeated Begin for the same ID/start is idempotent, including its votes.
	var generation int64
	err = tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_protection_states
		(plan_id,account_id,test_definition_id,result_id,result_started_at,rule_config)
		SELECT $1,$2,$3,r.id,r.started_at,$5::jsonb FROM scheduled_test_results r
		WHERE r.id=$4 AND r.plan_id=$1 AND r.account_id=$2 AND r.test_definition_id=$3
		ON CONFLICT (plan_id,account_id,test_definition_id) DO UPDATE SET
		 result_id=EXCLUDED.result_id,result_started_at=EXCLUDED.result_started_at,
		 generation=scheduled_test_protection_states.generation+1,
		 automated_verdict='pending',verdict='pending',completed=FALSE,rule_config=EXCLUDED.rule_config,
		 admin_verdict='',admin_user_id=NULL,admin_decided_at=NULL,updated_at=NOW()
		WHERE EXCLUDED.result_started_at >= scheduled_test_protection_states.round_started_at
		 AND (EXCLUDED.result_started_at,EXCLUDED.result_id) >
		 (scheduled_test_protection_states.result_started_at,COALESCE(scheduled_test_protection_states.result_id,0))
		RETURNING generation`, result.PlanID, *result.AccountID, rule.TestDefinitionID, result.ID, raw).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_votes WHERE result_id=$1`, result.ID); err != nil {
		return err
	}
	return tx.Commit()
}

type protectionState struct {
	adminVerdict                                          string
	roundStarted                                          time.Time
	planID, accountID, definitionID, resultID, generation int64
	started                                               time.Time
	automated, verdict, reason                            string
	blocked, completed                                    bool
	rule                                                  service.ScheduledTestProtectionRule
}

func loadProtectionState(ctx context.Context, tx *sql.Tx, resultID int64) (*protectionState, error) {
	s := &protectionState{}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT plan_id,account_id,test_definition_id,result_id,generation,result_started_at,
		automated_verdict,verdict,reason,blocked,completed,rule_config,round_started_at,admin_verdict
		FROM scheduled_test_protection_states WHERE result_id=$1 FOR UPDATE`, resultID).Scan(
		&s.planID, &s.accountID, &s.definitionID, &s.resultID, &s.generation, &s.started, &s.automated, &s.verdict, &s.reason, &s.blocked, &s.completed, &raw, &s.roundStarted, &s.adminVerdict)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.rule); err != nil {
		return nil, err
	}
	return s, nil
}

func protectionVoteCounts(ctx context.Context, tx *sql.Tx, state *protectionState) (int, int, error) {
	var pass, fail int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE vote='pass'),COUNT(*) FILTER (WHERE vote='fail')
		FROM scheduled_test_votes WHERE result_id=$1 AND generation=$2`, state.resultID, state.generation).Scan(&pass, &fail)
	return pass, fail, err
}

func combineProtectionVerdict(state *protectionState, pass, fail int) (string, bool, string) {
	if state.automated == "fail" {
		return "fail", true, state.reason
	}
	if vote := state.rule.Vote; vote != nil && vote.Enabled {
		if state.adminVerdict == "fail" {
			return "fail", true, "管理员判定渠道质量检测不通过"
		}
		if state.adminVerdict == "pass" && state.automated == "pass" {
			return "pass", false, ""
		}
		if fail > vote.RejectAbove {
			return "fail", true, "渠道质量检测不通过票数超过阈值"
		}
		if state.automated == "pass" && pass >= vote.PassAtLeast {
			return "pass", false, ""
		}
		return "pending", state.blocked, state.reason
	}
	if state.automated == "pass" {
		return "pass", false, ""
	}
	return "pending", state.blocked, state.reason
}

func saveProtectionVerdict(ctx context.Context, tx *sql.Tx, state *protectionState) error {
	pass, fail, err := protectionVoteCounts(ctx, tx, state)
	if err != nil {
		return err
	}
	verdict, _, reason := combineProtectionVerdict(state, pass, fail)
	blocked := state.blocked
	switch state.rule.OutcomeAction(verdict).Scheduling {
	case "pause":
		blocked = true
		if reason == "" {
			reason = "渠道质量检测规则触发自动暂停"
		}
	case "resume":
		blocked = false
	}

	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states
		SET automated_verdict=$2,verdict=$3,reason=$4,blocked=$5,completed=$6,
		 routing_verdict=CASE WHEN $3 IN ('pass','fail') THEN $3 ELSE routing_verdict END,updated_at=NOW()
		WHERE result_id=$1 AND generation=$7`, state.resultID, state.automated, verdict, reason, blocked, state.completed, state.generation)
	if err != nil {
		return err
	}
	if err := reconcileProtectionGroups(ctx, tx, state.accountID); err != nil {
		return err
	}
	return reconcileProtectionAccount(ctx, tx, state.accountID)
}

func reconcileProtectionAccount(ctx context.Context, tx *sql.Tx, accountID int64) error {
	var reason string
	err := tx.QueryRowContext(ctx, `SELECT reason FROM scheduled_test_protection_states WHERE account_id=$1 AND blocked
		ORDER BY updated_at DESC,plan_id,test_definition_id LIMIT 1`, accountID).Scan(&reason)
	blocked := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var changed sql.Result
	if blocked {
		if reason == "" {
			reason = "渠道质量检测保护"
		}
		changed, err = tx.ExecContext(ctx, `UPDATE accounts SET status='quality_paused',
		 extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{quality_protection_reason}',to_jsonb($2::text)),updated_at=NOW()
		 WHERE id=$1 AND deleted_at IS NULL AND schedulable=TRUE AND status IN ('active','quality_paused')
		 AND (status <> 'quality_paused' OR extra->>'quality_protection_reason' IS DISTINCT FROM $2)`, accountID, reason)
	} else {
		changed, err = tx.ExecContext(ctx, `UPDATE accounts SET status='active',extra=COALESCE(extra,'{}'::jsonb)-'quality_protection_reason',updated_at=NOW()
		 WHERE id=$1 AND deleted_at IS NULL AND schedulable=TRUE AND status='quality_paused'`, accountID)
	}
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil)
}

func (r *scheduledTestResultRepository) CompleteProtection(ctx context.Context, result *service.ScheduledTestResult, verdict, reason string) error {
	if verdict != "pass" && verdict != "fail" && verdict != "pending" {
		return fmt.Errorf("invalid protection verdict")
	}
	if result == nil || result.AccountID == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	config, err := lockProtectionPlan(ctx, tx, result.PlanID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	eligible, err := lockProtectionAccount(ctx, tx, *result.AccountID)
	if err != nil || !eligible {
		return err
	}
	state, err := loadProtectionState(ctx, tx, result.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// PostgreSQL timestamps round nanoseconds to microseconds. Manual retries
	// keep the in-memory start value rather than rereading the updated row.
	if state.planID != result.PlanID || state.accountID != *result.AccountID || result.StartedAt.Round(time.Microsecond).Before(state.roundStarted) || !state.started.Equal(result.StartedAt.Round(time.Microsecond)) || !protectionRuleCurrent(config, state.rule) {
		return nil
	}
	current, err := protectionResultCurrent(ctx, tx, result.ID)
	if err != nil || !current {
		return err
	}
	state.automated, state.reason, state.completed = verdict, reason, true
	if err := saveProtectionVerdict(ctx, tx, state); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *scheduledTestResultRepository) ClearPlanProtection(ctx context.Context, planID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := clearPlanProtectionTx(ctx, tx, planID); err != nil {
		return err
	}
	return tx.Commit()
}

func clearPlanProtectionTx(ctx context.Context, tx *sql.Tx, planID int64) error {
	return resetPlanProtectionTx(ctx, tx, planID, false)
}

func resetPlanProtectionTx(ctx context.Context, tx *sql.Tx, planID int64, retainTracking bool) error {
	if _, err := lockProtectionPlan(ctx, tx, planID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT account_id FROM scheduled_test_protection_states WHERE plan_id=$1 UNION SELECT account_id FROM scheduled_test_managed_accounts WHERE plan_id=$1 ORDER BY account_id`, planID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := lockProtectionAccount(ctx, tx, id); err != nil {
			return err
		}
		if !retainTracking {
			if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_managed_accounts WHERE plan_id=$1 AND account_id=$2`, planID, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=$2`, planID, id); err != nil {
			return err
		}
		if err := reconcileProtectionAccount(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// Public voting uses the same group entitlement as the normal results view,
// but can also read quality-paused accounts so users can vote on recovery.
const protectionVoteEntitlementSQL = `EXISTS (
	SELECT 1 FROM (
	 SELECT group_id FROM user_allowed_groups WHERE user_id=$1
	 UNION SELECT id FROM groups WHERE status='active' AND deleted_at IS NULL AND is_exclusive=FALSE AND subscription_type<>'subscription'
	 UNION SELECT group_id FROM user_subscriptions WHERE user_id=$1 AND deleted_at IS NULL AND status='active' AND starts_at<=NOW() AND expires_at>NOW()
	) entitled WHERE entitled.group_id=r.group_id OR (r.group_id IS NULL AND EXISTS (
	 SELECT 1 FROM account_groups ag WHERE ag.account_id=r.account_id AND ag.group_id=entitled.group_id
	)))`

const protectionVoteResultJSON = `jsonb_build_object(
	'id',r.id,'plan_id',r.plan_id,'plan_name',p.name,'test_definition_id',r.test_definition_id,
	'test_name',COALESCE(d.name,''),'test_order',COALESCE(d.sort_order,0),'group_name',COALESCE(g.name,''),'plan_order',p.sort_order,
	'target_mode',r.target_mode,'status',r.status,'response_text',r.response_text,'output_kind',r.output_kind,'output_html',r.output_html,'output_numeric',r.output_numeric,
	'account_id',CASE WHEN r.target_mode IN ('account','all_accounts') THEN r.account_id ELSE NULL END,
	'model_id',r.model_id,'reasoning_effort',r.reasoning_effort,'group_id',r.group_id,'error_message','','latency_ms',r.latency_ms,
	'started_at',r.started_at,'finished_at',r.finished_at,'created_at',r.created_at)`

const protectionVotingQuery = `SELECT ` + protectionVoteResultJSON + `,s.rule_config,p.protection,s.automated_verdict,s.completed,
	(a.status='quality_paused'),
	(SELECT COUNT(*) FROM scheduled_test_votes v WHERE v.result_id=r.id AND v.generation=s.generation AND v.vote='pass'),
	(SELECT COUNT(*) FROM scheduled_test_votes v WHERE v.result_id=r.id AND v.generation=s.generation AND v.vote='fail'),
	COALESCE((SELECT vote FROM scheduled_test_votes v WHERE v.result_id=r.id AND v.generation=s.generation AND v.user_id=$1),'')
	FROM scheduled_test_protection_states s
	JOIN scheduled_test_results r ON r.id=s.result_id
	JOIN scheduled_test_plans p ON p.id=s.plan_id
	JOIN accounts a ON a.id=s.account_id
	LEFT JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
	LEFT JOIN groups g ON g.id=r.group_id
	WHERE p.enabled AND p.protection->>'enabled'='true' AND s.rule_config->'vote'->>'enabled'='true'
	 AND s.completed AND r.status IN ('success','passed')
	 AND (NULLIF(BTRIM(r.response_text),'') IS NOT NULL OR NULLIF(BTRIM(r.output_html),'') IS NOT NULL OR r.output_numeric IS NOT NULL)
	 AND a.deleted_at IS NULL AND a.schedulable=TRUE AND a.status IN ('active','quality_paused')
	 AND ` + protectionSourceMembershipSQL + `
	 AND ` + protectionVoteEntitlementSQL

func scanProtectionVoting(rows *sql.Rows) ([]*service.ScheduledTestVoteResult, error) {
	results := make([]*service.ScheduledTestVoteResult, 0)
	for rows.Next() {
		var raw, ruleRaw, configRaw []byte
		var automated string
		var completed bool
		out := &service.ScheduledTestVoteResult{Result: &service.ScheduledTestResult{}}
		if err := rows.Scan(&raw, &ruleRaw, &configRaw, &automated, &completed, &out.Voting.AccountPaused, &out.Voting.PassCount, &out.Voting.FailCount, &out.Voting.MyVote); err != nil {
			return nil, err
		}
		var rule service.ScheduledTestProtectionRule
		var config service.ScheduledTestProtectionConfig
		if err := json.Unmarshal(raw, out.Result); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ruleRaw, &rule); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(configRaw, &config); err != nil {
			return nil, err
		}
		if !protectionRuleCurrent(&config, rule) || rule.Vote == nil || !rule.Vote.Enabled {
			continue
		}
		out.Voting.Enabled = true
		out.Voting.Open = completed && automated != "fail"
		out.Voting.RejectAbove = rule.Vote.RejectAbove
		out.Voting.PassAtLeast = rule.Vote.PassAtLeast
		out.Voting.ReferenceAnswer = rule.ExpectedAnswer
		results = append(results, out)
	}
	return results, rows.Err()
}

func (r *scheduledTestResultRepository) ListVotingResults(ctx context.Context, userID int64) ([]*service.ScheduledTestVoteResult, error) {
	rows, err := r.db.QueryContext(ctx, protectionVotingQuery+` ORDER BY p.sort_order,d.sort_order,r.started_at DESC,r.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProtectionVoting(rows)
}

func (r *scheduledTestResultRepository) CastTestVote(ctx context.Context, userID, resultID int64, vote string) (*service.ScheduledTestVoteResult, error) {
	if vote != "pass" && vote != "fail" {
		return nil, service.ErrScheduledTestVoteInvalid
	}
	var planID, accountID int64
	if err := r.db.QueryRowContext(ctx, `SELECT plan_id,account_id FROM scheduled_test_protection_states WHERE result_id=$1`, resultID).Scan(&planID, &accountID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrScheduledTestVoteUnavailable
		}
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	config, err := lockProtectionPlan(ctx, tx, planID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	if err != nil {
		return nil, err
	}
	eligible, err := lockProtectionAccount(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	state, err := loadProtectionState(ctx, tx, resultID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	if err != nil {
		return nil, err
	}
	if !state.completed || state.automated == "fail" || !protectionRuleCurrent(config, state.rule) || state.rule.Vote == nil || !state.rule.Vote.Enabled {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	// Keep a concurrent manual retry/result deletion from changing the row
	// between authorization and recording its ballot.
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM scheduled_test_results WHERE id=$1 FOR SHARE`, resultID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrScheduledTestVoteUnavailable
		}
		return nil, err
	}
	if status != "success" && status != "passed" {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	current, err := protectionResultCurrent(ctx, tx, resultID)
	if err != nil {
		return nil, err
	}
	if !current {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	var allowed bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduled_test_results r WHERE r.id=$2 AND r.status IN ('success','passed') AND `+protectionVoteEntitlementSQL+`)`, userID, resultID).Scan(&allowed)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_votes(result_id,user_id,generation,vote) VALUES($1,$2,$3,$4)
		ON CONFLICT(result_id,user_id) DO UPDATE SET generation=EXCLUDED.generation,vote=EXCLUDED.vote,updated_at=NOW()`, resultID, userID, state.generation, vote)
	if err != nil {
		return nil, err
	}
	if err := saveProtectionVerdict(ctx, tx, state); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, protectionVotingQuery+` AND r.id=$2`, userID, resultID)
	if err != nil {
		return nil, err
	}
	results, err := scanProtectionVoting(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, service.ErrScheduledTestVoteUnavailable
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results[0], nil
}
