//go:build integration

package qqbot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func spamReportReasonSource(t *testing.T, h *spamReportHarness, age time.Duration) Event {
	t.Helper()
	e := moderationEvent("private-source-message", spamReportTestAd)
	e.Author.MemberID = "reported-member"
	e.MessageScene.Ext = []string{"msg_idx=private-source-reference"}
	var err error
	e.Timestamp, err = json.Marshal(time.Now().UTC().Truncate(time.Microsecond).Add(-age).Format(time.RFC3339Nano))
	require.NoError(t, err)
	require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e, groupMessageCreate))
	return e
}

func spamReportReasonAssertResult(t *testing.T, h *spamReportHarness, job Job, reason spamReportSourceReason, calls int32, replies int) string {
	t.Helper()
	require.Equal(t, calls, h.calls.Load())
	h.assertNoOperations(t)
	var decision, result string
	require.NoError(t, h.db.QueryRow(`SELECT decision_reason,result_text FROM qq_bot_jobs WHERE id=$1`, job.ID).Scan(&decision, &result))
	require.Equal(t, job.DecisionReason, decision)
	require.Equal(t, job.ResultText, result)
	require.Contains(t, decision, string(reason), "the audit must retain the specific verified rejection")
	require.NotEmpty(t, result)
	require.NotEqual(t, spamReportUncertain, result, "a known safety rejection needs its actual cause")
	require.Contains(t, result, "本次不撤回、不禁言")
	if calls > 0 {
		require.NotContains(t, decision, "未调用模型", "a post-review rejection must not erase the completed AI calls")
	}
	for _, sensitive := range []string{spamReportTestAd, "private-source-message", "private-source-reference", "reported-member"} {
		require.NotContains(t, decision, sensitive)
		require.NotContains(t, result, sensitive)
	}
	h.mu.Lock()
	sent := append([]string(nil), h.sent...)
	h.mu.Unlock()
	require.Len(t, sent, replies)
	require.Contains(t, sent[len(sent)-1], result, "the final message must contain the persisted result, not a generic fallback")
	if replies == 2 {
		require.Contains(t, sent[0], "已收到举报，正在评估。")
	}
	return result
}

