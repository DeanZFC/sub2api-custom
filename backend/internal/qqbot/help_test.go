package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func helpTestSender(t *testing.T) (*Service, *[]qqTestReply) {
	t.Helper()
	var replies []qqTestReply
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"test-only","expires_in":7200}`))
		case "/v2/groups/group/messages":
			replies = append(replies, readQQTestReply(t, r))
			_, _ = w.Write([]byte(`{"id":"sent"}`))
		default:
			t.Errorf("help unexpectedly called %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	s := New(nil, nil, false, nil)
	s.qqURL, s.tokenURL = server.URL, server.URL+"/token"
	return s, &replies
}

func TestHelpKeyboardCommandsAndPagination(t *testing.T) {
	s, replies := helpTestSender(t)
	c := DefaultConfig()
	for i := 0; i < maxCustomCommands; i++ {
		c.CustomCommands = append(c.CustomCommands, CustomCommand{Command: fmt.Sprintf("/%02d%s", i, strings.Repeat("长", 30)), Reply: "reply", Enabled: true})
	}
	want := []string{"/签到", "/我的余额", "/我的奖励", "/渠道状态", "/抽奖", "/抽奖次数", "/邀请奖励", "/我的邀请", "/抽奖活动", "/绑定", "/奖励兑换", "/生图", "/转账", "/价格", "/今日用量", "/成长任务", "/领取任务"}
	for _, custom := range c.CustomCommands {
		want = append(want, custom.Command)
	}
	var got []string
	for page := 1; page <= 5; page++ {
		j := Job{EventType: groupMessageCreate, GroupID: "group", MemberID: "menu-requester", MessageID: fmt.Sprint(page), CreatedAt: time.Now()}
		require.NoError(t, s.message(context.Background(), c, &j, Event{Content: fmt.Sprintf("/帮助 %d", page)}))
		require.Len(t, *replies, page)
		reply := (*replies)[page-1]
		require.Equal(t, j.MessageID, reply.MsgID)
		require.Empty(t, reply.EventID)
		require.Contains(t, reply.text(), `<qqbot-at-user id="menu-requester" />`)
		require.Contains(t, reply.text(), fmt.Sprintf("第 %d/5 页", page))
		require.LessOrEqual(t, utf8.RuneCountInString(reply.text()), 1800)
		require.NotNil(t, reply.Keyboard)
		require.LessOrEqual(t, len(reply.Keyboard.Content.Rows), 5)
		ids := map[string]bool{}
		for _, row := range reply.Keyboard.Content.Rows {
			require.LessOrEqual(t, len(row.Buttons), 3)
			for _, button := range row.Buttons {
				require.False(t, ids[button.ID])
				ids[button.ID] = true
				require.LessOrEqual(t, utf8.RuneCountInString(button.RenderData.Label), 10)
				require.Equal(t, 2, button.Action.Permission.Type, "any member may click using their own identity")
				if button.Action.Type == 2 {
					require.Contains(t, []string{"/绑定 ", "/奖励兑换 ", "/生图 ", "/转账 ", "/价格 ", "/领取任务 "}, button.Action.Data)
					got = append(got, strings.TrimSpace(button.Action.Data))
					continue
				}
				require.Equal(t, 1, button.Action.Type, "direct execution in groups requires callback buttons")
				action := strings.TrimPrefix(button.Action.Data, helpActionPrefix)
				cmd, ok := helpActionCommand(c, action)
				require.True(t, ok)
				if strings.HasPrefix(action, "page:") {
					require.True(t, strings.HasPrefix(cmd, "/帮助 "))
					continue
				}
				got = append(got, cmd)
			}
		}
	}
	require.Equal(t, want, got, "every command must remain reachable without truncation or duplicate actions")
}

func TestHelpInvalidPagesAndActionAllowlist(t *testing.T) {
	s, replies := helpTestSender(t)
	c := DefaultConfig()
	c.CustomCommands = []CustomCommand{{Command: "/disabled", Enabled: false}, {Command: "/hello", Enabled: true, Reply: "hello"}}
	for _, arg := range []string{"0", "-1", "abc", "9999999999999999999999999", "4"} {
		j := Job{GroupID: "group", MessageID: "invalid", CreatedAt: time.Now()}
		require.NoError(t, s.sendHelp(context.Background(), c, j, arg))
		require.Nil(t, (*replies)[len(*replies)-1].Keyboard)
	}
	for _, action := range []string{"", "checkin\n/抽奖", "bind", "redeem", "image", "transfer", "custom:/disabled", "custom:/签到", "page:0", "page:01", "page:4", "prices", "growth_claim", "/转账 1 someone@example.com"} {
		_, ok := helpActionCommand(c, action)
		require.False(t, ok, action)
	}
	cmd, ok := helpActionCommand(c, "custom:/hello")
	require.True(t, ok)
	require.Equal(t, "/hello", cmd)
}

func TestInteractionReplyUsesEventIDAndClickingMember(t *testing.T) {
	s, replies := helpTestSender(t)
	j := Job{EventType: interactionCreate, GroupID: "group", MemberID: "clicker", MessageID: "outer-event-id", CreatedAt: time.Now()}
	require.NoError(t, s.message(context.Background(), DefaultConfig(), &j, Event{Content: "/帮助 2"}))
	require.Len(t, *replies, 1)
	reply := (*replies)[0]
	require.Equal(t, "outer-event-id", reply.EventID)
	require.Empty(t, reply.MsgID)
	require.Contains(t, reply.text(), `<qqbot-at-user id="clicker" />`)
	require.NotNil(t, reply.Keyboard)
	// Prefill buttons never contain an auto-send option or example arguments.
	raw, err := json.Marshal(reply.Keyboard)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"enter":true`)
	require.NotContains(t, string(raw), "邮箱或绑定码")
}
