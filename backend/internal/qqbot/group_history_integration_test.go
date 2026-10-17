//go:build integration

package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func sharedTestEvent(id, group, member, text string) Event {
	e := Event{ID: id, GroupID: group, Content: text}
	e.Author.MemberID = member
	e.Author.Username = "昵称-" + member
	return e
}

func enqueueSharedTestEvent(t *testing.T, s *Service, c Config, e Event, eventType string) {
	t.Helper()
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, s.Enqueue(context.Background(), c, Payload{Type: eventType, Data: raw}))
}

func sharedHistoryText(messages []Message) string {
	var content strings.Builder
	for _, m := range messages {
		content.WriteString(m.Content)
	}
	return content.String()
}

func TestQQBotSharedAmbientHistoryAndIsolation(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	alice := sharedTestEvent("a", "group", "alice", "今晚吃火锅")
	bob := sharedTestEvent("b", "group", "bob", "我也想去")
	enqueueSharedTestEvent(t, s, c, alice, groupMessageCreate)
	enqueueSharedTestEvent(t, s, c, bob, groupMessageCreate)
	for _, table := range []string{"qq_bot_jobs", "qq_bot_daily_usage"} {
		var n int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		require.Zero(t, n)
	}
	a := s.history(ctx, c, Job{GroupID: "group", MemberID: "alice"})
	require.Equal(t, a, s.history(ctx, c, Job{GroupID: "group", MemberID: "bob"}))
	require.Len(t, a, 4)
	require.Equal(t, c.SystemPrompt, a[0].Content)
	require.Contains(t, a[2].Content, groupSpeaker(c.AppID, "group", "alice"))
	require.Contains(t, a[3].Content, "昵称-bob")
	require.Len(t, s.history(ctx, c, Job{GroupID: "other-group"}), 2)
	otherApp := c
	otherApp.AppID = "other-app"
	require.Len(t, s.history(ctx, otherApp, Job{GroupID: "group"}), 2)
	// Current input is excluded, and messages received after it are not injected
	// into an older queued question. Both QQ delivery types remain idempotent.
	enqueueSharedTestEvent(t, s, c, bob, groupAtMessageCreate)
	enqueueSharedTestEvent(t, s, c, sharedTestEvent("future", "group", "charlie", "后来的消息"), groupMessageCreate)
	current := s.history(ctx, c, Job{GroupID: "group", MemberID: "bob", MessageID: "b", CreatedAt: time.Now()})
	require.Len(t, current, 3)
	require.Contains(t, current[2].Content, "今晚吃火锅")
	require.NotContains(t, sharedHistoryText(current), "后来的消息")
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages WHERE message_key='user:b'`).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs WHERE message_id='b'`).Scan(&n))
	require.Equal(t, 1, n)
	// Alice's answer arrives after Bob's input, but belongs before Bob's turn.
	// The job timestamp can also be later than Bob's event under concurrency.
	j := Job{GroupID: "group", MemberID: "alice", MessageID: "a", CreatedAt: time.Now()}
	require.NoError(t, s.rememberSentMessage(ctx, c, j, []byte(`{"id":"answer-a"}`), "先回复 Alice", false, true))
	current = s.history(ctx, c, Job{GroupID: "group", MessageID: "b"})
	require.Len(t, current, 4)
	require.Contains(t, current[3].Content, "先回复 Alice")
}

func TestQQBotSharedHistoryCountIdleAndDisable(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	c.HistoryMessages = 2
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		enqueueSharedTestEvent(t, s, c, sharedTestEvent(fmt.Sprint(i), "group", "member", fmt.Sprintf("message-%d", i)), groupMessageCreate)
	}
	history := s.history(ctx, c, Job{GroupID: "group", MessageID: "4"})
	require.Len(t, history, 4)
	require.Contains(t, history[2].Content, "message-2")
	require.Contains(t, history[3].Content, "message-3")
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Equal(t, 3, n)
	// A continuously active conversation does not lose an older retained message
	// solely because that individual message is older than thirty minutes.
	_, err = db.Exec(`UPDATE qq_bot_group_messages SET created_at=NOW()-INTERVAL '31 minutes' WHERE message_key='user:2'`)
	require.NoError(t, err)
	require.Contains(t, sharedHistoryText(s.history(ctx, c, Job{GroupID: "group", MessageID: "4"})), "message-2")
	_, err = db.Exec(`UPDATE qq_bot_group_messages SET created_at=NOW()-INTERVAL '31 minutes'`)
	require.NoError(t, err)
	require.Len(t, s.history(ctx, c, Job{GroupID: "group"}), 2)
	enqueueSharedTestEvent(t, s, c, sharedTestEvent("new", "group", "member", "新话题"), groupMessageCreate)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Equal(t, 1, n)
	require.NotContains(t, sharedHistoryText(s.history(ctx, c, Job{GroupID: "group"})), "message-")
	stale := sharedTestEvent("stale", "group", "member", "过期重投")
	stale.Timestamp = json.RawMessage(fmt.Sprintf("%d", time.Now().Add(-31*time.Minute).Unix()))
	enqueueSharedTestEvent(t, s, c, stale, groupMessageCreate)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Equal(t, 1, n)
	old := c
	c.HistoryMessages = 0
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	// Even a handler holding the previous config cannot resurrect disabled history.
	require.NoError(t, s.recordGroupEvent(ctx, old, sharedTestEvent("late", "group", "member", "迟到")))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Zero(t, n)
	require.Len(t, s.history(ctx, c, Job{GroupID: "group"}), 2)
	c.HistoryMessages = 12
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	require.Len(t, s.history(ctx, c, Job{GroupID: "group"}), 2)
}

