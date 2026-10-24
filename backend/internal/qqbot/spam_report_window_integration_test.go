//go:build integration

package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const spamReportWindowSkippedRecallReason = "原消息已超过安全撤回时限，按举报规则仅禁言"

func spamReportWindowEvent(t *testing.T, id, member, content string, age time.Duration) Event {
	t.Helper()
	e := moderationEvent(id, content)
	e.Author.MemberID = member
	e.MessageScene.Ext = []string{"msg_idx=" + id + "-ref"}
	var err error
	e.Timestamp, err = json.Marshal(time.Now().UTC().Truncate(time.Microsecond).Add(-age).Format(time.RFC3339Nano))
	require.NoError(t, err)
	return e
}

func spamReportWindowRemember(t *testing.T, h *spamReportHarness, e Event) {
	t.Helper()
	require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e, groupMessageCreate))
}

func spamReportWindowAssertMuteOnlyQueue(t *testing.T, db *sql.DB, reportID string) time.Time {
	t.Helper()
	var reportStatus, recallStatus, recallReason, muteStatus string
	var created, muteExpires time.Time
	var recallFinished sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT r.status,r.created_at,recall.status,recall.error,recall.finished_at,mute.status,mute.expires_at
 FROM qq_bot_spam_reports r
 JOIN qq_bot_moderation_operations recall ON recall.incident_id=r.incident_id AND recall.kind='recall'
 JOIN qq_bot_moderation_operations mute ON mute.incident_id=r.incident_id AND mute.kind='mute'
 WHERE r.id=$1`, reportID).Scan(&reportStatus, &created, &recallStatus, &recallReason, &recallFinished, &muteStatus, &muteExpires))
	require.Equal(t, "queued", reportStatus)
	require.Equal(t, "skipped", recallStatus)
	require.Equal(t, spamReportWindowSkippedRecallReason, recallReason)
	require.True(t, recallFinished.Valid, "skipped recall must be an explicit terminal decision")
	require.Equal(t, "queued", muteStatus)
	require.WithinDuration(t, created.Add(2*time.Minute), muteExpires, time.Millisecond, "the durable admission starts a bounded processing window")
	var operations int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_moderation_operations o JOIN qq_bot_spam_reports r ON r.incident_id=o.incident_id WHERE r.id=$1`, reportID).Scan(&operations))
	require.Equal(t, 2, operations)
	return created
}

