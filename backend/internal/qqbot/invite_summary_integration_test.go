//go:build integration

package qqbot

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQQBotMyInvitesCountsStatesAndDeduplicatesAcrossGroups(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	c.InviteDailyLimit = 20
	inviter := user(t, db)
	bind(t, db, inviter, "inviter")
	bind(t, db, user(t, db), "other-inviter")
	for _, member := range []string{"ready", "paid", "paid-elsewhere", "paused"} {
		bind(t, db, user(t, db), member)
	}
	unavailable := user(t, db)
	bind(t, db, unavailable, "unavailable")
	_, err = db.Exec(`UPDATE users SET status='disabled' WHERE id=$1`, unavailable)
	require.NoError(t, err)
	now := time.Now().Truncate(time.Second)
	invited, joined := now.Add(-2*time.Minute), now.Add(-time.Minute)
	add := func(group, member, from string, invitation, join, left any) {
		t.Helper()
		_, insertErr := db.Exec(`INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at,joined_at,left_at)
 VALUES('app',$1,$2,$3,$4,$5,$6)`, group, member, from, invitation, join, left)
		require.NoError(t, insertErr)
	}
	add("group", "pending", "inviter", invited, nil, nil)
	add("group", "unbound", "inviter", invited, joined, nil)
	add("other-group", "unbound", "inviter", invited, joined, now)
	add("group", "ready", "inviter", invited, joined, nil)
	add("blocked-group", "ready", "inviter", invited, joined, nil)
	add("other-group", "ready", "inviter", invited, joined, now)
	add("group", "paid", "inviter", invited, joined, nil)
	add("other-group", "paid", "inviter", invited, joined, nil)
	add("group", "left", "inviter", invited, joined, now)
	add("group", "invalid", "inviter", joined.Add(-8*24*time.Hour), joined, nil)
	add("group", "unavailable", "inviter", invited, joined, nil)
	add("group", "inviter", "inviter", invited, joined, nil)
	add("blocked-group", "paused", "inviter", invited, joined, nil)
	add("group", "paid-elsewhere", "inviter", invited, joined, nil)
	add("other-group", "paid-elsewhere", "other-inviter", invited, joined, nil)
	// Ordinary joins, substring matches, another inviter and another application
	// must not become this member's invitations.
	add("group", "ordinary", "", nil, joined, nil)
	add("group", "other-recipient", "other-inviter", invited, joined, nil)
	add("group", "substring", "prefix-inviter-suffix", invited, joined, nil)
	_, err = db.Exec(`INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at,joined_at)
 VALUES('other-app','group','other-app-member','inviter',$1,$2)`, invited, joined)
	require.NoError(t, err)
	for _, member := range []string{"paid", "paid-elsewhere"} {
		result, payErr := s.reward(ctx, c, Job{GroupID: "other-group", MemberID: member}, "invite")
		require.NoError(t, payErr)
		require.Contains(t, result, "邀请奖励已发放")
	}
	// A historical reward must remain rewarded even after leaving the paid group.
	_, err = db.Exec(`UPDATE qq_bot_members SET left_at=$1 WHERE member_id='paid'`, now)
	require.NoError(t, err)
	for range 2 {
		info, readErr := s.myInvites(ctx, c, Job{MemberID: "inviter"})
		require.NoError(t, readErr)
		require.Equal(t, "累计邀请：10 人\n待入群：1 人\n待绑定：1 人\n待结算：1 人\n已奖励：1 人\n已退群：1 人\n其他邀请已奖励：1 人\n奖励暂停：1 人\n账号不可用：1 人\n不符合奖励条件：2 人", info)
	}
	var rewards, history int
	var balance float64
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_rewards`).Scan(&rewards))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM redeem_codes`).Scan(&history))
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, inviter).Scan(&balance))
	require.Equal(t, 2, rewards)
	require.Equal(t, 2, history)
	require.Equal(t, 1.0, balance, "querying invitations must not settle the ready invitation")
}

