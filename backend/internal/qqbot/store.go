package qqbot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func codeHash(code string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(h[:])
}
func day(now time.Time) string {
	return now.In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02")
}

func (s *Service) Binding(ctx context.Context, app string, user int64) (*Binding, *BindRequest, error) {
	var b Binding
	err := s.db.QueryRowContext(ctx, `SELECT app_id,member_id,group_id,nickname,user_id,created_at FROM qq_bot_bindings WHERE app_id=$1 AND user_id=$2`, app, user).Scan(&b.AppID, &b.MemberID, &b.GroupID, &b.Nickname, &b.UserID, &b.CreatedAt)
	if err == nil {
		return &b, nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	var r BindRequest
	err = s.db.QueryRowContext(ctx, `SELECT member_id,group_id,nickname,expires_at FROM qq_bot_bind_requests WHERE user_id=$1 AND app_id=$2 AND expires_at>NOW()`, user, app).Scan(&r.MemberID, &r.GroupID, &r.Nickname, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return nil, &r, nil
}
func (s *Service) CreateBindingCode(ctx context.Context, user int64) (BindRequest, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return BindRequest{}, err
	}
	if !c.Enabled {
		return BindRequest{}, ErrDisabled
	}
	r := BindRequest{Code: randomID(), ExpiresAt: time.Now().Add(10 * time.Minute)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO qq_bot_bind_requests(user_id,app_id,code_hash,expires_at) VALUES($1,$2,$3,$4)
 ON CONFLICT(user_id) DO UPDATE SET app_id=EXCLUDED.app_id,code_hash=EXCLUDED.code_hash,expires_at=EXCLUDED.expires_at,member_id='',nickname='',group_id=''`, user, c.AppID, codeHash(r.Code), r.ExpiresAt)
	return r, err
}
func (s *Service) claimBinding(ctx context.Context, c Config, e Event, code string) string {
	if len(code) != 24 {
		return "绑定码格式无效，请从网站个人资料页复制。"
	}
	res, err := s.db.ExecContext(ctx, `UPDATE qq_bot_bind_requests SET member_id=$1,nickname=$2,group_id=$3 WHERE app_id=$4 AND code_hash=$5 AND expires_at>NOW() AND member_id=''`, e.Author.MemberID, limitText(e.Author.Username, 100), e.GroupID, c.AppID, codeHash(code))
	if err != nil {
		return "绑定失败，请稍后重试。"
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return "绑定码无效或已过期，请重新生成。"
	}
	return "绑定申请已受理，请在网站个人资料页确认。"
}
func (s *Service) ConfirmBinding(ctx context.Context, user int64, member string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lockConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return ErrDisabled
	}
	var r BindRequest
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,273))`, c.AppID); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT r.member_id,r.group_id,r.nickname,r.expires_at FROM qq_bot_bind_requests r JOIN users u ON u.id=r.user_id
WHERE r.user_id=$1 AND r.app_id=$2 AND r.expires_at>clock_timestamp() AND u.status='active' AND u.deleted_at IS NULL
FOR UPDATE OF r FOR NO KEY UPDATE OF u`, user, c.AppID).Scan(&r.MemberID, &r.GroupID, &r.Nickname, &r.ExpiresAt)
	// Row locking can wait after the SQL expiry predicate has been evaluated.
	if err != nil || r.MemberID == "" || r.MemberID != member || !r.ExpiresAt.After(time.Now()) {
		return errors.New("绑定请求不存在、尚未收到群内指令或已过期")
	}
	if !c.allows(r.GroupID) {
		return errors.New("该群已停止提供绑定服务")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_bindings(app_id,member_id,user_id,nickname,group_id) VALUES($1,$2,$3,$4,$5)`, c.AppID, r.MemberID, user, r.Nickname, r.GroupID)
	if err != nil {
		return errors.New("该 QQ 身份或网站账号已经绑定，不能重复绑定")
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM qq_bot_bind_requests WHERE user_id=$1`, user); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM qq_bot_email_bind_requests WHERE app_id=$1 AND (user_id=$2 OR member_id=$3)`, c.AppID, user, member); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) Enqueue(ctx context.Context, c Config, p Payload) error {
	var e Event
	if err := json.Unmarshal(p.Data, &e); err != nil {
		return err
	}
	if e.GroupID == "" || len(e.GroupID) > 128 {
		return nil
	}
	// Ignore ambient full-stream messages before touching the queue or database.
	if e.Author.Bot || (p.Type == groupMessageCreate && !shouldQueueGroupMessage(p.Type, e)) {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_groups(app_id,group_id,last_event) VALUES($1,$2,$3) ON CONFLICT(app_id,group_id) DO UPDATE SET last_event=EXCLUDED.last_event,last_seen_at=NOW()`, c.AppID, e.GroupID, p.Type)
	if err != nil {
		return err
	}
	if !c.Enabled || !c.allows(e.GroupID) {
		return nil
	}
	switch p.Type {
	case groupAtMessageCreate, groupMessageCreate, "GROUP_JOIN_REQUEST", "GROUP_MEMBER_ADD", "GROUP_MEMBER_REMOVE":
	default:
		return nil
	}
	member := e.MemberID
	if isGroupMessageEvent(p.Type) {
		member = e.Author.MemberID
	}
	if member == "" || len(member) > 128 {
		return nil
	}
	eventKey := p.ID
	if isGroupMessageEvent(p.Type) {
		if e.ID == "" {
			return nil
		}
		// Preserve existing keys and deduplicate delivery across both QQ events.
		eventKey = groupAtMessageCreate + ":" + e.ID
	}
	if eventKey == "" {
		return errors.New("事件缺少唯一 ID")
	}
	// A bounded durable queue prevents unbounded work and acknowledges only after commit.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71427102)`); err != nil {
		return err
	}
	var queued int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_jobs WHERE status='queued'`).Scan(&queued); err != nil {
		return err
	}
	if queued >= 200 {
		return errors.New("机器人队列已满")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_jobs(id,app_id,event_key,event_type,group_id,member_id,message_id,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(app_id,event_key) DO NOTHING`, randomID(), c.AppID, eventKey, p.Type, e.GroupID, member, e.ID, string(p.Data))
	if err != nil {
		return err
	}
	return tx.Commit()
}

