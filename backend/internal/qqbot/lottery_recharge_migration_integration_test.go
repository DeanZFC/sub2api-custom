//go:build integration

package qqbot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestLotteryRechargeMigrationPreservesChancesAndProgress(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	bot, err := s.Config(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO payment_orders(user_id,amount) VALUES($1,125)`, uid)
	require.NoError(t, err)

	const legacyLottery = `{
		"enabled":true,"title":"旧抽奖","recharge_step":50,"chances_per_step":1,
		"prizes":[{"name":"普通奖","amount":1,"probability_bps":10000}],
		"guarantee_enabled":true,"guarantee_every":2,
		"guarantee_prizes":[{"name":"保底奖","amount":2,"probability_bps":10000}]
	}`
	const legacyCheckin = `{
		"enabled":true,"min_amount":0.1,"max_amount":0.2,
		"growth":{"tasks":[],"periodic_tasks":[]}
	}`
	_, err = db.Exec(`INSERT INTO settings(key,value) VALUES($1,$2),($3,$4)`,
		lotteryConfigKey, legacyLottery, checkinConfigKey, legacyCheckin)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO qq_bot_lottery_draws(
		request_key,app_id,member_id,group_id,user_id,prize_name,amount,config_snapshot,
		recharge_total,earned,remaining,result_text)
		VALUES('app:old-draw','app','member','group',$1,'普通奖',1,$2,125,2,1,'原抽奖结果')`,
		uid, legacyLottery)
	require.NoError(t, err)

	migration, err := migrations.FS.ReadFile("357_qq_bot_lottery_recharge_growth.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(migration))
		require.NoError(t, err)
	}
	checkin, err := s.CheckinConfig(ctx)
	require.NoError(t, err)
	require.True(t, checkin.Enabled)
	require.Equal(t, 0.1, checkin.MinAmount)
	require.Equal(t, 0.2, checkin.MaxAmount)
	require.Empty(t, checkin.Growth.Tasks)
	require.Empty(t, checkin.Growth.PeriodicTasks)
	rule := &LotteryRechargeRule{RechargeStep: 50, ChancesPerStep: 1}
	require.Equal(t, rule, checkin.Growth.LotteryRecharge)

	lottery, err := s.LotteryConfig(ctx)
	require.NoError(t, err)
	require.True(t, lottery.Enabled)
	require.Equal(t, "旧抽奖", lottery.Title)
	require.Equal(t, "普通奖", lottery.Prizes[0].Name)
	require.Equal(t, "保底奖", lottery.GuaranteePrizes[0].Name)
	rulesJSON, err := json.Marshal(lottery)
	require.NoError(t, err)
	require.NotContains(t, string(rulesJSON), "recharge_step")
	require.NotContains(t, string(rulesJSON), "chances_per_step")

	state, err := s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, 125.0, state.RechargeTotal)
	require.Equal(t, int64(1), state.Used)
	require.Equal(t, int64(2), state.Earned)
	require.Equal(t, int64(1), state.Remaining)
	require.Equal(t, 25.0, state.NextRecharge)
	require.Equal(t, int64(1), state.DrawsUntilGuarantee)
	preview, err := s.PreviewActivities(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, state, preview.Lottery)
	require.Len(t, preview.Tasks, 1)
	require.Equal(t, "lottery:recharge", preview.Tasks[0].ID)
	require.Contains(t, preview.Tasks[0].Progress, "USD 25.00")

	job := Job{ID: "old-draw", GroupID: "group", MemberID: "member"}
	reply, err := s.drawLottery(ctx, bot, job)
	require.NoError(t, err)
	require.Equal(t, "原抽奖结果", reply)
	var snapshot string
	require.NoError(t, db.QueryRow(`SELECT config_snapshot::text FROM qq_bot_lottery_draws WHERE request_key='app:old-draw'`).Scan(&snapshot))
	require.JSONEq(t, legacyLottery, snapshot)

	_, grants, err := s.processGrowthTasks(ctx, bot, job, time.Now(), false)
	require.NoError(t, err)
	require.Zero(t, grants)
	stateAfterGrowth, err := s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, state, stateAfterGrowth)

	lottery.Title = "更新奖池"
	require.NoError(t, s.SaveLotteryConfig(ctx, lottery))
	checkin, err = s.CheckinConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, rule, checkin.Growth.LotteryRecharge)

	// The legacy draw counts towards the guarantee after the upgrade.
	job.ID = "first-new-draw"
	_, err = s.drawLottery(ctx, bot, job)
	require.NoError(t, err)
	draws, err := s.ListLotteryDraws(ctx, "", 1, 10)
	require.NoError(t, err)
	require.Len(t, draws.Items, 2)
	require.True(t, draws.Items[0].IsGuaranteed)
	require.Equal(t, "保底奖", draws.Items[0].PrizeName)
	require.Zero(t, draws.Items[0].Remaining)
	var storedRule LotteryRechargeRule
	require.NoError(t, db.QueryRow(`SELECT (config_snapshot->>'recharge_step')::numeric,
		(config_snapshot->>'chances_per_step')::bigint FROM qq_bot_lottery_draws
		WHERE request_key='app:first-new-draw'`).Scan(&storedRule.RechargeStep, &storedRule.ChancesPerStep))
	require.Equal(t, *rule, storedRule)

	// Fresh credits and refunds continue to affect availability immediately.
	_, err = db.Exec(`INSERT INTO payment_orders(user_id,amount) VALUES($1,25)`, uid)
	require.NoError(t, err)
	state, err = s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Remaining)
	_, err = db.Exec(`UPDATE payment_orders SET status='REFUNDED',refund_amount=25 WHERE user_id=$1 AND amount=25`, uid)
	require.NoError(t, err)
	state, err = s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Zero(t, state.Remaining)
	require.Equal(t, 25.0, state.NextRecharge)
}

