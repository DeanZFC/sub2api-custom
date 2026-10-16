package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
 WHERE m.app_id=$1 AND m.inviter_id=$2 AND m.inviter_id<>''`, []any{c.AppID, j.MemberID}, false, func(r ActivityRecord) {
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
	return fmt.Sprintf("待进群：%d 人\n待绑定：%d 人\n已奖励：%d 人\n已退群：%d 人",
		counts["pending_join"],
		counts["both_unbound"]+counts["inviter_unbound"]+counts["invitee_unbound"],
		counts["rewarded"], counts["left_group"]), nil
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
	case "recharge_required":
		return 4
	case "invitee_unbound", "inviter_unbound", "both_unbound":
		return 5
	case "account_unavailable":
		return 6
	case "self_invitation":
		return 7
	case "disabled", "group_disabled":
		return 8
	case "pending_join":
		return 9
	case "invalid_invitation":
		return 10
	default: // left_group
		return 11
	}
}

func (s *Service) inviteInfo(ctx context.Context, c Config, j Job) (string, error) {
	if c.InviteReward <= 0 || c.InviteDailyLimit <= 0 {
		return "邀请活动未开启。", nil
	}
	requirement := c.inviteRechargeRequirement()
	conditions := "参与条件：收到官方邀请申请，7 天内成功入群且仍在群内；双方绑定有效网站账号；同一被邀请账号仅奖励一次。"
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id
 WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Sprintf("单人奖励：$%.2f\n每日奖励上限：%d 人\n%s\n%s\n当前状态：请先使用 /绑定 绑定网站账号。",
			c.InviteReward, c.InviteDailyLimit, conditions, requirement.invite()), nil
	}
	if err != nil {
		return "", err
	}
	var joined, rewarded int
	// Count joins separately from payouts: an invitee can bind on a later day.
	// Match the payout limit's account-wide scope, including rewards from other groups.
	err = s.db.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(DISTINCT m.member_id) FROM qq_bot_members m
  WHERE m.app_id=$1 AND m.inviter_id=$2 AND m.member_id<>$2
  AND m.invited_at<=m.joined_at AND m.invited_at>m.joined_at-INTERVAL '7 days'
  AND (m.joined_at AT TIME ZONE 'Asia/Shanghai')::date=$3::date),
	 (SELECT COUNT(*) FROM qq_bot_rewards r WHERE r.kind='invite' AND r.user_id=$4
  AND (r.created_at AT TIME ZONE 'Asia/Shanghai')::date=$3::date)`, c.AppID, j.MemberID, day(time.Now()), userID).Scan(&joined, &rewarded)
	if err != nil {
		return "", err
	}
	info := fmt.Sprintf("单人奖励：$%.2f\n今日邀请入群：%d 人\n今日已奖励：%d 人\n每日奖励上限：%d 人\n%s\n%s\n每日按北京时间统计。",
		c.InviteReward, joined, rewarded, c.InviteDailyLimit, conditions, requirement.invite())
	if requirement.enabled() {
		eligibility, eligibilityErr := requirement.eligibility(ctx, s.db, userID, false)
		if eligibilityErr != nil {
			return "", eligibilityErr
		}
		status := "已满足"
		if !requirement.satisfied(eligibility) {
			status = "未满足"
		}
		info += fmt.Sprintf("\n充值资格：%s。当前累计有效充值：$%s；近 %d 天有效充值：$%s。", status, eligibility.Total.StringFixed(2), requirement.windowDays(), eligibility.Recent.StringFixed(2))
	}
	return info, nil
}