const jobColumns = `id,app_id,event_type,group_id,member_id,message_id,payload,kind,status,result_text,result_image,image_type,error,input_tokens,output_tokens,created_at,finished_at`

type scanner interface{ Scan(...any) error }

func scanJob(r scanner) (Job, error) {
	var j Job
	err := r.Scan(&j.ID, &j.AppID, &j.EventType, &j.GroupID, &j.MemberID, &j.MessageID, &j.Payload, &j.Kind, &j.Status, &j.ResultText, &j.Image, &j.ImageType, &j.Error, &j.InputTokens, &j.OutputTokens, &j.CreatedAt, &j.FinishedAt)
	return j, err
}
func (s *Service) claimJob(ctx context.Context) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71427102)`); err != nil {
		return Job{}, err
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_jobs WHERE status='running'`).Scan(&n); err != nil {
		return Job{}, err
	}
	if n >= 2 {
		return Job{}, sql.ErrNoRows
	}
	j, err := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM qq_bot_jobs q WHERE status='queued' AND NOT EXISTS(SELECT 1 FROM qq_bot_jobs r WHERE r.status='running' AND r.app_id=q.app_id AND r.group_id=q.group_id AND r.member_id=q.member_id) ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`))
	if err != nil {
		return j, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_jobs SET status='running',started_at=NOW() WHERE id=$1`, j.ID); err != nil {
		return j, err
	}
	return j, tx.Commit()
}

var (
	errFeatureUnavailable = errors.New("功能未启用。")
	errUsageCooldown      = errors.New("请求频繁，请稍后再试。")
	errUsageLimit         = errors.New("今日使用次数已达上限。")
)

