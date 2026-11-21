//go:build integration

package qqbot

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQQRewardReportComponentsCalendarAndHistoricalOwnership(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	u1, u2 := user(t, db), user(t, db)
	bind(t, db, u1, "first")
	bind(t, db, u2, "second")
	_, err := db.Exec(`UPDATE users SET email=CASE WHEN id=$1 THEN 'first@example.com' ELSE 'second@example.com' END`, u1)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE qq_bot_bindings SET nickname=CASE WHEN user_id=$1 THEN 'Alice' ELSE 'Bob' END`, u1)
	require.NoError(t, err)
	credit := func(key, app, group, member, kind, amount, at string, uid int64) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO qq_bot_rewards(reward_key,app_id,group_id,member_id,user_id,kind,amount,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, key, app, group, member, uid, kind, amount, at)
		require.NoError(t, err)
	}
	credit("checkin", "app", "g1", "first", "checkin", "1.25", "2026-11-18T00:00:00+08:00", u1)
	credit("invite", "app", "g2", "second", "invite", ".75", "2026-11-19T23:59:59.999999+08:00", u2)
	credit("growth", "app", "g1", "first", "growth", ".5", "2026-11-18T12:00:00+08:00", u1)
	credit("zero", "app", "g1", "first", "checkin", "0", "2026-11-18T01:00:00+08:00", u1)
	credit("before", "app", "g1", "first", "checkin", "10", "2026-11-17T23:59:59.999999+08:00", u1)
	credit("after", "app", "g1", "first", "checkin", "20", "2026-11-20T00:00:00+08:00", u1)
	credit("other-app", "different", "g1", "first", "checkin", "99", "2026-11-18T01:00:00+08:00", u1)
	var draw int64
	err = db.QueryRow(`INSERT INTO qq_bot_lottery_draws(request_key,app_id,member_id,group_id,user_id,prize_name,amount,config_snapshot,recharge_total,earned,remaining,result_text,created_at)
 VALUES('draw','app','first','g1',$1,'bundle',0,'{"awarded_chances":2,"coupon_quantity":3}',0,1,0,'ok','2026-11-17T23:59:59+08:00') RETURNING id`, u1).Scan(&draw)
	require.NoError(t, err)
	credit(fmt.Sprintf("lottery:%d", draw), "app", "g1", "first", "lottery", "0", "2026-11-18T00:00:01+08:00", u1)
	// App reports exclude grants without an application ID and count the draw once.
	_, err = db.Exec(`INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES($1,$2,2),('legacy:test',$2,77),('qq-growth:legacy',$2,88)`, fmt.Sprintf("qq-lottery:%d", draw), u1)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO qq_bot_reward_codes(app_id,code_hash,code_hint,batch_name,reward_type,amount,quantity,status,user_id,member_id,group_id,redeemed_at,fulfilled_at,source_kind,source_key) VALUES
 ('app',NULL,'','growth','balance',.5,0,'fulfilled',$1,'first','g1','2026-11-18T12:00:00+08:00','2026-11-18T12:00:00+08:00','growth','growth'),
 ('app',NULL,'','chances','chances',0,4,'fulfilled',$1,'first','g1','2026-11-18T13:00:00+08:00','2026-11-18T13:00:00+08:00','growth','chances'),
 ('app',NULL,'','parcel','physical',0,2,'pending',$2,'second','g2','2026-11-19T09:00:00+08:00',NULL,'ranking','parcel'),
 ('app',NULL,'','other','other',0,1,'fulfilled',$1,'first','g1','2026-11-18T14:00:00+08:00','2026-11-20T01:00:00+08:00','streak','other'),
 ('different',NULL,'','other','chances',0,99,'fulfilled',$1,'first','g1','2026-11-18T14:00:00+08:00','2026-11-18T14:00:00+08:00','growth','hidden'),
 ('app','unused-code','','unused','chances',0,55,'unused',NULL,'','',NULL,NULL,'code',NULL)`, u1, u2)
	require.NoError(t, err)
	// Real records have historical creation times for manual prizes; use the
	// redemption date, rather than generating or fulfilling date, for the report.
	_, err = db.Exec(`UPDATE qq_bot_reward_codes SET created_at='2026-01-01'`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE users SET status='disabled',deleted_at=NOW() WHERE id=$1`, u2)
	require.NoError(t, err)
	f := ReportFilter{StartDate: "2026-11-18", EndDate: "2026-11-19", PageSize: 3}
	r, err := s.RewardReport(ctx, f)
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", r.Timezone)
	require.Equal(t, ReportMetrics{RewardCount: 8, UserCount: 2, BalanceAmount: 2.5, ChancesQuantity: 6, CouponQuantity: 3, PendingQuantity: 2, FulfilledQuantity: 1}, r.Summary)
	require.Equal(t, int64(8), r.Total)
	require.Len(t, r.Items, 3)
	require.Len(t, r.Daily, 2)
	require.Equal(t, int64(6), r.Daily[0].RewardCount)
	require.Equal(t, int64(2), r.Daily[1].RewardCount)
	require.Equal(t, int64(2), r.Daily[0].ChancesQuantity-4, "lottery uses actual credit day across midnight")
	require.Len(t, r.TopUsers, 2)
	require.Equal(t, u1, r.TopUsers[0].UserID)
	require.Equal(t, 1.75, r.TopUsers[0].BalanceAmount)
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		f.Page = page
		result, err := s.RewardReport(ctx, f)
		require.NoError(t, err)
		require.Equal(t, r.Summary, result.Summary, "pagination only changes detail rows")
		for _, item := range result.Items {
			require.False(t, seen[item.ID])
			seen[item.ID] = true
		}
	}
	require.Len(t, seen, 8)
	for _, tc := range []struct {
		f     ReportFilter
		count int64
	}{
		{ReportFilter{GroupID: "g1"}, 6}, {ReportFilter{Kind: "lottery"}, 2},
		{ReportFilter{RewardType: "chances"}, 2}, {ReportFilter{Search: "ALICE"}, 6},
		{ReportFilter{Search: "SECOND@"}, 2}, {ReportFilter{Search: fmt.Sprint(u1)}, 6},
		{ReportFilter{Search: "%"}, 0},
	} {
		tc.f.StartDate, tc.f.EndDate = f.StartDate, f.EndDate
		got, err := s.RewardReport(ctx, tc.f)
		require.NoError(t, err)
		require.Equal(t, tc.count, got.Total, "%+v", tc.f)
		var sum int64
		for _, d := range got.Daily {
			sum += d.RewardCount
		}
		require.Equal(t, got.Total, sum)
	}
	// Rebinding never reattributes the original account's money or nickname.
	_, err = db.Exec(`DELETE FROM qq_bot_bindings WHERE user_id=$1`, u1)
	require.NoError(t, err)
	bind(t, db, user(t, db), "first")
	f.Page, f.PageSize, f.Search = 1, 100, "first@"
	r, err = s.RewardReport(ctx, f)
	require.NoError(t, err)
	for _, item := range r.Items {
		require.Equal(t, u1, item.UserID)
		require.Empty(t, item.Nickname)
	}
	var balance float64
	var pending int
	require.NoError(t, db.QueryRow(`SELECT balance FROM users WHERE id=$1`, u1).Scan(&balance))
	require.Zero(t, balance, "reading a report cannot grant balances")
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM qq_bot_reward_codes WHERE status='pending'`).Scan(&pending))
	require.Equal(t, 1, pending, "reading cannot fulfill pending prizes")
}

