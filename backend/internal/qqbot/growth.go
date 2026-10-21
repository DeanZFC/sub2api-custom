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

func (s *Service) growthTasks(ctx context.Context, c Config, j Job, claim string, actions ...*[]keyboardButton) (string, error) {
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
	if id, ok := rankingClaimID(claim); ok {
		return s.claimRankingReward(ctx, tx, current, j, uid, id, time.Now())
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
	tasks := cfg.Growth.tasks()
	claimIndex := -1
	if claim != "" {
		n, e := strconv.Atoi(claim)
		if e != nil || n < 1 || n > len(tasks) {
			return "任务编号不正确，请发送 /福利 查看当前任务，再使用 /福利 领取 任务编号。", nil
		}
		claimIndex = n - 1
	}
	progress := growthProgress{total: total, streak: streak}
	var needUsage, needRecharge bool
	for i, task := range tasks {
		if !task.Enabled || !task.reward().enabled() || (claimIndex >= 0 && i != claimIndex) {
			continue
		}
		needUsage = needUsage || task.Kind == "usage_count" || task.Kind == "usage_tokens" || task.Kind == "usage_amount"
		needRecharge = needRecharge || task.Kind == "recharge_total"
	}
	if needUsage {
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(COALESCE(input_tokens,0)::numeric+COALESCE(output_tokens,0)::numeric+COALESCE(cache_creation_tokens,0)::numeric+COALESCE(cache_read_tokens,0)::numeric),0),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE user_id=$1 AND created_at <= $2`, uid, at).Scan(&progress.usage, &progress.tokens, &progress.spent); err != nil {
			return "", err
		}
	}
	if needRecharge {
		progress.recharge, err = rechargeSum(ctx, tx, uid, nil, claimIndex >= 0)
		if err != nil {
			return "", err
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
	for i, task := range tasks {
		eligible, status := progress.task(task)
		key := fmt.Sprintf("qq-growth:%d:%s", uid, task.ID)
		claimed, claimErr := activityRewardClaimed(ctx, tx, key)
		if claimErr != nil {
			return "", claimErr
		}
		if i == claimIndex {
			if claimed {
				return "该成长任务奖励已领取。", nil
			}
			if !task.Enabled || !task.reward().enabled() {
				return "该成长任务奖励未开启。", nil
			}
			if !eligible {
				return "尚未完成该任务，请发送 /福利 查看进度。", nil
			}
			reward := task.reward()
			granted, grantErr := grantActivityReward(ctx, tx, current, j, uid, key, "growth", "成长任务："+task.Name, reward, at)
			if grantErr != nil {
				return "", grantErr
			}
			if !granted {
				return "该成长任务奖励已领取。", nil
			}
			if err = tx.Commit(); err != nil {
				return "", err
			}
			if reward.Type == "balance" {
				s.invalidate(ctx, uid)
			}
			return fmt.Sprintf("✅ **成长奖励已领取**\n\n%s\n奖励：%s\n%s", replyPlainLabel(task.Name), reward.description(), reward.deliveryNote()), nil
		}
		switch {
		case claimed:
			status = "已领取"
		case !task.Enabled || !task.reward().enabled():
			status = "未开启"
		case eligible:
			status = fmt.Sprintf("可领取 · /福利 领取 %d", i+1)
			if len(actions) > 0 && actions[0] != nil {
				*actions[0] = append(*actions[0], helpCommandButton(fmt.Sprintf("领取任务%d", i+1), "welfare:claim:"+task.ID, fmt.Sprintf("/福利 领取 %d", i+1)))
			}
		}
		text += fmt.Sprintf("\n\n**%d. %s**\n奖励：%s · %s", i+1, replyPlainLabel(task.Name), task.reward().description(), status)
	}
	periodic, err := rankingGrowthTasks(ctx, tx, current.AppID, uid, actions...)
	if err != nil {
		return "", err
	}
	return text + "\n\n以上为一次性任务，每项每个网站账号限领一次；历史完成记录也可参与。领取后余额和抽奖次数到账，实物与自定义奖品由管理员发放。\n\n" + periodic, nil
}