func TestSpamReportWindowOlderMessagesReceiveTwoReviewsAndExplicitMuteOnlyReply(t *testing.T) {
	for _, age := range []time.Duration{8 * time.Minute, 29 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			h := newSpamReportHarness(t)
			e := spamReportWindowEvent(t, "source", "reported-member", spamReportTestAd, age)
			spamReportWindowRemember(t, h, e)
			j, report := h.reportJob(t, "source", "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &j, report, ""))
			require.EqualValues(t, 2, h.calls.Load())
			created := spamReportWindowAssertMuteOnlyQueue(t, h.db, j.ID)
			require.Equal(t, spamReportMuteOnlyQueued, j.ResultText)
			require.Contains(t, j.ResultText, "禁言")
			require.Contains(t, j.ResultText, "撤回")
			require.NotContains(t, j.ResultText, "已撤回")
			h.mu.Lock()
			require.Len(t, h.sent, 2)
			require.Contains(t, h.sent[0], "已收到举报，正在评估。")
			require.Contains(t, h.sent[1], spamReportMuteOnlyQueued)
			require.Len(t, h.requests, 2)
			require.Equal(t, h.requests[0][1], h.requests[1][1])
			h.mu.Unlock()
			// Another reporter cannot renew the accepted report or its deadline.
			duplicate, duplicateEvent := h.reportJob(t, "source", "another-reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &duplicate, duplicateEvent, ""))
			require.EqualValues(t, 2, h.calls.Load())
			require.Contains(t, duplicate.DecisionReason, "重复举报")
			require.Equal(t, created, spamReportWindowAssertMuteOnlyQueue(t, h.db, j.ID))
			var reports int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_reports`).Scan(&reports))
			require.Equal(t, 1, reports)
		})
	}
}

func TestSpamReportWindowAcceptedReviewMayCrossThirtyMinuteBoundary(t *testing.T) {
	h := newSpamReportHarness(t)
	e := spamReportWindowEvent(t, "source", "reported-member", spamReportTestAd, 30*time.Minute-3*time.Second)
	spamReportWindowRemember(t, h, e)
	var stamp string
	require.NoError(t, json.Unmarshal(e.Timestamp, &stamp))
	sent, err := time.Parse(time.RFC3339Nano, stamp)
	require.NoError(t, err)
	h.hook = func(call int) {
		if call != 1 {
			return
		}
		// The first model call proves durable admission happened before waiting.
		// Cross the real source-age boundary without altering the stored evidence.
		if wait := time.Until(sent.Add(30*time.Minute + 100*time.Millisecond)); wait > 0 {
			time.Sleep(wait)
		}
		h.s.maintainSpamReports(context.Background())
	}
	j, report := h.reportJob(t, "source", "reporter")
	require.NoError(t, h.s.reportSpam(context.Background(), h.c, &j, report, ""))
	require.EqualValues(t, 2, h.calls.Load())
	created := spamReportWindowAssertMuteOnlyQueue(t, h.db, j.ID)
	require.False(t, created.After(sent.Add(30*time.Minute)), "admission remains inside the thirty-minute limit")
	require.True(t, time.Now().After(sent.Add(30*time.Minute)))
	require.Equal(t, spamReportMuteOnlyQueued, j.ResultText)
	// This retained source can finish its admitted report but cannot admit a new one.
	source, reason, err := h.s.inspectSpamReportSource(context.Background(), h.db, h.c, "group", "source")
	require.NoError(t, err)
	require.Nil(t, source)
	require.Equal(t, spamSourceExpired, reason)
}

func TestSpamReportWindowAfterThirtyMinutesRejectsBeforeAI(t *testing.T) {
	for _, age := range []time.Duration{30*time.Minute + time.Second, 31 * time.Minute, 33 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			h := newSpamReportHarness(t)
			e := spamReportWindowEvent(t, "source", "reported-member", spamReportTestAd, 29*time.Minute)
			spamReportWindowRemember(t, h, e)
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET sent_at=clock_timestamp()-($1::double precision * INTERVAL '1 second')`, age.Seconds())
			require.NoError(t, err)
			j, report := h.reportJob(t, "source", "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &j, report, ""))
			require.Zero(t, h.calls.Load())
			h.assertNoOperations(t)
			var reports int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_reports`).Scan(&reports))
			require.Zero(t, reports)
			require.Contains(t, j.DecisionReason, string(spamSourceExpired))
		})
	}
}

func TestSpamReportWindowOldNormalMessagesNeverQueueOnFalsePositiveModelOutput(t *testing.T) {
	for _, mode := range []string{"normal_link", "warning", "fabricated_evidence", "second_review_uncertain"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamReportHarness(t)
			content, calls := spamReportTestAd, int32(1)
			switch mode {
			case "normal_link":
				content = "项目文档在 https://docs.example.com/setup，大家可以参考配置说明。"
				h.outputs = []string{spamReportTestReview(t, "advertising", content)}
			case "warning":
				content = "警惕骗子发这种信息：" + spamReportTestAd
				calls = 0
			case "fabricated_evidence":
				h.outputs = []string{spamReportTestReview(t, "advertising", "低价出售普通会员，立即下单请私聊我。")}
			case "second_review_uncertain":
				h.outputs = append(h.outputs, `{"decision":"uncertain","category":"","confidence":0.9,"evidence":[],"reason":"无法确认商业目的"}`)
				calls = 2
			}
			spamReportWindowRemember(t, h, spamReportWindowEvent(t, "source", "reported-member", content, 8*time.Minute))
			j, report := h.reportJob(t, "source", "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &j, report, ""))
			require.Equal(t, calls, h.calls.Load())
			h.assertNoOperations(t)
			require.Contains(t, j.ResultText, "本次不撤回、不禁言")
			require.NotEqual(t, spamReportUncertain, j.ResultText, "the verified rejection cause is included")
		})
	}
}

func TestSpamReportWindowOldProtectedSourcesDoNotCallAI(t *testing.T) {
	for _, mode := range []string{"whitelist", "chat_authorized", "qq_admin", "qq_owner", "unknown_role", "bot"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamReportHarness(t)
			e := spamReportWindowEvent(t, "source", "reported-member", spamReportTestAd, 29*time.Minute)
			switch mode {
			case "whitelist":
				h.c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: e.Author.MemberID}}
			case "chat_authorized":
				h.c.GroupAdmins = []GroupAdmin{{AppID: h.c.AppID, GroupID: "group", MemberID: e.Author.MemberID}}
			case "qq_admin":
				e.Author.MemberRole = "admin"
			case "qq_owner":
				e.Author.MemberRole = "owner"
			case "unknown_role":
				e.Author.MemberRole = ""
			case "bot":
				e.Author.Bot = true
			}
			_, err := h.s.SaveConfig(context.Background(), h.c)
			require.NoError(t, err)
			spamReportWindowRemember(t, h, e)
			j, report := h.reportJob(t, "source", "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &j, report, ""))
			require.Zero(t, h.calls.Load())
			h.assertNoOperations(t)
			var reports int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_spam_reports`).Scan(&reports))
			require.Zero(t, reports)
		})
	}
}

