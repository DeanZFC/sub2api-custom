package qqbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

type channelStatusProviderFunc func(context.Context) (*ChannelStatusReport, error)

func (f channelStatusProviderFunc) ChannelStatus(ctx context.Context) (*ChannelStatusReport, error) {
	return f(ctx)
}

func channelStatusFixture() *ChannelStatusReport {
	cache, ttft := .7625, int64(1280)
	metric := ChannelStatusMetric{HasRequests: true, ErrorRate: .0032, CacheRate: &cache, TTFTP50Ms: &ttft, RPM: 12.34, TokensPerSecond: 56.78}
	return &ChannelStatusReport{
		UpdatedAt: time.Date(2026, 10, 16, 6, 30, 0, 0, time.UTC),
		Overall:   metric,
		Groups:    []ChannelStatusGroup{{Name: "OpenAI 标准组", Metrics: metric}, {Name: "Gemini 分组"}},
	}
}

func TestFormatChannelStatus(t *testing.T) {
	report := channelStatusFixture()
	chunks := formatChannelStatus(report)
	require.Equal(t, []string{`【渠道状态】
统计范围：最近 90 分钟
数据更新：2026-10-16 14:30（北京时间）

【整体汇总】
成功率：99.68%
错误率：0.32%
缓存率：76.25%
首 Token：1.28 秒（P50）

【各分组】

1. OpenAI 标准组
成功率：99.68% ｜ 错误率：0.32%
缓存率：76.25%
首 Token：1.28 秒（P50）

2. Gemini 分组
暂无请求数据`}, chunks)
	require.NotContains(t, chunks[0], "每分钟请求数")
	require.NotContains(t, chunks[0], "每秒 Token")
	report.ShowThroughput = true
	text := strings.Join(formatChannelStatus(report), "")
	require.Equal(t, 2, strings.Count(text, "每分钟请求数：12.34\n每秒 Token：56.78"))
}

func TestChannelStatusMissingSamplesAreNotZeroOrPerfectSuccess(t *testing.T) {
	report := &ChannelStatusReport{PartialCoverage: true}
	text := formatChannelStatus(report)[0]
	require.Contains(t, text, "数据更新：暂无数据")
	require.Contains(t, text, "暂无请求数据")
	require.Contains(t, text, "暂无可展示的分组")
	require.Contains(t, text, "当前仅覆盖部分统计时段")
	require.NotContains(t, text, "100.00%")
	report.Backfilling = true
	require.Contains(t, formatChannelStatus(report)[0], "正在补齐历史监控数据")
	report.Overall = ChannelStatusMetric{HasRequests: true, ErrorRate: 1}
	text = formatChannelStatus(report)[0]
	require.Contains(t, text, "成功率：0.00%\n错误率：100.00%")
	require.Contains(t, text, "缓存率：暂无数据\n首 Token：暂无数据")
	zero := 0.0
	report.Overall.CacheRate = &zero
	require.Contains(t, formatChannelStatus(report)[0], "缓存率：0.00%")
}

func TestChannelStatusSplitsWithoutLosingOrBreakingGroups(t *testing.T) {
	report := channelStatusFixture()
	report.ShowThroughput = true
	report.Groups = nil
	for i := 1; i <= 100; i++ {
		report.Groups = append(report.Groups, ChannelStatusGroup{Name: fmt.Sprintf("分组%03d%s", i, strings.Repeat("中", 90)), Metrics: report.Overall})
	}
	chunks := formatChannelStatus(report)
	require.Greater(t, len(chunks), 5)
	for _, chunk := range chunks {
		require.LessOrEqual(t, utf8.RuneCountInString(chunk), 1400)
		require.True(t, utf8.ValidString(chunk))
		require.NotContains(t, chunk, "截断")
	}
	for i, group := range report.Groups {
		block := fmt.Sprintf("%d. %s\n%s", i+1, group.Name, formatChannelStatusMetric(group.Metrics, true, true))
		count := 0
		for _, chunk := range chunks {
			count += strings.Count(chunk, block)
		}
		require.Equal(t, 1, count, group.Name)
	}
}

type channelStatusReply struct {
	Content  string `json:"content"`
	Sequence int    `json:"msg_seq"`
}

