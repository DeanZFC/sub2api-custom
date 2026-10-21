//go:build integration

package qqbot

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rankingClaimNumber(t *testing.T, db *sql.DB, uid int64, date, kind string) string {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRow(`SELECT id FROM qq_bot_ranking_claims WHERE app_id='app' AND user_id=$1 AND ranking_day=$2 AND ranking_kind=$3`, uid, date, kind).Scan(&id))
	return fmt.Sprintf("R%d", id)
}

func rankingClaimFixture(t *testing.T, reward GrowthReward) (*Service, *sql.DB, Config, int64, string) {
	t.Helper()
	db := testDB(t)
	s := savedBot(t, db)
	insightsUsageTable(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	c, err := s.Config(context.Background())
	require.NoError(t, err)
	cfg := defaultRankingConfig()
	cfg.RewardsEnabled = true
	cfg.TokenRewards = []RankingPrize{{1, reward}}
	cfg.AmountRewards = nil
	start := rankingTime(t, "2026-10-20T00:00:00+08:00")
	_, err = s.saveRankingConfigAt(context.Background(), cfg, start.Add(-time.Hour))
	require.NoError(t, err)
	rankingUsage(t, db, uid, 100, "1", start)
	require.NoError(t, s.settleDueRankings(context.Background(), start.AddDate(0, 0, 1).Add(10*time.Minute)))
	return s, db, c, uid, rankingClaimNumber(t, db, uid, "2026-10-20", "tokens")
}

func TestRankingClaimOwnershipFrozenRewardAndConcurrentRebinding(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid, number := rankingClaimFixture(t, GrowthReward{Type: "balance", Amount: 1.00000001})
	other := user(t, db)
	bind(t, db, other, "other-member")
	for _, tc := range []struct {
		job  Job
		want string
	}{
		{Job{MemberID: "unbound", GroupID: "group"}, bindFirst},
		{Job{MemberID: "other-member", GroupID: "group"}, "不属于当前账号"},
		{Job{MemberID: "member", GroupID: "forbidden"}, "当前群未启用"},
	} {
		text, err := s.growthTasks(ctx, c, tc.job, number)
		require.NoError(t, err)
		require.Contains(t, text, tc.want)
	}
	wrongApp := c
	wrongApp.AppID = "other-app"
	text, err := s.growthTasks(ctx, wrongApp, Job{MemberID: "member", GroupID: "group"}, number)
	require.NoError(t, err)
	require.Contains(t, text, "当前群未启用")
	_, err = db.Exec(`UPDATE users SET status='disabled' WHERE id=$1`, uid)
	require.NoError(t, err)
	text, err = s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, number)
	require.NoError(t, err)
	require.Equal(t, bindFirst, text)
	_, err = db.Exec(`UPDATE users SET status='active' WHERE id=$1`, uid)
	require.NoError(t, err)
	var actions []keyboardButton
	text, err = s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, "", &actions)
	require.NoError(t, err)
	require.Contains(t, text, "周期性成长任务")
	require.Contains(t, text, "余额 USD 1.00000001 · 可领取 · /福利 领取 "+number)
	require.Len(t, actions, 1)
	require.Equal(t, "/福利 领取 "+number, actions[0].Action.Data)
	text, err = s.growthTasks(ctx, c, Job{MemberID: "other-member", GroupID: "group"}, "")
	require.NoError(t, err)
	require.NotContains(t, text, "/福利 领取 "+number)
	// Later configuration and late usage must not change an earned snapshot.
	_, err = s.saveRankingConfigAt(ctx, defaultRankingConfig(), rankingTime(t, "2026-10-22T01:00:00+08:00"))
	require.NoError(t, err)
	rankingUsage(t, db, other, 99999, "999", rankingTime(t, "2026-10-20T01:00:00+08:00"))
	var wg sync.WaitGroup
	replies := make(chan string, 8)
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			group := "group"
			if i%2 == 1 {
				group = "other-group"
			}
			reply, err := s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: group}, number)
			replies <- reply
			errs <- err
		}(i)
	}
	wg.Wait()
	close(replies)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	granted := 0
	for reply := range replies {
		require.Contains(t, reply, "已领取")
		if strings.Contains(reply, "✅") {
			granted++
		}
	}
	require.Equal(t, 1, granted)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, 1.00000001, balance)
	for _, table := range []string{"qq_bot_rewards", "qq_bot_reward_codes", "redeem_codes"} {
		var count int
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		require.Equal(t, 1, count)
	}
	// Rebind the same winning account, then give the old QQ member another account.
	_, err = db.Exec(`UPDATE qq_bot_bindings SET member_id='rebound' WHERE user_id=$1`, uid)
	require.NoError(t, err)
	bind(t, db, user(t, db), "member")
	text, err = s.growthTasks(ctx, c, Job{MemberID: "rebound", GroupID: "other-group"}, number)
	require.NoError(t, err)
	require.Contains(t, text, "已领取")
	text, err = s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, number)
	require.NoError(t, err)
	require.Contains(t, text, "不属于当前账号")
}

