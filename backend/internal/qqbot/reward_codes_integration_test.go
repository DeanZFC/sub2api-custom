//go:build integration

package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func rewardCodeFixture(t *testing.T) (*Service, *sql.DB, Config, int64) {
	t.Helper()
	db := testDB(t)
	s := savedBot(t, db)
	uid := user(t, db)
	bind(t, db, uid, "member")
	c, err := s.Config(context.Background())
	require.NoError(t, err)
	return s, db, c, uid
}
func createTestRewardCode(t *testing.T, s *Service, kind string) GeneratedRewardCode {
	t.Helper()
	r := RewardCodeCreate{BatchName: "test batch", RewardType: kind, Count: 1, PrizeName: "定制水杯"}
	if kind == "balance" {
		r.Amount = 10
	} else {
		r.Quantity = 3
	}
	out, err := s.CreateRewardCodes(context.Background(), r)
	require.NoError(t, err)
	require.Len(t, out, 1)
	return out[0]
}
func rewardJob(code GeneratedRewardCode, id string) Job {
	return Job{ID: id, MessageID: id, AppID: "app", MemberID: "member", GroupID: "group", RewardCodeHash: rewardCodeHash(code.Code)}
}
func resetRewardCooldown(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`UPDATE qq_bot_reward_limits SET last_attempt_at=NOW()-INTERVAL '6 seconds'`)
	require.NoError(t, err)
}

func TestRewardCodeBalanceAtomicAndReplay(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid := rewardCodeFixture(t)
	code := createTestRewardCode(t, s, "balance")
	var hash string
	require.NoError(t, db.QueryRow(`SELECT code_hash FROM qq_bot_reward_codes WHERE id=$1`, code.ID).Scan(&hash))
	require.Equal(t, rewardCodeHash(code.Code), hash)
	var invalidated atomic.Int32
	s.invalidate = func(context.Context, int64) { invalidated.Add(1) }
	reply, err := s.redeemReward(ctx, c, rewardJob(code, "first"))
	require.NoError(t, err)
	require.Contains(t, reply, "USD 10.00")
	replay, err := s.redeemReward(ctx, c, rewardJob(code, "first"))
	require.NoError(t, err)
	require.Equal(t, reply, replay)
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, uid).Scan(&balance))
	require.Equal(t, 10.0, balance)
	require.Equal(t, int32(1), invalidated.Load())
	state, err := s.LotteryState(ctx, uid)
	require.NoError(t, err)
	require.Zero(t, state.Earned)
	require.Zero(t, state.RechargeTotal)
	summary, err := s.myRewards(ctx, c, Job{MemberID: "member"})
	require.NoError(t, err)
	require.Contains(t, summary, "兑换奖励：USD 10.00")
	resetRewardCooldown(t, db)
	reply, err = s.redeemReward(ctx, c, rewardJob(code, "new-message"))
	require.NoError(t, err)
	require.Equal(t, rewardCodeInvalid, reply)
	list, err := s.ListRewardCodes(ctx, "test batch", "fulfilled", 1)
	require.NoError(t, err)
	require.Equal(t, 1, list.Total)
	raw, err := json.Marshal(list)
	require.NoError(t, err)
	require.NotContains(t, string(raw), code.Code)
	require.NotContains(t, string(raw), hash)
}