func channelStatusSender(t *testing.T, failAt int) (*Service, *[]channelStatusReply) {
	t.Helper()
	var replies []channelStatusReply
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"test-only","expires_in":7200}`))
		case "/v2/groups/group/messages":
			var body channelStatusReply
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			replies = append(replies, body)
			if failAt > 0 && body.Sequence == failAt {
				w.WriteHeader(500)
				return
			}
			_, _ = w.Write([]byte(`{"id":"sent"}`))
		default:
			t.Errorf("channel status unexpectedly called %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	// A nil database proves this command does not require binding, reserve
	// model quota, or write model history. Only monitor and QQ calls are allowed.
	s := New(nil, nil, false, nil)
	s.qqURL, s.tokenURL = server.URL, server.URL+"/token"
	return s, &replies
}

func TestChannelStatusCommandMentionModesAndHelp(t *testing.T) {
	s, replies := channelStatusSender(t, 0)
	calls := 0
	s.SetChannelStatusProvider(channelStatusProviderFunc(func(ctx context.Context) (*ChannelStatusReport, error) {
		calls++
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 15*time.Second)
		return channelStatusFixture(), nil
	}))
	for _, eventType := range []string{groupMessageCreate, groupAtMessageCreate} {
		e := Event{Content: "/渠道状态"}
		require.True(t, shouldQueueGroupMessage(eventType, e))
		j := Job{EventType: eventType, GroupID: "group", MemberID: "member", MessageID: eventType, CreatedAt: time.Now()}
		require.NoError(t, s.message(context.Background(), DefaultConfig(), &j, e))
	}
	require.Equal(t, 2, calls)
	require.Len(t, *replies, 2)
	require.True(t, strings.HasPrefix((*replies)[0].Content, `<qqbot-at-user id="member" />`))
	require.NotContains(t, (*replies)[1].Content, "qqbot-at-user")
	require.False(t, unmentionedGroupCommand(Event{Content: "<@other> /渠道状态"}))
	j := Job{GroupID: "group", MessageID: "help", CreatedAt: time.Now()}
	require.NoError(t, s.message(context.Background(), DefaultConfig(), &j, Event{Content: "/帮助"}))
	require.Contains(t, (*replies)[2].Content, "指令：/渠道状态\n说明：查询整体及各分组的成功率、缓存率和首 Token")
}

func TestChannelStatusCommandFailures(t *testing.T) {
	for _, tt := range []struct {
		name, arg, want string
		provider        ChannelStatusProvider
	}{
		{name: "not wired", want: "渠道状态暂时不可用"},
		{name: "invalid page", arg: "0", want: "用法：/渠道状态"},
		{name: "invalid argument", arg: "24h", want: "用法：/渠道状态"},
		{name: "overflow", arg: strings.Repeat("9", 30), want: "用法：/渠道状态"},
		{name: "disabled", provider: channelStatusProviderFunc(func(context.Context) (*ChannelStatusReport, error) { return nil, ErrChannelStatusDisabled }), want: "渠道监控未开启"},
		{name: "v1", provider: channelStatusProviderFunc(func(context.Context) (*ChannelStatusReport, error) { return nil, ErrChannelStatusUnsupported }), want: "当前未启用被动监控"},
		{name: "query failed", provider: channelStatusProviderFunc(func(context.Context) (*ChannelStatusReport, error) {
			return nil, errors.New("internal database detail")
		}), want: "渠道状态查询失败"},
		{name: "nil report", provider: channelStatusProviderFunc(func(context.Context) (*ChannelStatusReport, error) { return nil, nil }), want: "渠道状态查询失败"},
		{name: "page out of range", arg: "99", provider: channelStatusProviderFunc(func(context.Context) (*ChannelStatusReport, error) { return channelStatusFixture(), nil }), want: "渠道状态共 1 页"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, replies := channelStatusSender(t, 0)
			s.SetChannelStatusProvider(tt.provider)
			j := Job{GroupID: "group", MessageID: "query", CreatedAt: time.Now()}
			require.NoError(t, s.sendChannelStatus(context.Background(), DefaultConfig(), j, tt.arg))
			require.Len(t, *replies, 1)
			require.Contains(t, (*replies)[0].Content, tt.want)
			require.NotContains(t, (*replies)[0].Content, "internal database detail")
		})
	}
}

func TestChannelStatusCommandPaginationAndDeliveryFailure(t *testing.T) {
	report := channelStatusFixture()
	report.Groups = nil
	for i := 0; i < 100; i++ {
		report.Groups = append(report.Groups, ChannelStatusGroup{Name: fmt.Sprintf("分组%03d", i), Metrics: report.Overall})
	}
	chunks := formatChannelStatus(report)
	require.Greater(t, len(chunks), 5)
	provider := channelStatusProviderFunc(func(context.Context) (*ChannelStatusReport, error) { return report, nil })
	s, replies := channelStatusSender(t, 0)
	s.SetChannelStatusProvider(provider)
	j := Job{EventType: groupMessageCreate, GroupID: "group", MemberID: strings.Repeat("m", 128), MessageID: "query", CreatedAt: time.Now()}
	pages := (len(chunks) + 4) / 5
	for page := 1; page <= pages; page++ {
		before := len(*replies)
		require.NoError(t, s.sendChannelStatus(context.Background(), DefaultConfig(), j, strconv.Itoa(page)))
		batch := (*replies)[before:]
		require.Len(t, batch, min(5, len(chunks)-(page-1)*5))
		for i, reply := range batch {
			require.Equal(t, i+1, reply.Sequence)
			require.LessOrEqual(t, utf8.RuneCountInString(reply.Content), 1800)
			require.Contains(t, reply.Content, chunks[(page-1)*5+i])
		}
		if page < pages {
			require.Contains(t, batch[len(batch)-1].Content, fmt.Sprintf("继续查看：/渠道状态 %d", page+1))
		}
	}
	require.Len(t, *replies, len(chunks))
	s, replies = channelStatusSender(t, 2)
	s.SetChannelStatusProvider(provider)
	require.Error(t, s.sendChannelStatus(context.Background(), DefaultConfig(), j, ""))
	require.Len(t, *replies, 2)
}
