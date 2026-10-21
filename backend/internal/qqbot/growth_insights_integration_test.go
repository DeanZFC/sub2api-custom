//go:build integration

package qqbot

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func insightsUsageTable(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`CREATE TABLE usage_logs(user_id BIGINT,model TEXT,input_tokens BIGINT DEFAULT 0,output_tokens BIGINT DEFAULT 0,cache_creation_tokens BIGINT DEFAULT 0,cache_read_tokens BIGINT DEFAULT 0,actual_cost NUMERIC(20,10) DEFAULT 0,billing_type SMALLINT DEFAULT 0,created_at TIMESTAMPTZ NOT NULL)`)
	require.NoError(t, err)
}

func seedCheckin(t *testing.T, db *sql.DB, uid int64, at time.Time) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO qq_bot_rewards(reward_key,app_id,group_id,member_id,user_id,kind,amount,created_at) VALUES($1,'app','group','member',$2,'checkin',0,$3)`, fmt.Sprintf("checkin:%d:%s", uid, day(at)), uid, at)
	require.NoError(t, err)
}

func TestGrowthConcurrentClaimsRebindingAndProgress(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	insightsUsageTable(t, db)
	c, err := s.Config(ctx)
	require.NoError(t, err)
	require.NoError(t, s.SaveCheckinConfig(ctx, CheckinConfig{Enabled: true, StreakChances: 2, Growth: GrowthConfig{BindingChances: 2, FirstUsageChances: 3, Checkin30Chances: 4}}))
	j := Job{GroupID: "group", MemberID: "member"}
	text, err := s.growthTasks(ctx, c, j, "")
	require.NoError(t, err)
	require.Contains(t, text, "可领取")
	require.Contains(t, text, "0/30 天")
	text, err = s.growthTasks(ctx, c, j, "2")
	require.NoError(t, err)
	require.Contains(t, text, "尚未完成")
	var won, failed atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bot := New(db, testEncryptor{}, true, nil)
			text, err := bot.growthTasks(ctx, c, j, "1")
			if err != nil {
				failed.Add(1)
			}
			if strings.Contains(text, "成长奖励已领取") {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, failed.Load())
	require.Equal(t, int32(1), won.Load())
	_, err = db.Exec(`UPDATE qq_bot_bindings SET member_id='rebound' WHERE user_id=$1`, uid)
	require.NoError(t, err)
	j.MemberID = "rebound"
	j.GroupID = "other-group"
	text, err = s.growthTasks(ctx, c, j, "1")
	require.NoError(t, err)
	require.Contains(t, text, "已领取")
	_, err = db.Exec(`INSERT INTO usage_logs(user_id,model,created_at) VALUES($1,'test',NOW())`, uid)
	require.NoError(t, err)
	text, err = s.growthTasks(ctx, c, j, "2")
	require.NoError(t, err)
	require.Contains(t, text, "3 次")
	for i := 1; i <= 30; i++ {
		seedCheckin(t, db, uid, time.Now().AddDate(0, 0, -i))
	}
	text, err = s.growthTasks(ctx, c, j, "3")
	require.NoError(t, err)
	require.Contains(t, text, "4 次")
	var total int
	require.NoError(t, db.QueryRow(`SELECT SUM(chances) FROM qq_bot_lottery_chance_grants WHERE user_id=$1`, uid).Scan(&total))
	require.Equal(t, 9, total)
	_, err = db.Exec(`UPDATE users SET status='disabled' WHERE id=$1`, uid)
	require.NoError(t, err)
	text, err = s.growthTasks(ctx, c, j, "")
	require.NoError(t, err)
	require.Equal(t, bindFirst, text)
}

func TestGrowthStreakAtomicBonusAndGap(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	c, err := s.Config(ctx)
	require.NoError(t, err)
	require.NoError(t, s.SaveCheckinConfig(ctx, CheckinConfig{Enabled: true, MinAmount: .5, MaxAmount: .5, StreakChances: 2}))
	for i := 1; i < 7; i++ {
		seedCheckin(t, db, uid, time.Now().AddDate(0, 0, -i))
	}
	_, err = db.Exec(`ALTER TABLE qq_bot_lottery_chance_grants ADD CONSTRAINT reject_bonus CHECK(chances<>2)`)
	require.NoError(t, err)
	j := Job{GroupID: "group", MemberID: "member"}
	_, err = s.reward(ctx, c, j, "checkin")
	require.Error(t, err)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Zero(t, balance)
	_, err = db.Exec(`ALTER TABLE qq_bot_lottery_chance_grants DROP CONSTRAINT reject_bonus`)
	require.NoError(t, err)
	text, err := s.reward(ctx, c, j, "checkin")
	require.NoError(t, err)
	require.Contains(t, text, "连续签到：7 天")
	require.Contains(t, text, "连签奖励：2 次")
	text, err = s.reward(ctx, c, j, "checkin")
	require.NoError(t, err)
	require.Contains(t, text, "今日已签到")
	var grants int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_lottery_chance_grants`).Scan(&grants))
	require.Equal(t, 1, grants)
	_, streak, err := checkinProgress(ctx, db, uid, time.Now().AddDate(0, 0, 2))
	require.NoError(t, err)
	require.Zero(t, streak)
	_, err = db.Exec(`DELETE FROM qq_bot_rewards WHERE user_id=$1 AND (created_at AT TIME ZONE 'Asia/Shanghai')::date=$2::date`, uid, day(time.Now().AddDate(0, 0, -2)))
	require.NoError(t, err)
	total, streak, err := checkinProgress(ctx, db, uid, time.Now())
	require.NoError(t, err)
	require.Equal(t, 6, total)
	require.Equal(t, 2, streak)
}

