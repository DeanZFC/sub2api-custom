package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestPublicVotingDefaultsClosedAndTogglesIndependently(t *testing.T) {
	rule := service.ScheduledTestProtectionRule{TestDefinitionID: 1, Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 3}}
	old := service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{rule}}
	current := protectionConfigWithoutPublicVote(old)
	require.True(t, protectionRuleCurrent(&old, rule), "administrators retain review access")
	require.False(t, protectionPublicVoteCurrent(&old, rule), "existing rules remain private")
	current.Rules[0].Vote.PublicEnabled = true
	require.True(t, protectionConfigSamePolicy(old, current), "opening only the public entry must not reset the current round")
	require.True(t, protectionPublicVoteCurrent(&current, rule), "public access uses current configuration, not the older state snapshot")
	require.False(t, old.Rules[0].Vote.PublicEnabled, "comparing config must not mutate it")
	current.Rules[0].Vote.PublicEnabled = false
	require.True(t, protectionRuleCurrent(&current, rule))
	require.False(t, protectionPublicVoteCurrent(&current, rule))
	current.Rules[0].Vote.PassAtLeast = 4
	require.False(t, protectionConfigSamePolicy(old, current), "changing policy still invalidates the round")
	require.False(t, protectionRuleCurrent(&current, rule))
}

func TestScheduledTestPublicVotingPolicyFieldsInvalidateRound(t *testing.T) {
	rule := service.ScheduledTestProtectionRule{TestDefinitionID: 1, Priority: 0, Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}}
	old := service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{rule}}
	current := protectionConfigWithoutPublicVote(old)
	current.Rules[0].Priority = 100
	require.False(t, protectionConfigSamePolicy(old, current))
	current.Rules[0].Priority = 0
	current.Rules[0].RequiredPass = true
	require.False(t, protectionConfigSamePolicy(old, current))
}
