package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOrdinaryPassiveTrialExpirySemanticsRemainUnchanged(t *testing.T) {
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Enabled: true, Mode: "enforce", MinSamples: 5, NewSamples: 2, Confirmations: 2, RecoverySamples: 5, CooldownSeconds: 30, MaxCooldownSeconds: 300, ErrorRate: service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}}}
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	s := service.PassiveQualityState{AccountID: 62, Phase: "trial", Generation: 7, Backoff: 3, TrialStartedAt: now.Add(-time.Minute), TrialEndsAt: now}
	advancePassiveObserved(p, &s, service.PassiveQualityEvidence{Complete: true}, now)
	require.Equal(t, "paused", s.Phase)
	require.Equal(t, "trial_expired: insufficient_samples", s.Reason)
	require.Equal(t, 4, s.Backoff)
}
