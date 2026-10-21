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

func TestHelpCompactHomeAndCompletePagination(t *testing.T) {
	for count := 0; count <= maxCustomCommands; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, replies := helpTestSender(t)
			c := DefaultConfig()
			want := []string{"/签到", "/我的", "/福利", "/渠道状态", "/生图", "/价格", "/绑定", "/转账", "/今日排行榜"}
			for i := 0; i < count; i++ {
				name := fmt.Sprintf("/custom%d", i)
				if count == maxCustomCommands {
					name = "/" + strings.Repeat("长", 30) + fmt.Sprintf("%02d", i)
				}
				c.CustomCommands = append(c.CustomCommands, CustomCommand{Command: name, Reply: "reply", Enabled: true})
				want = append(want, name)
			}
			c.CustomCommands = append(c.CustomCommands, CustomCommand{Command: "/disabled"}, CustomCommand{Command: "/我的余额", Enabled: true}, CustomCommand{Command: "/我的", Enabled: true})
			pages := helpPages(c)
			require.Len(t, pages, 1+(1+count+7)/8)
			var got []string
			for index := range pages {
				page := index + 1
				j := Job{EventType: groupMessageCreate, GroupID: "group", MemberID: "requester", MessageID: fmt.Sprint(page), CreatedAt: time.Now()}
				before := len(*replies)
				require.NoError(t, s.message(context.Background(), c, &j, Event{Content: fmt.Sprintf("/帮助 %d", page)}))
				require.Len(t, *replies, before+1, "one help page must be one QQ message")
				reply := (*replies)[before]
				require.Equal(t, 1, reply.Sequence)
				require.Equal(t, j.MessageID, reply.MsgID)
				require.Empty(t, reply.EventID)
				require.Contains(t, reply.text(), "requester")
				require.Contains(t, reply.text(), fmt.Sprintf("第 %d/%d 页", page, len(pages)))
				require.Contains(t, reply.text(), "点击按钮填入指令，再点发送")
				require.NotContains(t, reply.text(), "/disabled")
				require.NotContains(t, reply.text(), "/我的余额")
				require.LessOrEqual(t, utf8.RuneCountInString(reply.text()), 1800)
				require.NotNil(t, reply.Keyboard)
				require.LessOrEqual(t, len(reply.Keyboard.Content.Rows), 5)
				var pageCommands, navigation []string
				ids := map[string]bool{}
				for _, row := range reply.Keyboard.Content.Rows {
					require.NotEmpty(t, row.Buttons)
					require.LessOrEqual(t, len(row.Buttons), 2)
					for _, button := range row.Buttons {
						require.False(t, ids[button.ID])
						ids[button.ID] = true
						require.Equal(t, button.RenderData.Label, button.RenderData.VisitedLabel)
						require.Equal(t, 2, button.Action.Type)
						require.Equal(t, 2, button.Action.Permission.Type)
						if strings.HasPrefix(button.Action.Data, "/帮助 ") {
							navigation = append(navigation, button.Action.Data)
							continue
						}
						if button.RenderData.Style == 0 {
							require.Contains(t, []string{"/生图 ", "/价格 ", "/绑定 ", "/转账 "}, button.Action.Data)
						}
						pageCommands = append(pageCommands, strings.TrimSpace(button.Action.Data))
					}
				}
				if page == 1 {
					require.Equal(t, want[:8], pageCommands)
					require.Contains(t, reply.text(), "功能菜单")
					require.Contains(t, reply.text(), "## 指令一览")
					for _, command := range []string{"/福利 抽奖", "/福利 领取 任务编号", "/福利 兑换 兑换码", "/今日排行榜"} {
						require.Equal(t, 1, strings.Count(reply.text(), "- **"+command+"**："))
					}
					for _, command := range want[9:] {
						require.Equal(t, 1, strings.Count(reply.text(), "- **"+command+"**："))
					}
					require.Equal(t, 1, strings.Count(reply.text(), "- **/我的**："))
					require.Less(t, strings.Index(reply.text(), "- **/今日排行榜**："), strings.Index(reply.text(), "## 功能菜单"))
				} else {
					require.Contains(t, reply.text(), "更多服务")
				}
				var wantNav []string
				if page > 1 {
					wantNav = append(wantNav, fmt.Sprintf("/帮助 %d", page-1))
				}
				if page < len(pages) {
					wantNav = append(wantNav, fmt.Sprintf("/帮助 %d", page+1))
				}
				require.Equal(t, wantNav, navigation)
				got = append(got, pageCommands...)
				raw, err := json.Marshal(reply.Keyboard)
				require.NoError(t, err)
				require.NotContains(t, string(raw), "\"enter\":true")
			}
			require.Equal(t, want, got, "all enabled services must stay reachable once")
		})
	}
}

func TestHelpInvalidPagesAndActionAllowlist(t *testing.T) {
	s, replies := helpTestSender(t)
	c := DefaultConfig()
	c.CustomCommands = []CustomCommand{{Command: "/disabled", Enabled: false}, {Command: "/hello", Enabled: true, Reply: "hello"}, {Command: "/a", Enabled: true}, {Command: "/b", Enabled: true}, {Command: "/c", Enabled: true}}
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
	for action, want := range map[string]string{"checkin": "/签到", "account": "/我的", "welfare": "/福利", "rankings": "/今日排行榜", "page:1": "/帮助 1", "page:2": "/帮助 2"} {
		cmd, ok := helpActionCommand(c, action)
		require.True(t, ok, "only current actions are allowed")
		require.Equal(t, want, cmd)
	}
}

func TestInteractionReplyUsesEventIDAndClickingMember(t *testing.T) {
	s, replies := helpTestSender(t)
	j := Job{EventType: interactionCreate, GroupID: "group", MemberID: "clicker", MessageID: "outer-event-id", CreatedAt: time.Now()}
	require.NoError(t, s.message(context.Background(), DefaultConfig(), &j, Event{Content: "/帮助"}))
	require.Len(t, *replies, 1)
	for i, reply := range *replies {
		require.Equal(t, "outer-event-id", reply.EventID)
		require.Empty(t, reply.MsgID)
		require.Equal(t, i+1, reply.Sequence)
		require.Contains(t, reply.text(), `<qqbot-at-user id="clicker" />`)
		require.NotNil(t, reply.Keyboard)
		// Prefill buttons never contain an auto-send option or example arguments.
		raw, err := json.Marshal(reply.Keyboard)
		require.NoError(t, err)
		require.NotContains(t, string(raw), `"enter":true`)
		require.NotContains(t, string(raw), "邮箱或绑定码")
	}
}

func TestHelpStopsAfterSendFailure(t *testing.T) {
	var sequences []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"test-only","expires_in":7200}`))
		case "/v2/groups/group/messages":
			reply := readQQTestReply(t, r)
			sequences = append(sequences, reply.Sequence)
			if reply.Sequence == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`{"id":"sent"}`))
		default:
			t.Errorf("help unexpectedly called %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	s := New(nil, nil, false, nil)
	s.qqURL, s.tokenURL = server.URL, server.URL+"/token"
	c := DefaultConfig()
	for i := 0; i < 4; i++ {
		c.CustomCommands = append(c.CustomCommands, CustomCommand{Command: fmt.Sprintf("/custom%d", i), Enabled: true})
	}
	j := Job{GroupID: "group", MessageID: "help", CreatedAt: time.Now()}
	require.Error(t, s.sendHelp(context.Background(), c, j, ""))
	require.Equal(t, []int{1}, sequences, "a failed menu send must be returned to the caller")
}
