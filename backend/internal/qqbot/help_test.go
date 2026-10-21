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
	c.CustomCommands = append(c.CustomCommands, CustomCommand{Command: "/disabled", Enabled: false})
	inputCommands := []string{"/绑定 ", "/奖励兑换 ", "/生图 ", "/转账 ", "/价格 ", "/领取任务 "}
	var got []string
	for page := 1; page <= 4; page++ {
		j := Job{EventType: groupMessageCreate, GroupID: "group", MemberID: "menu-requester", MessageID: fmt.Sprint(page), CreatedAt: time.Now()}
		require.NoError(t, s.message(context.Background(), c, &j, Event{Content: fmt.Sprintf("/帮助 %d", page)}))
		require.Len(t, *replies, page)
		reply := (*replies)[page-1]
		require.Equal(t, j.MessageID, reply.MsgID)
		require.Empty(t, reply.EventID)
		require.Contains(t, reply.text(), `<qqbot-at-user id="menu-requester" />`)
		require.Contains(t, reply.text(), fmt.Sprintf("第 %d/4 页", page))
		require.Contains(t, reply.text(), "点击按钮填入指令，再点发送")
		require.NotContains(t, reply.text(), "/disabled")
		if page > 1 {
			require.Contains(t, reply.text(), "指令帮助 · 自定义指令")
			require.NotContains(t, reply.text(), "**/签到**")
		} else {
			require.Contains(t, reply.text(), "指令帮助 · 系统指令")
			require.NotContains(t, reply.text(), "自定义回复")
		}
		require.LessOrEqual(t, utf8.RuneCountInString(reply.text()), 1800)
		require.NotNil(t, reply.Keyboard)
		require.LessOrEqual(t, len(reply.Keyboard.Content.Rows), 5)
		ids := map[string]bool{}
		var navigation []string
		for _, row := range reply.Keyboard.Content.Rows {
			if page > 1 {
				require.LessOrEqual(t, len(row.Buttons), 2)
			} else {
				require.LessOrEqual(t, len(row.Buttons), 5)
			}
			for _, button := range row.Buttons {
				require.False(t, ids[button.ID])
				ids[button.ID] = true
				require.LessOrEqual(t, utf8.RuneCountInString(button.RenderData.Label), 10)
				require.NotEmpty(t, button.RenderData.Label)
				require.Equal(t, button.RenderData.Label, button.RenderData.VisitedLabel, "clicked labels must stay visible")
				require.Equal(t, 2, button.Action.Permission.Type, "any member may click using their own identity")
				require.Equal(t, 2, button.Action.Type, "new menus must not use callbacks that trigger QQ's success toast")
				if strings.HasPrefix(button.Action.Data, "/帮助 ") {
					navigation = append(navigation, button.Action.Data)
					continue
				}
				if button.RenderData.Style == 0 {
					require.Contains(t, inputCommands, button.Action.Data, "parameter buttons must only fill the command prefix")
				} else {
					require.Equal(t, 1, button.RenderData.Style)
					require.Equal(t, strings.TrimSpace(button.Action.Data), button.Action.Data)
				}
				got = append(got, strings.TrimSpace(button.Action.Data))
			}
		}
		var wantNavigation []string
		if page > 1 {
			wantNavigation = append(wantNavigation, fmt.Sprintf("/帮助 %d", page-1))
		}
		if page < 4 {
			wantNavigation = append(wantNavigation, fmt.Sprintf("/帮助 %d", page+1))
		}
		require.Equal(t, wantNavigation, navigation)
		raw, err := json.Marshal(reply.Keyboard)
		require.NoError(t, err)
		require.Equal(t, len(ids), strings.Count(string(raw), `"visited_label":`))
		require.NotContains(t, string(raw), `"enter":true`, "QQ group commands must not rely on C2C-only automatic sending")
	}
	require.Equal(t, want, got, "every command must remain reachable without truncation or duplicate actions")
}

func TestHelpSystemCommandsOccupyFirstPage(t *testing.T) {
	for _, count := range []int{0, 1, 8, 9, 16, 17, maxCustomCommands} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, replies := helpTestSender(t)
			c := DefaultConfig()
			c.CustomCommands = []CustomCommand{{Command: "/disabled", Enabled: false}}
			for i := 0; i < count; i++ {
				c.CustomCommands = append(c.CustomCommands, CustomCommand{Command: fmt.Sprintf("/custom%d", i), Enabled: true})
			}
			pages := (count+7)/8 + 1
			seen := 0
			for page := 1; page <= pages; page++ {
				j := Job{GroupID: "group", MessageID: fmt.Sprint(page), CreatedAt: time.Now()}
				require.NoError(t, s.sendHelp(context.Background(), c, j, fmt.Sprint(page)))
				reply := (*replies)[page-1]
				require.NotNil(t, reply.Keyboard)
				require.Contains(t, reply.text(), fmt.Sprintf("第 %d/%d 页", page, pages))
				require.NotContains(t, reply.text(), "/disabled")
				if page == 1 {
					require.Contains(t, reply.text(), "指令帮助 · 系统指令")
					require.Equal(t, 17, strings.Count(reply.text(), "- **/"))
					require.NotContains(t, reply.text(), "自定义回复")
				} else {
					require.Contains(t, reply.text(), "指令帮助 · 自定义指令")
					seen += strings.Count(reply.text(), "：自定义回复")
				}
				if pages == 1 {
					require.Len(t, reply.Keyboard.Content.Rows, 4, "no empty custom page or navigation row")
				}
			}
			require.Equal(t, count, seen)
		})
	}
}

func TestHelpInvalidPagesAndActionAllowlist(t *testing.T) {
	s, replies := helpTestSender(t)
	c := DefaultConfig()
	c.CustomCommands = []CustomCommand{{Command: "/disabled", Enabled: false}, {Command: "/hello", Enabled: true, Reply: "hello"}}
	for _, arg := range []string{"0", "-1", "abc", "9999999999999999999999999", "3"} {
		j := Job{GroupID: "group", MessageID: "invalid", CreatedAt: time.Now()}
		require.NoError(t, s.sendHelp(context.Background(), c, j, arg))
		require.Nil(t, (*replies)[len(*replies)-1].Keyboard)
	}
	for _, action := range []string{"", "checkin\n/抽奖", "bind", "redeem", "image", "transfer", "custom:/disabled", "custom:/签到", "page:0", "page:01", "page:3", "prices", "growth_claim", "/转账 1 someone@example.com"} {
		_, ok := helpActionCommand(c, action)
		require.False(t, ok, action)
	}
	cmd, ok := helpActionCommand(c, "custom:/hello")
	require.True(t, ok)
	require.Equal(t, "/hello", cmd)
	for action, want := range map[string]string{"checkin": "/签到", "growth": "/成长任务", "page:1": "/帮助 1", "page:2": "/帮助 2"} {
		cmd, ok := helpActionCommand(c, action)
		require.True(t, ok, "old callback cards still resolve supported actions")
		require.Equal(t, want, cmd)
	}
}

func TestInteractionReplyUsesEventIDAndClickingMember(t *testing.T) {
	s, replies := helpTestSender(t)
	j := Job{EventType: interactionCreate, GroupID: "group", MemberID: "clicker", MessageID: "outer-event-id", CreatedAt: time.Now()}
	require.NoError(t, s.message(context.Background(), DefaultConfig(), &j, Event{Content: "/帮助"}))
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
