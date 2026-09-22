package repository

import (
	"reflect"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Public visibility can change immediately without invalidating the completed
// result, existing ballots, administrator decisions or independent holds.
func protectionRuleWithoutPublicVote(rule service.ScheduledTestProtectionRule) service.ScheduledTestProtectionRule {
	if rule.Vote != nil {
		vote := *rule.Vote
		vote.PublicEnabled = false
		rule.Vote = &vote
	}
	return rule
}

func protectionConfigWithoutPublicVote(config service.ScheduledTestProtectionConfig) service.ScheduledTestProtectionConfig {
	if config.Rules != nil {
		rules := make([]service.ScheduledTestProtectionRule, len(config.Rules))
		for i, rule := range config.Rules {
			rules[i] = protectionRuleWithoutPublicVote(rule)
		}
		config.Rules = rules
	}
	return config
}

func protectionConfigSamePolicy(a, b service.ScheduledTestProtectionConfig) bool {
	return reflect.DeepEqual(protectionConfigWithoutPublicVote(a), protectionConfigWithoutPublicVote(b))
}

func protectionPublicVoteCurrent(config *service.ScheduledTestProtectionConfig, rule service.ScheduledTestProtectionRule) bool {
	if !protectionRuleCurrent(config, rule) {
		return false
	}
	for _, current := range config.Rules {
		if current.TestDefinitionID == rule.TestDefinitionID {
			return current.Vote != nil && current.Vote.Enabled && current.Vote.PublicEnabled
		}
	}
	return false
}
