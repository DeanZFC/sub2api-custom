package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Keep live targets and their summary on the same text-output population.
// Image inputs remain valid text requests; generated media does not.
const passiveGroupTargetTextUsageSQL = `COALESCE(ul.image_count,0)=0
 AND COALESCE(ul.video_count,0)=0 AND COALESCE(ul.image_output_tokens,0)=0
 AND COALESCE(ul.inbound_endpoint,'') !~* '(^|/)(images|videos|audio)(/|$)'
 AND COALESCE(ul.upstream_endpoint,'') !~* '(^|/)(images|videos|audio)(/|$)'`

// Measured group targets do not infer provider cache reuse opportunities. Only
// explicit reported counters (including zero) or positive reads are evidence.
// Non-streaming requests may report real counters too; TTFT still needs a stream.
func passiveGroupTargetSamples(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) (service.PassiveQualityEvidence, error) {
	return passiveActiveGroupTargetSamples(ctx, tx, p, s, now)
}

func beginPassiveTrial(p service.PassiveQualityPolicy, s *service.PassiveQualityState, now time.Time, reason string) {
	s.Phase, s.Reason = "trial", reason
	s.Generation++
	s.TrialStartedAt = now
	s.TrialEndsAt = now.Add(time.Duration(p.TrialSeconds) * time.Second)
	s.Used, s.Inflight, s.Hits = 0, 0, 0
	s.RecoveryHits = 0
	s.SuspicionSince, s.RecoverySince, s.HealthySince, s.StabilityEvidenceAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	s.LastTrialAdmissionAt = time.Time{}
	s.LastTrialObservedAt = time.Time{}
	s.LastEvidenceAt = now
	s.Evidence = service.PassiveQualityEvidence{Complete: true}
}
