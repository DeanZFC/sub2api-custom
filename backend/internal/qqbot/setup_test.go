package qqbot

import (
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestSetupGroupInheritanceAndExceptions(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.AppID = "app"
	c.AllowedGroups = []string{"a", "b"}
	c.Autonomous.Enabled = true
	c.Autonomous.InheritGroups = true
	c.Moderation.Enabled = true
	c.Moderation.InheritGroups = true
	require.NoError(t, validateAutonomous(c))
	require.NoError(t, validateModeration(c))
	require.True(t, c.autonomousGroup("a"))
	require.True(t, c.moderates("a"))
	require.False(t, c.moderates("outside"))
	c.GroupPolicies = []GroupPolicy{{GroupID: "b", DisableAutonomous: true, DisableModeration: true}}
	require.False(t, c.autonomousGroup("b"))
	require.False(t, c.moderates("b"))
	c.Moderation.Groups = []string{"a", "a"}
	require.Error(t, validateModeration(c))
	c.Moderation.Groups = nil
	c.Moderation.InheritGroups = false
	require.Error(t, validateModeration(c))
	c.Autonomous.InheritGroups = false
	require.Error(t, validateAutonomous(c))
}
func TestSetupRemovedGroupsNeverExpandNotificationScope(t *testing.T) {
	c := DefaultConfig()
	c.AllowedGroups = []string{"keep"}
	c.Autonomous.Enabled = true
	c.Autonomous.Groups = []string{"removed"}
	c.PriceNotifications.Enabled = true
	c.PriceNotifications.Groups = []string{"removed"}
	c.GroupPolicies = []GroupPolicy{{GroupID: "keep", Label: "label"}, {GroupID: "removed"}}
	c.Moderation.Exemptions = []ModerationExemption{{GroupID: "removed", MemberID: "member"}}
	reconcileRemovedGroups(&c)
	require.False(t, c.PriceNotifications.Enabled)
	require.False(t, c.Autonomous.Enabled)
	require.Empty(t, c.Moderation.Exemptions)
	require.Len(t, c.GroupPolicies, 1)
}
func TestSetupBudgetValidationAndProbeFingerprint(t *testing.T) {
	c := DefaultConfig()
	for _, v := range []float64{-1, math.NaN(), math.Inf(1), 1000001, .123456789} {
		c.RewardBudgetDaily = &v
		require.Error(t, validateConfig(c))
	}
	for _, v := range []float64{0, .01, .12345678, 1000000} {
		c.RewardBudgetDaily = &v
		require.NoError(t, validateConfig(c))
	}
	c.RewardBudgetDaily = nil
	before := probeFingerprint(c)
	c.AllowedGroups = []string{"a"}
	require.Equal(t, before, probeFingerprint(c))
	c.ChatModel = "new"
	require.NotEqual(t, before, probeFingerprint(c))
	c.ChatDisabled = true
	c.ImageEnabled = false
	require.Equal(t, []string{"qq"}, requiredSetupProbes(c))
	c.ImageEnabled = true
	require.Equal(t, []string{"qq", "image", "image_edit"}, requiredSetupProbes(c))
}
