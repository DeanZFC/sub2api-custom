//go:build integration

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

func TestAccountOverviewCombinesOwnedDataAndPreservesAmounts(t *testing.T) {
	s, db, c, uid, _ := lotteryFixture(t, 20)
	insightsUsageTable(t, db)
	other := user(t, db)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	_, err := db.Exec(`UPDATE users SET balance=12.5 WHERE id=$1`, uid)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(user_id,model,input_tokens,output_tokens,actual_cost,billing_type,created_at) VALUES ($1,'own-model',100,20,1.25,0,$3::timestamptz-INTERVAL '1 minute'),($1,'own-plan',200,30,2.5,1,$3::timestamptz-INTERVAL '2 minutes'),($2,'private-other-model',999,999,999,0,$3)`, uid, other, now)
	require.NoError(t, err)
	text, err := s.accountOverview(ctx, c, Job{MemberID: "member", GroupID: "group"}, now)
	require.NoError(t, err)
	for _, expected := range []string{"# 👤 我的账户", "账户额度：USD 12.50", "## 今日用量", "已记录请求：2 次", "实际计费：USD 3.7500", "余额扣费：USD 1.2500", "套餐消耗：USD 2.5000", "## 累计奖励", "剩余抽奖次数：2 次"} {
		require.Contains(t, text, expected)
	}
	for _, hidden := range []string{"private-other-model", "999", "@example", "|---"} {
		require.NotContains(t, text, hidden)
	}
	text, err = s.accountOverview(ctx, c, Job{MemberID: "unbound"}, now)
	require.NoError(t, err)
	require.Equal(t, bindFirst, text)
}

func TestWelfareCommandsQueryClaimDrawAndRejectRetiredActions(t *testing.T) {
	s, db, c, uid, _ := lotteryFixture(t, 20)
	insightsUsageTable(t, db)
	ctx := context.Background()
	require.NoError(t, s.SaveCheckinConfig(ctx, CheckinConfig{Enabled: true, Growth: GrowthConfig{BindingChances: 2}}))
	sender, replies := helpTestSender(t)
	s.qqURL, s.tokenURL = sender.qqURL, sender.tokenURL
	enqueue := func(id, content string) {
		e := Event{ID: id, GroupID: "group", Content: content}
		e.Author.MemberID = "member"
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		require.NoError(t, s.Enqueue(ctx, c, Payload{ID: "outer-" + id, Type: groupMessageCreate, Data: raw}))
		s.runOne(ctx)
	}
	enqueue("welfare", "/福利")
	require.Len(t, *replies, 1)
	require.Contains(t, (*replies)[0].text(), "# 🎁 福利中心")
	var claimCommand string
	for page := 1; page <= 4 && claimCommand == ""; page++ {
		if page > 1 {
			enqueue(fmt.Sprintf("welfare-%d", page), fmt.Sprintf("/福利 %d", page))
		}
		reply := (*replies)[len(*replies)-1]
		if reply.Keyboard == nil {
			break
		}
		require.LessOrEqual(t, len(reply.Keyboard.Content.Rows), 5)
		for _, row := range reply.Keyboard.Content.Rows {
			require.LessOrEqual(t, len(row.Buttons), 2)
			for _, button := range row.Buttons {
				if strings.HasPrefix(button.Action.Data, "/福利 领取 ") {
					claimCommand = button.Action.Data
				}
			}
		}
	}
	require.Equal(t, "/福利 领取 1", claimCommand)
	state, err := s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, 2, state.Remaining, "browsing welfare must not spend or claim rewards")
	enqueue("claim", claimCommand)
	require.Contains(t, (*replies)[len(*replies)-1].text(), "成长奖励已领取")
	enqueue("claim-retry", claimCommand)
	require.Contains(t, (*replies)[len(*replies)-1].text(), "已领取")
	enqueue("draw", "/福利 抽奖")
	require.Contains(t, (*replies)[len(*replies)-1].text(), "抽奖结果")
	enqueue("draw", "/福利 抽奖")
	state, err = s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.EqualValues(t, 1, state.Used)
	require.EqualValues(t, 3, state.Remaining)
	before := len(*replies)
	for i, content := range []string{"/抽奖", "/我的余额", "/领取任务 1", "/画图 a cat"} {
		enqueue(fmt.Sprintf("retired-%d", i), content)
	}
	require.Len(t, *replies, before)
	enqueue("account", "/我的")
	require.Contains(t, (*replies)[len(*replies)-1].text(), "我的账户")
}
