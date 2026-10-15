//go:build integration

package qqbot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type testEncryptor struct{}

func (testEncryptor) Encrypt(s string) (string, error) { return "encrypted:" + s, nil }
func (testEncryptor) Decrypt(s string) (string, error) { return s[len("encrypted:"):], nil }

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("QQBOT_TEST_DSN")
	if dsn == "" {
		t.Skip("QQBOT_TEST_DSN is required")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "qqbot_test_" + randomID()
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); _ = admin.Close() })
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 CREATE TABLE users(id BIGSERIAL PRIMARY KEY,email TEXT NOT NULL DEFAULT '',balance NUMERIC(20,8) NOT NULL DEFAULT 0,status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 CREATE TABLE redeem_codes(id BIGSERIAL PRIMARY KEY,code VARCHAR(32) UNIQUE NOT NULL,type VARCHAR(20),value NUMERIC(20,8),status TEXT,used_by BIGINT REFERENCES users(id),used_at TIMESTAMPTZ,notes TEXT);`)
	require.NoError(t, err)
	raw, err := migrations.FS.ReadFile("271_qq_bot.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	raw, err = migrations.FS.ReadFile("272_qq_bot_email_binding.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	return db
}
func savedBot(t *testing.T, db *sql.DB) *Service {
	t.Helper()
	s := New(db, testEncryptor{}, true, nil)
	c := DefaultConfig()
	c.Enabled = true
	c.AppID = "app"
	c.AppSecret = "test-secret"
	c.BaseURL = "https://example.invalid/v1"
	c.APIKey = "operator-key"
	c.ChatModel = "test-model"
	c.AllowedGroups = []string{"group", "other-group"}
	c.CheckinReward = .1
	c.InviteReward = 1
	c.CooldownSeconds = 0
	_, err := s.SaveConfig(context.Background(), c)
	require.NoError(t, err)
	return s
}
func user(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRow(`INSERT INTO users DEFAULT VALUES RETURNING id`).Scan(&id))
	return id
}
func bind(t *testing.T, db *sql.DB, uid int64, member string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO qq_bot_bindings(app_id,member_id,user_id,group_id) VALUES('app',$1,$2,'group')`, member, uid)
	require.NoError(t, err)
}