func TestQQBotSharedHistoryBudgetAndConcurrentAppend(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	c.HistoryMessages = 100
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 32; i++ {
		e := sharedTestEvent(fmt.Sprint(i), "group", fmt.Sprint(i), fmt.Sprintf("topic-%d: ", i)+strings.Repeat("中文🙂 long mixed text! ", 1000))
		for repeat := 0; repeat < 2; repeat++ {
			wg.Add(1)
			go func() { defer wg.Done(); errs <- s.recordGroupEvent(ctx, c, e) }()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Equal(t, 32, n)
	// A new short message must survive when older messages exhaust the budget.
	require.NoError(t, s.recordGroupEvent(ctx, c, sharedTestEvent("latest", "group", "last", "最新消息必须保留")))
	history := s.history(ctx, c, Job{GroupID: "group"})
	tokens := 0
	for _, m := range history[2:] {
		tokens += groupHistoryTokens(m.Content) + 8
		var entry groupHistoryEntry
		require.NoError(t, json.Unmarshal([]byte(m.Content), &entry))
	}
	require.LessOrEqual(t, tokens, groupHistoryTokenBudget)
	require.Contains(t, history[len(history)-1].Content, "最新消息必须保留")
	require.Less(t, len(history), 34)
	c.HistoryMessages = 3
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	require.NoError(t, s.recordGroupEvent(ctx, c, sharedTestEvent("trim", "group", "last", "只保留配置条数")))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Equal(t, 4, n)
}

func TestQQBotSharedHistoryExcludesAccountCommandsAndQuotedResults(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	for i, text := range []string{"/绑定 private-code", "/转账 10 secret@example.invalid", "/我的余额", "/自定义 secret"} {
		require.NoError(t, s.recordGroupEvent(ctx, c, sharedTestEvent(fmt.Sprint(i), "group", "alice", text)))
	}
	j := Job{GroupID: "group", MemberID: "alice", MessageID: "cmd", CreatedAt: time.Now()}
	require.NoError(t, s.rememberSentMessage(ctx, c, j, []byte(`{"id":"private-reply"}`), "balance and https://example.invalid/confirm#secret", false, false))
	quote := sharedTestEvent("quote", "group", "bob", "解释这个余额")
	quote.MessageReference.MessageID = "private-reply"
	require.NoError(t, s.recordGroupEvent(ctx, c, quote))
	ref, err := s.referencedMessage(ctx, c, "group", "private-reply")
	require.NoError(t, err)
	require.NotNil(t, ref)
	require.False(t, shareableGroupEvent(quote, ref))
	input, err := s.chatMessage(ctx, quote, quote.Content, ref)
	require.NoError(t, err)
	require.Contains(t, input.Content, "confirm#secret", "explicit quote is still available to this request only")
	require.Len(t, s.history(ctx, c, Job{GroupID: "group"}), 2)
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&n))
	require.Zero(t, n)
	// Only a successfully delivered, shareable AI answer enters the history.
	require.NoError(t, s.rememberSentMessage(ctx, c, j, []byte(`{"id":"chat-reply"}`), "普通群聊回复", false, true))
	require.Len(t, s.history(ctx, c, Job{GroupID: "group"}), 3)
}

func TestQQBotSharedGroupJobsAreSerial(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	for _, e := range []Event{sharedTestEvent("a", "group", "alice", "问题一"), sharedTestEvent("b", "group", "bob", "问题二"), sharedTestEvent("c", "other-group", "charlie", "另一个群")} {
		enqueueSharedTestEvent(t, s, c, e, groupAtMessageCreate)
	}
	first, err := s.claimJob(ctx)
	require.NoError(t, err)
	require.Equal(t, "a", first.MessageID)
	second, err := s.claimJob(ctx)
	require.NoError(t, err)
	require.Equal(t, "c", second.MessageID)
	_, err = s.claimJob(ctx)
	require.ErrorIs(t, err, sql.ErrNoRows)
	s.finish(first, "completed", "")
	third, err := s.claimJob(ctx)
	require.NoError(t, err)
	require.Equal(t, "b", third.MessageID)
}