func TestRewardCodeChancesAndManualFulfillment(t *testing.T) {
	for _, kind := range []string{"chances", "physical", "other"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s, db, c, uid := rewardCodeFixture(t)
			code := createTestRewardCode(t, s, kind)
			reply, err := s.redeemReward(ctx, c, rewardJob(code, "redeem"))
			require.NoError(t, err)
			if kind == "chances" {
				require.Contains(t, reply, "抽奖次数 × 3")
				state, err := s.LotteryState(ctx, uid)
				require.NoError(t, err)
				require.Equal(t, int64(3), state.Remaining)
				cfg, err := s.LotteryConfig(ctx)
				require.NoError(t, err)
				cfg.Enabled = true
				require.NoError(t, s.SaveLotteryConfig(ctx, cfg))
				_, err = s.drawLottery(ctx, c, Job{ID: "draw", MemberID: "member", GroupID: "group"})
				require.NoError(t, err)
				state, err = s.LotteryState(ctx, uid)
				require.NoError(t, err)
				require.Equal(t, int64(2), state.Remaining)
				require.ErrorIs(t, s.UpdateRewardCode(ctx, code.ID, "fulfill", "no"), ErrRewardCodeRequest)
			} else {
				require.Contains(t, reply, "定制水杯 × 3")
				require.Contains(t, reply, "待发放")
				require.NoError(t, s.UpdateRewardCode(ctx, code.ID, "fulfill", "delivered"))
				require.ErrorIs(t, s.UpdateRewardCode(ctx, code.ID, "fulfill", "duplicate"), ErrRewardCodeRequest)
				var status, note string
				var fulfilled time.Time
				require.NoError(t, db.QueryRow(`SELECT status,fulfillment_note,fulfilled_at FROM qq_bot_reward_codes WHERE id=$1`, code.ID).Scan(&status, &note, &fulfilled))
				require.Equal(t, "fulfilled", status)
				require.Equal(t, "delivered", note)
			}
		})
	}
}

func TestRewardCodeConcurrentUsersOnlyOneGrant(t *testing.T) {
	ctx := context.Background()
	s, db, c, _ := rewardCodeFixture(t)
	code := createTestRewardCode(t, s, "balance")
	var wg sync.WaitGroup
	var won, failed atomic.Int32
	for i := range 12 {
		uid := user(t, db)
		member := fmt.Sprintf("member-%d", i)
		bind(t, db, uid, member)
		wg.Add(1)
		go func(i int, member string) {
			defer wg.Done()
			bot := New(db, testEncryptor{}, true, nil)
			j := rewardJob(code, fmt.Sprint(i))
			j.MemberID = member
			reply, err := bot.redeemReward(ctx, c, j)
			if err != nil {
				failed.Add(1)
			}
			if strings.Contains(reply, "奖励兑换成功") {
				won.Add(1)
			}
		}(i, member)
	}
	wg.Wait()
	require.Zero(t, failed.Load())
	require.Equal(t, int32(1), won.Load())
	var total float64
	require.NoError(t, db.QueryRow(`SELECT SUM(balance) FROM users`).Scan(&total))
	require.Equal(t, 10.0, total)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_rewards WHERE kind='redemption'`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestRewardCodeLimitsPersistAcrossGroupsRestartsAndRebinding(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid := rewardCodeFixture(t)
	valid := createTestRewardCode(t, s, "balance")
	j := rewardJob(GeneratedRewardCode{Code: "wrong"}, "wrong-1")
	reply, err := s.redeemReward(ctx, c, j)
	require.NoError(t, err)
	require.Equal(t, rewardCodeInvalid, reply)
	j.ID = "fast"
	j.MessageID = "fast"
	j.GroupID = "other-group"
	reply, err = s.redeemReward(ctx, c, j)
	require.NoError(t, err)
	require.Contains(t, reply, "5 秒")
	for i := 2; i <= 5; i++ {
		resetRewardCooldown(t, db)
		j.ID = fmt.Sprint(i)
		j.MessageID = j.ID
		reply, err = s.redeemReward(ctx, c, j)
		require.NoError(t, err)
	}
	require.Equal(t, rewardCodeLocked, reply)
	bot := New(db, testEncryptor{}, true, nil)
	reply, err = bot.redeemReward(ctx, c, rewardJob(valid, "blocked-valid"))
	require.NoError(t, err)
	require.Equal(t, rewardCodeLocked, reply)
	// Same QQ with a new website account retains the member lock.
	other := user(t, db)
	_, err = db.Exec(`UPDATE qq_bot_bindings SET user_id=$1 WHERE member_id='member'`, other)
	require.NoError(t, err)
	reply, err = bot.redeemReward(ctx, c, rewardJob(valid, "rebound-member"))
	require.NoError(t, err)
	require.Equal(t, rewardCodeLocked, reply)
	// Same website account with a new QQ retains the user lock.
	bind(t, db, uid, "new-member")
	j = rewardJob(valid, "rebound-user")
	j.MemberID = "new-member"
	reply, err = bot.redeemReward(ctx, c, j)
	require.NoError(t, err)
	require.Equal(t, rewardCodeLocked, reply)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_attempts`).Scan(&count))
	require.Equal(t, 5, count)
}

