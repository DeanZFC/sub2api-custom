package repository

import (
	"database/sql"
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

func TestScheduledTestAdminApprovalRetainsDurationCondition(t *testing.T) {
	for _, tc := range []struct {
		ms    int64
		valid bool
		want  string
	}{
		{60001, true, "fail"}, {60000, true, "pass"}, {0, false, "pending"},
	} {
		state := &protectionState{completed: true, automated: "pass", adminVerdict: "pass", resultStatus: sql.NullString{String: "success", Valid: true}, latencyMs: sql.NullInt64{Int64: tc.ms, Valid: tc.valid}, rule: service.ScheduledTestProtectionRule{
			Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}, Thresholds: []service.ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 60000}},
		}}
		verdict, _, _ := combineProtectionVerdict(state, 100, 0)
		require.Equal(t, tc.want, verdict)
	}
}

func TestScheduledTestHTMLReviewDefaultsToPassUntilRejected(t *testing.T) {
	for _, tc := range []struct {
		name, kind, status, automatic, admin, want string
		completed                                  bool
		fail                                       int
	}{
		{name: "no manual decision", kind: "html", status: "success", automatic: "pass", completed: true, want: "pass"},
		{name: "administrator rejects", kind: "html", status: "success", automatic: "pass", admin: "fail", completed: true, want: "fail"},
		{name: "administrator corrects automatic failure", kind: "html", status: "success", automatic: "fail", admin: "pass", completed: true, want: "pass"},
		{name: "automatic rejects", kind: "html", status: "success", automatic: "fail", completed: true, want: "fail"},
		{name: "human votes reject", kind: "html", status: "success", automatic: "pass", completed: true, fail: 3, want: "fail"},
		{name: "human rejection below threshold", kind: "html", status: "success", automatic: "pass", completed: true, fail: 2, want: "pass"},
		{name: "numeric review still requires approval", kind: "number", status: "success", automatic: "pass", completed: true, want: "pending"},
		{name: "generation unfinished", kind: "html", status: "running", automatic: "pending", want: "pending"},
		{name: "generation failed", kind: "html", status: "failed", automatic: "pending", completed: true, want: "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &protectionState{outputKind: tc.kind, completed: tc.completed, resultStatus: sql.NullString{String: tc.status, Valid: true}, automated: tc.automatic, adminVerdict: tc.admin,
				rule: service.ScheduledTestProtectionRule{Vote: &service.ScheduledTestVoteConfig{Enabled: true, RejectAbove: 2, PassAtLeast: 3}}}
			verdict, _, _ := combineProtectionVerdict(state, 0, tc.fail)
			require.Equal(t, tc.want, verdict)
		})
	}
}
