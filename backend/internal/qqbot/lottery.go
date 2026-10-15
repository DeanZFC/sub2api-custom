package qqbot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type LotteryState struct {
	UserID        int64   `json:"user_id"`
	RechargeTotal float64 `json:"recharge_total"`
	Earned        int64   `json:"earned"`
	Used          int64   `json:"used"`
	Remaining     int64   `json:"remaining"`
	NextRecharge  float64 `json:"next_recharge"`
}
type LotteryDraw struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Email     string    `json:"email"`
	MemberID  string    `json:"member_id"`
	PrizeName string    `json:"prize_name"`
	Amount    float64   `json:"amount"`
	Remaining int64     `json:"remaining"`
	CreatedAt time.Time `json:"created_at"`
}
type LotteryDrawsPage struct {
	Items    []LotteryDraw `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// Count USD principal from completed online balance orders and redeemed positive
// balance codes, including history. Payment-generated codes only count as orders.
// Bonus credits, subscriptions and administrative credits never create chances.
// Refund-in-progress orders are suspended; partial refunds remove principal pro rata.
func lotteryState(ctx context.Context, db queryRower, c LotteryConfig, uid int64, lockSources bool) (LotteryState, error) {
	out := LotteryState{UserID: uid}
	orderQuery := `SELECT amount,bonus_amount,refund_amount,status FROM payment_orders
        WHERE user_id=$1 AND order_type='balance' AND paid_at IS NOT NULL AND completed_at IS NOT NULL ORDER BY id`
	if lockSources {
		orderQuery += ` FOR SHARE`
	}
	var totalText string
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(CASE
        WHEN status IN ('COMPLETED','REFUND_FAILED') THEN GREATEST(amount-bonus_amount,0)
        WHEN status='PARTIALLY_REFUNDED' AND amount>0 THEN ROUND(GREATEST(amount-bonus_amount,0)*GREATEST(amount-refund_amount,0)/amount,8)
        ELSE 0 END),0)::text FROM (`+orderQuery+`) orders`, uid).Scan(&totalText)
	if err != nil {
		return out, err
	}
	total, err := decimal.NewFromString(totalText)
	if err != nil {
		return out, err
	}
	// Online fulfillment also redeems a balance code, sometimes before the order
	// is completed. Exclude codes belonging to any order so bonuses, in-flight
	// fulfillment and refunds cannot re-enter eligibility through the code ledger.
	// Match globally: inconsistent legacy ownership must not bypass this exclusion.
	redeemQuery := `SELECT r.value FROM redeem_codes r
        WHERE r.used_by=$1 AND r.type='balance' AND r.status='used' AND r.used_at IS NOT NULL AND r.value>0
        AND NOT EXISTS(SELECT 1 FROM payment_orders p WHERE p.recharge_code=r.code) ORDER BY r.id`
	if lockSources {
		redeemQuery += ` FOR SHARE OF r`
	}
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(value),0)::text FROM (`+redeemQuery+`) codes`, uid).Scan(&totalText); err != nil {
		return out, err
	}
	redeemed, err := decimal.NewFromString(totalText)
	if err != nil {
		return out, err
	}
	total = total.Add(redeemed)
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_lottery_draws WHERE user_id=$1`, uid).Scan(&out.Used); err != nil {
		return out, err
	}
	step := decimal.NewFromFloat(c.RechargeStep)
	if step.LessThanOrEqual(decimal.Zero) || c.ChancesPerStep < 1 {
		return out, errors.New("抽奖规则不正确")
	}
	quotient, _ := total.QuoRem(step, 0)
	earned := quotient.Mul(decimal.NewFromInt(c.ChancesPerStep))
	if earned.GreaterThan(decimal.NewFromInt(1e12)) {
		return out, errors.New("抽奖次数超出支持范围，请联系管理员")
	}
	out.RechargeTotal = total.InexactFloat64()
	out.Earned = earned.IntPart()
	out.Remaining = max(out.Earned-out.Used, 0)
	// When refunds/rule changes put usage ahead of entitlement, new recharge
	// must cover that deficit before another draw becomes available.
	nextTier := max(quotient.IntPart()+1, out.Used/c.ChancesPerStep+1)
	out.NextRecharge = decimal.NewFromInt(nextTier).Mul(step).Sub(total).InexactFloat64()
	return out, nil
}
func (s *Service) LotteryState(ctx context.Context, uid int64) (LotteryState, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return LotteryState{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL)`, uid).Scan(&exists); err != nil {
		return LotteryState{}, err
	}
	if !exists {
		return LotteryState{}, errors.New("网站账号不存在")
	}
	c, err := loadLotteryConfig(ctx, tx)
	if err != nil {
		return LotteryState{}, err
	}
	out, err := lotteryState(ctx, tx, c, uid, false)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) ListLotteryDraws(ctx context.Context, search string, page, pageSize int) (LotteryDrawsPage, error) {
	out := LotteryDrawsPage{Items: []LotteryDraw{}, Page: page, PageSize: pageSize}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	const filter = ` FROM qq_bot_lottery_draws d JOIN users u ON u.id=d.user_id WHERE ($1='' OR d.user_id::text=$1 OR strpos(lower(u.email),lower($1))>0 OR strpos(lower(d.member_id),lower($1))>0)`
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*)`+filter, search).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.user_id,u.email,d.member_id,d.prize_name,d.amount,d.remaining,d.created_at`+filter+` ORDER BY d.id DESC LIMIT $2 OFFSET $3`, search, pageSize, (page-1)*pageSize)
	if err != nil {
		return out, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var d LotteryDraw
		if err = rows.Scan(&d.ID, &d.UserID, &d.Email, &d.MemberID, &d.PrizeName, &d.Amount, &d.Remaining, &d.CreatedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, d)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Service) lotteryInfo(ctx context.Context, c Config, j Job, showRules bool) (string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := loadLotteryConfig(ctx, tx)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "抽奖活动未开启。", nil
	}
	var uid int64
	err = tx.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	if err != nil {
		return "", err
	}
	if showRules {
		var b strings.Builder
		fmt.Fprintf(&b, "活动名称：%s\n抽奖规则：每累计有效充值 $%.2f 赠送 %d 次\n每次消耗：1 次", cfg.Title, cfg.RechargeStep, cfg.ChancesPerStep)
		for _, p := range cfg.Prizes {
			fmt.Fprintf(&b, "\n%s：$%.2f，概率 %.2f%%", p.Name, p.Amount, float64(p.ProbabilityBPS)/100)
		}
		return b.String(), tx.Commit()
	}
	state, err := lotteryState(ctx, tx, cfg, uid, false)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("剩余抽奖次数：%d 次\n下次赠送还需充值：$%.2f", state.Remaining, state.NextRecharge), tx.Commit()
}
func (s *Service) drawLottery(ctx context.Context, c Config, j Job) (string, error) {
	if j.ID == "" {
		return "", errors.New("缺少抽奖请求标识")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	// Match unbinding's reward lock. Protect config changes while settling a draw.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(71427101)`); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,272))`, c.AppID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, lotteryConfigLock); err != nil {
		return "", err
	}
	current, err := s.loadConfig(ctx, tx)
	if err != nil {
		return "", err
	}
	if !current.Enabled || current.AppID != c.AppID || !current.allows(j.GroupID) {
		return "当前群未启用机器人。", nil
	}
	cfg, err := loadLotteryConfig(ctx, tx)
	if err != nil {
		return "", err
	}
	var uid int64
	err = tx.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	if err != nil {
		return "", err
	}
	// User-level serialization also protects lifetime usage across AppID changes.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,274))`, strconv.FormatInt(uid, 10)); err != nil {
		return "", err
	}
	requestKey := c.AppID + ":" + j.ID
	if j.MessageID != "" {
		requestKey = c.AppID + ":message:" + j.MessageID
	}
	var result string
	err = tx.QueryRowContext(ctx, `SELECT result_text FROM qq_bot_lottery_draws WHERE request_key=$1 AND user_id=$2 AND member_id=$3`, requestKey, uid, j.MemberID).Scan(&result)
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_jobs SET kind='lottery',result_text=$2 WHERE id=$1`, j.ID, result); err != nil {
			return "", err
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if !cfg.Enabled {
		return "抽奖活动未开启。", nil
	}
	if err = validateLottery(cfg); err != nil {
		return "", err
	}
	state, err := lotteryState(ctx, tx, cfg, uid, true)
	if err != nil {
		return "", err
	}
	if state.Remaining < 1 {
		return fmt.Sprintf("抽奖次数不足。\n下次赠送还需充值：$%.2f", state.NextRecharge), nil
	}
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	prize := lotteryPrizeAt(cfg, n.Int64())
	result = fmt.Sprintf("抽奖结果：%s\n奖励额度：$%.2f\n剩余抽奖次数：%d 次", prize.Name, prize.Amount, state.Remaining-1)
	snapshot, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	var drawID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO qq_bot_lottery_draws(request_key,app_id,member_id,group_id,user_id,prize_name,amount,config_snapshot,recharge_total,earned,remaining,result_text) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`, requestKey, c.AppID, j.MemberID, j.GroupID, uid, prize.Name, prize.Amount, string(snapshot), state.RechargeTotal, state.Earned, state.Remaining-1, result).Scan(&drawID)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_rewards(reward_key,app_id,group_id,member_id,user_id,kind,amount) VALUES($1,$2,$3,$4,$5,'lottery',$6)`, fmt.Sprintf("lottery:%d", drawID), c.AppID, j.GroupID, j.MemberID, uid, prize.Amount); err != nil {
		return "", err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$1,updated_at=NOW() WHERE id=$2 AND status='active' AND deleted_at IS NULL`, prize.Amount, uid)
	if err != nil {
		return "", err
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return "", err
	}
	if count != 1 {
		return "", errors.New("账户不可用")
	}
	if prize.Amount > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes) VALUES($1,'admin_balance',$2,'used',$3,NOW(),$4)`, "QQ"+randomID(), prize.Amount, uid, fmt.Sprintf("QQ lottery reward #%d: %s", drawID, prize.Name)); err != nil {
			return "", err
		}
	}
	// Persist the result before delivery for auditing and message deduplication.
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_jobs SET kind='lottery',result_text=$2 WHERE id=$1`, j.ID, result); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.invalidate(ctx, uid)
	return result, nil
}