// Limits reserve attempts before calling an upstream. Unknown upstream outcomes
// are never retried automatically: a timeout can still represent a paid request.
func (s *Service) reserveUsage(ctx context.Context, c Config, j Job, kind string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := s.lockConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !current.Enabled || current.AppID != c.AppID || !current.allows(j.GroupID) {
		return errFeatureUnavailable
	}
	c = current
	per, total := c.DailyChatLimit, c.TotalDailyChatLimit
	if kind == "image" {
		if !c.ImageEnabled {
			return errFeatureUnavailable
		}
		per, total = c.DailyImageLimit, c.TotalDailyImageLimit
	} else if kind != "chat" {
		return errFeatureUnavailable
	}
	if per == 0 || total == 0 {
		return errFeatureUnavailable
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,271))`, c.AppID); err != nil {
		return err
	}
	var recent int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_jobs WHERE app_id=$1 AND member_id=$2 AND id<>$3 AND kind IN ('chat','image') AND started_at>NOW()-($4*INTERVAL '1 second')`, c.AppID, j.MemberID, j.ID, c.CooldownSeconds).Scan(&recent); err != nil {
		return err
	}
	if recent > 0 {
		return errUsageCooldown
	}
	usageDay := day(time.Now())
	for i, scope := range []string{"all", "member:" + j.MemberID} {
		limit := total
		if i == 1 {
			limit = per
		}
		var count int
		err = tx.QueryRowContext(ctx, `INSERT INTO qq_bot_daily_usage(app_id,scope,day,kind,count) VALUES($1,$2,$3,$4,1)
  ON CONFLICT(app_id,scope,day,kind) DO UPDATE SET count=qq_bot_daily_usage.count+1 WHERE qq_bot_daily_usage.count<$5 RETURNING count`, c.AppID, scope, usageDay, kind, limit).Scan(&count)
		if errors.Is(err, sql.ErrNoRows) {
			return errUsageLimit
		}
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_jobs SET kind=$2 WHERE id=$1`, j.ID, kind); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) reward(ctx context.Context, c Config, j Job, kind string) (string, error) {
	if kind != "checkin" && kind != "invite" {
		return "", errors.New("奖励类型不正确")
	}
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
	c = current
	amount := c.CheckinReward
	if kind == "invite" {
		if c.InviteReward <= 0 || c.InviteDailyLimit <= 0 {
			return "邀请奖励未开启。", nil
		}
		amount = c.InviteReward
	}
	// Serialize reward eligibility and balance writes, including across replicas.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,272))`, c.AppID); err != nil {
		return "", err
	}
	// Use one settlement instant for both the daily key and ledger timestamp;
	// NOW() would retain the transaction's pre-lock date across midnight.
	rewardAt := time.Now()
	rewardDay := day(rewardAt)
	if kind == "checkin" {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, checkinConfigLock); err != nil {
			return "", err
		}
		cfg, loadErr := loadCheckinConfig(ctx, tx, c.CheckinReward)
		if loadErr != nil {
			return "", loadErr
		}
		if !cfg.Enabled {
			return "签到活动未开启。", nil
		}
		amount, err = randomCheckinAmount(cfg)
		if err != nil {
			return "", err
		}
	}
	var userID int64
	err = tx.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "请先使用 /绑定 绑定网站账号。", nil
	}
	if err != nil {
		return "", err
	}
	// Admin-assisted rebinding must not reset a QQ identity's reward eligibility.
	var rewarded bool
	if kind == "checkin" {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_bot_rewards WHERE app_id=$1 AND member_id=$2 AND kind='checkin' AND (created_at AT TIME ZONE 'Asia/Shanghai')::date=$3::date)`, c.AppID, j.MemberID, rewardDay).Scan(&rewarded)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_bot_rewards WHERE app_id=$1 AND source_member_id=$2 AND kind='invite')`, c.AppID, j.MemberID).Scan(&rewarded)
	}
	if err != nil {
		return "", err
	}
	if rewarded {
		if kind == "checkin" {
			return "今日已签到。", nil
		}
		return "该邀请奖励已发放。", nil
	}
	key := fmt.Sprintf("checkin:%d:%s", userID, rewardDay)
	sourceUser := int64(0)
	sourceMember := ""
	recipientMember := j.MemberID
	if kind == "invite" {
		sourceUser = userID
		sourceMember = j.MemberID
		err = tx.QueryRowContext(ctx, `SELECT m.inviter_id,b.user_id FROM qq_bot_members m JOIN qq_bot_bindings b ON b.app_id=m.app_id AND b.member_id=m.inviter_id JOIN users u ON u.id=b.user_id
   WHERE m.app_id=$1 AND m.group_id=$2 AND m.member_id=$3 AND m.joined_at IS NOT NULL AND m.left_at IS NULL AND m.invited_at<=m.joined_at AND m.invited_at>m.joined_at-INTERVAL '7 days' AND u.status='active' AND u.deleted_at IS NULL FOR SHARE OF m`, c.AppID, j.GroupID, j.MemberID).Scan(&recipientMember, &userID)
		if errors.Is(err, sql.ErrNoRows) {
			return "暂无符合条件的邀请记录。", nil
		}
		if err != nil {
			return "", err
		}
		if userID == sourceUser {
			return "本人邀请不计奖励。", nil
		}
		key = fmt.Sprintf("invite:%d", sourceUser)
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_rewards WHERE kind='invite' AND user_id=$1 AND (created_at AT TIME ZONE 'Asia/Shanghai')::date=$2::date`, userID, rewardDay).Scan(&count); err != nil {
			return "", err
		}
		if count >= c.InviteDailyLimit {
			return "邀请人今日奖励次数已达上限。", nil
		}
	}
	var rewardID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO qq_bot_rewards(reward_key,app_id,group_id,member_id,user_id,kind,source_user_id,source_member_id,amount,created_at) VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,0),$8,$9,$10) ON CONFLICT(reward_key) DO NOTHING RETURNING id`, key, c.AppID, j.GroupID, recipientMember, userID, kind, sourceUser, sourceMember, amount, rewardAt).Scan(&rewardID)
	if errors.Is(err, sql.ErrNoRows) {
		if kind == "checkin" {
			return "今日已签到。", nil
		}
		return "该邀请奖励已发放。", nil
	}
	if err != nil {
		return "", err
	}
	// Ledger, user balance, and visible balance history commit together. No recharge rebate.
	result, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$1,updated_at=NOW() WHERE id=$2 AND status='active' AND deleted_at IS NULL`, amount, userID)
	if err != nil {
		return "", err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return "", errors.New("账户不可用")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO redeem_codes(code,type,value,status,used_by,used_at,notes) VALUES($1,'admin_balance',$2,'used',$3,NOW(),$4)`, "QQ"+randomID(), amount, userID, fmt.Sprintf("QQ %s reward #%d", kind, rewardID))
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	s.invalidate(ctx, userID)
	if kind == "invite" {
		return fmt.Sprintf("邀请奖励已发放\n邀请人到账额度：$%.2f", amount), nil
	}
	return fmt.Sprintf("签到成功\n到账额度：$%.2f", amount), nil
}

