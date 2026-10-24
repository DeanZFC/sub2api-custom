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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type spamSourceHarness struct {
	s           *Service
	c           Config
	db          *sql.DB
	mu          sync.Mutex
	requests    []string
	recallFails bool
}

func newSpamSourceHarness(t *testing.T) *spamSourceHarness {
	t.Helper()
	db := testDB(t)
	h := &spamSourceHarness{db: db, s: savedBot(t, db)}
	var err error
	h.c, err = h.s.Config(context.Background())
	require.NoError(t, err)
	h.c.ChatDisabled, h.c.HistoryMessages = true, 0
	h.c.Moderation.ReportEnabled = true
	h.c.Moderation.Groups = []string{"group", "other-group"}
	h.save(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"access_token":"test-only","expires_in":7200}`)
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.requests = append(h.requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/messages/"):
			if h.recallFails {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"code":40062003}`)
			}
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/restrict_chat_setting"):
			var body struct {
				Members []struct {
					Op     string `json:"op"`
					Member string `json:"member_openid"`
					Until  string `json:"mute_expire_at"`
				} `json:"members"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Len(t, body.Members, 1)
			require.Equal(t, "add", body.Members[0].Op)
			require.Equal(t, "target", body.Members[0].Member)
			until, err := time.Parse(time.RFC3339, body.Members[0].Until)
			require.NoError(t, err)
			require.WithinDuration(t, time.Now().Add(time.Duration(h.c.Moderation.ReportMuteMinutes)*time.Minute), until, 2*time.Second)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	h.s.qqURL, h.s.tokenURL = server.URL, server.URL+"/token"
	return h
}

func (h *spamSourceHarness) save(t *testing.T) {
	t.Helper()
	_, err := h.s.SaveConfig(context.Background(), h.c)
	require.NoError(t, err)
}

func (h *spamSourceHarness) enqueue(t *testing.T, e Event) {
	t.Helper()
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, h.s.Enqueue(context.Background(), h.c, Payload{Type: groupMessageCreate, Data: raw}))
}

func (h *spamSourceHarness) source(t *testing.T, reference string) *spamReportSource {
	t.Helper()
	source, err := h.s.spamReportSource(context.Background(), h.c, "group", reference)
	require.NoError(t, err)
	return source
}

func (h *spamSourceHarness) stage(t *testing.T, e Event) (string, spamReportSource) {
	t.Helper()
	h.enqueue(t, e)
	source := h.source(t, e.MessageScene.value("msg_idx"))
	require.NotNil(t, source)
	id := randomID()
	_, err := h.db.Exec(`INSERT INTO qq_bot_spam_reports(id,app_id,sandbox,group_id,reporter_id,message_id,member_id,policy_hash,status)
 VALUES($1,$2,$3,$4,'reporter',$5,$6,$7,'evaluating')`, id, h.c.AppID, h.c.Sandbox, e.GroupID, source.MessageID, source.MemberID, spamReportPolicy(h.c))
	require.NoError(t, err)
	return id, *source
}

func (h *spamSourceHarness) confirm(t *testing.T, id string, source spamReportSource) bool {
	t.Helper()
	ok, err := h.s.confirmSpamReport(context.Background(), h.c, id, source, "advertising", "两次审核一致，直接证据通过")
	require.NoError(t, err)
	return ok
}

func (h *spamSourceHarness) operations(t *testing.T) map[string]string {
	t.Helper()
	rows, err := h.db.Query(`SELECT kind,status FROM qq_bot_moderation_operations ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	statuses := map[string]string{}
	for rows.Next() {
		var kind, status string
		require.NoError(t, rows.Scan(&kind, &status))
		statuses[kind] = status
	}
	require.NoError(t, rows.Err())
	return statuses
}