func TestQQBotRewardsAreAtomicAndDeduplicated(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	c, err := s.Config(context.Background())
	require.NoError(t, err)
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.reward(context.Background(), c, Job{GroupID: "group", MemberID: "member"}, "checkin")
			if err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, failures.Load())
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, .1, balance)
	var records int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_rewards`).Scan(&records))
	require.Equal(t, 1, records)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM redeem_codes`).Scan(&records))
	require.Equal(t, 1, records)
	_, err = s.reward(context.Background(), c, Job{GroupID: "other-group", MemberID: "member"}, "checkin")
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, .1, balance)
	// A history write failure rolls back both the credit and its eligibility key.
	uid2 := user(t, db)
	bind(t, db, uid2, "member2")
	_, err = db.Exec(`ALTER TABLE redeem_codes ADD CONSTRAINT reject_second CHECK(used_by<>` + strconv.FormatInt(uid2, 10) + `)`)
	require.NoError(t, err)
	_, err = s.reward(context.Background(), c, Job{GroupID: "group", MemberID: "member2"}, "checkin")
	require.Error(t, err)
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid2).Scan(&balance))
	require.Zero(t, balance)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_rewards WHERE user_id=$1`, uid2).Scan(&records))
	require.Zero(t, records)
}

func TestQQBotBindingNeedsWebsiteConfirmation(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	uid := user(t, db)
	c, err := s.Config(ctx)
	require.NoError(t, err)
	r, err := s.CreateBindingCode(ctx, uid)
	require.NoError(t, err)
	require.Len(t, r.Code, 24)
	e := Event{GroupID: "group"}
	e.Author.MemberID = "member"
	e.Author.Username = "Alice"
	require.Contains(t, s.claimBinding(ctx, c, e, r.Code), "确认")
	b, p, err := s.Binding(ctx, c.AppID, uid)
	require.NoError(t, err)
	require.Nil(t, b)
	require.Equal(t, "member", p.MemberID)
	require.Error(t, s.ConfirmBinding(ctx, uid, "attacker"))
	require.NoError(t, s.ConfirmBinding(ctx, uid, "member"))
	b, p, err = s.Binding(ctx, c.AppID, uid)
	require.NoError(t, err)
	require.NotNil(t, b)
	require.Nil(t, p)
	require.Contains(t, s.claimBinding(ctx, c, e, r.Code), "过期")
	uid2 := user(t, db)
	r, err = s.CreateBindingCode(ctx, uid2)
	require.NoError(t, err)
	s.claimBinding(ctx, c, e, r.Code)
	require.Error(t, s.ConfirmBinding(ctx, uid2, "member"))
}

func TestQQBotInvitationRequiresJoinAndBothBindings(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	inviter, invitee := user(t, db), user(t, db)
	bind(t, db, inviter, "inviter")
	e := Event{GroupID: "group", MemberID: "invitee", InvitedBy: "inviter", ApplySource: "invited", ApplyAt: time.Now().Add(-time.Minute).Format(time.RFC3339)}
	j := Job{GroupID: "group", MemberID: "invitee", EventType: "GROUP_JOIN_REQUEST"}
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	bind(t, db, invitee, "invitee")
	result, err := s.reward(ctx, c, j, "invite")
	require.NoError(t, err)
	require.Contains(t, result, "尚无")
	e.Timestamp = json.RawMessage(strconv.FormatInt(time.Now().Unix(), 10))
	j.EventType = "GROUP_MEMBER_ADD"
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, inviter).Scan(&balance))
	require.Equal(t, 1.0, balance)
	j.EventType = "GROUP_MEMBER_REMOVE"
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	j.EventType = "GROUP_MEMBER_ADD"
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, inviter).Scan(&balance))
	require.Equal(t, 1.0, balance)
	// Existing members cannot acquire an invitation retroactively.
	old := user(t, db)
	bind(t, db, old, "old")
	j.MemberID = "old"
	e.MemberID = "old"
	j.EventType = "GROUP_MEMBER_ADD"
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	j.EventType = "GROUP_JOIN_REQUEST"
	e.ApplyAt = time.Now().Add(time.Minute).Format(time.RFC3339)
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	_, err = s.reward(ctx, c, j, "invite")
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, inviter).Scan(&balance))
	require.Equal(t, 1.0, balance)
}

func TestQQBotDailyLimitsAndEventDedup(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	c.TotalDailyChatLimit = 5
	c.DailyChatLimit = 3
	e := Event{ID: "message", GroupID: "group", Content: "hi"}
	e.Author.MemberID = "member"
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	p := Payload{ID: "delivery-a", Type: "GROUP_AT_MESSAGE_CREATE", Data: raw}
	require.NoError(t, s.Enqueue(ctx, c, p))
	p.ID = "delivery-b"
	require.NoError(t, s.Enqueue(ctx, c, p))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_jobs`).Scan(&count))
	require.Equal(t, 1, count)
	j, err := s.claimJob(ctx)
	require.NoError(t, err)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.reserveUsage(ctx, c, j, "chat") == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 3, successes.Load())
	j.MemberID = "second"
	for i := 0; i < 5; i++ {
		if s.reserveUsage(ctx, c, j, "chat") == nil {
			successes.Add(1)
		}
	}
	require.EqualValues(t, 5, successes.Load())
	// No member balance is touched by free usage reservations.
	uid := user(t, db)
	bind(t, db, uid, "member")
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Zero(t, balance)
}

