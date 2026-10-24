//go:build integration

package qqbot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func spamReportExecutionAssertReason(t *testing.T, h *spamSourceHarness, kind, status string, reason spamReportSourceReason) {
	t.Helper()
	var actualStatus, detail string
	require.NoError(t, h.db.QueryRow(`SELECT status,error FROM qq_bot_moderation_operations WHERE kind=$1`, kind).Scan(&actualStatus, &detail))
	require.Equal(t, status, actualStatus)
	require.Contains(t, detail, string(reason))
	require.Contains(t, detail, "此前已完成的操作结果不受影响")
	for _, unsafe := range []string{spamReportTestAd, "private-execution-message", "private-execution-conflict", "test-only", "target", "本次不撤回、不禁言", "未调用模型", "未提交处罚", "未执行处罚"} {
		require.NotContains(t, detail, unsafe)
	}
	if kind == "mute" {
		require.Contains(t, detail, "当前禁言操作已取消")
	} else {
		require.Contains(t, detail, "当前撤回操作已取消")
	}
}

func TestSpamReportExecutionRevokedErrorPreservesSentinelAndFixedReason(t *testing.T) {
	for _, reason := range []spamReportSourceReason{spamSourceWhitelist, spamSourceChatAuthorized, spamSourceRoleChanged, spamSourceConflict, spamSourceProcessingExpired, spamSourceRecallExpired, spamSourceRecallUnconfirmed} {
		err := fmt.Errorf("execution wrapper: %w", spamReportRevoked(reason))
		require.ErrorIs(t, err, errSpamReportRevoked)
		require.Equal(t, reason, spamReportRevocationReason(err))
	}
	require.Equal(t, spamSourceUnverified, spamReportRevocationReason(errSpamReportRevoked))
	require.Equal(t, spamSourceUnverified, spamReportRevocationReason(spamReportRevoked(spamSourceOK)))
}

func TestSpamReportExecutionTokenWaitKeepsSpecificReasonAfterSuccessfulRecall(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason spamReportSourceReason
		mutate func(*testing.T, *spamSourceHarness, Event)
	}{
		{"whitelist", spamSourceWhitelist, func(t *testing.T, h *spamSourceHarness, _ Event) {
			changed := h.c
			changed.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "target"}}
			_, err := h.s.SaveConfig(context.Background(), changed)
			require.NoError(t, err)
		}},
		{"chat_authorization", spamSourceChatAuthorized, func(t *testing.T, h *spamSourceHarness, _ Event) {
			changed := h.c
			changed.GroupAdmins = []GroupAdmin{{AppID: h.c.AppID, GroupID: "group", MemberID: "target"}}
			_, err := h.s.SaveConfig(context.Background(), changed)
			require.NoError(t, err)
		}},
		{"whitelist_added_then_removed", spamSourceWhitelistChanged, func(t *testing.T, h *spamSourceHarness, _ Event) {
			changed := h.c
			changed.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "target"}}
			_, err := h.s.SaveConfig(context.Background(), changed)
			require.NoError(t, err)
			_, err = h.s.SaveConfig(context.Background(), h.c)
			require.NoError(t, err)
		}},
		{"reports_disabled", spamSourcePolicyInactive, func(t *testing.T, h *spamSourceHarness, _ Event) {
			changed := h.c
			changed.Moderation.ReportEnabled = false
			_, err := h.s.SaveConfig(context.Background(), changed)
			require.NoError(t, err)
		}},
		{"model_changed", spamSourcePolicyChanged, func(t *testing.T, h *spamSourceHarness, _ Event) {
			changed := h.c
			changed.ChatModel = "private-execution-review-model"
			_, err := h.s.SaveConfig(context.Background(), changed)
			require.NoError(t, err)
		}},
		{"later_role_change", spamSourceRoleChanged, func(t *testing.T, h *spamSourceHarness, _ Event) {
			later := spamSourceTestEvent("private-execution-later-role")
			later.Author.MemberRole = "admin"
			h.enqueue(t, later)
		}},
		{"callback_conflict", spamSourceConflict, func(t *testing.T, h *spamSourceHarness, original Event) {
			original.Content = "private-execution-conflict"
			h.enqueue(t, original)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			original := spamSourceTestEvent("private-execution-message")
			id, source := h.stage(t, original)
			require.True(t, h.confirm(t, id, source))
			h.s.runModeration(context.Background())
			require.Equal(t, map[string]string{"recall": "succeeded", "mute": "queued"}, h.operations(t))

			entered, release, done := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseToken := func() { releaseOnce.Do(func() { close(release) }) }
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/token" {
					t.Errorf("unexpected token request %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
					_, _ = fmt.Fprint(w, `{"access_token":"refreshed-test-only","expires_in":7200}`)
				case <-r.Context().Done():
				}
			}))
			t.Cleanup(tokenServer.Close)
			h.s.tokenURL, h.s.tokenExpires = tokenServer.URL+"/token", time.Now().Add(-time.Minute)
			go func() {
				defer close(done)
				h.s.runModeration(context.Background())
			}()
			defer func() {
				releaseToken()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					t.Error("moderation worker did not finish after releasing token refresh")
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("moderation did not reach token refresh before final authorization")
			}
			tc.mutate(t, h, original)
			releaseToken()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("moderation did not finish after the verified cancellation")
			}
			require.Equal(t, map[string]string{"recall": "succeeded", "mute": "cancelled"}, h.operations(t))
			spamReportExecutionAssertReason(t, h, "mute", "cancelled", tc.reason)
			h.mu.Lock()
			requests := append([]string(nil), h.requests...)
			h.mu.Unlock()
			require.Equal(t, []string{"DELETE /v2/groups/group/messages/private-execution-message"}, requests)
		})
	}
}

