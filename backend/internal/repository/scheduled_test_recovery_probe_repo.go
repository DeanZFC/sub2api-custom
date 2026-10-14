package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestRecoveryProbeRepository = (*scheduledTestResultRepository)(nil)

func (r *scheduledTestResultRepository) StatisticsRecoveryProbeNeeded(ctx context.Context, result *service.ScheduledTestResult) (bool, error) {
	if result == nil || result.ID <= 0 || result.AccountID == nil || result.OutputKind != "statistics" {
		return false, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	config, err := lockProtectionPlan(ctx, tx, result.PlanID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	eligible, err := lockProtectionAccount(ctx, tx, *result.AccountID)
	if err != nil || !eligible {
		return false, err
	}
	state, err := loadProtectionState(ctx, tx, result.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !statisticsProbeStateCurrent(state, result) || !protectionRuleCurrent(config, state.rule) {
		return false, nil
	}
	return protectionResultCurrent(ctx, tx, result.ID)
}

func statisticsProbeStateCurrent(state *protectionState, result *service.ScheduledTestResult) bool {
	return state.blocked && !state.completed && state.automated == "pending" &&
		state.rule.Recovery != nil && state.rule.Recovery.Enabled &&
		state.planID == result.PlanID && state.accountID == *result.AccountID &&
		state.started.Equal(result.StartedAt.Round(time.Microsecond)) &&
		!result.StartedAt.Round(time.Microsecond).Before(state.roundStarted)
}

// All existing plan/account/round locks and checks run before this transition.
// A recovery sweep that already completed this state takes precedence over an
// in-flight probe. Replayed or superseded results cannot release a newer hold.
func completeStatisticsRecoveryProbe(ctx context.Context, tx *sql.Tx, state *protectionState, result *service.ScheduledTestResult) (bool, error) {
	if !statisticsProbeStateCurrent(state, result) {
		return false, nil
	}
	verdict, reason, valid := service.EvaluateScheduledTestRecoveryProbe(state.rule, result)
	if !valid {
		return false, nil
	}
	var freshAfter *time.Time
	if verdict == "pass" {
		freshAfter = &result.OutputStatistics.RecoveryProbe.FinishedAt
	}
	_, err := tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET
	 recovery_phase='',recovery_cooldown_until=NULL,recovery_trial_started_at=$3,
	 recovery_trial_ends_at=NULL,recovery_trial_requests=0
	 WHERE result_id=$1 AND generation=$2`, result.ID, state.generation, freshAfter)
	if err != nil {
		return true, err
	}
	state.recoveryPhase = ""
	state.automated, state.reason, state.completed = verdict, reason, true
	return true, saveProtectionVerdict(ctx, tx, state)
}