func TestRewardCodeDailyLimitAndLockExpiry(t *testing.T) {
	ctx := context.Background()
	s, db, c, _ := rewardCodeFixture(t)
	for i := 1; i <= 20; i++ {
		resetRewardCooldown(t, db)
		if i > 1 && (i-1)%5 == 0 {
			_, err := db.Exec(`UPDATE qq_bot_reward_limits SET blocked_until=NOW()-INTERVAL '1 second'; UPDATE qq_bot_reward_attempts SET created_at=NOW()-INTERVAL '16 minutes'`)
			require.NoError(t, err)
		}
		reply, err := s.redeemReward(ctx, c, rewardJob(GeneratedRewardCode{Code: "invalid"}, fmt.Sprint(i)))
		require.NoError(t, err)
		if i%5 == 0 && i < 20 {
			require.Equal(t, rewardCodeLocked, reply)
		}
		if i == 20 {
			require.Contains(t, reply, "今日兑换失败次数已达上限")
		}
	}
	_, err := db.Exec(`UPDATE qq_bot_reward_limits SET blocked_until=NOW()-INTERVAL '1 second'`)
	require.NoError(t, err)
	resetRewardCooldown(t, db)
	code := createTestRewardCode(t, s, "balance")
	reply, err := s.redeemReward(ctx, c, rewardJob(code, "day-blocked"))
	require.NoError(t, err)
	require.Contains(t, reply, "今日兑换失败次数已达上限")
}

func TestRewardCodeUnavailableResponsesAndNoConsumption(t *testing.T) {
	for _, mode := range []string{"expired", "disabled", "target", "app"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, db, c, _ := rewardCodeFixture(t)
			code := createTestRewardCode(t, s, "balance")
			var err error
			switch mode {
			case "expired":
				_, err = db.Exec(`UPDATE qq_bot_reward_codes SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, code.ID)
			case "disabled":
				err = s.UpdateRewardCode(ctx, code.ID, "disable", "")
			case "target":
				_, err = db.Exec(`UPDATE qq_bot_reward_codes SET target_user_id=$2 WHERE id=$1`, code.ID, user(t, db))
			case "app":
				_, err = db.Exec(`UPDATE qq_bot_reward_codes SET app_id='other-app' WHERE id=$1`, code.ID)
			}
			require.NoError(t, err)
			reply, err := s.redeemReward(ctx, c, rewardJob(code, "redeem"))
			require.NoError(t, err)
			require.Equal(t, rewardCodeInvalid, reply)
			var used bool
			require.NoError(t, db.QueryRow(`SELECT user_id IS NOT NULL FROM qq_bot_reward_codes WHERE id=$1`, code.ID).Scan(&used))
			require.False(t, used)
		})
	}
}

func TestRewardCodeFailureRollsBackEntitlementAndLimits(t *testing.T) {
	ctx := context.Background()
	s, db, c, uid := rewardCodeFixture(t)
	code := createTestRewardCode(t, s, "chances")
	_, err := db.Exec(`INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES($1,$2,1)`, fmt.Sprintf("qq-reward-code:%d", code.ID), uid)
	require.NoError(t, err)
	_, err = s.redeemReward(ctx, c, rewardJob(code, "rollback"))
	require.Error(t, err)
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM qq_bot_reward_codes WHERE id=$1`, code.ID).Scan(&status))
	require.Equal(t, "unused", status)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_attempts`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_limits`).Scan(&count))
	require.Zero(t, count)
	_, err = db.Exec(`DELETE FROM qq_bot_lottery_chance_grants`)
	require.NoError(t, err)
	reply, err := s.redeemReward(ctx, c, rewardJob(code, "rollback"))
	require.NoError(t, err)
	require.Contains(t, reply, "奖励兑换成功")
}

