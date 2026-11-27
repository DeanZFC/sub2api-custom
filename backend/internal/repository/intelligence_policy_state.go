package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Review edits and terminal result writes take locks in plan -> account order,
// then replay this target's independent rounds before publishing the scheduler event.
func intelligencePlanTx(ctx context.Context, tx *sql.Tx, id int64) (*service.IntelligencePolicy, bool, error) {
	var raw []byte
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT protection->'intelligence',enabled FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE`, id).Scan(&raw, &enabled)
	if err != nil || len(raw) == 0 {
		return nil, enabled, err
	}
	var policy *service.IntelligencePolicy
	err = json.Unmarshal(raw, &policy)
	return policy, enabled, err
}
func (r *scheduledTestResultRepository) finishIntelligence(ctx context.Context, res *service.ScheduledTestResult, query string, args []any) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, enabled, err := intelligencePlanTx(ctx, tx, res.PlanID)
	if err != nil {
		return err
	}
	if err = checkScheduledTestResultLease(ctx, tx, res); err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	ev := res.ExecutionInfo.Intelligence
	if cfg == nil || !enabled || res.AccountID == nil || res.RunID == "" || cfg.Version != ev.PolicyVersion {
		return tx.Commit()
	}
	account := *res.AccountID
	if _, err = lockProtectionAccount(ctx, tx, account); err != nil {
		return err
	}
	var eligible bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT r.protection_revoked AND NOT EXISTS(SELECT 1 FROM intelligence_observations o WHERE o.result_id=r.id AND o.released) AND r.model_id=ANY(p.model_ids) AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused') AND NOT(a.id=ANY(p.excluded_account_ids)) AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(p.group_ids)) FROM scheduled_test_results r JOIN accounts a ON a.id=r.account_id JOIN scheduled_test_plans p ON p.id=r.plan_id WHERE r.id=$1`, res.ID).Scan(&eligible); err != nil {
		return err
	}
	if !eligible {
		return tx.Commit()
	}
	verdict := ev.AutoVerdict
	if verdict == "pass" && (ev.Artwork != "default" || res.OutputHTML == "") {
		verdict = "unknown"
	}
	var deferred *time.Time
	if res.ExecutionInfo.FailureKind == "quality_deferred" {
		deferred = res.ExecutionInfo.NextCheckAt
		if deferred == nil {
			deferred = res.ExecutionInfo.RetryAfter
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO intelligence_observations(result_id,plan_id,account_id,model_key,policy_version,run_id,started_at,finished_at,verdict,auto_isolate,recovery_passes,cooldown_minutes,deferred_until)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(result_id) DO UPDATE SET verdict=EXCLUDED.verdict,finished_at=EXCLUDED.finished_at,deferred_until=EXCLUDED.deferred_until`, res.ID, res.PlanID, account, res.ModelID, cfg.Version, res.RunID, res.StartedAt, res.FinishedAt, verdict, cfg.AutoIsolate, cfg.RecoveryPasses, cfg.CooldownMinutes, deferred)
	if err != nil {
		return err
	}
	round := intelligenceRound{id: res.ID, started: res.StartedAt, version: cfg.Version, verdict: verdict, automatic: cfg.AutoIsolate, required: cfg.RecoveryPasses, cool: cfg.CooldownMinutes, at: res.FinishedAt, deferred: deferred}
	if err = updateIntelligenceState(ctx, tx, res.PlanID, account, res.ModelID, &round); err != nil {
		return err
	}
	return tx.Commit()
}

type intelligenceRound struct {
	id               int64
	started          time.Time
	version, verdict string
	automatic        bool
	required, cool   int
	at               time.Time
	deferred         *time.Time
}
type intelligenceState struct {
	holdVersion      string
	last             int64
	started          *time.Time
	version, verdict string
	blocked          bool
	passes           int
	next             *time.Time
}

func (s *intelligenceState) append(r intelligenceRound) {
	if s.version != r.version {
		s.passes = 0
	}
	s.last = r.id
	s.started = &r.started
	s.version = r.version
	s.verdict = r.verdict
	quietUntil := s.next
	wasBlocked := s.blocked
	delay := 0
	switch r.verdict {
	case "confirmed":
		s.passes = 0
		if r.automatic {
			s.blocked = true
			s.holdVersion = r.version
		}
		if s.blocked {
			delay = r.cool
		}
	case "suspect":
		s.passes = 0
		delay = 5
	case "pass":
		if s.blocked {
			// A manual or periodic run during the quiet period can show a good
			// result, but cannot shorten the required interval between confirmations.
			if s.next != nil && r.started.Before(*s.next) {
				return
			}
			s.passes++
			if s.passes >= r.required {
				s.blocked = false
				s.holdVersion = ""
			} else {
				delay = 5
			}
		} else {
			s.passes = 0
		}
	default:
		s.passes = 0
		if s.blocked {
			delay = r.cool
		}
	}
	s.next = nil
	if delay > 0 {
		t := r.at.Add(time.Duration(delay) * time.Minute)
		s.next = &t
	}
	// A periodic inconclusive run during a hold cannot shorten its quiet period.
	if wasBlocked && s.blocked && quietUntil != nil && s.next != nil && quietUntil.After(*s.next) {
		s.next = quietUntil
	}
	if r.deferred != nil && (s.next == nil || s.next.Before(*r.deferred)) {
		s.next = r.deferred
	}
}

// Ordered new completions advance in constant space. A correction to an
// earlier round replays that target, including any later recovery evidence.
func updateIntelligenceState(ctx context.Context, tx *sql.Tx, planID, accountID int64, model string, newRound *intelligenceRound) error {
	var s intelligenceState
	if newRound != nil {
		err := tx.QueryRowContext(ctx, `SELECT last_result_id,last_started_at,policy_version,hold_version,blocked,passes,verdict,next_check FROM intelligence_model_states WHERE plan_id=$1 AND account_id=$2 AND model_key=$3`, planID, accountID, model).Scan(&s.last, &s.started, &s.version, &s.holdVersion, &s.blocked, &s.passes, &s.verdict, &s.next)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if newRound != nil && (s.started == nil || newRound.started.After(*s.started) || (newRound.started.Equal(*s.started) && newRound.id > s.last)) {
		s.append(*newRound)
	} else {
		s = intelligenceState{}
		rows, err := tx.QueryContext(ctx, `SELECT result_id,started_at,policy_version,CASE WHEN routing_veto THEN 'confirmed' ELSE verdict END,auto_isolate,recovery_passes,cooldown_minutes,finished_at,deferred_until FROM intelligence_observations WHERE plan_id=$1 AND account_id=$2 AND model_key=$3 AND NOT released ORDER BY started_at,result_id`, planID, accountID, model)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r intelligenceRound
			if err = rows.Scan(&r.id, &r.started, &r.version, &r.verdict, &r.automatic, &r.required, &r.cool, &r.at, &r.deferred); err != nil {
				_ = rows.Close()
				return err
			}
			s.append(r)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO intelligence_model_states(plan_id,account_id,model_key,blocked,passes,verdict,next_check,last_result_id,last_started_at,policy_version,hold_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(plan_id,account_id,model_key) DO UPDATE SET blocked=EXCLUDED.blocked,passes=EXCLUDED.passes,verdict=EXCLUDED.verdict,next_check=EXCLUDED.next_check,last_result_id=EXCLUDED.last_result_id,last_started_at=EXCLUDED.last_started_at,policy_version=EXCLUDED.policy_version,hold_version=EXCLUDED.hold_version,updated_at=now()`, planID, accountID, model, s.blocked, s.passes, s.verdict, s.next, s.last, s.started, s.version, s.holdVersion)
	if err != nil {
		return err
	}
	return reconcileProtectionAccount(ctx, tx, accountID)
}
func (r *scheduledTestResultRepository) ReviewIntelligenceArt(ctx context.Context, resultID, admin int64, verdict, reason string) error {
	if admin <= 0 || (verdict != "pass" && verdict != "fail") {
		return fmt.Errorf("invalid artwork review")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var planID, accountID int64
	var model string
	if err = tx.QueryRowContext(ctx, `SELECT o.plan_id,o.account_id,o.model_key FROM intelligence_observations o JOIN scheduled_test_results r ON r.id=o.result_id WHERE o.result_id=$1 AND r.execution_info->'intelligence'->>'artwork'='default' AND r.output_html<>''`, resultID).Scan(&planID, &accountID, &model); err != nil {
		return err
	}
	cfg, enabled, err := intelligencePlanTx(ctx, tx, planID)
	if err != nil {
		return err
	}
	if _, err = lockProtectionAccount(ctx, tx, accountID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO intelligence_artwork_reviews(result_id,verdict,admin_id,reason) VALUES($1,$2,$3,$4) ON CONFLICT(result_id) DO UPDATE SET verdict=EXCLUDED.verdict,admin_id=EXCLUDED.admin_id,reason=EXCLUDED.reason,reviewed_at=now()`, resultID, verdict, admin, reason); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO intelligence_artwork_review_log(result_id,verdict,admin_id,reason) VALUES($1,$2,$3,$4)`, resultID, verdict, admin, reason); err != nil {
		return err
	}
	if cfg != nil && enabled {
		var current bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM intelligence_observations o JOIN scheduled_test_results r ON r.id=o.result_id JOIN scheduled_test_plans p ON p.id=o.plan_id JOIN account_groups ag ON ag.account_id=o.account_id AND ag.group_id=ANY(p.group_ids) WHERE o.result_id=$1 AND NOT o.released AND NOT r.protection_revoked AND o.policy_version=$2 AND NOT(o.account_id=ANY(p.excluded_account_ids)))`, resultID, cfg.Version).Scan(&current); err != nil {
			return err
		}
		if current {
			if _, err = tx.ExecContext(ctx, `UPDATE intelligence_observations SET routing_veto=$2 WHERE result_id=$1`, resultID, verdict == "fail"); err != nil {
				return err
			}
			if err = updateIntelligenceState(ctx, tx, planID, accountID, model, nil); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
