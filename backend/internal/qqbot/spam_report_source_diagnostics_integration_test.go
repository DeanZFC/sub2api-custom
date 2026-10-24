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

func TestSpamReportSourceDiagnosticsRejectWithoutAIOrPunishment(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reason     spamReportSourceReason
		configure  func(*Config)
		editSource func(*Event)
		eventType  string
		mutate     func(*testing.T, *spamReportHarness, Event)
		reference  string
	}{
		{name: "not_found", reason: spamSourceNotFound, reference: "not-retained"},
		{name: "expired", reason: spamSourceExpired, mutate: func(t *testing.T, h *spamReportHarness, _ Event) {
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET sent_at=clock_timestamp()-INTERVAL '30 minutes 1 second'`)
			require.NoError(t, err)
		}},
		{name: "ambiguous", reason: spamSourceAmbiguous, reference: "source-ref", mutate: func(t *testing.T, h *spamReportHarness, e Event) {
			e.ID, e.Author.MemberID, e.Author.MemberRole = "other", "other-member", "admin"
			require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e))
		}},
		{name: "whitelist", reason: spamSourceWhitelist, configure: func(c *Config) {
			c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "reported-member"}}
		}},
		{name: "chat_authorized", reason: spamSourceChatAuthorized, configure: func(c *Config) {
			c.GroupAdmins = []GroupAdmin{{AppID: c.AppID, GroupID: "group", MemberID: "reported-member"}}
		}},
		{name: "qq_admin", reason: spamSourceQQAdmin, editSource: func(e *Event) { e.Author.MemberRole = "admin" }},
		{name: "qq_owner", reason: spamSourceQQAdmin, editSource: func(e *Event) { e.Author.MemberRole = "owner" }},
		{name: "bot", reason: spamSourceBot, editSource: func(e *Event) { e.Author.Bot = true }},
		{name: "role_missing", reason: spamSourceRoleMissing, editSource: func(e *Event) { e.Author.MemberRole = "" }},
		{name: "role_unknown", reason: spamSourceRoleMissing, editSource: func(e *Event) { e.Author.MemberRole = "unknown" }},
		{name: "role_changed", reason: spamSourceRoleChanged, mutate: func(t *testing.T, h *spamReportHarness, e Event) {
			e.ID, e.Author.MemberRole = "later-message", "admin"
			e.MessageScene.Ext = []string{"msg_idx=later-ref"}
			e.Timestamp, _ = json.Marshal(time.Now().UTC().Add(time.Millisecond).Format(time.RFC3339Nano))
			require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e))
		}},
		{name: "policy_inactive", reason: spamSourcePolicyInactive, mutate: func(_ *testing.T, h *spamReportHarness, _ Event) {
			h.c.Moderation.ReportEnabled = false
		}},
		{name: "policy_changed", reason: spamSourcePolicyChanged, mutate: func(_ *testing.T, h *spamReportHarness, _ Event) {
			h.c.ChatModel = "changed-review-model"
		}},
		{name: "quoted_content", reason: spamSourceIneligible, editSource: func(e *Event) { e.MessageReference.MessageID = "another-source" }},
		{name: "attachment", reason: spamSourceIneligible, editSource: func(e *Event) {
			e.Attachments = []Attachment{{URL: "https://example.invalid/source.png"}}
		}},
		{name: "empty_content", reason: spamSourceIneligible, editSource: func(e *Event) { e.Content = " \n\t" }},
		{name: "duplicate_conflict", reason: spamSourceConflict, mutate: func(t *testing.T, h *spamReportHarness, e Event) {
			e.Content = "另一条完全不同的正常消息。"
			require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e))
		}},
		{name: "mention_only", reason: spamSourceMentionOnly, eventType: groupAtMessageCreate},
		{name: "legacy_provenance", reason: spamSourceLegacy, mutate: func(t *testing.T, h *spamReportHarness, _ Event) {
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET eligible=false,content_hash='legacy-hash'`)
			require.NoError(t, err)
		}},
		{name: "future_time", reason: spamSourceInvalidTime, mutate: func(t *testing.T, h *spamReportHarness, _ Event) {
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET sent_at=clock_timestamp()+INTERVAL '6 seconds'`)
			require.NoError(t, err)
		}},
		{name: "invalid_reference", reason: spamSourceInvalidRef, reference: strings.Repeat("x", 1025)},
		{name: "unverified", reason: spamSourceUnverified, mutate: func(t *testing.T, h *spamReportHarness, _ Event) {
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET eligible=false,content_hash='{"v":1,"kind":"invalid"}'`)
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamReportHarness(t)
			if tc.configure != nil {
				tc.configure(&h.c)
				_, err := h.s.SaveConfig(context.Background(), h.c)
				require.NoError(t, err)
			}
			e := moderationEvent("source", spamReportTestAd)
			e.Author.MemberID = "reported-member"
			e.MessageScene.Ext = []string{"msg_idx=source-ref"}
			if tc.editSource != nil {
				tc.editSource(&e)
			}
			eventType := tc.eventType
			if eventType == "" {
				eventType = groupMessageCreate
			}
			require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e, eventType))
			if tc.mutate != nil {
				tc.mutate(t, h, e)
			}
			reference := tc.reference
			if reference == "" {
				reference = "source"
			}
			source, reason, err := h.s.inspectSpamReportSource(context.Background(), h.db, h.c, "group", reference)
			require.NoError(t, err)
			require.Nil(t, source)
			require.Equal(t, tc.reason, reason)
			wrapped, err := h.s.loadSpamReportSource(context.Background(), h.db, h.c, "group", reference)
			require.NoError(t, err)
			require.Nil(t, wrapped, "execution and confirmation keep the fail-closed wrapper")
			job, report := h.reportJob(t, reference, "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
			require.Zero(t, h.calls.Load())
			h.assertNoOperations(t)
			var count int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_reports`).Scan(&count))
			require.Zero(t, count)
			var decision, reply string
			require.NoError(t, h.db.QueryRow(`SELECT decision_reason,result_text FROM qq_bot_jobs WHERE id=$1`, job.ID).Scan(&decision, &reply))
			if tc.reason == spamSourceInvalidRef {
				// Event.referenceID rejects oversized references before source lookup.
				require.Equal(t, "用法不符，必须引用原消息且不附加参数。", decision)
				require.Contains(t, reply, "请引用要举报的原消息")
			} else {
				require.Equal(t, tc.reason.decision(), decision)
				require.Equal(t, tc.reason.reply(), reply)
			}
			require.NotContains(t, decision, spamReportTestAd)
			require.NotContains(t, reply, "reported-member")
			h.mu.Lock()
			require.Len(t, h.sent, 1)
			require.Contains(t, h.sent[0], reply)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportSourceDiagnosticsAcceptOriginalWithoutMentionOrChatAuthorization(t *testing.T) {
	h := newSpamReportHarness(t)
	e := h.source(t, "source", spamReportTestAd)
	source, reason, err := h.s.inspectSpamReportSource(context.Background(), h.db, h.c, "group", e.ID)
	require.NoError(t, err)
	require.Equal(t, spamSourceOK, reason)
	require.NotNil(t, source)
	require.Equal(t, e.Content, source.Content)
	require.False(t, h.c.moderationExempt("group", e.Author.MemberID))
	require.NotContains(t, e.Content, "<@")
}

func TestSpamReportSourceDiagnosticsEligibilityCannotBypassFullCallbackProvenance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hash   string
		reason spamReportSourceReason
	}{
		{"legacy_eligible", "legacy-unmarked-hash", spamSourceLegacy},
		{"at_eligible", `{"v":1,"kind":"at"}`, spamSourceMentionOnly},
		{"invalid_eligible", `{"v":1,"kind":"invalid","reason":"callback_conflict"}`, spamSourceConflict},
		{"unknown_version_eligible", `{"v":2,"kind":"full"}`, spamSourceLegacy},
		{"unknown_kind_eligible", `{"v":1,"kind":"unknown"}`, spamSourceUnverified},
		{"missing_hash_eligible", `{"v":1,"kind":"full"}`, spamSourceUnverified},
		{"full_with_rejection_eligible", `{"v":1,"kind":"full","reason":"ineligible_content"}`, spamSourceIneligible},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamReportHarness(t)
			h.source(t, "source", spamReportTestAd)
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET eligible=true,content_hash=$1`, tc.hash)
			require.NoError(t, err)
			source, reason, err := h.s.inspectSpamReportSource(context.Background(), h.db, h.c, "group", "source")
			require.NoError(t, err)
			require.Nil(t, source)
			require.Equal(t, tc.reason, reason)
			wrapped, err := h.s.loadSpamReportSource(context.Background(), h.db, h.c, "group", "source")
			require.NoError(t, err)
			require.Nil(t, wrapped)
			job, report := h.reportJob(t, "source", "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &job, report, ""))
			require.Zero(t, h.calls.Load())
			h.assertNoOperations(t)
			var count int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_reports`).Scan(&count))
			require.Zero(t, count)
			var decision, reply string
			require.NoError(t, h.db.QueryRow(`SELECT decision_reason,result_text FROM qq_bot_jobs WHERE id=$1`, job.ID).Scan(&decision, &reply))
			require.Equal(t, tc.reason.decision(), decision)
			require.Equal(t, tc.reason.reply(), reply)
		})
	}
}
