package qqbot

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

const rewardCodeInvalid = "兑换码无效或不可用，请核对后重试。"
const rewardCodeLocked = "尝试次数过多，请 30 分钟后再试。"

var ErrRewardCodeRequest = errors.New("奖励兑换参数不正确或记录状态已变化，请刷新后重试")

type RewardCodeCreate struct {
	BatchName         string     `json:"batch_name"`
	RewardType        string     `json:"reward_type"`
	Count             int        `json:"count"`
	Amount            float64    `json:"amount"`
	Quantity          int        `json:"quantity"`
	PrizeName         string     `json:"prize_name"`
	ClaimInstructions string     `json:"claim_instructions"`
	TargetUserID      *int64     `json:"target_user_id"`
	ExpiresAt         *time.Time `json:"expires_at"`
}

func (r RewardCodeCreate) Validate() error {
	if strings.TrimSpace(r.BatchName) == "" || len([]rune(r.BatchName)) > 80 || r.Count < 1 || r.Count > 200 || len([]rune(r.PrizeName)) > 100 || len([]rune(r.ClaimInstructions)) > 500 || (r.TargetUserID != nil && *r.TargetUserID < 1) || (r.ExpiresAt != nil && !r.ExpiresAt.After(time.Now())) {
		return ErrRewardCodeRequest
	}
	switch r.RewardType {
	case "balance":
		if !validRewardAmount(r.Amount) || r.Amount < 0.01 || math.Abs(r.Amount*100-math.Round(r.Amount*100)) > .000001 || r.Quantity != 0 {
			return ErrRewardCodeRequest
		}
	case "chances", "physical", "other":
		if r.Amount != 0 || r.Quantity < 1 || r.Quantity > 1000 {
			return ErrRewardCodeRequest
		}
		if r.RewardType != "chances" && strings.TrimSpace(r.PrizeName) == "" {
			return ErrRewardCodeRequest
		}
	default:
		return ErrRewardCodeRequest
	}
	return nil
}