func TestSpamReportPendingWorkerRetainsSourceRejectionReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason spamReportSourceReason
		mutate func(*testing.T, *spamSourceHarness, Event, string)
	}{
		{"later_role_change", spamSourceRoleChanged, func(t *testing.T, h *spamSourceHarness, _ Event, _ string) {
			later := spamSourceTestEvent("later-protected-message")
			later.Author.MemberRole = "admin"
			h.enqueue(t, later)
		}},
		{"callback_conflict", spamSourceConflict, func(t *testing.T, h *spamSourceHarness, original Event, _ string) {
			original.Content = "private-execution-conflict"
			h.enqueue(t, original)
		}},
		{"cancelled_report_untrusted_reason", spamSourceReportInvalid, func(t *testing.T, h *spamSourceHarness, _ Event, id string) {
			_, err := h.db.Exec(`UPDATE qq_bot_spam_reports SET status='cancelled',reason='private-execution-conflict' WHERE id=$1`, id)
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			original := spamSourceTestEvent("private-execution-message")
			id, source := h.stage(t, original)
			require.True(t, h.confirm(t, id, source))
			tc.mutate(t, h, original, id)
			h.s.runModeration(context.Background())
			h.s.runModeration(context.Background())
			spamReportExecutionAssertReason(t, h, "recall", "cancelled", tc.reason)
			spamReportExecutionAssertReason(t, h, "mute", "cancelled", tc.reason)
			h.mu.Lock()
			requests := append([]string(nil), h.requests...)
			h.mu.Unlock()
			require.Empty(t, requests)
		})
	}
}

func TestSpamReportProcessingExpiryAfterRecallRetainsTimingAndPriorOutcome(t *testing.T) {
	for _, maintenance := range []bool{false, true} {
		t.Run(fmt.Sprintf("maintenance=%t", maintenance), func(t *testing.T) {
			h := newSpamSourceHarness(t)
			id, source := h.stage(t, spamSourceTestEvent("private-execution-message"))
			require.True(t, h.confirm(t, id, source))
			h.s.runModeration(context.Background())
			_, err := h.db.Exec(`UPDATE qq_bot_moderation_operations SET expires_at=clock_timestamp()-INTERVAL '1 second' WHERE kind='mute'`)
			require.NoError(t, err)
			if maintenance {
				h.s.maintainModeration(context.Background())
			} else {
				h.s.runModeration(context.Background())
			}
			require.Equal(t, map[string]string{"recall": "succeeded", "mute": "expired"}, h.operations(t))
			spamReportExecutionAssertReason(t, h, "mute", "expired", spamSourceProcessingExpired)
			var detail string
			require.NoError(t, h.db.QueryRow(`SELECT error FROM qq_bot_moderation_operations WHERE kind='mute'`).Scan(&detail))
			require.NotContains(t, detail, "30 分钟")
			h.mu.Lock()
			requests := append([]string(nil), h.requests...)
			h.mu.Unlock()
			require.Equal(t, []string{"DELETE /v2/groups/group/messages/private-execution-message"}, requests)
		})
	}
}
