//go:build integration

package qqbot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQQBotFullGroupCommandsAndMentionedModels(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	uid := user(t, db)
	bind(t, db, uid, "member")
	var chats, images, replies atomic.Int32
	var lastReply atomic.Value
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 16, 16))))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"qq-test-only","expires_in":7200}`))
		case "/v2/groups/group/bot_state":
			_, _ = w.Write([]byte(`{"member_openid":"this-bot"}`))
		case "/v1/chat/completions":
			chats.Add(1)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer"}}]}`))
		case "/v1/images/generations":
			images.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(picture.Bytes())}}})
		case "/v2/groups/group/files":
			_, _ = w.Write([]byte(`{"file_info":"test-image"}`))
		case "/v2/groups/group/messages":
			var body struct {
				Content string `json:"content"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			lastReply.Store(body.Content)
			replies.Add(1)
			_, _ = w.Write([]byte(`{"id":"sent"}`))
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	c.BaseURL = upstream.URL + "/v1"
	c.ImageEnabled, c.ImageModel = true, "image-model"
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	s.qqURL, s.tokenURL = upstream.URL, upstream.URL+"/token"
	enqueue := func(id, content, member string, mentions []Mention) Payload {
		e := Event{ID: id, GroupID: "group", Content: content, Mentions: mentions}
		e.Author.MemberID = member
		raw, marshalErr := json.Marshal(e)
		require.NoError(t, marshalErr)
		p := Payload{ID: "delivery-" + id, Type: groupMessageCreate, Data: raw}
		require.NoError(t, s.Enqueue(ctx, c, p))
		return p
	}
	for _, content := range []string{"你好", "/生图 一只猫", "/不存在"} {
		enqueue("ignored-"+content, content, "member", nil)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs`).Scan(&count))
	require.Zero(t, count)
	checkin := enqueue("checkin", "/签到", "member", nil)
	// QQ can deliver the same message through both event types, in either order.
	checkin.Type, checkin.ID = groupAtMessageCreate, "other-checkin-delivery"
	require.NoError(t, s.Enqueue(ctx, c, checkin))
	s.runOne(ctx)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, .1, balance)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs WHERE message_id='checkin'`).Scan(&count))
	require.Equal(t, 1, count)
	require.EqualValues(t, 1, replies.Load())

	// The binding-code flow also works without a mention.
	secondUser := user(t, db)
	code, err := s.CreateBindingCode(ctx, secondUser)
	require.NoError(t, err)
	enqueue("binding", "/绑定 "+code.Code, "new-member", nil)
	s.runOne(ctx)
	require.NoError(t, s.ConfirmBinding(ctx, secondUser, "new-member"))
	bound, _, err := s.Binding(ctx, c.AppID, secondUser)
	require.NoError(t, err)
	require.Equal(t, "new-member", bound.MemberID)
	for _, content := range []string{"/帮助", "/我的额度", "/我的奖励", "/邀请活动", "/我的邀请", "/抽奖次数", "/抽奖活动"} {
		enqueue("command-"+content, content, "member", nil)
		s.runOne(ctx)
		var status string
		require.NoError(t, db.QueryRow(`SELECT status FROM qq_bot_jobs WHERE message_id=$1`, "command-"+content).Scan(&status))
		require.Equal(t, "completed", status)
		if content == "/我的邀请" {
			require.Contains(t, lastReply.Load(), "累计邀请：0 人")
		}
	}
	enqueue("unbound-invites", "/我的邀请", "unbound-member", nil)
	s.runOne(ctx)
	require.Contains(t, lastReply.Load(), "累计邀请：0 人")
	require.Zero(t, chats.Load())
	require.Zero(t, images.Load())
	beforeModels := replies.Load()
	for i, content := range []string{"你好", "/签到", "/抽奖", "/我的邀请"} {
		enqueue(fmt.Sprintf("other-mention-%d", i), content, "member", []Mention{{ID: "another-bot"}})
		s.runOne(ctx)
	}
	require.Equal(t, beforeModels, replies.Load())
	require.Zero(t, chats.Load())

	p := enqueue("chat", "你好", "member", []Mention{{ID: "this-bot"}})
	s.runOne(ctx)
	p.Type, p.ID = groupAtMessageCreate, "other-chat-delivery"
	require.NoError(t, s.Enqueue(ctx, c, p))
	s.runOne(ctx)
	require.EqualValues(t, 1, chats.Load())
	require.Equal(t, beforeModels+2, replies.Load())
	enqueue("image", "/生图 一只猫", "member", []Mention{{MemberID: "this-bot"}})
	s.runOne(ctx)
	require.EqualValues(t, 1, images.Load())
	require.Equal(t, beforeModels+4, replies.Load())

	// The mention event can arrive first and omit mentions of the bot itself.
	e := Event{ID: "mention-first", GroupID: "group", Content: "继续"}
	e.Author.MemberID = "another-member"
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	require.NoError(t, s.Enqueue(ctx, c, Payload{ID: "mention-delivery", Type: groupAtMessageCreate, Data: raw}))
	s.runOne(ctx)
	enqueue(e.ID, e.Content, e.Author.MemberID, []Mention{{ID: "this-bot"}})
	s.runOne(ctx)
	require.EqualValues(t, 2, chats.Load())
	require.Equal(t, beforeModels+6, replies.Load())
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs WHERE message_id='mention-first'`).Scan(&count))
	require.Equal(t, 1, count)

	enqueue("expired-command", "/帮助", "member", nil)
	_, err = db.Exec(`UPDATE qq_bot_jobs SET created_at=NOW()-INTERVAL '6 minutes' WHERE message_id='expired-command'`)
	require.NoError(t, err)
	s.maintenance(ctx)
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM qq_bot_jobs WHERE message_id='expired-command'`).Scan(&status))
	require.Equal(t, "expired", status)
}
