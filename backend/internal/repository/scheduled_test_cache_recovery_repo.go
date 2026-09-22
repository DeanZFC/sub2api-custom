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

var _ service.ScheduledTestCacheRecoveryRepository = (*scheduledTestResultRepository)(nil)

func (r *scheduledTestResultRepository) CacheRecoveryWindowStart(ctx context.Context, planID, accountID, definitionID int64) (*time.Time, error) {
	var started sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT recovery_trial_started_at FROM scheduled_test_protection_states
	 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3`, planID, accountID, definitionID).Scan(&started)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !started.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &started.Time, nil
}

func startCacheRecoveryCooldown(ctx context.Context, tx *sql.Tx, state *protectionState, now time.Time, reason string) error {
	c := state.rule.Recovery
	if c == nil || !c.Enabled || c.CooldownSeconds <= 0 {
		return fmt.Errorf("cache recovery cooldown is invalid")
	}
	until := now.Add(time.Duration(c.CooldownSeconds) * time.Second)
	reason = fmt.Sprintf("%s；冷却至 %s 后限量试运行", reason, until.Format(time.RFC3339))
	_, err := tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET
	 blocked=TRUE,automated_verdict='fail',verdict='fail',completed=TRUE,
	 recovery_phase='cooldown',recovery_cooldown_until=$4,
	 recovery_trial_started_at=NULL,recovery_trial_ends_at=NULL,recovery_trial_requests=0,
	 reason=$5,updated_at=$6 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3`,
		state.planID, state.accountID, state.definitionID, until, reason, now)
	state.reason = reason
	return err
}

func cacheRecoverySampleReason(reason string, state *protectionState, stats *service.ScheduledTestStatistics) string {
	samples := int64(0)
	rate := "无"
	if stats != nil {
		samples = stats.CacheSamples
		if stats.CacheRate != nil {
			rate = fmt.Sprintf("%.2f%%", *stats.CacheRate*100)
		}
	}
	return fmt.Sprintf("%s；试运行有效缓存样本 %d/%d，实测缓存率 %s，恢复阈值 %.2f%%", reason, samples, state.rule.Recovery.MinSamples, rate, state.rule.Recovery.RecoverRate)
}

type cacheRecoveryCandidate struct {
	planID, accountID, definitionID int64
	phase                           string
	trialStarted, trialEnds         sql.NullTime
	rule                            service.ScheduledTestProtectionRule
	plan                            service.ScheduledTestPlan
}