func TestTodayUsageBeijingBoundaryAndAccountIsolation(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	other := user(t, db)
	bind(t, db, uid, "member")
	insightsUsageTable(t, db)
	c, err := s.Config(ctx)
	require.NoError(t, err)
	at := time.Date(2026, 10, 21, 0, 5, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
	_, err = db.Exec(`INSERT INTO usage_logs(user_id,model,input_tokens,actual_cost,billing_type,created_at) VALUES
 ($1,'wallet-model',10,1.25,0,$3::timestamptz-INTERVAL '4 minutes'),
 ($1,'plan-model',20,2.5,1,$3::timestamptz-INTERVAL '3 minutes'),
 ($1,'yesterday',99,999,0,$3::timestamptz-INTERVAL '6 minutes'),
 ($1,'future',99,999,0,$3::timestamptz+INTERVAL '1 minute'),
 ($2,'someone-else',99,999,0,$3)`, uid, other, at)
	require.NoError(t, err)
	text, err := s.todayUsage(ctx, c, Job{MemberID: "member"}, at)
	require.NoError(t, err)
	require.Contains(t, text, "已记录请求：2 次")
	require.Contains(t, text, "实际计费：USD 3.7500")
	require.Contains(t, text, "余额扣费：USD 1.2500")
	require.Contains(t, text, "套餐消耗：USD 2.5000")
	for _, hidden := range []string{"yesterday", "future", "someone-else", "999"} {
		require.NotContains(t, text, hidden)
	}
	text, err = s.todayUsage(ctx, c, Job{MemberID: "unbound"}, at)
	require.NoError(t, err)
	require.Equal(t, bindFirst, text)
}

type modelPriceFunc func(context.Context, string, int64, time.Time) ([]string, error)

func (f modelPriceFunc) ModelPrice(ctx context.Context, model string, uid int64, at time.Time) ([]string, error) {
	return f(ctx, model, uid, at)
}

func TestModelPriceCommandPaginationAndBoundIdentity(t *testing.T) {
	db := testDB(t)
	s, replies := helpTestSender(t)
	s.db = db
	uid := user(t, db)
	bind(t, db, uid, "member")
	c := DefaultConfig()
	c.AppID = "app"
	var received int64
	s.SetModelPriceProvider(modelPriceFunc(func(_ context.Context, model string, id int64, _ time.Time) ([]string, error) {
		require.Equal(t, "gpt-5", model)
		received = id
		return []string{"group1", "group2", "group3", "group4", "group5", "group6"}, nil
	}))
	j := Job{GroupID: "group", MemberID: "member", MessageID: "message", CreatedAt: time.Now()}
	require.NoError(t, s.sendModelPrice(context.Background(), c, j, "gpt-5"))
	require.Equal(t, uid, received)
	require.Len(t, *replies, 5)
	require.NoError(t, s.sendModelPrice(context.Background(), c, j, "gpt-5 2"))
	require.Len(t, *replies, 6)
	require.Contains(t, (*replies)[5].text(), "group6")
	j.MemberID = "unbound"
	require.NoError(t, s.sendModelPrice(context.Background(), c, j, "gpt-5 2"))
	require.Zero(t, received)
	require.NoError(t, s.sendModelPrice(context.Background(), c, j, "gpt-5 2junk"))
	require.Contains(t, (*replies)[len(*replies)-1].text(), "页码")
}
