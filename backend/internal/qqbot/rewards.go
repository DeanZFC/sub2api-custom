package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Service) myRewards(ctx context.Context, c Config, j Job) (string, error) {
	var uid int64
	err := s.db.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	if err != nil {
		return "", err
	}
	// Retry eligible invitations before reading totals; reward writes remain idempotent.
	if err = s.settleInvites(ctx, c, j.MemberID); err != nil {
		return "", err
	}
	var lottery, invite, checkin, redemption, total float64
	err = s.db.QueryRowContext(ctx, `SELECT
 COALESCE(SUM(amount) FILTER (WHERE kind='lottery'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='invite'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='checkin'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='redemption'),0),
 COALESCE(SUM(amount),0)
 FROM qq_bot_rewards WHERE user_id=$1`, uid).Scan(&lottery, &invite, &checkin, &redemption, &total)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("🎁 **累计奖励**\n\n- 抽奖奖励：USD %.2f\n- 邀请奖励：USD %.2f\n- 签到奖励：USD %.2f\n- 兑换奖励：USD %.2f\n\n**合计：USD %.2f**", lottery, invite, checkin, redemption, total), nil
}