type GeneratedRewardCode struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
}
type RewardCode struct {
	SourceKind        string     `json:"source_kind"`
	ID                int64      `json:"id"`
	AppID             string     `json:"app_id"`
	CodeHint          string     `json:"code_hint"`
	BatchName         string     `json:"batch_name"`
	RewardType        string     `json:"reward_type"`
	Amount            float64    `json:"amount"`
	Quantity          int        `json:"quantity"`
	PrizeName         string     `json:"prize_name"`
	ClaimInstructions string     `json:"claim_instructions"`
	TargetUserID      *int64     `json:"target_user_id"`
	ExpiresAt         *time.Time `json:"expires_at"`
	Status            string     `json:"status"`
	UserID            *int64     `json:"user_id"`
	Email             string     `json:"email"`
	MemberID          string     `json:"member_id"`
	RedeemedAt        *time.Time `json:"redeemed_at"`
	FulfilledAt       *time.Time `json:"fulfilled_at"`
	FulfillmentNote   string     `json:"fulfillment_note"`
	CreatedAt         time.Time  `json:"created_at"`
}
type RewardCodesPage struct {
	Items    []RewardCode `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

// 160 random bits, with no sequential, timestamp or user-selected component.
func generateRewardCode() (string, error) {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	raw := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	parts := make([]string, 0, 8)
	for i := 0; i < len(raw); i += 4 {
		parts = append(parts, raw[i:i+4])
	}
	return strings.Join(parts, "-"), nil
}
func rewardCodeHash(raw string) string {
	if len(raw) > 64 {
		return "invalid"
	}
	code := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(raw), "-", ""))
	if len(code) != 32 {
		return "invalid"
	}
	for _, ch := range code {
		if !(ch >= 'A' && ch <= 'Z') && !(ch >= '2' && ch <= '7') {
			return "invalid"
		}
	}
	return codeHash("qq-reward:" + code)
}

// Build a minimal event before persistence, dropping all alternate copies of
// content (elements, attachments, quoted payloads and unknown JSON properties).
func redactRewardCommand(e Event) (Event, string) {
	cmd, arg := command(e.Content)
	cleanCommand := "/奖励兑换"
	if cmd == "福利" {
		_, arg = command("/" + arg)
		cleanCommand = "/福利 兑换"
	}
	hash := ""
	if arg != "" && cmd == "福利" {
		hash = rewardCodeHash(arg)
	}
	clean := Event{ID: e.ID, GroupID: e.GroupID, Author: e.Author, Mentions: e.Mentions,
		Content: mentionPrefix.FindString(strings.TrimSpace(e.Content)) + cleanCommand}
	return clean, hash
}

// Sanitize retired redemption messages too, without executing them.
func containsRewardCode(content string) bool {
	cmd, arg := command(content)
	action, _ := command("/" + arg)
	return cmd == "奖励兑换" || (cmd == "福利" && action == "兑换")
}

func (s *Service) CreateRewardCodes(ctx context.Context, r RewardCodeCreate) ([]GeneratedRewardCode, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	c, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	if c.AppID == "" {
		return nil, ErrRewardCodeRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if r.TargetUserID != nil {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL)`, *r.TargetUserID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrRewardCodeRequest
		}
	}
	out := make([]GeneratedRewardCode, 0, r.Count)
	for range r.Count {
		raw, e := generateRewardCode()
		if e != nil {
			return nil, e
		}
		compact := strings.ReplaceAll(raw, "-", "")
		item := GeneratedRewardCode{Code: raw}
		err = tx.QueryRowContext(ctx, `INSERT INTO qq_bot_reward_codes(app_id,code_hash,code_hint,batch_name,reward_type,amount,quantity,prize_name,claim_instructions,target_user_id,expires_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, c.AppID, rewardCodeHash(raw), compact[len(compact)-6:], strings.TrimSpace(r.BatchName), r.RewardType, r.Amount, r.Quantity, strings.TrimSpace(r.PrizeName), strings.TrimSpace(r.ClaimInstructions), r.TargetUserID, r.ExpiresAt).Scan(&item.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, tx.Commit()
}

func (s *Service) ListRewardCodes(ctx context.Context, search, status string, page int) (RewardCodesPage, error) {
	out := RewardCodesPage{Items: []RewardCode{}, Page: page, PageSize: 20}
	if page < 1 || page > 1000000 || len(search) > 254 {
		return out, ErrRewardCodeRequest
	}
	switch status {
	case "", "unused", "disabled", "pending", "fulfilled", "expired":
	default:
		return out, ErrRewardCodeRequest
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	const from = ` FROM qq_bot_reward_codes r LEFT JOIN users u ON u.id=r.user_id WHERE
 ($1='' OR strpos(lower(r.batch_name),lower($1))>0 OR strpos(lower(r.code_hint),lower($1))>0 OR r.id::text=$1 OR r.user_id::text=$1 OR r.target_user_id::text=$1 OR strpos(lower(COALESCE(u.email,'')),lower($1))>0)
 AND ($2='' OR CASE WHEN r.status='unused' AND r.expires_at<=NOW() THEN 'expired' ELSE r.status END=$2)`
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*)`+from, search, status).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.id,r.app_id,r.code_hint,r.batch_name,r.reward_type,r.amount,r.quantity,r.prize_name,r.claim_instructions,r.target_user_id,r.expires_at,
 CASE WHEN r.status='unused' AND r.expires_at<=NOW() THEN 'expired' ELSE r.status END,r.user_id,COALESCE(u.email,''),r.member_id,r.redeemed_at,r.fulfilled_at,r.fulfillment_note,r.created_at,r.source_kind`+from+` ORDER BY r.id DESC LIMIT 20 OFFSET $3`, search, status, (page-1)*20)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var r RewardCode
		if err = rows.Scan(&r.ID, &r.AppID, &r.CodeHint, &r.BatchName, &r.RewardType, &r.Amount, &r.Quantity, &r.PrizeName, &r.ClaimInstructions, &r.TargetUserID, &r.ExpiresAt, &r.Status, &r.UserID, &r.Email, &r.MemberID, &r.RedeemedAt, &r.FulfilledAt, &r.FulfillmentNote, &r.CreatedAt, &r.SourceKind); err != nil {
			return out, err
		}
		out.Items = append(out.Items, r)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Service) UpdateRewardCode(ctx context.Context, id int64, action, note string) error {
	if id < 1 || len([]rune(note)) > 500 {
		return ErrRewardCodeRequest
	}
	var query string
	switch action {
	case "disable":
		query = `UPDATE qq_bot_reward_codes SET status='disabled' WHERE id=$1 AND status='unused'`
	case "fulfill":
		query = `UPDATE qq_bot_reward_codes SET status='fulfilled',fulfilled_at=NOW(),fulfillment_note=$2 WHERE id=$1 AND status='pending' AND reward_type IN ('physical','other')`
	default:
		return ErrRewardCodeRequest
	}
	args := []any{id}
	if action == "fulfill" {
		args = append(args, strings.TrimSpace(note))
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrRewardCodeRequest
	}
	return nil
}

func rewardRedemptionReply(r RewardCode) string {
	switch r.RewardType {
	case "balance":
		return fmt.Sprintf("✅ **奖励兑换成功**\n\n奖励：USD %.2f\n已存入绑定账号。", r.Amount)
	case "chances":
		return fmt.Sprintf("✅ **奖励兑换成功**\n\n奖励：抽奖次数 × %d\n发送 /福利 抽奖 即可使用。", r.Quantity)
	default:
		instruction := r.ClaimInstructions
		if instruction == "" {
			instruction = "请联系管理员领取。"
		}
		return fmt.Sprintf("🎁 **奖励兑换成功**\n\n奖品：%s × %d\n状态：待发放\n%s", replyPlainLabel(r.PrizeName), r.Quantity, replyPlainLabel(instruction))
	}
}

func (s *Service) redeemReward(ctx context.Context, c Config, j Job) (string, error) {
	if j.RewardCodeHash == "" {
		return "用法：/福利 兑换 兑换码", nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	// Coordinate config changes, rebinding, and the existing reward/lottery path.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(71427101)`); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,272))`, c.AppID); err != nil {
		return "", err
	}
	current, err := s.loadConfig(ctx, tx)
	if err != nil {
		return "", err
	}
	if !current.Enabled || current.AppID != c.AppID || !current.allows(j.GroupID) {
		return "当前群未启用机器人。", nil
	}
	var uid int64
	err = tx.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,274))`, strconv.FormatInt(uid, 10)); err != nil {
		return "", err
	}
	requestKey := c.AppID + ":job:" + j.ID
	if j.MessageID != "" {
		requestKey = c.AppID + ":message:" + j.MessageID
	}
	if j.ID == "" && j.MessageID == "" {
		return "", ErrRewardCodeRequest
	}
	var replay string
	err = tx.QueryRowContext(ctx, `SELECT result_text FROM qq_bot_reward_attempts WHERE request_key=$1 AND user_id=$2 AND member_id=$3`, requestKey, uid, j.MemberID).Scan(&replay)
	if err == nil {
		return replay, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	scopes := []string{"member:" + codeHash(c.AppID+":"+j.MemberID), "user:" + strconv.FormatInt(uid, 10)}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return "", err
	}
	for _, scope := range scopes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_reward_limits(scope) VALUES($1) ON CONFLICT DO NOTHING`, scope); err != nil {
			return "", err
		}
		var last, blocked time.Time
		if err = tx.QueryRowContext(ctx, `SELECT last_attempt_at,blocked_until FROM qq_bot_reward_limits WHERE scope=$1 FOR UPDATE`, scope).Scan(&last, &blocked); err != nil {
			return "", err
		}
		if now.Before(blocked) {
			return rewardCodeLocked, nil
		}
		if now.Sub(last) < 5*time.Second {
			return "操作过于频繁，请 5 秒后再试。", nil
		}
	}
	var recent, daily int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE created_at>$4::timestamptz-INTERVAL '15 minutes'),COUNT(*) FILTER (WHERE (created_at AT TIME ZONE 'Asia/Shanghai')::date=($4::timestamptz AT TIME ZONE 'Asia/Shanghai')::date)
 FROM qq_bot_reward_attempts WHERE failed AND ((app_id=$1 AND member_id=$2) OR user_id=$3) AND created_at>$4::timestamptz-INTERVAL '1 day'`, c.AppID, j.MemberID, uid, now).Scan(&recent, &daily)
	if err != nil {
		return "", err
	}
	if daily >= 20 {
		return "今日兑换失败次数已达上限，请明天再试。", nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_reward_limits SET last_attempt_at=$2 WHERE scope=ANY($1)`, pq.Array(scopes), now); err != nil {
		return "", err
	}
	// Acquire the active account before checking a code's expiry, so a slow
	// concurrent account update cannot turn an expired code into a valid grant.
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT status='active' AND deleted_at IS NULL FROM users WHERE id=$1 FOR NO KEY UPDATE`, uid).Scan(&active)
	if err != nil {
		return "", err
	}
	if !active {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	var r RewardCode
	err = tx.QueryRowContext(ctx, `SELECT id,reward_type,amount,quantity,prize_name,claim_instructions FROM qq_bot_reward_codes
 WHERE app_id=$1 AND code_hash=$2 AND status='unused' AND (expires_at IS NULL OR expires_at>clock_timestamp()) AND (target_user_id IS NULL OR target_user_id=$3) FOR UPDATE`, c.AppID, j.RewardCodeHash, uid).Scan(&r.ID, &r.RewardType, &r.Amount, &r.Quantity, &r.PrizeName, &r.ClaimInstructions)
	if errors.Is(err, sql.ErrNoRows) {
		result := rewardCodeInvalid
		if recent+1 >= 5 {
			if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_reward_limits SET blocked_until=$2::timestamptz+INTERVAL '30 minutes' WHERE scope=ANY($1)`, pq.Array(scopes), now); err != nil {
				return "", err
			}
			result = rewardCodeLocked
		}
		if daily+1 >= 20 {
			result = "今日兑换失败次数已达上限，请明天再试。"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_reward_attempts(request_key,app_id,member_id,user_id,failed,result_text,created_at) VALUES($1,$2,$3,$4,true,$5,$6)`, requestKey, c.AppID, j.MemberID, uid, result, now); err != nil {
			return "", err
		}
		return result, tx.Commit()
	}
	if err != nil {
		return "", err
	}
	result := rewardRedemptionReply(r)
	status := "fulfilled"
	if r.RewardType == "physical" || r.RewardType == "other" {
		status = "pending"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_reward_codes SET status=$2,user_id=$3,member_id=$4,group_id=$5,redeemed_at=clock_timestamp(),fulfilled_at=CASE WHEN $2='fulfilled' THEN clock_timestamp() ELSE NULL END WHERE id=$1`, r.ID, status, uid, j.MemberID, j.GroupID); err != nil {
		return "", err
	}
	source := fmt.Sprintf("qq-reward-code:%d", r.ID)
	switch r.RewardType {
	case "balance":
		if _, err = tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2,updated_at=NOW() WHERE id=$1`, uid, r.Amount); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_rewards(reward_key,app_id,group_id,member_id,user_id,kind,amount) VALUES($1,$2,$3,$4,$5,'redemption',$6)`, source, c.AppID, j.GroupID, j.MemberID, uid, r.Amount); err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes) VALUES($1,'admin_balance',$2,'used',$3,NOW(),$4)`, "QQ"+randomID(), r.Amount, uid, fmt.Sprintf("QQ reward redemption #%d", r.ID)); err != nil {
			return "", err
		}
	case "chances":
		if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_lottery_chance_grants(source_key,user_id,chances) VALUES($1,$2,$3)`, source, uid, r.Quantity); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_reward_attempts(request_key,app_id,member_id,user_id,code_id,failed,result_text,created_at) VALUES($1,$2,$3,$4,$5,false,$6,$7)`, requestKey, c.AppID, j.MemberID, uid, r.ID, result, now); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	if r.RewardType == "balance" {
		s.invalidate(ctx, uid)
	}
	return result, nil
}
