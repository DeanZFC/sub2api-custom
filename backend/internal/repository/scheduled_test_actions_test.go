package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func routingTestRule(verdict string, pass, fail []int64) protectionRoutingRule {
	return protectionRoutingRule{verdict: verdict, currentVerdict: verdict, rule: service.ScheduledTestProtectionRule{
		OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: pass},
		OnFail: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: fail},
	}}
}

func TestProtectionRoutingChanges(t *testing.T) {
	current := map[int64]bool{8: true, 99: true}
	for _, tc := range []struct {
		name     string
		verdicts []string
		want     []int64
		changes  int
	}{
		{"initial type waits for other type", []string{"pass", "pending"}, nil, 0},
		{"all pass promotes to multiple groups", []string{"pass", "pass"}, []int64{10, 11}, 1},
		{"failure wins regardless of pass", []string{"pass", "fail"}, []int64{8}, 1},
		{"failure can downgrade before first vote", []string{"pending", "fail"}, []int64{8}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := []protectionRoutingRule{routingTestRule(tc.verdicts[0], []int64{10, 11}, []int64{8}), routingTestRule(tc.verdicts[1], []int64{10, 11}, []int64{8})}
			for n := 0; n < 2; n++ {
				changes := protectionRoutingChanges(rules, current)
				require.Len(t, changes, tc.changes)
				if len(changes) > 0 {
					require.Equal(t, tc.want, sortedProtectionIDs(changes[0].desired))
					require.Equal(t, []int64{8, 10, 11}, sortedProtectionIDs(changes[0].scope))
				}
				rules[0], rules[1] = rules[1], rules[0]
			}
		})
	}
	t.Run("disjoint tiers do not block each other", func(t *testing.T) {
		changes := protectionRoutingChanges([]protectionRoutingRule{routingTestRule("pass", []int64{10}, []int64{8}), routingTestRule("pending", []int64{20}, []int64{18})}, current)
		require.Len(t, changes, 1)
		require.Equal(t, []int64{10}, sortedProtectionIDs(changes[0].desired))
	})
	t.Run("transitively overlapping scopes form one tier", func(t *testing.T) {
		changes := protectionRoutingChanges([]protectionRoutingRule{routingTestRule("pass", []int64{10}, []int64{8}), routingTestRule("pass", []int64{20}, []int64{18}), routingTestRule("fail", []int64{10, 20}, []int64{28})}, current)
		require.Len(t, changes, 1)
		require.Equal(t, []int64{28}, sortedProtectionIDs(changes[0].desired))
	})
	t.Run("keep branch does not change groups", func(t *testing.T) {
		rule := routingTestRule("fail", []int64{10}, nil)
		rule.rule.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
		require.Empty(t, protectionRoutingChanges([]protectionRoutingRule{rule}, current))
	})
	t.Run("empty assign removes only managed scope", func(t *testing.T) {
		changes := protectionRoutingChanges([]protectionRoutingRule{routingTestRule("fail", []int64{10}, nil)}, current)
		require.Len(t, changes, 1)
		require.Empty(t, changes[0].desired)
		require.Equal(t, []int64{10}, sortedProtectionIDs(changes[0].scope))
	})
	t.Run("new round cannot promote using previous round pass", func(t *testing.T) {
		rule := routingTestRule("pass", []int64{10}, []int64{8})
		rule.currentVerdict = "pending"
		require.Empty(t, protectionRoutingChanges([]protectionRoutingRule{rule}, current))
	})
}