func TestRankingClaimRollsBackEntireGrantAndPhysicalFulfillmentWaits(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid, number := rankingClaimFixture(t, GrowthReward{Type: "balance", Amount: 2})
	_, err := db.Exec(`CREATE FUNCTION reject_claim_ledger() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test ledger failure'; END $$;
 CREATE TRIGGER reject_claim_ledger BEFORE INSERT ON redeem_codes FOR EACH ROW EXECUTE FUNCTION reject_claim_ledger()`)
	require.NoError(t, err)
	_, err = s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, number)
	require.Error(t, err)
	var count int
	for _, table := range []string{"qq_bot_rewards", "qq_bot_reward_codes", "redeem_codes"} {
		require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM "+table).Scan(&count))
		require.Zero(t, count)
	}
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_ranking_claims WHERE claimed_at IS NULL`).Scan(&count))
	require.Equal(t, 1, count)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Zero(t, balance)
	_, err = db.Exec(`DROP TRIGGER reject_claim_ledger ON redeem_codes`)
	require.NoError(t, err)
	text, err := s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, number)
	require.NoError(t, err)
	require.Contains(t, text, "周期性成长奖励已领取")
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, 2., balance)
	for _, kind := range []string{"physical", "other"} {
		t.Run(kind, func(t *testing.T) {
			s, db, c, _, number := rankingClaimFixture(t, GrowthReward{Type: kind, Quantity: 2, PrizeName: "Cup", ClaimInstructions: "联系管理员"})
			var count int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_codes`).Scan(&count))
			require.Zero(t, count)
			text, err := s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, number)
			require.NoError(t, err)
			require.Contains(t, text, "待发放")
			require.Contains(t, text, "联系管理员")
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_codes WHERE status='pending' AND source_kind='ranking' AND code_hash IS NULL`).Scan(&count))
			require.Equal(t, 1, count)
		})
	}
}

func TestRankingClaimPreservesLegacyAutomaticGrants(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid, number := rankingClaimFixture(t, GrowthReward{Type: "balance", Amount: 3})
	key := fmt.Sprintf("qq-ranking:app:2026-10-20:tokens:%d", uid)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `SELECT id FROM users WHERE id=$1 FOR UPDATE`, uid)
	require.NoError(t, err)
	granted, err := grantActivityReward(ctx, tx, c, Job{}, uid, key, "ranking", "legacy reward", GrowthReward{Type: "balance", Amount: 3}, time.Now())
	require.NoError(t, err)
	require.True(t, granted)
	require.NoError(t, tx.Commit())
	text, err := s.growthTasks(ctx, c, Job{MemberID: "member", GroupID: "group"}, number)
	require.NoError(t, err)
	require.Equal(t, "该周期性成长任务奖励已领取。", text)
	// Formerly settled dates and even a lost settlement marker must not pay again.
	_, err = db.Exec(`DELETE FROM qq_bot_ranking_claims`)
	require.NoError(t, err)
	now := rankingTime(t, "2026-10-21T00:10:00+08:00")
	require.NoError(t, s.settleDueRankings(ctx, now))
	_, err = db.Exec(`DELETE FROM qq_bot_ranking_settlements`)
	require.NoError(t, err)
	require.NoError(t, s.settleDueRankings(ctx, now))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_ranking_claims`).Scan(&count))
	require.Zero(t, count)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, 3., balance)
}

func TestRankingWelfarePaginationAndStableCommand(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid, number := rankingClaimFixture(t, GrowthReward{Type: "chances", Quantity: 2})
	_, err := db.Exec(`INSERT INTO qq_bot_ranking_claims(app_id,ranking_day,ranking_kind,rank,user_id,source_key,reward)
 SELECT 'app','2026-10-19'::date-n,'tokens',1,$1,'historic-test-'||n,'{"type":"chances","quantity":1}'::jsonb FROM generate_series(0,29) n`, uid)
	require.NoError(t, err)
	sender, replies := helpTestSender(t)
	s.qqURL, s.tokenURL = sender.qqURL, sender.tokenURL
	seen := map[string]bool{}
	for page := 1; page <= 12; page++ {
		require.NoError(t, s.sendWelfare(ctx, c, Job{MemberID: "member", GroupID: "group", MessageID: fmt.Sprint(page), CreatedAt: time.Now()}, page))
		reply := (*replies)[len(*replies)-1]
		require.NotNil(t, reply.Keyboard)
		require.LessOrEqual(t, len(reply.Keyboard.Content.Rows), 5)
		next := false
		for _, row := range reply.Keyboard.Content.Rows {
			require.LessOrEqual(t, len(row.Buttons), 2)
			for _, button := range row.Buttons {
				cmd := button.Action.Data
				if strings.HasPrefix(cmd, "/福利 领取 R") {
					require.Contains(t, reply.text(), cmd)
					require.False(t, seen[cmd])
					seen[cmd] = true
				}
				next = next || cmd == fmt.Sprintf("/福利 %d", page+1)
			}
		}
		if !next {
			break
		}
	}
	require.Len(t, seen, 31)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_codes`).Scan(&count))
	require.Zero(t, count)
	j := Job{MemberID: "member", GroupID: "group", MessageID: "claim-periodic", CreatedAt: time.Now()}
	require.NoError(t, s.handleWelfare(ctx, c, &j, "领取 "+number))
	require.Contains(t, (*replies)[len(*replies)-1].text(), "周期性成长奖励已领取")
	require.NoError(t, s.handleWelfare(ctx, c, &j, "领取 "+number))
	require.Contains(t, (*replies)[len(*replies)-1].text(), "该周期性成长任务奖励已领取")
	require.NoError(t, db.QueryRow(`SELECT SUM(chances) FROM qq_bot_lottery_chance_grants WHERE user_id=$1`, uid).Scan(&count))
	require.Equal(t, 2, count)
	var actions []keyboardButton
	text, err := s.growthTasks(ctx, c, j, "", &actions)
	require.NoError(t, err)
	require.Contains(t, text, "**"+number+". 2026-10-20")
	require.Len(t, actions, 30)
	for _, button := range actions {
		require.NotEqual(t, "/福利 领取 "+number, button.Action.Data)
	}
	_, err = db.Exec(`UPDATE qq_bot_ranking_claims SET claimed_at=NOW()`)
	require.NoError(t, err)
	text, err = s.growthTasks(ctx, c, j, "")
	require.NoError(t, err)
	require.Equal(t, 10, strings.Count(text, " · 已领取"))
}