func TestSpamReportSourcesWorkWithoutChatHistoryAndResolveExactReferences(t *testing.T) {
	h := newSpamSourceHarness(t)
	e := spamSourceTestEvent("original-d-id")
	h.enqueue(t, e)
	byIndex, byID := h.source(t, "idx-original-d-id"), h.source(t, "original-d-id")
	require.NotNil(t, byIndex)
	require.Equal(t, byIndex, byID)
	require.Equal(t, e.ID, byIndex.MessageID)
	require.Equal(t, e.Author.MemberID, byIndex.MemberID)
	require.Equal(t, e.Content, byIndex.Content)
	for _, table := range []string{"qq_bot_jobs", "qq_bot_group_messages", "qq_bot_moderation_events"} {
		var n int
		require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		require.Zero(t, n, table)
	}
	var ciphertext, rowJSON string
	require.NoError(t, h.db.QueryRow(`SELECT content_encrypted,row_to_json(s)::text FROM qq_bot_spam_report_sources s`).Scan(&ciphertext, &rowJSON))
	require.Equal(t, "encrypted:"+e.Content, ciphertext)
	require.NotContains(t, rowJSON, "must-not-be-retained")
	for _, tc := range []struct {
		name  string
		edit  func(*Config)
		group string
	}{
		{"application", func(c *Config) { c.AppID = "other-app" }, "group"},
		{"sandbox", func(c *Config) { c.Sandbox = true }, "group"},
		{"group", func(*Config) {}, "other-group"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := h.c
			tc.edit(&changed)
			source, err := h.s.spamReportSource(context.Background(), changed, tc.group, "idx-original-d-id")
			require.NoError(t, err)
			require.Nil(t, source)
		})
	}
	require.Nil(t, h.source(t, "idx-original-d-id-other"))
	require.Nil(t, h.source(t, ""))
}

func TestSpamReportSourcesRejectUnverifiableContentAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Event, *Config)
	}{
		{"role_missing", func(e *Event, _ *Config) { e.Author.MemberRole = "" }},
		{"administrator", func(e *Event, _ *Config) { e.Author.MemberRole = "admin" }},
		{"owner", func(e *Event, _ *Config) { e.Author.MemberRole = "owner" }},
		{"robot", func(e *Event, _ *Config) { e.Author.Bot = true }},
		{"whitelist", func(_ *Event, c *Config) {
			c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "target"}}
		}},
		{"bound_administrator", func(_ *Event, c *Config) {
			c.GroupAdmins = []GroupAdmin{{AppID: "app", GroupID: "group", MemberID: "target"}}
		}},
		{"quoted_message", func(e *Event, _ *Config) { e.MessageReference.MessageID = "another-message" }},
		{"quoted_index", func(e *Event, _ *Config) {
			e.MessageScene.Ext = append(e.MessageScene.Ext, "ref_msg_idx=another-message")
		}},
		{"attachment", func(e *Event, _ *Config) { e.Attachments = []Attachment{{URL: "https://example.invalid/image"}} }},
		{"nested_content", func(e *Event, _ *Config) { e.MsgElements = []MessageElement{{Content: e.Content}} }},
		{"overlong_content", func(e *Event, _ *Config) { e.Content = strings.Repeat("文", 6001) }},
		{"expired", func(e *Event, _ *Config) {
			e.Timestamp, _ = json.Marshal(time.Now().Add(-spamReportSourceRetention - time.Second).Format(time.RFC3339Nano))
		}},
		{"future", func(e *Event, _ *Config) {
			e.Timestamp, _ = json.Marshal(time.Now().Add(time.Minute).Format(time.RFC3339Nano))
		}},
		{"timestamp_missing", func(e *Event, _ *Config) { e.Timestamp = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			e := spamSourceTestEvent(tc.name)
			tc.edit(&e, &h.c)
			h.save(t)
			require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, e))
			require.Nil(t, h.source(t, e.ID))
			require.Empty(t, h.operations(t))
			var stored bool
			require.NoError(t, h.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM qq_bot_spam_report_sources WHERE content_encrypted<>'')`).Scan(&stored))
			require.False(t, stored)
		})
	}
}

func TestSpamReportSourceAmbiguityRoleChangesAndConflictingCallbacksFailClosed(t *testing.T) {
	for _, mode := range []string{"same_index", "index_collides_with_id", "promoted", "conflicting_author", "conflicting_body"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			e := spamSourceTestEvent("original")
			h.enqueue(t, e)
			require.NotNil(t, h.source(t, "idx-original"))
			other := spamSourceTestEvent("another")
			other.Author.MemberID = "different-member"
			switch mode {
			case "same_index":
				other.MessageScene.Ext = []string{"msg_idx=idx-original"}
				other.Author.MemberRole = "admin" // Invalid records still make references ambiguous.
			case "index_collides_with_id":
				other.ID = "idx-original"
			case "promoted":
				other.Author.MemberID, other.Author.MemberRole = "target", "admin"
			case "conflicting_author":
				other = e
				other.Author.MemberID = "different-member"
			case "conflicting_body":
				other = e
				other.Content = "这是一条完全不同的正常消息。"
			}
			require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, other))
			require.Nil(t, h.source(t, "idx-original"))
		})
	}
}

func TestSpamReportConfirmationIsBoundToEvidenceAndIdempotent(t *testing.T) {
	h := newSpamSourceHarness(t)
	id, source := h.stage(t, spamSourceTestEvent("target-message"))
	require.False(t, h.confirm(t, "missing-report-reservation", source))
	ok, err := h.s.confirmSpamReport(context.Background(), h.c, id, source, "unknown-category", "unsupported")
	require.NoError(t, err)
	require.False(t, ok)
	for _, tc := range []struct {
		name string
		edit func(*spamReportSource)
	}{
		{"wrong_author", func(s *spamReportSource) { s.MemberID = "someone-else" }},
		{"wrong_message", func(s *spamReportSource) { s.MessageID = "other-message" }},
		{"tampered_body", func(s *spamReportSource) { s.Content += " changed" }},
		{"wrong_time", func(s *spamReportSource) { s.SentAt = s.SentAt.Add(time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := source
			tc.edit(&changed)
			require.False(t, h.confirm(t, id, changed))
			require.Empty(t, h.operations(t))
		})
	}
	require.True(t, h.confirm(t, id, source))
	require.False(t, h.confirm(t, id, source))
	var incidents, operations int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_moderation_incidents`).Scan(&incidents))
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_moderation_operations`).Scan(&operations))
	require.Equal(t, 1, incidents)
	require.Equal(t, 2, operations)
}

func TestSpamReportConcurrentConfirmationCreatesOnePunishment(t *testing.T) {
	h := newSpamSourceHarness(t)
	id, source := h.stage(t, spamSourceTestEvent("reported"))
	var wg sync.WaitGroup
	results, failures := make(chan bool, 8), make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := h.s.confirmSpamReport(context.Background(), h.c, id, source, "advertising", "direct evidence")
			results <- ok
			failures <- err
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	confirmed := 0
	for ok := range results {
		if ok {
			confirmed++
		}
	}
	for err := range failures {
		require.NoError(t, err)
	}
	require.Equal(t, 1, confirmed)
	var operations int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_moderation_operations`).Scan(&operations))
	require.Equal(t, 2, operations)
}

