package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Other automatic protection owns an independent hold and must not prevent
// this protection from proving its own recovery. Manual status still blocks
// execution. Serialize the same account/model across policies and instances.
func passiveActiveRecoveryAllowed(ctx context.Context, tx *sql.Tx, aid int64, model string, pid int64, now time.Time) (bool, error) {
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT a.status IN ('active','quality_paused') AND a.schedulable AND a.deleted_at IS NULL
 AND NOT COALESCE(a.temp_unschedulable_until>$4,false) AND NOT COALESCE(a.rate_limit_reset_at>$4,false)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_states s WHERE s.account_id=a.id AND s.model_key=$2 AND s.policy_id<>$3 AND s.data->>'phase'='probing' AND (s.data->>'next_at')::timestamptz>$4)
 FROM accounts a WHERE a.id=$1`, aid, model, pid, now).Scan(&allowed)
	return allowed, err
}

// FinishActiveRecovery never trusts a success flag alone. It rechecks the
// current policy, job lease, account and every enabled metric, then releases
// only this policy's hold. The historical real-traffic evidence stays intact.
func (r *passiveQualityRepository) FinishActiveRecovery(ctx context.Context, probe service.PassiveQualityState, result service.PassiveQualityActiveRecoveryResult, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	p, s, err := r.lockedState(ctx, tx, probe.PolicyID, probe.AccountID, probe.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	valid, err := passiveTarget(ctx, tx, p, s.AccountID)
	if err != nil {
		return err
	}
	if !valid || !p.Enabled || p.Mode != "enforce" || !p.IsActiveRecovery() || s.Generation != probe.Generation || s.Version != probe.Version || s.Version != p.DecisionVersion() || s.Phase != "probing" || !now.Before(s.NextAt) {
		return nil
	}
	allowed, err := passiveActiveRecoveryAllowed(ctx, tx, s.AccountID, s.Model, p.ID, now)
	if err != nil {
		return err
	}
	verdict, reason := validateActiveRecoveryResult(p.PassiveQualityConfig, s, result, now)
	if !allowed {
		verdict, reason = "pending", "account_unavailable_for_recovery"
	}
	if verdict != "pending" || result.Verdict != "deferred" || !allowed {
		result.Verdict = verdict
	}
	result.Passed, result.Reason = verdict == "pass", reason
	s.ActiveRecovery = &result
	s.Generation++
	s.UpdatedAt = now
	s.ActiveRecoveryConfig = nil
	switch verdict {
	case "pass":
		s.Phase, s.Reason = "healthy", "active_recovery_passed"
		s.RecoveredAt, s.LastEvidenceAt = now, now
		s.ProbeStartedAt, s.NextAt = time.Time{}, time.Time{}
		s.CacheAllocationReduced = false
		s.ActiveConfirmations = nil
		s.Hits, s.RecoveryHits = 0, 0
		s.SuspicionSince, s.RecoverySince, s.HealthySince, s.StabilityEvidenceAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
		s.ConfirmationMetric = ""
	case "fail":
		service.PassiveQualityPause(p.PassiveQualityConfig, &s, now, "active_recovery_failed: "+reason)
	default:
		s.Phase, s.Reason = "paused", "active_recovery_pending: "+reason
		s.NextAt = now.Add(time.Minute)
		if result.DeferredUntil != nil && result.DeferredUntil.After(s.NextAt) {
			s.NextAt = *result.DeferredUntil
		}
	}
	if err = savePassiveState(ctx, tx, s); err != nil {
		return err
	}
	if err = passiveEvent(ctx, tx, s, "active_recovery_"+verdict); err != nil {
		return err
	}
	if err = reconcilePassiveAccount(ctx, tx, s.AccountID); err != nil {
		return err
	}
	return tx.Commit()
}

func validateActiveRecoveryResult(c service.PassiveQualityConfig, s service.PassiveQualityState, r service.PassiveQualityActiveRecoveryResult, now time.Time) (string, string) {
	if r.Verdict == "deferred" || r.DeferredUntil != nil {
		return "pending", r.Reason
	}
	if r.StartedAt.Before(s.ProbeStartedAt) || r.FinishedAt.Before(r.StartedAt) || r.FinishedAt.After(now) || !r.FinishedAt.Before(s.NextAt) {
		return "pending", "invalid_active_recovery_chronology"
	}
	if r.Verdict == "fail" {
		return "fail", r.Reason
	}
	if r.Verdict != "pass" || !r.Passed {
		return "pending", r.Reason
	}
	minimum := max(5, c.RecoverySamples)
	c.RecoverySamples = minimum
	verdict, reason := service.EvaluatePassiveQuality(c, r.Evidence, true)
	if verdict != "pass" {
		return verdict, reason
	}
	confirmations := max(2, c.Confirmations)
	if r.Batches < confirmations || len(r.EvidenceBatches) < confirmations || r.Requests < minimum*confirmations || r.FinishedAt.Sub(r.StartedAt) < max(120*time.Second, time.Duration(c.GroupTargetStability().RecoverySeconds)*time.Second) {
		return "pending", "insufficient_active_recovery_batches"
	}
	var firstBatch, lastBatch time.Time
	for _, batch := range r.EvidenceBatches {
		if (c.CacheRate.Enabled && batch.CacheLatest.IsZero()) || (c.TTFT.Enabled && batch.TTFTLatest.IsZero()) || (c.TPS.Enabled && batch.TPSLatest.IsZero()) {
			return "pending", "missing_active_recovery_metric_time"
		}
		if batch.Latest.Before(r.StartedAt) || batch.Latest.After(r.FinishedAt) || (!lastBatch.IsZero() && !batch.Latest.After(lastBatch)) {
			return "pending", "invalid_active_recovery_batch_chronology"
		}
		if firstBatch.IsZero() {
			firstBatch = batch.Latest
		}
		lastBatch = batch.Latest
		for _, at := range []time.Time{batch.CacheLatest, batch.TTFTLatest, batch.TPSLatest} {
			if !at.IsZero() && (at.Before(r.StartedAt) || at.After(batch.Latest)) {
				return "pending", "invalid_active_recovery_batch_chronology"
			}
		}
		if verdict, reason := service.EvaluatePassiveQuality(c, batch, true); verdict != "pass" {
			return verdict, reason
		}
	}
	if lastBatch.Sub(firstBatch) < max(120*time.Second, time.Duration(c.GroupTargetStability().RecoverySeconds)*time.Second) {
		return "pending", "insufficient_active_recovery_confirmation_duration"
	}
	return "pass", "active_recovery_passed"
}
