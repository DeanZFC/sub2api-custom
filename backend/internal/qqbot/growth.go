package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Zero disables each reward independently. Entitlements are keyed by website
// account, so rebinding, multiple groups and process restarts cannot reset them.
type GrowthConfig struct {
	BindingChances    int `json:"binding_chances"`
	FirstUsageChances int `json:"first_usage_chances"`
	Checkin30Chances  int `json:"checkin_30_chances"`
}

func checkinProgress(ctx context.Context, db queryRower, uid int64, at time.Time) (total, streak int, err error) {
	err = db.QueryRowContext(ctx, `WITH dates AS (
 SELECT DISTINCT (created_at AT TIME ZONE 'Asia/Shanghai')::date AS d FROM qq_bot_rewards WHERE user_id=$1 AND kind='checkin' AND created_at <= $2
), ordered AS (SELECT d,ROW_NUMBER() OVER(ORDER BY d DESC)::int AS n,MAX(d) OVER() AS last_day FROM dates)
SELECT COUNT(*),COUNT(*) FILTER(WHERE last_day >= $3::date-1 AND d=last_day-(n-1)) FROM ordered`, uid, at, day(at)).Scan(&total, &streak)
	return
}

func grantStreak(ctx context.Context, tx *sql.Tx, uid int64, at time.Time, chances int) (int, int, error) {
	_, streak, err := checkinProgress(ctx, tx, uid, at)
	if err != nil || chances == 0 || streak == 0 || streak%7 != 0 {
		return streak, 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES($1,$2,$3) ON CONFLICT(source_key) DO NOTHING`, fmt.Sprintf("qq-streak:%d:%s", uid, day(at)), uid, chances)
	if err != nil {
		return 0, 0, err
	}
	n, err := result.RowsAffected()
	if n == 0 {
		chances = 0
	}
	return streak, chances, err
}

func (s *Service) growthTasks(ctx context.Context, c Config, j Job, claim string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := s.lockConfig(ctx, tx)
	if err != nil {
		return "", err
	}
	if !current.Enabled || current.AppID != c.AppID || !current.allows(j.GroupID) {
		return "当前群未启用机器人。", nil
	}
	// Match reward and unbinding lock order.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,272))`, c.AppID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, checkinConfigLock); err != nil {
		return "", err
	}
	uid, err := boundUser(ctx, tx, c.AppID, j.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return bindFirst, nil
	}
	if err != nil {
		return "", err
	}
	// Keep the account active through settlement and serialize grants with account changes.
	var active int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, uid).Scan(&active); err != nil {
		return "", err
	}
	cfg, err := loadCheckinConfig(ctx, tx, current.CheckinReward)
	if err != nil {
		return "", err
	}
	at := time.Now()
	total, streak, err := checkinProgress(ctx, tx, uid, at)
	if err != nil {
		return "", err
	}
	var used bool
	if cfg.Growth.FirstUsageChances > 0 {
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM usage_logs WHERE user_id=$1 AND created_at <= $2)`, uid, at).Scan(&used); err != nil {
			return "", err
		}
	}
	tasks := []struct {
		key, name string
		chances   int
		eligible  bool
		progress  string
	}{
		{"binding", "完成账号绑定", cfg.Growth.BindingChances, true, "已完成"},
		{"first_usage", "首次成功 API 调用", cfg.Growth.FirstUsageChances, used, "待完成"},
		{"checkin_30", "累计签到 30 天", cfg.Growth.Checkin30Chances, total >= 30, fmt.Sprintf("%d/30 天", total)},
	}
	if used {
		tasks[1].progress = "已完成"
	}
	claimIndex := -1
	if claim != "" {
		n, e := strconv.Atoi(claim)
		if e != nil || n < 1 || n > len(tasks) {
			return "用法：/领取任务 1、/领取任务 2 或 /领取任务 3；/成长任务 查看进度。", nil
		}
		claimIndex = n - 1
	}
	text := fmt.Sprintf("🌱 **成长任务**\n\n当前连续签到：%d 天\n累计签到：%d 天", streak, total)
	if cfg.StreakChances > 0 {
		text += fmt.Sprintf("\n每连续签到满 7 天自动赠送 %d 次抽奖机会。", cfg.StreakChances)
	}
	for i, task := range tasks {
		key := fmt.Sprintf("qq-growth:%d:%s", uid, task.key)
		var claimed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_bot_lottery_chance_grants WHERE source_key=$1)`, key).Scan(&claimed); err != nil {
			return "", err
		}
		if i == claimIndex {
			if claimed {
				return "该成长任务奖励已领取。", nil
			}
			if task.chances == 0 {
				return "该成长任务奖励未开启。", nil
			}
			if !task.eligible {
				return "尚未完成该任务，请发送 /成长任务 查看进度。", nil
			}
			res, e := tx.ExecContext(ctx, `INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES($1,$2,$3) ON CONFLICT(source_key) DO NOTHING`, key, uid, task.chances)
			if e != nil {
				return "", e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return "", e
			}
			if n == 0 {
				return "该成长任务奖励已领取。", nil
			}
			if err = tx.Commit(); err != nil {
				return "", err
			}
			return fmt.Sprintf("✅ **成长奖励已领取**\n\n%s\n赠送抽奖次数：%d 次\n发送 /抽奖次数 查看余额。", task.name, task.chances), nil
		}
		status := task.progress
		switch {
		case claimed:
			status = "已领取"
		case task.chances == 0:
			status = "未开启"
		case task.eligible:
			status = fmt.Sprintf("可领取 · /领取任务 %d", i+1)
		}
		text += fmt.Sprintf("\n\n**%d. %s**\n奖励：%d 次抽奖 · %s", i+1, task.name, task.chances, status)
	}
	return text + "\n\n每项任务每个网站账号限领一次；历史完成记录也可参与。抽奖须由管理员开启。", nil
}
