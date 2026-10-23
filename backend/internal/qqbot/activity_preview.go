package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/shopspring/decimal"
	"time"
)

type PreviewTask struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Progress string `json:"progress"`
	Eligible bool   `json:"eligible"`
	Status   string `json:"status"`
	Reward   string `json:"reward"`
}
type ActivityPreview struct {
	UserID              int64         `json:"user_id"`
	Bound               bool          `json:"bound"`
	CheckinEligible     bool          `json:"checkin_eligible"`
	CheckinRule         string        `json:"checkin_rule"`
	CheckinDone         bool          `json:"checkin_done"`
	InviteEligible      bool          `json:"invite_eligible"`
	InviteRule          string        `json:"invite_rule"`
	Lottery             LotteryState  `json:"lottery"`
	Tasks               []PreviewTask `json:"tasks"`
	CheckinExpected     float64       `json:"checkin_expected"`
	LotteryExpected     float64       `json:"lottery_expected"`
	LotteryNextExpected float64       `json:"lottery_next_expected"`
}

func expectedPrize(prizes []LotteryPrize) float64 {
	total := decimal.Zero
	for _, p := range prizes {
		total = total.Add(decimal.NewFromFloat(p.Amount).Mul(decimal.NewFromInt(int64(p.ProbabilityBPS))).Div(decimal.NewFromInt(10000)))
	}
	v, _ := total.Float64()
	return v
}
func lotteryExpected(c LotteryConfig) float64 {
	normal := expectedPrize(c.Prizes)
	if !c.GuaranteeEnabled || c.GuaranteeEvery < 1 {
		return normal
	}
	return (normal*float64(c.GuaranteeEvery-1) + expectedPrize(c.GuaranteePrizes)) / float64(c.GuaranteeEvery)
}

// Preview runs in a read-only transaction and never settles rewards or consumes chances.
func (s *Service) PreviewActivities(ctx context.Context, uid int64) (ActivityPreview, error) {
	out := ActivityPreview{UserID: uid, Tasks: []PreviewTask{}}
	if uid <= 0 {
		return out, errors.New("网站用户 ID 不正确")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.loadConfig(ctx, tx)
	if err != nil {
		return out, err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL)`, uid).Scan(&active)
	if err != nil {
		return out, err
	}
	if !active {
		return out, errors.New("用户不存在或已停用")
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_bot_bindings WHERE app_id=$1 AND user_id=$2)`, c.AppID, uid).Scan(&out.Bound)
	if err != nil {
		return out, err
	}
	checkin, err := loadCheckinConfig(ctx, tx, c.CheckinReward)
	if err != nil {
		return out, err
	}
	lottery, err := loadLotteryConfig(ctx, tx)
	if err != nil {
		return out, err
	}
	eligible, err := checkin.eligibility(ctx, tx, uid, false)
	if err != nil {
		return out, err
	}
	out.CheckinRule = checkin.describe()
	out.CheckinEligible = out.Bound && c.Enabled && checkin.Enabled && checkin.satisfied(eligible)
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_bot_rewards WHERE app_id=$1 AND user_id=$2 AND kind='checkin' AND (created_at AT TIME ZONE 'Asia/Shanghai')::date=$3::date)`, c.AppID, uid, day(time.Now())).Scan(&out.CheckinDone)
	if err != nil {
		return out, err
	}
	invite := c.inviteRechargeRequirement()
	invEligibility, err := invite.eligibility(ctx, tx, uid, false)
	if err != nil {
		return out, err
	}
	out.InviteRule = invite.describe()
	out.InviteEligible = out.Bound && c.Enabled && c.InviteReward > 0 && c.InviteDailyLimit > 0 && invite.satisfied(invEligibility)
	out.CheckinExpected = (checkin.MinAmount + checkin.MaxAmount) / 2
	out.LotteryExpected = lotteryExpected(lottery)
	out.Lottery, err = lotteryState(ctx, tx, lottery, uid, false)
	if err != nil {
		return out, err
	}
	out.LotteryNextExpected = expectedPrize(lottery.Prizes)
	if out.Lottery.DrawsUntilGuarantee == 1 {
		out.LotteryNextExpected = expectedPrize(lottery.GuaranteePrizes)
	}
	total, streak, err := checkinProgress(ctx, tx, uid, time.Now())
	if err != nil {
		return out, err
	}
	progress := growthProgress{total: total, streak: streak, recharge: eligible.Total}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(COALESCE(input_tokens,0)::numeric+COALESCE(output_tokens,0)::numeric+COALESCE(cache_creation_tokens,0)::numeric+COALESCE(cache_read_tokens,0)::numeric),0),COALESCE(SUM(actual_cost),0) FROM usage_logs WHERE user_id=$1`, uid).Scan(&progress.usage, &progress.tokens, &progress.spent)
	if err != nil {
		return out, err
	}
	for _, task := range checkin.Growth.tasks() {
		ok, description := progress.task(task)
		snapshot, status, e := activityRewardSnapshot(ctx, tx, fmt.Sprintf("qq-growth:%d:%s", uid, task.ID))
		if e != nil {
			return out, e
		}
		reward := task.reward()
		if status != "" {
			reward = snapshot
		}
		out.Tasks = append(out.Tasks, PreviewTask{ID: task.ID, Name: task.Name, Progress: description, Eligible: out.Bound && c.Enabled && ok && task.Enabled && reward.enabled() && status == "", Status: status, Reward: reward.description()})
	}
	for _, task := range checkin.Growth.PeriodicTasks {
		consumed, cycles := decimal.Zero, int64(0)
		err = tx.QueryRowContext(ctx, `SELECT consumed,cycles FROM qq_bot_periodic_growth_progress WHERE user_id=$1 AND task_id=$2 AND kind=$3`, uid, task.ID, task.Kind).Scan(&consumed, &cycles)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		target := decimal.NewFromFloat(task.Target)
		remaining := decimal.Max(decimal.Zero, progress.value(task.Kind).Sub(consumed))
		reward := task.reward()
		out.Tasks = append(out.Tasks, PreviewTask{ID: "periodic:" + task.ID, Name: task.Name, Progress: periodicProgressText(task.Kind, remaining, target), Eligible: out.Bound && c.Enabled && task.Enabled && reward.enabled() && target.IsPositive() && remaining.GreaterThanOrEqual(target), Status: fmt.Sprintf("已结算 %d 轮", cycles), Reward: reward.description()})
	}
	return out, tx.Commit()
}
