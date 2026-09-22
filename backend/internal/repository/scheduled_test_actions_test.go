package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func routingTestRule(verdict string, priority int, review bool) protectionRoutingRule {
	rule := service.ScheduledTestProtectionRule{Priority: priority, OnPass: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{10}}, OnFail: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{8}}}
	if review {
		rule.Vote = &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}
	}
	return protectionRoutingRule{currentVerdict: verdict, automated: verdict, rule: rule}
}
func TestProtectionRoutingChanges(t *testing.T) {
	current := map[int64]bool{8: true, 99: true}
	tests := []struct {
		name  string
		rules []protectionRoutingRule
		want  []int64
	}{
		{"same priority waits for all automatic checks", []protectionRoutingRule{routingTestRule("pass", 0, false), routingTestRule("pending", 0, false)}, nil},
		{"same priority all pass", []protectionRoutingRule{routingTestRule("pass", 0, false), routingTestRule("pass", 0, false)}, []int64{10}},
		{"same priority failure wins", []protectionRoutingRule{routingTestRule("pass", 0, false), routingTestRule("fail", 0, false)}, []int64{8}},
		{"pending review permits automatic tier", []protectionRoutingRule{routingTestRule("pass", 0, false), routingTestRule("pending", 100, true)}, []int64{10}},
		{"higher conclusive review overrides automatic failure", []protectionRoutingRule{routingTestRule("fail", 0, false), routingTestRule("pass", 100, true)}, []int64{10}},
		{"higher failure overrides automatic pass", []protectionRoutingRule{routingTestRule("pass", 0, false), routingTestRule("fail", 100, true)}, []int64{8}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for n := 0; n < 2; n++ {
				changes := protectionRoutingChanges(tc.rules, current)
				if tc.want == nil {
					require.Empty(t, changes)
				} else {
					require.Len(t, changes, 1)
					require.Equal(t, tc.want, sortedProtectionIDs(changes[0].desired))
					require.True(t, changes[0].scope[99], "replacement removes unrelated groups")
				}
				tc.rules[0], tc.rules[1] = tc.rules[1], tc.rules[0]
			}
		})
	}
	t.Run("administrator overrides higher automatic or public tier", func(t *testing.T) {
		admin := routingTestRule("pass", 0, true)
		admin.adminVerdict = "pass"
		changes := protectionRoutingChanges([]protectionRoutingRule{admin, routingTestRule("fail", 100, false)}, current)
		require.Len(t, changes, 1)
		require.Equal(t, []int64{10}, sortedProtectionIDs(changes[0].desired))
	})
	t.Run("equal priority administrator rejection wins", func(t *testing.T) {
		pass, fail := routingTestRule("pass", 10, true), routingTestRule("fail", 10, true)
		pass.adminVerdict = "pass"
		fail.adminVerdict = "fail"
		changes := protectionRoutingChanges([]protectionRoutingRule{pass, fail}, current)
		require.Len(t, changes, 1)
		require.Equal(t, []int64{8}, sortedProtectionIDs(changes[0].desired))
	})
	t.Run("required automatic checks cannot be bypassed", func(t *testing.T) {
		for _, verdict := range []string{"pending", "fail", "pass"} {
			required := routingTestRule(verdict, 0, false)
			required.rule.RequiredPass = true
			required.rule.OnPass = nil
			admin := routingTestRule("pass", 100, true)
			admin.adminVerdict = "pass"
			changes := protectionRoutingChanges([]protectionRoutingRule{required, admin}, current)
			switch verdict {
			case "pending":
				require.Empty(t, changes)
			case "fail":
				require.Len(t, changes, 1)
				require.Equal(t, []int64{8}, sortedProtectionIDs(changes[0].desired))
			case "pass":
				require.Len(t, changes, 1)
				require.Equal(t, []int64{10}, sortedProtectionIDs(changes[0].desired))
			}
		}
	})
	t.Run("pending new round ignores old routing verdict", func(t *testing.T) {
		old := routingTestRule("pass", 0, false)
		old.currentVerdict = "pending"
		old.automated = "pending"
		require.Empty(t, protectionRoutingChanges([]protectionRoutingRule{old}, current))
	})
	t.Run("explicit keep prevents lower tier move", func(t *testing.T) {
		high := routingTestRule("fail", 100, true)
		high.rule.OnFail = &service.ScheduledTestOutcomeAction{GroupMode: "keep"}
		require.Empty(t, protectionRoutingChanges([]protectionRoutingRule{routingTestRule("pass", 0, false), high}, current))
	})
	t.Run("required automatic failure wins even if that rule has an administrator pass", func(t *testing.T) {
		required := routingTestRule("pass", 0, false)
		required.rule.RequiredPass = true
		required.automated = "fail"
		required.adminVerdict = "pass"
		changes := protectionRoutingChanges([]protectionRoutingRule{required}, current)
		require.Len(t, changes, 1)
		require.Equal(t, []int64{8}, sortedProtectionIDs(changes[0].desired))
	})
	t.Run("required failure without assignment permits a downgrade", func(t *testing.T) {
		for _, admin := range []bool{false, true} {
			required := routingTestRule("fail", 100, false)
			required.rule.RequiredPass = true
			required.rule.OnFail = &service.ScheduledTestOutcomeAction{GroupMode: "keep"}
			lower := routingTestRule("fail", 0, true)
			if admin {
				lower.adminVerdict = "fail"
			}
			changes := protectionRoutingChanges([]protectionRoutingRule{required, lower}, current)
			require.Len(t, changes, 1)
			require.Equal(t, []int64{8}, sortedProtectionIDs(changes[0].desired))
		}
	})
	t.Run("observation without a group action cannot swallow lower routing", func(t *testing.T) {
		for _, verdict := range []string{"pass", "fail", "pending"} {
			observation := routingTestRule(verdict, 100, false)
			observation.rule.OnPass = nil
			observation.rule.OnFail = nil
			changes := protectionRoutingChanges([]protectionRoutingRule{observation, routingTestRule("pass", 0, false)}, current)
			require.Len(t, changes, 1)
			require.Equal(t, []int64{10}, sortedProtectionIDs(changes[0].desired))
		}
	})

}
