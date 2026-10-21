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
	var lottery, invite, checkin, redemption, growth, streak, ranking, total float64
	err = s.db.QueryRowContext(ctx, `SELECT
 COALESCE(SUM(amount) FILTER (WHERE kind='lottery'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='invite'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='checkin'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='redemption'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='growth'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='streak'),0),
 COALESCE(SUM(amount) FILTER (WHERE kind='ranking'),0),
 COALESCE(SUM(amount),0)
 FROM qq_bot_rewards WHERE user_id=$1`, uid).Scan(&lottery, &invite, &checkin, &redemption, &growth, &streak, &ranking, &total)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("🎁 **累计奖励**\n抽奖 %.2f · 邀请 %.2f · 签到 %.2f\n成长 %.2f · 连签 %.2f · 排行 %.2f\n合计 USD %.2f", lottery, invite, checkin, growth, streak, ranking, total)
	if redemption > 0 {
		text += fmt.Sprintf("（含历史奖励 %.2f）", redemption)
	}
	return text, nil
}