func TestSpamReportReasonBeforeAdmissionIsSpecificAndSentWithoutAI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason spamReportSourceReason
	}{
		{"after_thirty_minutes", spamSourceExpired},
		{"whitelist", spamSourceWhitelist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamReportHarness(t)
			if tc.reason == spamSourceWhitelist {
				h.c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "reported-member"}}
				_, err := h.s.SaveConfig(context.Background(), h.c)
				require.NoError(t, err)
			}
			age := 8 * time.Minute
			if tc.reason == spamSourceExpired {
				age = 30*time.Minute + time.Second
			}
			e := spamReportReasonSource(t, h, age)
			job, report := h.reportJob(t, e.ID, "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
			result := spamReportReasonAssertResult(t, h, job, tc.reason, 0, 1)
			require.Equal(t, tc.reason.reply(), result)
			if tc.reason == spamSourceExpired {
				require.Contains(t, result, "30 分钟")
				require.NotContains(t, result, "已受理举报的处理时间")
				require.NotContains(t, result, "处理超时")
			} else {
				require.Contains(t, result, "白名单")
			}
			var reports int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_reports`).Scan(&reports))
			require.Zero(t, reports, "rejection precedes durable admission and paid review")
		})
	}
}

func TestSpamReportReasonReviewConfigChangesKeepExactCauseInSentResult(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reason  spamReportSourceReason
		keyword string
		revert  bool
		change  func(*Config)
	}{
		{"whitelist", spamSourceWhitelistChanged, "白名单", false, func(c *Config) {
			c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "reported-member"}}
		}},
		{"chat_authorization", spamSourceChatAuthorizationChanged, "聊天授权", false, func(c *Config) {
			c.GroupAdmins = []GroupAdmin{{AppID: c.AppID, GroupID: "group", MemberID: "reported-member"}}
		}},
		{"whitelist_added_then_removed", spamSourceWhitelistChanged, "白名单", true, func(c *Config) {
			c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "reported-member"}}
		}},
		{"chat_authorization_added_then_removed", spamSourceChatAuthorizationChanged, "聊天授权", true, func(c *Config) {
			c.GroupAdmins = []GroupAdmin{{AppID: c.AppID, GroupID: "group", MemberID: "reported-member"}}
		}},
		{"policy_changed_then_restored", spamSourcePolicyChanged, "配置", true, func(c *Config) {
			c.ChatModel = "changed-review-model"
		}},
		{"reports_disabled", spamSourcePolicyDisabled, "举报", false, func(c *Config) {
			c.Moderation.ReportEnabled = false
		}},
		{"reports_disabled_then_restored", spamSourcePolicyDisabled, "举报", true, func(c *Config) {
			c.Moderation.ReportEnabled = false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamReportHarness(t)
			e := spamReportReasonSource(t, h, 8*time.Minute)
			job, report := h.reportJob(t, e.ID, "reporter")
			var originalCancellation string
			h.hook = func(call int) {
				if call != 2 {
					return
				}
				changed := h.c
				tc.change(&changed)
				_, err := h.s.SaveConfig(context.Background(), changed)
				require.NoError(t, err)
				require.NoError(t, h.db.QueryRow(`SELECT reason FROM qq_bot_spam_reports WHERE id=$1`, job.ID).Scan(&originalCancellation))
				require.Contains(t, originalCancellation, string(tc.reason), "the first invalidation persists its verified cause")
				if tc.revert {
					_, err = h.s.SaveConfig(context.Background(), h.c)
					require.NoError(t, err)
				}
			}
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
			result := spamReportReasonAssertResult(t, h, job, tc.reason, 2, 2)
			require.Contains(t, result, tc.keyword)
			var status, persistedReason string
			require.NoError(t, h.db.QueryRow(`SELECT status,reason FROM qq_bot_spam_reports WHERE id=$1`, job.ID).Scan(&status, &persistedReason))
			require.Equal(t, "cancelled", status)
			require.Equal(t, originalCancellation, persistedReason, "a later save or result write must not replace the original cancellation")
			for _, sensitive := range []string{spamReportTestAd, e.ID, e.Author.MemberID} {
				require.NotContains(t, persistedReason, sensitive)
			}
			if tc.revert {
				current, err := h.s.Config(context.Background())
				require.NoError(t, err)
				require.False(t, current.moderationExempt("group", e.Author.MemberID))
				require.True(t, current.reportsSpam("group"))
				for _, misleading := range []string{"发送者在本群消息治理白名单内", "发送者是本群配置的聊天授权成员", "当前也受消息治理保护", "本群当前未启用"} {
					require.NotContains(t, result, misleading, "a persisted historical cancellation must not claim current protection or policy state")
				}
				require.True(t, strings.Contains(result, "期间") || strings.Contains(result, "曾") || strings.Contains(result, "变更") || strings.Contains(result, "变化"), "explain the historical cancellation: %s", result)
			}
		})
	}
}

func TestSpamReportReasonProcessingExpiryDuringReviewDoesNotClaimAdmissionExpiry(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		name := "confirmation_detects_expiry"
		if maintenance {
			name = "maintenance_already_expired_report"
		}
		t.Run(name, func(t *testing.T) {
			h := newSpamReportHarness(t)
			e := spamReportReasonSource(t, h, 8*time.Minute)
			job, report := h.reportJob(t, e.ID, "reporter")
			h.hook = func(call int) {
				if call != 2 {
					return
				}
				_, err := h.db.Exec(`UPDATE qq_bot_spam_reports SET created_at=clock_timestamp()-INTERVAL '121 seconds' WHERE id=$1`, job.ID)
				require.NoError(t, err)
				if maintenance {
					h.s.maintainSpamReports(context.Background())
				}
			}
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
			result := spamReportReasonAssertResult(t, h, job, spamSourceProcessingExpired, 2, 2)
			require.Equal(t, spamSourceProcessingExpired.reply(), result)
			require.Contains(t, result, "处理")
			require.NotContains(t, result, "30 分钟")
			require.NotContains(t, job.DecisionReason, string(spamSourceExpired))
			// The source itself remains in the admission window; only this admitted
			// report has consumed its bounded processing time.
			source, sourceReason, err := h.s.inspectSpamReportSource(context.Background(), h.db, h.c, "group", e.ID)
			require.NoError(t, err)
			require.NotNil(t, source)
			require.Equal(t, spamSourceOK, sourceReason)
		})
	}
}

func TestSpamReportReasonSourceConflictDuringReviewIsSentAndPersists(t *testing.T) {
	h := newSpamReportHarness(t)
	e := spamReportReasonSource(t, h, 8*time.Minute)
	job, report := h.reportJob(t, e.ID, "reporter")
	h.hook = func(call int) {
		if call != 2 {
			return
		}
		conflict := e
		conflict.Content = "另一条完全不同的正常消息。"
		require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, conflict, groupMessageCreate))
	}
	require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
	result := spamReportReasonAssertResult(t, h, job, spamSourceConflict, 2, 2)
	require.Equal(t, spamSourceConflict.reply(), result)
	require.Contains(t, result, "冲突")
	var status, reason string
	require.NoError(t, h.db.QueryRow(`SELECT status,reason FROM qq_bot_spam_reports WHERE id=$1`, job.ID).Scan(&status, &reason))
	require.Equal(t, "cancelled", status)
	require.Contains(t, reason, string(spamSourceConflict))
	// An identical later observation cannot restore evidence once it conflicted.
	require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e, groupMessageCreate))
	source, sourceReason, err := h.s.inspectSpamReportSource(context.Background(), h.db, h.c, "group", e.ID)
	require.NoError(t, err)
	require.Nil(t, source)
	require.Equal(t, spamSourceConflict, sourceReason)
	h.assertNoOperations(t)
}

func TestSpamReportReasonCancellationWinsUncertainOrFailedModelResult(t *testing.T) {
	for _, mode := range []string{"uncertain", "failed", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamReportHarness(t)
			e := spamReportReasonSource(t, h, 8*time.Minute)
			job, report := h.reportJob(t, e.ID, "reporter")
			if mode == "uncertain" {
				h.outputs = []string{"{\"decision\":\"uncertain\",\"category\":\"\",\"confidence\":0.9,\"evidence\":[],\"reason\":\"private-model-reason\"}"}
			} else if mode == "failed" {
				h.failModel.Store(true)
			} else {
				h.outputs = []string{"private-invalid-model-response"}
			}
			h.hook = func(call int) {
				require.Equal(t, 1, call)
				changed := h.c
				changed.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "reported-member"}}
				_, err := h.s.SaveConfig(context.Background(), changed)
				require.NoError(t, err)
				_, err = h.s.SaveConfig(context.Background(), h.c)
				require.NoError(t, err)
			}
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
			result := spamReportReasonAssertResult(t, h, job, spamSourceWhitelistChanged, 1, 2)
			require.Equal(t, spamSourceWhitelistChanged.reply(), result)
			var status, reason string
			require.NoError(t, h.db.QueryRow("SELECT status,reason FROM qq_bot_spam_reports WHERE id=$1", job.ID).Scan(&status, &reason))
			require.Equal(t, "cancelled", status)
			require.Equal(t, spamSourceWhitelistChanged.audit(), reason)
		})
	}
}