func (r *scheduledTestResultRepository) AdvanceCacheRecovery(ctx context.Context, now time.Time) error {
	// Read statistics before taking mutation locks, so a slow query never holds
	// an account lock or needs a second pooled connection inside a transaction.
	rows, err := r.db.QueryContext(ctx, `SELECT s.plan_id,s.account_id,s.test_definition_id,s.recovery_phase,
	 s.recovery_trial_started_at,s.recovery_trial_ends_at,s.rule_config,p.group_id,p.model_id,p.protection
	 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id JOIN accounts a ON a.id=s.account_id
	 WHERE s.blocked AND p.enabled AND p.protection->>'enabled'='true'
	 AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
	 AND (p.account_id IS NULL OR p.account_id=s.account_id)
	 AND (s.test_definition_id=ANY(p.test_definition_ids) OR s.test_definition_id=p.test_definition_id)
	 AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=s.account_id AND ag.group_id=ANY(p.group_ids))
	 AND (s.recovery_phase='trial' OR (s.recovery_phase='cooldown' AND s.recovery_cooldown_until<=$1))
	 ORDER BY s.updated_at,s.plan_id,s.account_id,s.test_definition_id LIMIT 200`, now)
	if err != nil {
		return err
	}
	var candidates []cacheRecoveryCandidate
	for rows.Next() {
		var c cacheRecoveryCandidate
		var raw, protection []byte
		if err := rows.Scan(&c.planID, &c.accountID, &c.definitionID, &c.phase, &c.trialStarted, &c.trialEnds, &raw, &c.plan.GroupID, &c.plan.ModelID, &protection); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &c.rule); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(protection, &c.plan.Protection); err != nil {
			rows.Close()
			return err
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var sweepErrors []error
	for _, c := range candidates {
		if c.rule.Recovery == nil || !c.rule.Recovery.Enabled || !protectionRuleCurrent(&c.plan.Protection, c.rule) {
			continue
		}
		var stats *service.ScheduledTestStatistics
		if c.phase == "trial" && c.trialStarted.Valid && c.trialEnds.Valid && now.After(c.trialStarted.Time) {
			end := now
			if c.trialEnds.Time.Before(end) {
				end = c.trialEnds.Time
			}
			var groupID *int64
			if end.After(c.trialStarted.Time) {
				stats, err = r.CollectStatistics(ctx, service.ScheduledTestStatisticsFilter{
					GroupID: groupID, AccountID: &c.accountID, Model: c.plan.ModelID,
					WindowStart: c.trialStarted.Time, WindowEnd: end, RequestStartedAfter: &c.trialStarted.Time,
				})
				if err != nil {
					sweepErrors = append(sweepErrors, fmt.Errorf("cache recovery statistics for plan %d account %d: %w", c.planID, c.accountID, err))
					continue
				}
			}
		}
		if err := r.advanceCacheRecoveryCandidate(ctx, c, stats, now); err != nil {
			sweepErrors = append(sweepErrors, fmt.Errorf("cache recovery plan %d account %d: %w", c.planID, c.accountID, err))
		}
	}
	return errors.Join(sweepErrors...)
}

func loadCacheRecoveryState(ctx context.Context, tx *sql.Tx, c cacheRecoveryCandidate) (*protectionState, error) {
	s := &protectionState{}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT plan_id,account_id,test_definition_id,COALESCE(result_id,0),generation,result_started_at,
	 automated_verdict,verdict,reason,blocked,completed,rule_config,round_started_at,admin_verdict,
	 recovery_phase,recovery_cooldown_until,recovery_trial_started_at,recovery_trial_ends_at,recovery_trial_requests
	 FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 FOR UPDATE`,
		c.planID, c.accountID, c.definitionID).Scan(&s.planID, &s.accountID, &s.definitionID, &s.resultID, &s.generation, &s.started,
		&s.automated, &s.verdict, &s.reason, &s.blocked, &s.completed, &raw, &s.roundStarted, &s.adminVerdict,
		&s.recoveryPhase, &s.recoveryCooldownUntil, &s.recoveryTrialStarted, &s.recoveryTrialEnds, &s.recoveryTrialRequests)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.rule); err != nil {
		return nil, err
	}
	return s, nil
}

func (r *scheduledTestResultRepository) advanceCacheRecoveryCandidate(ctx context.Context, c cacheRecoveryCandidate, stats *service.ScheduledTestStatistics, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	config, err := lockProtectionPlan(ctx, tx, c.planID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || !protectionRuleCurrent(config, c.rule) {
		return err
	}
	eligible, err := lockProtectionAccount(ctx, tx, c.accountID)
	if err != nil || !eligible {
		return err
	}
	state, err := loadCacheRecoveryState(ctx, tx, c)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !state.blocked || state.recoveryPhase != c.phase || !reflect.DeepEqual(state.rule, c.rule) ||
		!state.recoveryTrialStarted.Time.Equal(c.trialStarted.Time) || !state.recoveryTrialEnds.Time.Equal(c.trialEnds.Time) {
		return nil
	}
	// Plan edits clear its holds under the same plan lock. Check model/group as
	// well before using the statistics snapshot collected outside this lock.
	var model string
	var groupID *int64
	var member bool
	err = tx.QueryRowContext(ctx, `SELECT p.model_id,p.group_id,
	 (p.account_id IS NULL OR p.account_id=$2)
	 AND ($3=ANY(p.test_definition_ids) OR $3=p.test_definition_id)
	 AND EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=$2 AND ag.group_id=ANY(p.group_ids))
	 FROM scheduled_test_plans p WHERE p.id=$1`, c.planID, c.accountID, c.definitionID).Scan(&model, &groupID, &member)
	if err != nil || !member || model != c.plan.ModelID || !reflect.DeepEqual(groupID, c.plan.GroupID) {
		return err
	}
	before, err := readProtectionAccountSnapshot(ctx, tx, state.accountID)
	if err != nil {
		return err
	}
	verdict, reason := "pending", "缓存质量冷却结束，限量试运行中"
	if c.phase == "cooldown" {
		if !state.recoveryCooldownUntil.Valid || now.Before(state.recoveryCooldownUntil.Time) {
			return nil
		}
		// Open overlapping trials together. Independent timers can otherwise
		// alternate forever while another hold prevents any real traffic.
		var ready bool
		err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS (
		 SELECT 1 FROM scheduled_test_combination_states WHERE account_id=$1 AND blocked)
		 AND NOT EXISTS (
		 SELECT 1 FROM scheduled_test_protection_states other WHERE other.account_id=$1 AND other.blocked
		 AND (other.plan_id<>$2 OR other.test_definition_id<>$3)
		 AND NOT COALESCE(other.rule_config->'recovery'->>'enabled'='true'
		 AND ((other.recovery_phase='trial' AND other.recovery_trial_started_at<=$4 AND other.recovery_trial_ends_at>$4)
		 OR (other.recovery_phase='cooldown' AND other.recovery_cooldown_until<=$4)),FALSE)
		)`, state.accountID, state.planID, state.definitionID, now).Scan(&ready)
		if err != nil {
			return err
		}
		if !ready {
			_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET updated_at=$4
			 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3`, state.planID, state.accountID, state.definitionID, now)
			if err != nil {
				return err
			}
			return tx.Commit()
		}
		_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET recovery_phase='trial',
		 recovery_cooldown_until=NULL,recovery_trial_started_at=$4,recovery_trial_ends_at=$5,recovery_trial_requests=0,
		 reason=$6,updated_at=$4 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3`,
			state.planID, state.accountID, state.definitionID, now, now.Add(time.Duration(state.rule.Recovery.TrialSeconds)*time.Second), reason)
	} else {
		verdict, reason = service.EvaluateScheduledTestCacheRecovery(state.rule, stats)
		if verdict == "pass" {
			reason = cacheRecoverySampleReason("限量试运行缓存质量达标，解除本规则调度保护", state, stats)
			// Keep trial_started_at as the lower bound for later scheduled checks.
			_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET blocked=FALSE,
			 automated_verdict='pass',verdict='pass',completed=TRUE,
			 recovery_phase='',recovery_cooldown_until=NULL,recovery_trial_ends_at=NULL,
			 reason=$4,updated_at=$5 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3`,
				state.planID, state.accountID, state.definitionID, reason, now)
		} else if verdict == "fail" || !state.recoveryTrialEnds.Valid || !now.Before(state.recoveryTrialEnds.Time) {
			if verdict != "fail" {
				reason = "限量试运行超时，有效缓存样本不足，重新冷却"
			}
			verdict = "fail"
			reason = cacheRecoverySampleReason(reason, state, stats)
			err = startCacheRecoveryCooldown(ctx, tx, state, now, reason)
			reason = state.reason
		} else {
			// Rotate pending trials behind other due rows in the bounded sweep.
			reason = cacheRecoverySampleReason(reason, state, stats)
			_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET updated_at=$4,reason=$5
			 WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3`, state.planID, state.accountID, state.definitionID, now, reason)
		}
	}
	if err != nil {
		return err
	}
	if err := reconcileProtectionGroups(ctx, tx, state); err != nil {
		return err
	}
	if err := reconcileProtectionAccount(ctx, tx, state.accountID); err != nil {
		return err
	}
	if verdict != "pending" || c.phase == "cooldown" {
		if err := recordProtectionAction(ctx, tx, state, before, verdict, reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}