func TestQQBotSecretsAndConversationIsolation(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	var raw string
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key=$1`, configKey).Scan(&raw))
	require.Contains(t, raw, "encrypted:operator-key")
	changed := c.Public()
	changed.CheckinReward = .2
	_, err = s.SaveConfig(ctx, changed)
	require.NoError(t, err)
	got, err := s.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, "operator-key", got.APIKey)
	changed.BaseURL = "https://other.invalid/v1"
	_, err = s.SaveConfig(ctx, changed)
	require.Error(t, err)
	s.saveHistory(ctx, c, Job{GroupID: "group", MemberID: "a"}, []Message{{Role: "user", Content: "private"}, {Role: "assistant", Content: "answer"}})
	require.Len(t, s.history(ctx, c, Job{GroupID: "group", MemberID: "a"}), 3)
	require.Len(t, s.history(ctx, c, Job{GroupID: "group", MemberID: "b"}), 1)
	require.Len(t, s.history(ctx, c, Job{GroupID: "other-group", MemberID: "a"}), 1)
}

func TestQQBotMembershipEventsMayArriveOutOfOrder(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	inviter, invitee := user(t, db), user(t, db)
	bind(t, db, inviter, "inviter")
	bind(t, db, invitee, "invitee")
	now := time.Now().Unix()
	e := Event{GroupID: "group", MemberID: "invitee", Timestamp: json.RawMessage(strconv.FormatInt(now, 10)), ApplySource: "invited", InvitedBy: "inviter", ApplyAt: time.Unix(now-60, 0).Format(time.RFC3339)}
	j := Job{GroupID: "group", MemberID: "invitee", EventType: "GROUP_MEMBER_ADD"}
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	j.EventType = "GROUP_JOIN_REQUEST"
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, inviter).Scan(&balance))
	require.Equal(t, 1.0, balance)
	j.EventType = "GROUP_MEMBER_REMOVE"
	e.Timestamp = json.RawMessage(strconv.FormatInt(now+60, 10))
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	j.EventType = "GROUP_MEMBER_ADD"
	e.Timestamp = json.RawMessage(strconv.FormatInt(now, 10))
	require.NoError(t, s.memberEvent(ctx, c, j, e))
	var left bool
	require.NoError(t, db.QueryRow(`SELECT left_at IS NOT NULL FROM qq_bot_members WHERE member_id='invitee'`).Scan(&left))
	require.True(t, left, "late join must not undo a newer departure")
}

func TestQQBotWorkerChatAndImageResultReplay(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	uid := user(t, db)
	bind(t, db, uid, "member")
	var chatCalls, imageCalls, replies int
	var picture bytes.Buffer
	require.NoError(t, png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 16, 16))))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatCalls++
			require.Equal(t, "Bearer operator-key", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"free response"}}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`))
		case "/v1/images/generations":
			imageCalls++
			require.Equal(t, "Bearer operator-key", r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(picture.Bytes())}}})
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"qq-test-token","expires_in":7200}`))
		case "/v2/groups/group/files":
			_, _ = w.Write([]byte(`{"file_info":"saved-image"}`))
		case "/v2/groups/group/messages":
			replies++
			_, _ = w.Write([]byte(`{"id":"sent"}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	c.BaseURL = upstream.URL + "/v1"
	c.ImageEnabled = true
	c.ImageModel = "image-model"
	_, err = s.SaveConfig(ctx, c)
	require.NoError(t, err)
	s.qqURL = upstream.URL
	s.tokenURL = upstream.URL + "/token"
	enqueue := func(id, content string) Payload {
		e := Event{ID: id, GroupID: "group", Content: content}
		e.Author.MemberID = "member"
		raw, _ := json.Marshal(e)
		p := Payload{ID: id, Type: "GROUP_AT_MESSAGE_CREATE", Data: raw}
		require.NoError(t, s.Enqueue(ctx, c, p))
		return p
	}
	p := enqueue("message1", "hello")
	s.runOne(ctx)
	var status, kind, result string
	require.NoError(t, db.QueryRow(`SELECT status,kind,result_text FROM qq_bot_jobs WHERE message_id='message1'`).Scan(&status, &kind, &result))
	require.Equal(t, "completed", status)
	require.Equal(t, "chat", kind)
	require.Equal(t, "free response", result)
	require.NoError(t, s.Enqueue(ctx, c, p))
	s.runOne(ctx)
	require.Equal(t, 1, chatCalls)
	enqueue("message2", "生图 cat")
	s.runOne(ctx)
	var id string
	var size int
	require.NoError(t, db.QueryRow(`SELECT id,status,octet_length(result_image) FROM qq_bot_jobs WHERE message_id='message2'`).Scan(&id, &status, &size))
	require.Equal(t, "completed", status)
	require.Positive(t, size)
	enqueue("message3", "查看结果 "+id)
	s.runOne(ctx)
	require.Equal(t, 1, imageCalls)
	require.Equal(t, 5, replies)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Zero(t, balance)
}
