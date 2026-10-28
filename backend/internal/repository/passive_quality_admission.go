package repository

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Pace only bounded group-target recovery trials. The last fresh batch is
// reserved until the observed recovery clock has elapsed, so a busy account
// cannot exhaust its finite trial allowance before it can prove recovery.
// Called under the existing account/state transaction locks across instances.
func passiveTrialAdmissionReady(c service.PassiveQualityConfig, s service.PassiveQualityState, now time.Time) bool {
	if !c.IsGroupQualityTarget() {
		return true
	}
	batch := c.GroupTargetRecoveryBatchSize()
	if s.Used < batch {
		return true
	}
	if s.Hits < c.GroupTargetRecoveryConfirmations() && s.Used <= c.TrialRequests-batch && s.Used%batch == 0 && s.LastTrialAdmissionAt.After(s.LastTrialObservedAt) {
		// Wait until a sweep consumes the preceding full batch. Otherwise one
		// sweep could merge several batches and exhaust a high-confirmation
		// policy's budget before all its confirmations can be observed. Once
		// confirmed, only time pacing and the finite cap remain; a large full
		// sample minimum must not require one sweep per extra small batch.
		return false
	}
	if c.GroupTargetStability().RecoverySeconds <= 0 {
		return true
	}
	duration := time.Duration(c.GroupTargetStability().RecoverySeconds) * time.Second
	if s.Used >= c.TrialRequests-batch {
		// Release the complete reserved batch together. Pacing these final
		// requests individually could push the last confirmation past expiry.
		return !s.RecoverySince.IsZero() && !now.Before(s.RecoverySince.Add(duration))
	}
	// Thirty seconds cover the normal sweep cadence and asynchronous usage
	// delivery. A monotonic last-admission time also bounds reopening bursts.
	interval := passiveTrialAdmissionInterval(c)
	return s.LastTrialAdmissionAt.IsZero() || !now.Before(s.LastTrialAdmissionAt.Add(interval))
}

func passiveTrialAdmissionInterval(c service.PassiveQualityConfig) time.Duration {
	duration := time.Duration(c.GroupTargetStability().RecoverySeconds) * time.Second
	return (duration + 30*time.Second) / time.Duration(max(1, c.TrialRequests-c.GroupTargetRecoveryBatchSize()))
}

func passiveGroupTargetEvidenceGap(c service.PassiveQualityConfig, trial bool) time.Duration {
	gap := time.Duration(min(c.WindowSeconds, 120)) * time.Second
	if gap <= 0 {
		gap = 120 * time.Second
	}
	if trial && c.GroupTargetStability().RecoverySeconds > 0 {
		// A paced fresh batch may legitimately arrive later than the ordinary
		// traffic gap. Include sweep and telemetry delivery time in its bound.
		gap = max(gap, passiveTrialAdmissionInterval(c)*time.Duration(c.GroupTargetRecoveryBatchSize())+60*time.Second)
	}
	return gap
}
