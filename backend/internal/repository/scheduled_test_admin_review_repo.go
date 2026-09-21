package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestAdminReviewRepository = (*scheduledTestResultRepository)(nil)

// This projection is only served behind administrator authentication. Include
// paused accounts so a failed manual judgment can be reversed from this page.
func (r *scheduledTestResultRepository) ListAdminReviews(ctx context.Context) ([]*service.ScheduledTestAdminReview, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT to_jsonb(r) || `+protectionVoteResultJSON+` || jsonb_build_object('account_name',a.name),
	 s.generation,s.verdict,s.admin_verdict,s.admin_user_id,s.admin_decided_at,(a.status='quality_paused'),s.rule_config,p.protection
	 FROM scheduled_test_protection_states s
	 JOIN scheduled_test_results r ON r.id=s.result_id
	 JOIN scheduled_test_plans p ON p.id=s.plan_id
	 JOIN accounts a ON a.id=s.account_id
	 LEFT JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
	 LEFT JOIN groups g ON g.id=r.group_id
	 WHERE p.enabled AND p.protection->>'enabled'='true' AND s.rule_config->'vote'->>'enabled'='true'
	 AND s.completed AND r.status IN ('success','passed')
	 AND r.started_at=s.result_started_at AND r.started_at>=s.round_started_at
	 AND r.group_id IS NOT DISTINCT FROM p.group_id AND r.model_id=p.model_id
	 AND r.target_mode=p.target_mode AND (p.account_id IS NULL OR p.account_id=r.account_id)
	 AND (r.output_kind='statistics' OR r.reasoning_effort=p.reasoning_effort)
	 AND (r.test_definition_id=ANY(p.test_definition_ids) OR r.test_definition_id=p.test_definition_id)
	 AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
	 AND `+protectionSourceMembershipSQL+`
	 ORDER BY p.sort_order,d.sort_order,r.started_at DESC,r.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]*service.ScheduledTestAdminReview, 0)
	for rows.Next() {
		out := &service.ScheduledTestAdminReview{Result: &service.ScheduledTestResult{}}
		var raw, ruleRaw, configRaw []byte
		if err := rows.Scan(&raw, &out.Generation, &out.Verdict, &out.AdminVerdict, &out.AdminUserID, &out.DecidedAt, &out.AccountPaused, &ruleRaw, &configRaw); err != nil {
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
		if protectionRuleCurrent(&config, rule) {
			results = append(results, out)
		}
	}
	return results, rows.Err()
}

func (r *scheduledTestResultRepository) DecideTestResult(ctx context.Context, adminID, resultID, generation int64, verdict string) error {
	if verdict != "pass" && verdict != "fail" {
		return service.ErrScheduledTestVoteInvalid
	}
	var planID, accountID int64
	if err := r.db.QueryRowContext(ctx, `SELECT plan_id,account_id FROM scheduled_test_protection_states WHERE result_id=$1`, resultID).Scan(&planID, &accountID); err != nil {
		return adminReviewError(err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	config, err := lockProtectionPlan(ctx, tx, planID)
	if err != nil {
		return adminReviewError(err)
	}
	eligible, err := lockProtectionAccount(ctx, tx, accountID)
	if err != nil {
		return err
	}
	if !eligible {
		return service.ErrScheduledTestVoteUnavailable
	}
	state, err := loadProtectionState(ctx, tx, resultID)
	if err != nil {
		return adminReviewError(err)
	}
	if state.generation != generation || !state.completed || !protectionRuleCurrent(config, state.rule) || state.rule.Vote == nil || !state.rule.Vote.Enabled {
		return service.ErrScheduledTestVoteUnavailable
	}
	// A retry reuses its ID. Check both generation and start time while keeping
	// the result locked, so a stale page cannot decide its replacement execution.
	var status string
	var started time.Time
	if err := tx.QueryRowContext(ctx, `SELECT status,started_at FROM scheduled_test_results WHERE id=$1 FOR SHARE`, resultID).Scan(&status, &started); err != nil {
		return adminReviewError(err)
	}
	if (status != "success" && status != "passed") || !started.Equal(state.started) || started.Before(state.roundStarted) {
		return service.ErrScheduledTestVoteUnavailable
	}
	current, err := protectionResultCurrent(ctx, tx, resultID)
	if err != nil {
		return err
	}
	if !current {
		return service.ErrScheduledTestVoteUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET admin_verdict=$2,admin_user_id=$3,admin_decided_at=NOW() WHERE result_id=$1`, resultID, verdict, adminID); err != nil {
		return err
	}
	state.adminVerdict = verdict
	if err := saveProtectionVerdict(ctx, tx, state); err != nil {
		return err
	}
	return tx.Commit()
}

func adminReviewError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrScheduledTestVoteUnavailable
	}
	return err
}