func TestQQBotMyInvitesUsesCurrentConditionsAndKeepsHistoricalRewards(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at,joined_at)
 VALUES('app','group','invitee','inviter',NOW()-INTERVAL '2 minutes',NOW()-INTERVAL '1 minute')`)
	require.NoError(t, err)
	read := func(config Config) string {
		t.Helper()
		info, readErr := s.myInvites(ctx, config, Job{MemberID: "inviter"})
		require.NoError(t, readErr)
		return info
	}
	require.Contains(t, read(c), "待绑定：1 人", "both users can be unbound")
	invitee := user(t, db)
	bind(t, db, invitee, "invitee")
	require.Contains(t, read(c), "待绑定：1 人", "the inviter can still be unbound")
	inviter := user(t, db)
	bind(t, db, inviter, "inviter")
	require.Contains(t, read(c), "待结算：1 人")
	for _, disabled := range []struct {
		name   string
		change func(*Config)
	}{
		{"bot disabled", func(c *Config) { c.Enabled = false }},
		{"zero reward", func(c *Config) { c.InviteReward = 0 }},
		{"zero limit", func(c *Config) { c.InviteDailyLimit = 0 }},
		{"group disabled", func(c *Config) { c.AllowedGroups = []string{"other-group"} }},
	} {
		t.Run(disabled.name, func(t *testing.T) {
			changed := c
			disabled.change(&changed)
			info := read(changed)
			require.Contains(t, info, "奖励暂停：1 人")
			require.Contains(t, info, "待结算：0 人")
		})
	}
	for _, update := range []string{
		`UPDATE users SET status='disabled' WHERE id=$1`,
		`UPDATE users SET status='active',deleted_at=NOW() WHERE id=$1`,
	} {
		_, err = db.Exec(update, invitee)
		require.NoError(t, err)
		require.Contains(t, read(c), "账号不可用：1 人")
	}
	_, err = db.Exec(`UPDATE users SET deleted_at=NULL WHERE id=$1`, invitee)
	require.NoError(t, err)
	c.InviteDailyLimit = 1
	result, err := s.reward(ctx, c, Job{GroupID: "group", MemberID: "invitee"}, "invite")
	require.NoError(t, err)
	require.Contains(t, result, "邀请奖励已发放")
	bind(t, db, user(t, db), "second-invitee")
	_, err = db.Exec(`INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at,joined_at)
 VALUES('app','group','second-invitee','inviter',NOW()-INTERVAL '2 minutes',NOW()-INTERVAL '1 minute')`)
	require.NoError(t, err)
	info := read(c)
	require.Contains(t, info, "已奖励：1 人")
	require.Contains(t, info, "今日达上限：1 人")
	_, err = db.Exec(`DELETE FROM qq_bot_bindings WHERE member_id IN ('inviter','invitee')`)
	require.NoError(t, err)
	bind(t, db, user(t, db), "inviter")
	info = read(c)
	require.Contains(t, info, "已奖励：1 人", "rebinding cannot erase a historical QQ invitation reward")
	require.Contains(t, info, "待结算：1 人", "the daily limit follows the currently bound account")
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_rewards`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestQQBotMyInvitesHasNoPaginationLimitAndDoesNotGuessRelationships(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	c, err := s.Config(ctx)
	require.NoError(t, err)
	zero := "累计邀请：0 人\n待入群：0 人\n待绑定：0 人\n待结算：0 人\n已奖励：0 人\n已退群：0 人"
	info, err := s.myInvites(ctx, c, Job{MemberID: "inviter"})
	require.NoError(t, err)
	require.Equal(t, zero, info)
	_, err = db.Exec(`INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at,joined_at)
 SELECT 'app',g,'invitee-'||n,'inviter',NOW()-INTERVAL '2 minutes',NOW()-INTERVAL '1 minute'
 FROM generate_series(1,150) n CROSS JOIN (VALUES ('group'),('other-group')) groups(g)`)
	require.NoError(t, err)
	info, err = s.myInvites(ctx, c, Job{MemberID: "inviter"})
	require.NoError(t, err)
	require.Equal(t, "累计邀请：150 人\n待入群：0 人\n待绑定：150 人\n待结算：0 人\n已奖励：0 人\n已退群：0 人", info)
	// A member with no attributed invitations must never inherit another QQ's
	// results, even if it sees the same group or passes a search-like identifier.
	for _, member := range []string{"new-member", "inviter' OR true --", ""} {
		info, err = s.myInvites(ctx, c, Job{MemberID: member})
		require.NoError(t, err)
		require.Equal(t, zero, info)
	}
}
