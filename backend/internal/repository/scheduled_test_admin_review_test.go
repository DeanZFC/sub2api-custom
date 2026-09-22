package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestAdminVerdictPriority(t *testing.T) {
	for _, tc := range []struct {
		name, automatic, admin, want string
		pass, fail                   int
	}{
		{"admin passes without public votes", "pass", "pass", "pass", 0, 0},
		{"admin overrides public rejection", "pass", "pass", "pass", 0, 100},
		{"admin rejects despite public approval", "pass", "fail", "fail", 100, 0},
		{"administrator overrides ordinary automatic failure", "fail", "pass", "pass", 100, 0},
		{"administrator can decide an ordinary pending result", "pending", "pass", "pass", 100, 0},
		{"ordinary vote threshold stays intact", "pass", "", "pending", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &protectionState{automated: tc.automatic, adminVerdict: tc.admin, rule: service.ScheduledTestProtectionRule{Vote: &service.ScheduledTestVoteConfig{Enabled: true, RejectAbove: 2, PassAtLeast: 3}}}
			verdict, _, _ := combineProtectionVerdict(state, tc.pass, tc.fail)
			require.Equal(t, tc.want, verdict)
		})
	}
}
