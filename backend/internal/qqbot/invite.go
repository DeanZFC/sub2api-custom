package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) myInvites(ctx context.Context, c Config, j Job) (string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	// Scope by the authenticated QQ identity, even before it binds a website
	// account. Read all relationships rather than a page of admin search results.
	byMember := make(map[string]string)
	err = visitInviteRecords(ctx, tx, c, inviteRelationshipSelect+`
 WHERE m.app_id=$1 AND m.inviter_id=$2 AND m.inviter_id<>''`, []any{c.AppID, j.MemberID}, func(r ActivityRecord) {
		previous, found := byMember[r.Participant.MemberID]
		if !found || invitationStatusPriority(r.Status) < invitationStatusPriority(previous) {
			byMember[r.Participant.MemberID] = r.Status
		}
	})
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	counts := make(map[string]int)
	for _, status := range byMember {
		counts[status]++
	}
	var text strings.Builder
	fmt.Fprintf(&text, "累计邀请：%d 人\n待入群：%d 人\n待绑定：%d 人\n待结算：%d 人\n已奖励：%d 人\n已退群：%d 人",
		len(byMember), counts["pending_join"], counts["both_unbound"]+counts["inviter_unbound"]+counts["invitee_unbound"],
		counts["pending_reward"], counts["rewarded"], counts["left_group"])
	for _, extra := range []struct {
		label string
		count int
	}{
		{"今日达上限", counts["daily_limit"]},
		{"其他邀请已奖励", counts["already_rewarded"]},
		{"奖励暂停", counts["disabled"] + counts["group_disabled"]},
		{"账号不可用", counts["account_unavailable"]},
		{"不符合奖励条件", counts["invalid_invitation"] + counts["self_invitation"]},
	} {
		if extra.count > 0 {
			fmt.Fprintf(&text, "\n%s：%d 人", extra.label, extra.count)
		}
	}
	return text.String(), nil
}

// Count each QQ invitee once across groups. A historical payout wins; otherwise
// prefer a joined relationship that can progress towards a reward over a stale
// request or a group the invitee has left.
func invitationStatusPriority(status string) int {
	switch status {
	case "rewarded":
		return 0
	case "already_rewarded":
		return 1
	case "pending_reward":
		return 2
	case "daily_limit":
		return 3
	case "invitee_unbound", "inviter_unbound", "both_unbound":
		return 4
	case "account_unavailable":
		return 5
	case "self_invitation":
		return 6
	case "disabled", "group_disabled":
		return 7
	case "pending_join":
		return 8
	case "invalid_invitation":
		return 9
	default: // left_group
		return 10
	}
}

func (s *Service) inviteInfo(ctx context.Context, c Config, j Job) (string, error) {
	if c.InviteReward <= 0 || c.InviteDailyLimit <= 0 {
		return "邀请活动未开启。", nil
	}
	var joined, rewarded int
	// Count joins separately from payouts: an invitee can bind on a later day.
	// Match the payout limit's account-wide scope, including rewards from other groups.
	err := s.db.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(DISTINCT m.member_id) FROM qq_bot_members m
  WHERE m.app_id=b.app_id AND m.inviter_id=b.member_id AND m.member_id<>b.member_id
  AND m.invited_at<=m.joined_at AND m.invited_at>m.joined_at-INTERVAL '7 days'
  AND (m.joined_at AT TIME ZONE 'Asia/Shanghai')::date=$3::date),
 (SELECT COUNT(*) FROM qq_bot_rewards r WHERE r.kind='invite' AND r.user_id=b.user_id
  AND (r.created_at AT TIME ZONE 'Asia/Shanghai')::date=$3::date)
 FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id
 WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID, day(time.Now())).Scan(&joined, &rewarded)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("单人奖励：$%.2f\n今日邀请入群：%d 人\n今日已奖励：%d 人\n每日奖励上限：%d 人\n奖励条件：新用户受邀入群，双方完成绑定。\n同一用户仅奖励一次；每日按北京时间统计。", c.InviteReward, joined, rewarded, c.InviteDailyLimit), nil
}