func TestSpamReportExecutionRecallsFirstAndNeverExpandsToOtherMessages(t *testing.T) {
	h := newSpamSourceHarness(t)
	h.c.Moderation.Enabled = true
	h.save(t)
	id, source := h.stage(t, spamSourceTestEvent("reported"))
	require.True(t, h.confirm(t, id, source))
	h.s.runModeration(context.Background())
	require.Equal(t, map[string]string{"recall": "succeeded", "mute": "queued"}, h.operations(t))
	h.s.runModeration(context.Background())
	require.Equal(t, map[string]string{"recall": "succeeded", "mute": "succeeded"}, h.operations(t))
	h.mu.Lock()
	require.Equal(t, []string{"DELETE /v2/groups/group/messages/reported", "POST /v2/groups/group/restrict_chat_setting"}, h.requests)
	h.mu.Unlock()
	var cooldown, lastMuted time.Time
	var active string
	require.NoError(t, h.db.QueryRow(`SELECT cooldown_until,last_muted_at,active_incident_id FROM qq_bot_moderation_members WHERE member_id='target'`).Scan(&cooldown, &lastMuted, &active))
	require.Equal(t, int64(0), cooldown.Unix())
	require.Equal(t, int64(0), lastMuted.Unix())
	require.Empty(t, active)
	normal := spamSourceTestEvent("next-normal")
	normal.Content = "稍后回来，今天要完成的工作已经记录好了。"
	h.enqueue(t, normal)
	var blocked bool
	require.NoError(t, h.db.QueryRow(`SELECT blocked FROM qq_bot_moderation_events WHERE message_id=$1`, normal.ID).Scan(&blocked))
	require.False(t, blocked)
	var recalls int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_moderation_operations WHERE kind='recall'`).Scan(&recalls))
	require.Equal(t, 1, recalls)
}

func TestSpamReportRecallFailureAndUnknownOutcomeNeverMuteOrRetry(t *testing.T) {
	for _, mode := range []string{"http_failure", "process_interrupted"} {
		t.Run(mode, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			id, source := h.stage(t, spamSourceTestEvent("reported"))
			require.True(t, h.confirm(t, id, source))
			if mode == "http_failure" {
				h.recallFails = true
				h.s.runModeration(context.Background())
			} else {
				_, err := h.db.Exec(`UPDATE qq_bot_moderation_operations SET status='started',started_at=NOW()-INTERVAL '2 minutes' WHERE kind='recall'`)
				require.NoError(t, err)
				h.s.maintainModeration(context.Background())
			}
			h.s.runModeration(context.Background())
			h.s.runModeration(context.Background())
			require.Equal(t, map[string]string{"recall": "unconfirmed", "mute": "cancelled"}, h.operations(t))
			h.mu.Lock()
			defer h.mu.Unlock()
			if mode == "http_failure" {
				require.Equal(t, []string{"DELETE /v2/groups/group/messages/reported"}, h.requests)
			} else {
				require.Empty(t, h.requests)
			}
		})
	}
}

func TestSpamReportPolicyRevocationCancelsReviewAndQueuedPunishment(t *testing.T) {
	for _, stage := range []string{"evaluating", "queued"} {
		for _, change := range []string{"model", "whitelist", "disabled", "sandbox"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				h := newSpamSourceHarness(t)
				id, source := h.stage(t, spamSourceTestEvent("reported"))
				original := h.c
				if stage == "queued" {
					require.True(t, h.confirm(t, id, source))
				}
				switch change {
				case "model":
					h.c.ChatModel = "replacement-model"
				case "whitelist":
					h.c.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "target"}}
				case "disabled":
					h.c.Moderation.ReportEnabled = false
				case "sandbox":
					h.c.Sandbox = true
				}
				h.save(t)
				if stage == "evaluating" {
					ok, err := h.s.confirmSpamReport(context.Background(), original, id, source, "advertising", "direct evidence")
					require.NoError(t, err)
					require.False(t, ok)
					require.Empty(t, h.operations(t))
				} else {
					h.s.runModeration(context.Background())
					h.s.runModeration(context.Background())
					require.Equal(t, map[string]string{"recall": "cancelled", "mute": "cancelled"}, h.operations(t))
				}
				h.mu.Lock()
				require.Empty(t, h.requests)
				h.mu.Unlock()
			})
		}
	}
}

func TestSpamReportEvidenceExpiryPreventsConfirmationAndExecution(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprint(queued), func(t *testing.T) {
			h := newSpamSourceHarness(t)
			id, source := h.stage(t, spamSourceTestEvent("reported"))
			if queued {
				require.True(t, h.confirm(t, id, source))
			}
			_, err := h.db.Exec(`UPDATE qq_bot_spam_report_sources SET sent_at=NOW()-INTERVAL '32 minutes 1 second'`)
			require.NoError(t, err)
			require.Nil(t, h.source(t, "reported"))
			if queued {
				h.s.runModeration(context.Background())
				h.s.runModeration(context.Background())
				require.Equal(t, map[string]string{"recall": "cancelled", "mute": "cancelled"}, h.operations(t))
			} else {
				require.False(t, h.confirm(t, id, source))
				require.Empty(t, h.operations(t))
			}
			h.mu.Lock()
			require.Empty(t, h.requests)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportPolicyChangeAndRestoreNeverRevivesOldEvidence(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(fmt.Sprint(queued), func(t *testing.T) {
			h := newSpamSourceHarness(t)
			id, source := h.stage(t, spamSourceTestEvent("reported"))
			original := h.c
			if queued {
				require.True(t, h.confirm(t, id, source))
			}
			h.c.ChatModel = "temporarily-replaced-model"
			h.save(t)
			h.c = original
			h.save(t)
			if queued {
				h.s.runModeration(context.Background())
				h.s.runModeration(context.Background())
				require.Equal(t, map[string]string{"recall": "cancelled", "mute": "cancelled"}, h.operations(t))
			} else {
				require.False(t, h.confirm(t, id, source))
				require.Empty(t, h.operations(t))
			}
			h.mu.Lock()
			require.Empty(t, h.requests)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportConfirmationRequiresSafetyTimeRemaining(t *testing.T) {
	h := newSpamSourceHarness(t)
	e := spamSourceTestEvent("almost-expired")
	e.Timestamp, _ = json.Marshal(time.Now().Add(-29 * time.Minute).UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
	id, source := h.stage(t, e)
	_, err := h.db.Exec(`UPDATE qq_bot_spam_reports SET created_at=clock_timestamp()-INTERVAL '118 seconds' WHERE id=$1`, id)
	require.NoError(t, err)
	require.False(t, h.confirm(t, id, source))
	require.Empty(t, h.operations(t))
}

func TestSpamReportPromotionAfterRecallCancelsMute(t *testing.T) {
	h := newSpamSourceHarness(t)
	id, source := h.stage(t, spamSourceTestEvent("reported"))
	require.True(t, h.confirm(t, id, source))
	h.s.runModeration(context.Background())
	promoted := spamSourceTestEvent("after-promotion")
	promoted.Author.MemberRole = "admin"
	h.enqueue(t, promoted)
	h.s.runModeration(context.Background())
	require.Equal(t, map[string]string{"recall": "succeeded", "mute": "cancelled"}, h.operations(t))
	h.mu.Lock()
	require.Equal(t, []string{"DELETE /v2/groups/group/messages/reported"}, h.requests)
	h.mu.Unlock()
}

func TestSpamReportConfigChangeDuringTokenRefreshCancelsMute(t *testing.T) {
	for _, change := range []string{"whitelist", "disable_reports", "restore_and_replay"} {
		t.Run(change, func(t *testing.T) {
			h := newSpamSourceHarness(t)
			original := spamSourceTestEvent("reported")
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
					fmt.Fprint(w, `{"access_token":"refreshed-test-only","expires_in":7200}`)
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
				t.Fatal("moderation did not reach the blocked token refresh")
			}

			// Keep the harness config immutable while its HTTP handler may read it.
			// The durable settings change must complete before releasing the token.
			changed := h.c
			if change == "whitelist" {
				changed.Moderation.Exemptions = []ModerationExemption{{GroupID: "group", MemberID: "target"}}
			} else {
				changed.Moderation.ReportEnabled = false
			}
			saveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := h.s.SaveConfig(saveCtx, changed)
			cancel()
			require.NoError(t, err, "token refresh must not hold the config lock before final authorization")
			if change == "restore_and_replay" {
				restoreCtx, restoreCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err = h.s.SaveConfig(restoreCtx, h.c)
				restoreCancel()
				require.NoError(t, err)
				require.NoError(t, h.s.rememberSpamReportSource(context.Background(), h.c, original))
				require.NotNil(t, h.source(t, original.ID), "recreated source evidence must not revive an already-cancelled report")
			}
			releaseToken()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("moderation worker did not finish after policy revocation")
			}
			require.Equal(t, map[string]string{"recall": "succeeded", "mute": "cancelled"}, h.operations(t))
			h.mu.Lock()
			require.Equal(t, []string{"DELETE /v2/groups/group/messages/reported"}, h.requests)
			h.mu.Unlock()
		})
	}
}

func TestSpamReportDuplicateRoleProtectionStaysStickyForEarlierMessages(t *testing.T) {
	for _, queued := range []bool{false, true} {
		for _, protection := range []string{"administrator", "unknown_role", "robot"} {
			t.Run(fmt.Sprintf("queued=%t/%s", queued, protection), func(t *testing.T) {
				h := newSpamSourceHarness(t)
				id, source := h.stage(t, spamSourceTestEvent("earlier-A"))
				if queued {
					require.True(t, h.confirm(t, id, source))
				}
				later := spamSourceTestEvent("later-B")
				later.Content = "今天的安排已收到。"
				h.enqueue(t, later)
				require.NotNil(t, h.source(t, "earlier-A"))
				protected := later
				switch protection {
				case "administrator":
					protected.Author.MemberRole = "admin"
				case "unknown_role":
					protected.Author.MemberRole = ""
				case "robot":
					protected.Author.Bot = true
				}
				h.enqueue(t, protected)
				require.Nil(t, h.source(t, "earlier-A"), "a protective duplicate must invalidate earlier member evidence")
				// A late repeat of the original ordinary-member callback must never
				// discard the protective identity observation already persisted.
				h.enqueue(t, later)
				require.Nil(t, h.source(t, "earlier-A"), "protection must survive subsequent member callbacks")
				if queued {
					h.s.runModeration(context.Background())
					h.s.runModeration(context.Background())
					require.Equal(t, map[string]string{"recall": "cancelled", "mute": "cancelled"}, h.operations(t))
				} else {
					require.False(t, h.confirm(t, id, source))
					require.Empty(t, h.operations(t))
				}
				h.mu.Lock()
				require.Empty(t, h.requests)
				h.mu.Unlock()
			})
		}
	}
}