func TestLotteryRechargeMigrationWithoutSavedCheckinAndExplicitDisable(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	bot, err := s.Config(ctx)
	require.NoError(t, err)
	before, err := s.CheckinConfig(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO settings(key,value) VALUES($1,'{"enabled":false,"title":"等待启用"}')`, lotteryConfigKey)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("357_qq_bot_lottery_recharge_growth.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	checkin, err := s.CheckinConfig(ctx)
	require.NoError(t, err)
	before.Growth.LotteryRecharge = &LotteryRechargeRule{RechargeStep: 10, ChancesPerStep: 1}
	require.Equal(t, before, checkin)

	lottery, err := s.LotteryConfig(ctx)
	require.NoError(t, err)
	require.False(t, lottery.Enabled)
	lottery.Enabled = true
	require.NoError(t, s.SaveLotteryConfig(ctx, lottery))
	_, err = db.Exec(`INSERT INTO payment_orders(user_id,amount) VALUES($1,20)`, uid)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES('legacy:bonus',$1,1)`, uid)
	require.NoError(t, err)
	state, err := s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, int64(3), state.Remaining)

	saveLotteryRechargeRule(t, s, nil)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	checkin, err = s.CheckinConfig(ctx)
	require.NoError(t, err)
	require.Nil(t, checkin.Growth.LotteryRecharge)
	state, err = s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.BonusChances)
	require.Equal(t, int64(1), state.Remaining)
	require.Zero(t, state.NextRecharge)
	job := Job{ID: "bonus-draw", GroupID: "group", MemberID: "member"}
	reply, err := s.lotteryInfo(ctx, bot, job, false)
	require.NoError(t, err)
	require.NotContains(t, reply, "再充值")
	_, err = s.drawLottery(ctx, bot, job)
	require.NoError(t, err)
	state, err = s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, int64(1), state.Used)
	require.Zero(t, state.Remaining)
}