func TestSpamReportWindowOldReviewPolicyChangesCancelAndCannotRevive(t *testing.T) {
	for _, mode := range []string{"whitelist", "model", "disable_reports"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamReportHarness(t)
			spamReportWindowRemember(t, h, spamReportWindowEvent(t, "source", "reported-member", spamReportTestAd, 29*time.Minute))
			h.hook = func(call int) {
				if call != 2 {
					return
				}
				changed := h.c
				switch mode {
				case "whitelist":
					changed.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "reported-member"}}
				case "model":
					changed.ChatModel = "different-reviewer"
				case "disable_reports":
					changed.Moderation.ReportEnabled = false
				}
				_, err := h.s.SaveConfig(context.Background(), changed)
				require.NoError(t, err)
				_, err = h.s.SaveConfig(context.Background(), h.c)
				require.NoError(t, err, "restoring prior config must not revive the old admission")
			}
			j, report := h.reportJob(t, "source", "reporter")
			require.NoError(t, h.s.reportSpam(context.Background(), h.c, &j, report, ""))
			require.EqualValues(t, 2, h.calls.Load())
			h.assertNoOperations(t)
			var status string
			require.NoError(t, h.db.QueryRow(`SELECT status FROM qq_bot_spam_reports WHERE id=$1`, j.ID).Scan(&status))
			require.Equal(t, "cancelled", status)
		})
	}
}