func TestQQRewardReportEmptyDaysAndLegacyRedemption(t *testing.T) {
	db := testDB(t)
	s := savedBot(t, db)
	ctx := context.Background()
	uid := user(t, db)
	_, err := db.Exec(`INSERT INTO qq_bot_reward_codes(app_id,code_hash,code_hint,batch_name,reward_type,quantity,status,user_id,member_id,group_id,redeemed_at,fulfilled_at)
 VALUES('app','claimed','','old chances','chances',3,'fulfilled',$1,'member','group','2026-11-18T23:59:59+08:00','2026-11-18T23:59:59+08:00')`, uid)
	require.NoError(t, err)
	r, err := s.RewardReport(ctx, ReportFilter{StartDate: "2026-11-17", EndDate: "2026-11-19", Kind: "redemption"})
	require.NoError(t, err)
	require.Equal(t, int64(1), r.Total)
	require.Len(t, r.Daily, 3)
	require.Zero(t, r.Daily[0].RewardCount)
	require.Equal(t, int64(1), r.Daily[1].RewardCount)
	require.Zero(t, r.Daily[2].RewardCount)
	require.Equal(t, "redemption", r.Items[0].Kind)
	_, err = db.Exec(`INSERT INTO qq_bot_lottery_draws(request_key,app_id,member_id,group_id,user_id,prize_name,amount,config_snapshot,recharge_total,earned,remaining,result_text,created_at)
 VALUES('legacy','app','member','group',$1,'legacy bundle',0,'{"awarded_chances":5}',0,1,0,'ok','2026-11-19T23:59:59+08:00')`, uid)
	require.NoError(t, err)
	r, err = s.RewardReport(ctx, ReportFilter{StartDate: "2026-11-19", EndDate: "2026-11-19", Kind: "lottery"})
	require.NoError(t, err)
	require.Equal(t, int64(1), r.Total, "legacy draw without ledger uses its own creation time")
	require.Equal(t, int64(5), r.Summary.ChancesQuantity)
	r, err = s.RewardReport(ctx, ReportFilter{StartDate: "2026-11-17", EndDate: "2026-11-19", Search: "absent", Page: 10})
	require.NoError(t, err)
	require.Empty(t, r.Items)
	require.Empty(t, r.ByKind)
	require.Empty(t, r.TopUsers)
	require.Zero(t, r.Summary)
	require.Len(t, r.Daily, 3)
	require.Equal(t, "2026-11-17", r.Daily[0].Date)
}