func (s *Service) Runtime(ctx context.Context, c Config) (Runtime, error) {
	out := Runtime{Groups: []Group{}, Jobs: []Job{}, Rewards: []Reward{}, EncryptionReady: s.encryptionReady}
	rows, err := s.db.QueryContext(ctx, `SELECT group_id,last_event,last_seen_at FROM qq_bot_groups WHERE app_id=$1 ORDER BY last_seen_at DESC LIMIT 100`, c.AppID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var g Group
		if err = rows.Scan(&g.ID, &g.LastEvent, &g.LastSeenAt); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Groups = append(out.Groups, g)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id,app_id,event_type,group_id,member_id,kind,status,error,input_tokens,output_tokens,created_at,finished_at FROM qq_bot_jobs WHERE app_id=$1 ORDER BY created_at DESC LIMIT 50`, c.AppID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.ID, &j.AppID, &j.EventType, &j.GroupID, &j.MemberID, &j.Kind, &j.Status, &j.Error, &j.InputTokens, &j.OutputTokens, &j.CreatedAt, &j.FinishedAt); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Jobs = append(out.Jobs, j)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT id,user_id,kind,amount,created_at FROM qq_bot_rewards WHERE app_id=$1 ORDER BY created_at DESC LIMIT 50`, c.AppID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r Reward
		if err = rows.Scan(&r.ID, &r.UserID, &r.Kind, &r.Amount, &r.CreatedAt); err != nil {
			_ = rows.Close()
			return out, err
		}
		out.Rewards = append(out.Rewards, r)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return out, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_bindings WHERE app_id=$1`, c.AppID).Scan(&out.Bindings)
	return out, err
}