func TestSpamReportWindowOldWorkerOnlyMutesAndDoesNotDelete(t *testing.T) {
	for _, age := range []time.Duration{8 * time.Minute, 29 * time.Minute} {
		t.Run(age.String(), func(t *testing.T) {
			h := newSpamSourceHarness(t)
			e := spamReportWindowEvent(t, "reported", "target", spamReportTestAd, age)
			id, source := h.stage(t, e)
			require.True(t, h.confirm(t, id, source))
			spamReportWindowAssertMuteOnlyQueue(t, h.db, id)
			h.s.runModeration(context.Background())
			h.s.runModeration(context.Background())
			require.Equal(t, map[string]string{"recall": "skipped", "mute": "succeeded"}, h.operations(t))
			h.mu.Lock()
			require.Equal(t, []string{"POST /v2/groups/group/restrict_chat_setting"}, h.requests)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportWindowEnforcementGraceRequiresTimelyDurableAdmission(t *testing.T) {
	for _, mode := range []string{"valid_grace", "late_admission", "processing_expired"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			id, source := h.stage(t, spamReportWindowEvent(t, "reported", "target", spamReportTestAd, 29*time.Minute))
			var now time.Time
			require.NoError(t, h.db.QueryRow(`SELECT clock_timestamp()`).Scan(&now))
			source.SentAt = now.Add(-30*time.Minute - 30*time.Second).UTC().Truncate(time.Microsecond)
			created := now.Add(-time.Minute)
			if mode == "late_admission" {
				created = now.Add(-10 * time.Second)
			} else if mode == "processing_expired" {
				created = now.Add(-121 * time.Second)
			}
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET sent_at=$1 WHERE message_id=$2`, source.SentAt, source.MessageID)
			require.NoError(t, err)
			_, err = h.db.Exec(`UPDATE qq_bot_spam_reports SET created_at=$1 WHERE id=$2`, created, id)
			require.NoError(t, err)
			h.s.maintainSpamReports(context.Background())
			if mode != "valid_grace" {
				require.False(t, h.confirm(t, id, source))
				require.Empty(t, h.operations(t))
				return
			}
			require.Nil(t, h.source(t, source.MessageID), "default admission must remain closed after thirty minutes")
			require.True(t, h.confirm(t, id, source), "the previously admitted report retains bounded grace")
			spamReportWindowAssertMuteOnlyQueue(t, h.db, id)
			h.s.runModeration(context.Background())
			require.Equal(t, map[string]string{"recall": "skipped", "mute": "succeeded"}, h.operations(t))
			h.mu.Lock()
			require.Equal(t, []string{"POST /v2/groups/group/restrict_chat_setting"}, h.requests)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportWindowQueuedOldMuteExpiresWithProcessingWindow(t *testing.T) {
	h := newSpamSourceHarness(t)
	id, source := h.stage(t, spamReportWindowEvent(t, "reported", "target", spamReportTestAd, 8*time.Minute))
	require.True(t, h.confirm(t, id, source))
	_, err := h.db.Exec(`UPDATE qq_bot_spam_reports SET created_at=clock_timestamp()-INTERVAL '121 seconds' WHERE id=$1`, id)
	require.NoError(t, err)
	// A still-future operation expiry must not override the durable report deadline.
	h.s.runModeration(context.Background())
	require.NotEqual(t, "succeeded", h.operations(t)["mute"])
	h.mu.Lock()
	require.Empty(t, h.requests)
	h.mu.Unlock()
}

func TestSpamReportWindowFreshRecallFailureNeverFallsBackToMuteOnly(t *testing.T) {
	for _, mode := range []string{"http_failure", "unknown_success_body"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					_, _ = fmt.Fprint(w, `{"access_token":"test-only","expires_in":7200}`)
					return
				}
				h.mu.Lock()
				h.requests = append(h.requests, r.Method+" "+r.URL.Path)
				h.mu.Unlock()
				if r.Method != http.MethodDelete || !strings.Contains(r.URL.Path, "/messages/") {
					t.Errorf("unconfirmed recall must not authorize %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if mode == "http_failure" {
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = fmt.Fprint(w, `{"code":40062003}`)
			}))
			t.Cleanup(server.Close)
			h.s.qqURL, h.s.tokenURL = server.URL, server.URL+"/token"
			id, source := h.stage(t, spamSourceTestEvent("reported"))
			require.True(t, h.confirm(t, id, source))
			h.s.runModeration(context.Background())
			require.Equal(t, "unconfirmed", h.operations(t)["recall"])
			// Even if source age now enters the mute-only range, an attempted recall
			// stays unconfirmed; it cannot be converted to the explicit skip marker.
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET sent_at=clock_timestamp()-INTERVAL '3 minutes' WHERE message_id='reported'`)
			require.NoError(t, err)
			h.s.runModeration(context.Background())
			require.Equal(t, map[string]string{"recall": "unconfirmed", "mute": "cancelled"}, h.operations(t))
			h.mu.Lock()
			require.Equal(t, []string{"DELETE /v2/groups/group/messages/reported"}, h.requests)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportWindowArbitrarySkippedRecallDoesNotAuthorizeMute(t *testing.T) {
	h := newSpamSourceHarness(t)
	id, source := h.stage(t, spamReportWindowEvent(t, "reported", "target", spamReportTestAd, 8*time.Minute))
	require.True(t, h.confirm(t, id, source))
	_, err := h.db.Exec(`UPDATE qq_bot_moderation_operations SET error='arbitrary reason' WHERE kind='recall'`)
	require.NoError(t, err)
	h.s.runModeration(context.Background())
	require.NotEqual(t, "succeeded", h.operations(t)["mute"])
	h.mu.Lock()
	require.Empty(t, h.requests)
	h.mu.Unlock()
}
