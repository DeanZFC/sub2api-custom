package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Zero disables each reward independently. Entitlements are keyed by website
// account, so rebinding, multiple groups and process restarts cannot reset them.
type GrowthConfig struct {
	Tasks             []GrowthTask `json:"tasks"`
	BindingChances    int          `json:"binding_chances"`
	FirstUsageChances int          `json:"first_usage_chances"`
	Checkin30Chances  int          `json:"checkin_30_chances"`
}

func checkinProgress(ctx context.Context, db queryRower, uid int64, at time.Time) (total, streak int, err error) {
	err = db.QueryRowContext(ctx, `WITH dates AS (
 SELECT DISTINCT (created_at AT TIME ZONE 'Asia/Shanghai')::date AS d FROM qq_bot_rewards WHERE user_id=$1 AND kind='checkin' AND created_at <= $2
), ordered AS (SELECT d,ROW_NUMBER() OVER(ORDER BY d DESC)::int AS n,MAX(d) OVER() AS last_day FROM dates)
SELECT COUNT(*),COUNT(*) FILTER(WHERE last_day >= $3::date-1 AND d=last_day-(n-1)) FROM ordered`, uid, at, day(at)).Scan(&total, &streak)
	return
}

func grantStreak(ctx context.Context, tx *sql.Tx, c Config, j Job, uid int64, at time.Time, reward GrowthReward, interval int) (int, bool, error) {
	_, streak, err := checkinProgress(ctx, tx, uid, at)
	if err != nil || !reward.enabled() || streak == 0 || interval < 1 || streak%interval != 0 {
		return streak, false, err
	}
	granted, err := grantActivityReward(ctx, tx, c, j, uid, fmt.Sprintf("qq-streak:%d:%s", uid, day(at)), "streak", fmt.Sprintf("连续签到 %d 天奖励", interval), reward, at)
	return streak, granted, err
}

// Queries and the background worker share the same transaction and account key.
// No command or task number is required to receive an eligible reward.
func (s *Service) growthTasks(ctx context.Context, c Config, j Job) (string, error) {
	text, _, err := s.processGrowthTasks(ctx, c, j, time.Now(), true)
	return text, err
}

func (s *Service) processGrowthTasks(ctx context.Context, c Config, j Job, at time.Time, render bool) (string, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := s.lockConfig(ctx, tx)
	if err != nil {
		return "", 0, err
	}
	if !current.Enabled || current.AppID != c.AppID || !current.allows(j.GroupID) {
		return "当前群未启用机器人。", 0, nil
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,272))`, c.AppID); err != nil {
		return "", 0, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, checkinConfigLock); err != nil {
		return "", 0, err
	}
	uid, err := boundUser(ctx, tx, c.AppID, j.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return bindFirst, 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	var active int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, uid).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return bindFirst, 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	cfg, err := loadCheckinConfig(ctx, tx, current.CheckinReward)
	if err != nil {
		return "", 0, err
	}
	text, grants, balanceChanged, err := settleGrowthForUser(ctx, tx, current, j, uid, cfg, at)
	if err != nil {
		return "", 0, err
	}
	if render {
		periodic, e := rankingGrowthTasks(ctx, tx, current.AppID, uid)
		if e != nil {
			return "", 0, e
		}
		text += "\n\n" + periodic
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	if balanceChanged {
		s.invalidate(ctx, uid)
	}
	return text, grants, nil
}

// Caller locks the active user row and check-in configuration before granting.
func settleGrowthForUser(ctx context.Context, tx *sql.Tx, c Config, j Job, uid int64, cfg CheckinConfig, at time.Time) (string, int, bool, error) {
	total, streak, err := checkinProgress(ctx, tx, uid, at)
	if err != nil {
		return "", 0, false, err
	}
	tasks := cfg.Growth.tasks()
	claimed := make([]bool, len(tasks))
	rewards := make([]GrowthReward, len(tasks))
	statuses := make([]string, len(tasks))
	progress := growthProgress{total: total, streak: streak}
	var needUsage, needRecharge bool
	for i, task := range tasks {
		key := fmt.Sprintf("qq-growth:%d:%s", uid, task.ID)
		rewards[i] = task.reward()
		snapshot, status, e := activityRewardSnapshot(ctx, tx, key)
		err = e
		claimed[i] = status != ""
		if claimed[i] {
			rewards[i], statuses[i] = snapshot, status
		}
		if err != nil {
			return "", 0, false, err
		}
		if claimed[i] || !task.Enabled || !task.reward().enabled() {
			continue
		}
		needUsage = needUsage || task.Kind == "usage_count" || task.Kind == "usage_tokens" || task.Kind == "usage_amount"
		needRecharge = needRecharge || task.Kind == "recharge_total"
	}
	if needUsage {
		err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(COALESCE(input_tokens,0)::numeric+COALESCE(output_tokens,0)::numeric+COALESCE(cache_creation_tokens,0)::numeric+COALESCE(cache_read_tokens,0)::numeric),0),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE user_id=$1 AND created_at <= $2`, uid, at).Scan(&progress.usage, &progress.tokens, &progress.spent)
		if err != nil {
			return "", 0, false, err
		}
	}
	if needRecharge {
		progress.recharge, err = rechargeSum(ctx, tx, uid, nil, true)
		if err != nil {
			return "", 0, false, err
		}
	}
	text := fmt.Sprintf("🌱 **成长任务**\n\n当前连续签到：%d 天\n累计签到：%d 天", streak, total)
	if reward := cfg.streakReward(); reward.enabled() {
		text += fmt.Sprintf("\n每连续签到满 %d 天自动获得：%s。", cfg.streakDays(), reward.description())
	}
	text += "\n\n### 一次性成长任务"
	if len(tasks) == 0 {
		text += "\n\n当前暂无一次性成长任务。"
	}
	grants := 0
	balanceChanged := false
	for i, task := range tasks {
		eligible, status := progress.task(task)
		switch {
		case claimed[i]:
			status = "已发放"
			if statuses[i] == "pending" {
				status = "已登记，待管理员发放"
			}
		case !task.Enabled || !task.reward().enabled():
			status = "未开启"
		case eligible:
			reward := task.reward()
			granted, e := grantActivityReward(ctx, tx, c, j, uid, fmt.Sprintf("qq-growth:%d:%s", uid, task.ID), "growth", "成长任务："+task.Name, reward, at)
			if e != nil {
				return "", 0, false, e
			}
			status = "已发放"
			if granted {
				grants++
				balanceChanged = balanceChanged || reward.Type == "balance"
				status = "已自动发放"
			}
			if reward.Type == "physical" || reward.Type == "other" {
				status = "已登记，待管理员发放"
			}
		}
		text += fmt.Sprintf("\n\n**%d. %s**\n奖励：%s · %s", i+1, replyPlainLabel(task.Name), rewards[i].description(), status)
	}
	return text + "\n\n达标奖励自动发放，无需领取。每项任务每个网站账号仅奖励一次；历史完成记录也可参与。余额与抽奖次数直接到账，实物及自定义奖品由管理员发放。", grants, balanceChanged, nil
}
