//go:build integration

package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type autoHarness struct {
	s         *Service
	c         Config
	db        *sql.DB
	calls     atomic.Int32
	sends     atomic.Int32
	output    atomic.Value
	sent      atomic.Value
	failModel atomic.Bool
	failSend  atomic.Bool
	hook      func()
}

func newAutoHarness(t *testing.T) *autoHarness {
	t.Helper()
	db := testDB(t)
	s := savedBot(t, db)
	h := &autoHarness{s: s, db: db}
	h.output.Store(`{"action":"reply","text":"可以先看看错误日志。"}`)
	h.sent.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			fmt.Fprint(w, `{"access_token":"test-only","expires_in":7200}`)
		case r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses":
			h.calls.Add(1)
			if h.hook != nil {
				h.hook()
			}
			if h.failModel.Load() {
				w.WriteHeader(500)
				return
			}
			content := h.output.Load().(string)
			if r.URL.Path == "/v1/responses" {
				_ = json.NewEncoder(w).Encode(map[string]any{"output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": content}}}}, "usage": map[string]int{"input_tokens": 20, "output_tokens": 10}})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10}})
			}
		case strings.HasSuffix(r.URL.Path, "/messages"):
			body := readQQTestReply(t, r)
			h.sent.Store(body.text())
			n := h.sends.Add(1)
			if h.failSend.Load() {
				w.WriteHeader(500)
				return
			}
			fmt.Fprintf(w, `{"id":"reply-%d","ext_info":{"ref_idx":"ref-%d"}}`, n, n)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	c, err := s.Config(context.Background())
	require.NoError(t, err)
	c.BaseURL = server.URL + "/v1"
	c.Autonomous.Enabled = true
	c.Autonomous.Groups = []string{"group", "other-group"}
	c.Autonomous.StartHour = 0
	c.Autonomous.EndHour = 0
	c.Autonomous.MinMessages = 0
	h.c = c
	h.save(t)
	s.qqURL = server.URL
	s.tokenURL = server.URL + "/token"
	return h
}
func (h *autoHarness) save(t *testing.T) {
	t.Helper()
	_, err := h.s.SaveConfig(context.Background(), h.c)
	require.NoError(t, err)
}
func (h *autoHarness) enqueue(t *testing.T, id, text, member string) Payload {
	t.Helper()
	e := Event{ID: id, GroupID: "group", Content: text}
	e.Author.MemberID = member
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	p := Payload{ID: id, Type: groupMessageCreate, Data: raw}
	require.NoError(t, h.s.Enqueue(context.Background(), h.c, p))
	return p
}
func (h *autoHarness) run() { h.s.runOne(context.Background()) }
func (h *autoHarness) resetCooldown(t *testing.T) {
	t.Helper()
	_, err := h.db.Exec(`UPDATE qq_bot_autonomous_state SET last_candidate_at=NOW()-INTERVAL '1 hour',last_reply_at=NOW()-INTERVAL '1 hour',last_reply_counter=0`)
	require.NoError(t, err)
}
func (h *autoHarness) job(t *testing.T, id string) Job {
	t.Helper()
	j, err := scanJob(h.db.QueryRow(`SELECT `+jobColumns+` FROM qq_bot_jobs WHERE message_id=$1`, id))
	require.NoError(t, err)
	return j
}
func TestAutonomousReplyAndOneFollowup(t *testing.T) {
	h := newAutoHarness(t)
	p := h.enqueue(t, "first", "这个错误怎么解决？", "alice")
	for range 3 {
		require.NoError(t, h.s.Enqueue(context.Background(), h.c, p))
	}
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	require.EqualValues(t, 1, h.sends.Load())
	require.NotContains(t, h.sent.Load(), "qqbot-at-user")
	j := h.job(t, "first")
	require.Equal(t, "replied", j.DecisionReason)
	require.Equal(t, "completed", j.Status)
	require.Equal(t, "question", j.TriggerReason)
	var personal, total int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_daily_usage WHERE scope='member:alice'`).Scan(&personal))
	require.Zero(t, personal)
	require.NoError(t, h.db.QueryRow(`SELECT count FROM qq_bot_daily_usage WHERE scope='all' AND kind='chat'`).Scan(&total))
	require.Equal(t, 1, total)
	var shared bool
	require.NoError(t, h.db.QueryRow(`SELECT shareable FROM qq_bot_message_references WHERE message_id='reply-1'`).Scan(&shared))
	require.True(t, shared)
	h.enqueue(t, "follow", "那具体应该怎么检查？", "alice")
	h.run()
	require.EqualValues(t, 2, h.calls.Load())
	require.Equal(t, "followup", h.job(t, "follow").TriggerReason)
	h.enqueue(t, "third", "那还有什么办法可以检查？", "alice")
	h.run()
	require.EqualValues(t, 2, h.calls.Load())
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs WHERE message_id='third'`).Scan(&count))
	require.Zero(t, count)
	runtime, err := h.s.Runtime(context.Background(), h.c)
	require.NoError(t, err)
	require.Contains(t, runtime.AutonomousStats, AutonomousStat{GroupID: "group", Reason: "model_calls", Count: 2})
	require.Contains(t, runtime.AutonomousStats, AutonomousStat{GroupID: "group", Reason: "sent", Count: 2})
}
func TestAutonomousSilenceErrorsAndCallLimit(t *testing.T) {
	for _, mode := range []string{"skip", "invalid", "model_error", "send_error"} {
		t.Run(mode, func(t *testing.T) {
			h := newAutoHarness(t)
			h.c.Autonomous.DailyCalls = 1
			h.save(t)
			switch mode {
			case "skip":
				h.output.Store(`{"action":"skip"}`)
			case "invalid":
				h.output.Store("bad JSON")
			case "model_error":
				h.failModel.Store(true)
			case "send_error":
				h.failSend.Store(true)
			}
			h.enqueue(t, "one", "这个报错怎么处理？", "alice")
			h.run()
			h.run()
			require.EqualValues(t, 1, h.calls.Load())
			sends := int32(0)
			if mode == "send_error" {
				sends = 1
			}
			require.Equal(t, sends, h.sends.Load())
			if mode == "skip" {
				require.Equal(t, "skipped", h.job(t, "one").Status)
			}
			h.resetCooldown(t)
			if mode == "send_error" {
				_, err := h.db.Exec(`UPDATE qq_bot_jobs SET reply_reserved_at=NOW()-INTERVAL '15 minutes' WHERE message_id='one'`)
				require.NoError(t, err)
			}
			h.enqueue(t, "two", "另外这个故障如何解决？", "bob")
			h.run()
			require.EqualValues(t, 1, h.calls.Load())
			require.Equal(t, "call_limit", h.job(t, "two").DecisionReason)
		})
	}
}
func TestAutonomousRechecksConversationAndRevocation(t *testing.T) {
	for _, mode := range []string{"new_message", "disable", "quiet_hours", "max_length"} {
		t.Run(mode, func(t *testing.T) {
			h := newAutoHarness(t)
			h.hook = func() {
				switch mode {
				case "new_message":
					h.enqueue(t, "new", "哈哈", "bob")
				case "disable":
					h.c.Autonomous.Enabled = false
					h.save(t)
				case "quiet_hours":
					hour := time.Now().In(time.FixedZone("CST", 28800)).Hour()
					h.c.Autonomous.StartHour = (hour + 1) % 24
					h.c.Autonomous.EndHour = (hour + 2) % 24
					h.save(t)
				case "max_length":
					h.c.Autonomous.MaxChars = 20
					h.save(t)
				}
			}
			if mode == "max_length" {
				h.output.Store(`{"action":"reply","text":"` + strings.Repeat("字", 21) + `"}`)
			}
			h.enqueue(t, "one", "这个报错怎么处理？", "alice")
			h.run()
			require.EqualValues(t, 1, h.calls.Load())
			require.Zero(t, h.sends.Load())
		})
	}
}
func TestAutonomousDirectNameUsesNormalQuotaAndProtocol(t *testing.T) {
	h := newAutoHarness(t)
	h.c.Autonomous.DailyCalls = 0
	h.c.HistoryMessages = 0
	h.save(t)
	h.output.Store("普通回答")
	p := h.enqueue(t, "name", "CTAV，帮我看看这个问题", "alice")
	p.Type = groupAtMessageCreate
	require.NoError(t, h.s.Enqueue(context.Background(), h.c, p))
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	require.EqualValues(t, 1, h.sends.Load())
	require.Contains(t, h.sent.Load(), "qqbot-at-user")
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT count FROM qq_bot_daily_usage WHERE scope='member:alice' AND kind='chat'`).Scan(&count))
	require.Equal(t, 1, count)
	require.Equal(t, "name", h.job(t, "name").TriggerReason)
	h.c.Autonomous.DailyCalls = 40
	h.c.ChatProtocol = "responses"
	h.c.Autonomous.MentionUser = true
	h.save(t)
	h.resetCooldown(t)
	h.output.Store(`{"action":"reply","text":"可以先看看错误日志。"}`)
	h.enqueue(t, "responses", "另一个问题怎么排查？", "bob")
	h.run()
	require.EqualValues(t, 2, h.calls.Load())
	require.Equal(t, "replied", h.job(t, "responses").DecisionReason)
	require.Contains(t, h.sent.Load(), "qqbot-at-user")
}
func TestAutonomousAtomicReservationsAndReplay(t *testing.T) {
	h := newAutoHarness(t)
	h.enqueue(t, "one", "这个错误怎么处理？", "alice")
	j, err := h.s.claimJob(context.Background())
	require.NoError(t, err)
	var successes atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reason, err := h.s.reserveAutonomous(context.Background(), h.c, j, false)
			if err != nil {
				failures.Add(1)
			} else if reason == "" {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, failures.Load())
	require.EqualValues(t, 1, successes.Load())
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT count FROM qq_bot_daily_usage WHERE scope='all' AND kind='chat'`).Scan(&count))
	require.Equal(t, 1, count)
	h.c.Autonomous.HourlyReplies = 1
	h.save(t)
	successes.Store(0)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, reason, err := h.s.reserveAutonomous(context.Background(), h.c, j, true)
			if err != nil {
				failures.Add(1)
			} else if reason == "" {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, failures.Load())
	require.EqualValues(t, 1, successes.Load())
	h.s.finish(j, "interrupted", "")
	_, err = h.db.Exec(`UPDATE qq_bot_jobs SET reply_reserved_at=NOW()-INTERVAL '15 minutes' WHERE id=$1`, j.ID)
	require.NoError(t, err)
	h.resetCooldown(t)
	h.enqueue(t, "two", "另一个故障怎么处理？", "bob")
	h.run()
	require.Zero(t, h.calls.Load())
	require.Equal(t, "reply_limit", h.job(t, "two").DecisionReason)
}
func TestAutonomousLocalFiltersIsolationAndMessageGap(t *testing.T) {
	h := newAutoHarness(t)
	h.c.Autonomous.MinMessages = 3
	h.c.Autonomous.SamplePercent = 0
	h.save(t)
	h.enqueue(t, "first", "这是第一条普通聊天", "alice")
	h.run()
	require.Zero(t, h.calls.Load())
	h.enqueue(t, "repeat", "这是第一条普通聊天", "bob")
	h.enqueue(t, "second", "这是第二条正常聊天", "alice")
	h.enqueue(t, "third", "这个错误怎么解决？", "bob")
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	h.resetCooldown(t)
	h.c.Autonomous.Groups = []string{"other-group"}
	h.save(t)
	h.enqueue(t, "disabled-group", "这个故障如何排查？", "new-user")
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	h.c.Autonomous.Groups = []string{"group"}
	h.save(t)
	h.resetCooldown(t)
	var wg sync.WaitGroup
	var errorsCount atomic.Int32
	e := Event{ID: "parallel", GroupID: "group", Content: "这个服务如何排查？"}
	e.Author.MemberID = "charlie"
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	p := Payload{Type: groupMessageCreate, Data: raw}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h.s.Enqueue(context.Background(), h.c, p) != nil {
				errorsCount.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, errorsCount.Load())
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs WHERE message_id='parallel'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAutonomousBudgetRollbackAndPolicyChanges(t *testing.T) {
	for _, mode := range []string{"global", "disabled", "stale", "new_message", "member_cooldown", "failed_send_cooldown"} {
		t.Run(mode, func(t *testing.T) {
			h := newAutoHarness(t)
			if mode == "global" {
				h.c.TotalDailyChatLimit = 0
				h.save(t)
			}
			h.enqueue(t, "one", "这个故障怎么排查？", "alice")
			if mode == "global" {
				require.Zero(t, h.calls.Load())
				return
			}
			switch mode {
			case "disabled":
				h.c.Autonomous.Enabled = false
				h.save(t)
			case "stale":
				_, err := h.db.Exec(`UPDATE qq_bot_jobs SET created_at=NOW()-INTERVAL '1 minute' WHERE message_id='one'`)
				require.NoError(t, err)
			case "new_message":
				h.enqueue(t, "new", "我已经找到原因了", "bob")
			case "member_cooldown":
				_, err := h.db.Exec(`INSERT INTO qq_bot_jobs(id,app_id,event_key,event_type,group_id,member_id,payload,kind,status,reply_reserved_at) VALUES('old','app','old','GROUP_MESSAGE_CREATE','group','alice','{}','ambient_chat','delivery_failed',NOW()-INTERVAL '3 minutes')`)
				require.NoError(t, err)
			case "failed_send_cooldown":
				_, err := h.db.Exec(`INSERT INTO qq_bot_jobs(id,app_id,event_key,event_type,group_id,member_id,payload,kind,status,reply_reserved_at) VALUES('old','app','old','GROUP_MESSAGE_CREATE','group','bob','{}','ambient_chat','delivery_failed',NOW())`)
				require.NoError(t, err)
			}
			h.run()
			require.Zero(t, h.calls.Load())
			require.Zero(t, h.sends.Load())
			var count int
			require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_daily_usage WHERE kind='ambient_chat'`).Scan(&count))
			require.Zero(t, count)
			reasons := map[string]string{"disabled": "config_changed", "stale": "stale", "new_message": "conversation_moved", "member_cooldown": "member_cooldown", "failed_send_cooldown": "group_cooldown"}
			require.Equal(t, reasons[mode], h.job(t, "one").DecisionReason)
		})
	}
	h := newAutoHarness(t)
	h.c.TotalDailyChatLimit = 1
	h.save(t)
	_, err := h.db.Exec(`INSERT INTO qq_bot_daily_usage(app_id,scope,day,kind,count) VALUES('app','all',$1,'chat',1)`, day(time.Now()))
	require.NoError(t, err)
	h.enqueue(t, "budget", "这个报错怎么处理？", "alice")
	h.run()
	require.Zero(t, h.calls.Load())
	require.Equal(t, "global_limit", h.job(t, "budget").DecisionReason)
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_daily_usage WHERE kind='ambient_chat'`).Scan(&count))
	require.Zero(t, count, "group reservation rolls back when global quota fails")
}
func TestAutonomousFollowupCannotBeRetriedAfterModelSkip(t *testing.T) {
	h := newAutoHarness(t)
	h.enqueue(t, "one", "这个报错怎么处理？", "alice")
	h.run()
	h.output.Store(`{"action":"skip"}`)
	h.enqueue(t, "two", "那如何进一步检查？", "alice")
	h.run()
	h.enqueue(t, "three", "那还能怎么继续检查？", "alice")
	h.run()
	require.EqualValues(t, 2, h.calls.Load())
	require.EqualValues(t, 1, h.sends.Load())
}

func TestAutonomousMentionDeliveryUpgradesPendingCandidate(t *testing.T) {
	h := newAutoHarness(t)
	p := h.enqueue(t, "both", "这个故障怎么排查？", "alice")
	p.Type = groupAtMessageCreate
	require.NoError(t, h.s.Enqueue(context.Background(), h.c, p))
	h.output.Store("正常回答")
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	require.Equal(t, "chat", h.job(t, "both").Kind)
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT count FROM qq_bot_daily_usage WHERE scope='member:alice' AND kind='chat'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs WHERE message_id='both'`).Scan(&count))
	require.Equal(t, 1, count)
}
func TestAutonomousZeroHistoryStillRepliesWithoutFollowup(t *testing.T) {
	h := newAutoHarness(t)
	h.c.HistoryMessages = 0
	h.save(t)
	h.enqueue(t, "one", "这个故障怎么排查？", "alice")
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	h.enqueue(t, "two", "那应该怎么继续排查？", "alice")
	h.run()
	require.EqualValues(t, 1, h.calls.Load())
	var count int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&count))
	require.Zero(t, count)
}
