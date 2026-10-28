package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupTargetStabilityDefaultsPreservePolicyEvidence(t *testing.T) {
	old := service.PassiveQualityConfig{Purpose: "group_quality_target", Name: "target", GroupIDs: []int64{8}}
	next := old
	defaults := old.GroupTargetStability()
	next.Stability = &defaults
	unchanged, metadataOnly := passivePolicyChange(old, next)
	require.True(t, unchanged)
	require.True(t, metadataOnly)
	next.Name = "renamed"
	unchanged, metadataOnly = passivePolicyChange(old, next)
	require.False(t, unchanged)
	require.True(t, metadataOnly)
	for _, field := range []string{"availability", "performance", "recovery", "allocation"} {
		t.Run(field, func(t *testing.T) {
			settings := old.GroupTargetStability()
			switch field {
			case "availability":
				settings.AvailabilityPauseSeconds = 0
			case "performance":
				settings.PerformancePauseSeconds++
			case "recovery":
				settings.RecoverySeconds++
			case "allocation":
				settings.NewSessionPercent++
			}
			changed := old
			changed.Stability = &settings
			unchanged, metadataOnly := passivePolicyChange(old, changed)
			require.False(t, unchanged)
			require.False(t, metadataOnly)
		})
	}
}
