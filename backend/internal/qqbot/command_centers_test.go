package qqbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetiredCommandsNeverExecuteOrCallModels(t *testing.T) {
	s, replies := helpTestSender(t) // No database or model endpoint.
	c := DefaultConfig()
	for _, name := range []string{"help", "今日用量", "成长任务", "领取任务", "邀请奖励", "邀请活动", "我的邀请", "我的余额", "我的奖励", "抽奖", "抽奖次数", "我的抽奖", "抽奖活动", "画图", "奖励兑换"} {
		t.Run(name, func(t *testing.T) {
			require.False(t, isBuiltinCommand(name))
			c.CustomCommands = []CustomCommand{{Command: "/" + name, Reply: "must not run", Enabled: true}}
			for _, eventType := range []string{groupMessageCreate, groupAtMessageCreate} {
				j := Job{EventType: eventType, GroupID: "group", MessageID: name, CreatedAt: time.Now()}
				before := len(*replies)
				require.NoError(t, s.message(context.Background(), c, &j, Event{Content: "/" + name}))
				if eventType == groupMessageCreate {
					require.Len(t, *replies, before)
				} else {
					require.Len(t, *replies, before+1)
					require.Contains(t, (*replies)[before].text(), "未知指令")
				}
			}
		})
	}
}

func TestWelfareInvalidActionsNeverSpendOrAccessStorage(t *testing.T) {
	s, replies := helpTestSender(t)
	for _, arg := range []string{"抽奖 2", "领取", "领取 0", "领取 -1", "领取 01", "领取 1 2", "领取 x", "领取 R0", "领取 R01", "领取 R-1", "领取 R1 2", "领取 R9223372036854775808", "0", "-1", "未知"} {
		j := Job{GroupID: "group", MessageID: arg, CreatedAt: time.Now()}
		before := len(*replies)
		require.NoError(t, s.handleWelfare(context.Background(), DefaultConfig(), &j, arg))
		require.Len(t, *replies, before+1)
		require.Contains(t, (*replies)[before].text(), "用法")
	}
}

func TestWelfareRedemptionSanitizesNewAndRetiredInput(t *testing.T) {
	code, err := generateRewardCode()
	require.NoError(t, err)
	for _, prefix := range []string{"/福利 兑换 ", "<@bot> /福利\t兑换\n", "/奖励兑换 "} {
		e := Event{Content: prefix + code, MsgElements: []MessageElement{{Content: code}}, Attachments: []Attachment{{URL: code}}}
		require.True(t, containsRewardCode(e.Content))
		clean, hash := redactRewardCommand(e)
		raw, err := json.Marshal(clean)
		require.NoError(t, err)
		require.NotContains(t, string(raw), code)
		if strings.HasPrefix(prefix, "/奖励兑换") {
			require.Empty(t, hash)
			require.False(t, isBuiltinOrCustomCommand(clean, nil))
		} else {
			require.Equal(t, rewardCodeHash(code), hash)
			require.Contains(t, clean.Content, "/福利 兑换")
			require.True(t, isBuiltinOrCustomCommand(clean, nil))
		}
	}
	for _, content := range []string{"/福利", "/福利 抽奖", "/福利 领取 1", "/福利 兑换说明"} {
		require.False(t, containsRewardCode(content))
	}
}

func TestWelfarePaginationPreservesAllTasksAndKeyboardCapacity(t *testing.T) {
	var paragraphs []string
	var buttons []keyboardButton
	for i := 1; i <= 30; i++ {
		cmd := fmt.Sprintf("/福利 领取 %d", i)
		paragraphs = append(paragraphs, fmt.Sprintf("**%d. 成长任务**\n奖励：抽奖次数 × 1 · 可领取 · %s", i, cmd))
		buttons = append(buttons, helpCommandButton(fmt.Sprintf("领取%d", i), fmt.Sprint(i), cmd))
	}
	body := strings.Join(paragraphs, "\n\n")
	pages := paginateWelfare(body, buttons)
	var restored []string
	var seen []keyboardButton
	for _, page := range pages {
		require.LessOrEqual(t, len([]rune(page.text)), 1300)
		require.LessOrEqual(t, len(page.buttons), 6)
		require.NotContains(t, page.text, "已截断")
		for _, button := range page.buttons {
			require.Contains(t, page.text+"\n", button.Action.Data+"\n")
		}
		restored = append(restored, page.text)
		seen = append(seen, page.buttons...)
	}
	require.Equal(t, body, strings.Join(restored, "\n\n"))
	require.Equal(t, buttons, seen)
	long := strings.Repeat("规则\n", 1800)
	pages = paginateWelfare(long, nil)
	var rebuilt strings.Builder
	for _, page := range pages {
		require.LessOrEqual(t, len([]rune(page.text)), 1300)
		_, _ = rebuilt.WriteString(page.text)
	}
	require.Equal(t, long, rebuilt.String())
}