func TestRewardCodeWebhookRedactionDedupAndDelivery(t *testing.T) {
	ctx := context.Background()
	s, db, c, _ := rewardCodeFixture(t)
	code := createTestRewardCode(t, s, "balance")
	e := Event{ID: "message", GroupID: "group", Content: "/奖励兑换 " + code.Code, MsgElements: []MessageElement{{Content: code.Code}}}
	e.Author.MemberID = "member"
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	for _, event := range []string{groupMessageCreate, groupAtMessageCreate} {
		require.NoError(t, s.Enqueue(ctx, c, Payload{ID: event, Type: event, Data: raw}))
	}
	var payload, hash string
	require.NoError(t, db.QueryRow(`SELECT payload::text,reward_code_hash FROM qq_bot_jobs`).Scan(&payload, &hash))
	require.NotContains(t, payload, code.Code)
	require.Equal(t, rewardCodeHash(code.Code), hash)
	var reply string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"access_token":"test","expires_in":7200}`))
			return
		}
		require.Equal(t, "/v2/groups/group/messages", r.URL.Path)
		reply = readQQTestReply(t, r).text()
		_, _ = w.Write([]byte(`{"id":"sent"}`))
	}))
	defer server.Close()
	s.qqURL, s.tokenURL = server.URL, server.URL+"/token"
	s.runOne(ctx)
	require.Contains(t, reply, "奖励兑换成功")
	require.NotContains(t, reply, code.Code)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_rewards WHERE kind='redemption'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_group_messages`).Scan(&count))
	require.Zero(t, count)
}

func TestRewardCodeConcurrentFailuresDoNotBypassCooldown(t *testing.T) {
	ctx := context.Background()
	s, db, c, _ := rewardCodeFixture(t)
	var wg sync.WaitGroup
	var failed atomic.Int32
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bot := s
			if i%2 == 0 {
				bot = New(db, testEncryptor{}, true, nil)
			}
			_, err := bot.redeemReward(ctx, c, rewardJob(GeneratedRewardCode{Code: "wrong"}, fmt.Sprint(i)))
			if err != nil {
				failed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Zero(t, failed.Load())
	var attempts int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_attempts`).Scan(&attempts))
	require.Equal(t, 1, attempts)
}

func TestRewardCodeRechecksConfigAndAccountBeforeConsuming(t *testing.T) {
	for _, mode := range []string{"disabled_bot", "removed_group", "inactive_user", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, db, c, uid := rewardCodeFixture(t)
			code := createTestRewardCode(t, s, "balance")
			var err error
			switch mode {
			case "disabled_bot":
				updated := c
				updated.Enabled = false
				_, err = s.SaveConfig(ctx, updated)
			case "removed_group":
				updated := c
				updated.AllowedGroups = []string{"other-group"}
				_, err = s.SaveConfig(ctx, updated)
			case "inactive_user":
				_, err = db.Exec(`UPDATE users SET status='disabled' WHERE id=$1`, uid)
			case "unbound":
				_, err = db.Exec(`DELETE FROM qq_bot_bindings WHERE user_id=$1`, uid)
			}
			require.NoError(t, err)
			reply, err := s.redeemReward(ctx, c, rewardJob(code, "stale"))
			require.NoError(t, err)
			require.NotContains(t, reply, "兑换成功")
			var state string
			require.NoError(t, db.QueryRow(`SELECT status FROM qq_bot_reward_codes WHERE id=$1`, code.ID).Scan(&state))
			require.Equal(t, "unused", state)
			var attempts int
			require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_attempts`).Scan(&attempts))
			require.Zero(t, attempts)
		})
	}
}
