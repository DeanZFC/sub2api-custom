package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lib/pq"
)

type Service struct {
	db              *sql.DB
	encryptor       Encryptor
	encryptionReady bool
	invalidate      func(context.Context, int64)
	bindingEmail    BindingEmailDelivery
	channelStatus   ChannelStatusProvider
	http            *http.Client
	tokenURL        string
	qqURL           string
	tokenMu         sync.Mutex
	accessToken     string
	tokenKey        string
	tokenExpires    time.Time
	groupBotMu      sync.Mutex
	groupBots       map[string]groupBotIdentity
	imageDownload   func(context.Context, string) ([]byte, error)
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

func New(db *sql.DB, encryptor Encryptor, ready bool, invalidate func(context.Context, int64)) *Service {
	if invalidate == nil {
		invalidate = func(context.Context, int64) {}
	}
	return &Service{db: db, encryptor: encryptor, encryptionReady: ready, invalidate: invalidate, http: &http.Client{Timeout: 210 * time.Second, CheckRedirect: noRedirect}, imageDownload: downloadImage, tokenURL: "https://bots.qq.com/app/getAppAccessToken", qqURL: "https://api.sgroup.qq.com"}
}
func (s *Service) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.runOne(ctx)
				}
			}
		}()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.maintenance(ctx)
			}
		}
	}()
}
func (s *Service) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}
func (s *Service) maintenance(ctx context.Context) {
	// An interrupted model request may already have cost money. Preserve its
	// status for inspection and never replay it after a process crash.
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET status='interrupted',error='处理进程中断，未自动重试；请检查调用用量',finished_at=NOW(),payload='{}' WHERE status='running' AND started_at<NOW()-INTERVAL '10 minutes'`)
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET status='expired',error='排队超过回复时限，请重新发送',finished_at=NOW(),payload='{}' WHERE status='queued' AND created_at<NOW()-INTERVAL '5 minutes' AND event_type IN ('GROUP_AT_MESSAGE_CREATE','GROUP_MESSAGE_CREATE')`)
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET result_image=NULL,result_text='',payload='{}' WHERE created_at<NOW()-INTERVAL '24 hours' AND (result_image IS NOT NULL OR result_text<>'' OR payload<>'{}'::jsonb)`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_bind_requests WHERE expires_at<NOW()`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_email_bind_requests WHERE expires_at<NOW()`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_email_limits WHERE day<CURRENT_DATE-2`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_conversations WHERE updated_at<NOW()-INTERVAL '1 day'`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_message_references WHERE created_at<NOW()-INTERVAL '24 hours'`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_daily_usage WHERE day<CURRENT_DATE-32`)
	// Keep small deduplication tombstones for the signed event replay lifetime.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_jobs WHERE created_at<NOW()-INTERVAL '30 days' AND status NOT IN ('queued','running')`)
}
func (s *Service) runOne(parent context.Context) {
	j, err := s.claimJob(parent)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 260*time.Second)
	defer cancel()
	c, err := s.Config(ctx)
	if err != nil {
		logMembershipEvent(ctx, j, nil, "process", "failed", "config_unavailable")
		s.finish(j, "failed", err.Error())
		return
	}
	if !c.Enabled || c.AppID != j.AppID || !c.allows(j.GroupID) {
		reason := "bot_disabled"
		if c.AppID != j.AppID {
			reason = "app_changed"
		} else if c.Enabled && !c.allows(j.GroupID) {
			reason = "group_not_allowed"
		}
		logMembershipEvent(ctx, j, nil, "process", "ignored", reason)
		s.finish(j, "failed", ErrDisabled.Error())
		return
	}
	var e Event
	if err = json.Unmarshal(j.Payload, &e); err != nil {
		logMembershipEvent(ctx, j, nil, "process", "failed", "invalid_payload")
		s.finish(j, "failed", "事件格式不正确")
		return
	}
	if !isGroupMessageEvent(j.EventType) {
		err = s.memberEvent(ctx, c, j, e)
		if err != nil {
			s.finish(j, "failed", "群成员事件处理失败")
		} else {
			s.finish(j, "completed", "")
		}
		return
	}
	if time.Since(j.CreatedAt) > 240*time.Second {
		s.finish(j, "expired", "排队超过回复时限，请重新发送")
		return
	}
	err = s.message(ctx, c, &j, e)
	if err != nil {
		status := "delivery_failed"
		if j.Error != "" {
			status = "failed"
		}
		s.finish(j, status, err.Error())
	} else {
		s.finish(j, "completed", "")
	}
}
func (s *Service) finish(j Job, status, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET status=$2,error=$3,finished_at=NOW(),payload='{}' WHERE id=$1`, j.ID, status, limitText(detail, 250)); err != nil {
		logMembershipEvent(ctx, j, nil, "finish", "failed", "job_status_write_failed")
	}
}
func (s *Service) memberEvent(ctx context.Context, c Config, j Job, e Event) error {
	outcome, reason := "failed", "database_error"
	defer func() { logMembershipEvent(ctx, j, &e, "process", outcome, reason) }()
	switch j.EventType {
	case "GROUP_JOIN_REQUEST":
		if e.ApplySource != "invited" {
			outcome, reason = "ignored", "not_an_invitation"
			return nil
		}
		if e.InvitedBy == "" {
			outcome, reason = "ignored", "missing_inviter"
			return nil
		}
		if e.InvitedBy == e.MemberID {
			outcome, reason = "ignored", "self_invitation"
			return nil
		}
		applied, err := time.Parse(time.RFC3339, e.ApplyAt)
		if err != nil {
			reason = "invalid_invitation_time"
			return errors.New("invalid invitation time")
		}
		// Signed events can arrive out of order. A delayed invitation may
		// complete an earlier join, but cannot be attached after that join.
		res, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at) VALUES($1,$2,$3,$4,$5)
        ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET inviter_id=EXCLUDED.inviter_id,invited_at=EXCLUDED.invited_at
        WHERE qq_bot_members.inviter_id='' AND (qq_bot_members.joined_at IS NULL OR EXCLUDED.invited_at<=qq_bot_members.joined_at)`, c.AppID, e.GroupID, e.MemberID, e.InvitedBy, applied)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		_, err = s.reward(ctx, c, j, "invite")
		if err != nil {
			reason = "reward_settlement_failed"
			return err
		}
		if n == 0 {
			outcome, reason = "ignored", "existing_inviter_or_late_invitation"
		} else {
			outcome, reason = "recorded", "invitation_recorded"
		}
		return err
	case "GROUP_MEMBER_ADD", "GROUP_MEMBER_REMOVE":
		var ts int64
		if json.Unmarshal(e.Timestamp, &ts) != nil || ts <= 0 {
			reason = "invalid_membership_time"
			return errors.New("invalid membership time")
		}
		at := time.Unix(ts, 0)
		if j.EventType == "GROUP_MEMBER_ADD" {
			res, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_members(app_id,group_id,member_id,joined_at,last_membership_at) VALUES($1,$2,$3,$4,$4)
            ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET joined_at=COALESCE(qq_bot_members.joined_at,EXCLUDED.joined_at),left_at=NULL,last_membership_at=EXCLUDED.last_membership_at
            WHERE qq_bot_members.last_membership_at IS NULL OR EXCLUDED.last_membership_at>qq_bot_members.last_membership_at`, c.AppID, e.GroupID, e.MemberID, at)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			_, err = s.reward(ctx, c, j, "invite")
			if err != nil {
				reason = "reward_settlement_failed"
				return err
			}
			outcome, reason = "recorded", "member_join_recorded"
			if n == 0 {
				outcome, reason = "ignored", "stale_membership_event"
			}
			return err
		}
		res, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_members(app_id,group_id,member_id,left_at,last_membership_at) VALUES($1,$2,$3,$4,$4)
        ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET left_at=EXCLUDED.left_at,last_membership_at=EXCLUDED.last_membership_at
        WHERE qq_bot_members.last_membership_at IS NULL OR EXCLUDED.last_membership_at>=qq_bot_members.last_membership_at`, c.AppID, e.GroupID, e.MemberID, at)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		outcome, reason = "recorded", "member_leave_recorded"
		if n == 0 {
			outcome, reason = "ignored", "stale_membership_event"
		}
		return nil
	}
	outcome, reason = "ignored", "unsupported_event"
	return nil
}

func (s *Service) SettleInvites(ctx context.Context, member string) error {
	c, err := s.Config(ctx)
	if err != nil {
		return err
	}
	return s.settleInvites(ctx, c, member)
}

func (s *Service) settleInvites(ctx context.Context, c Config, member string) error {
	if !c.Enabled || c.InviteReward <= 0 || c.InviteDailyLimit <= 0 {
		return nil
	}
	eligibleAsInviter := true
	requirement := c.inviteRechargeRequirement()
	if requirement.enabled() {
		var userID int64
		err := s.db.QueryRowContext(ctx, `SELECT b.user_id FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id
 WHERE b.app_id=$1 AND b.member_id=$2 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, member).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		eligibility, err := requirement.eligibility(ctx, s.db, userID, false)
		if err != nil {
			return err
		}
		eligibleAsInviter = requirement.satisfied(eligibility)
	}
	// Filter eligibility before bounding the batch. Otherwise old unbound or
	// already-paid relationships can permanently hide later valid invitations.
	rows, err := s.db.QueryContext(ctx, `SELECT m.group_id,m.member_id FROM qq_bot_members m
 JOIN qq_bot_bindings b ON b.app_id=m.app_id AND b.member_id=m.member_id
 JOIN users u ON u.id=b.user_id AND u.status='active' AND u.deleted_at IS NULL
 JOIN qq_bot_bindings ib ON ib.app_id=m.app_id AND ib.member_id=m.inviter_id
 JOIN users iu ON iu.id=ib.user_id AND iu.status='active' AND iu.deleted_at IS NULL
 WHERE m.app_id=$1 AND (m.member_id=$2 OR (m.inviter_id=$2 AND $6)) AND m.group_id=ANY($3)
 AND m.joined_at IS NOT NULL AND m.left_at IS NULL AND m.inviter_id<>'' AND m.member_id<>m.inviter_id
 AND m.invited_at<=m.joined_at AND m.invited_at>m.joined_at-INTERVAL '7 days' AND b.user_id<>ib.user_id
 AND NOT EXISTS(SELECT 1 FROM qq_bot_rewards r WHERE r.kind='invite' AND r.app_id=m.app_id AND r.source_member_id=m.member_id)
 AND NOT EXISTS(SELECT 1 FROM qq_bot_rewards r WHERE r.kind='invite' AND r.source_user_id=b.user_id)
 AND (SELECT COUNT(*) FROM qq_bot_rewards r WHERE r.kind='invite' AND r.user_id=ib.user_id
      AND (r.created_at AT TIME ZONE 'Asia/Shanghai')::date=$5::date)<$4
 ORDER BY m.joined_at,m.group_id,m.member_id LIMIT 100`, c.AppID, member, pq.Array(c.AllowedGroups), c.InviteDailyLimit, day(time.Now()), eligibleAsInviter)
	if err != nil {
		return err
	}
	var jobs []Job
	for rows.Next() {
		j := Job{AppID: c.AppID}
		if err = rows.Scan(&j.GroupID, &j.MemberID); err != nil {
			_ = rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if c.allows(j.GroupID) {
			if _, err = s.reward(ctx, c, j, "invite"); err != nil {
				return err
			}
		}
	}
	return nil
}

var mentionPrefix = regexp.MustCompile(`^<@!?[^>]+>\s*`)

func command(content string) (string, string) {
	content = strings.TrimSpace(mentionPrefix.ReplaceAllString(strings.TrimSpace(content), ""))
	if !strings.HasPrefix(content, "/") {
		return "", ""
	}
	content = strings.TrimPrefix(content, "/")
	pos := strings.IndexFunc(content, unicode.IsSpace)
	fields := []string{content}
	if pos >= 0 {
		fields = []string{content[:pos], content[pos:]}
	}
	arg := ""
	if len(fields) == 2 {
		arg = strings.TrimSpace(fields[1])
	}
	return fields[0], arg
}
func (s *Service) message(ctx context.Context, c Config, j *Job, e Event) error {
	ref, err := s.referencedMessage(ctx, c, j.GroupID, e.referenceID())
	if err != nil {
		return err
	}
	if j.EventType == groupMessageCreate && !unmentionedGroupCommand(e, c.CustomCommands...) {
		addressed, err := s.groupMessageMentionsBot(ctx, c, e)
		if err != nil {
			return err
		}
		if !addressed && (ref == nil || len(groupMentionIDs(e)) > 0) {
			return nil
		}
	}
	j.ReplyReference = e.MessageScene.value("msg_idx")
	parts, attachments := eventParts(e)
	hasImages := len(imageAttachments(attachments)) > 0
	cmd, arg := command(e.Content)
	if strings.TrimSpace(mentionPrefix.ReplaceAllString(strings.TrimSpace(e.Content), "")) == "" && !hasImages && parts == "" && e.referenceID() == "" {
		cmd = "帮助"
	}
	text := ""
	var img []byte
	seq := 1
	switch cmd {
	case "帮助", "help":
		// Keep QQ's automatic reply mention above the command list.
		text = `
指令：/帮助
说明：查看帮助

指令：/签到
说明：每日签到

指令：/我的额度
说明：查询账户额度

指令：/我的奖励
说明：查询各项累计奖励

指令：/渠道状态
说明：查询整体及各分组的成功率、缓存率和首 Token

指令：/绑定 邮箱或绑定码
说明：绑定账号

指令：/邀请奖励
说明：查看邀请奖励规则、条件和今日进度

指令：/我的邀请
说明：查询邀请状态和人数

指令：/抽奖
说明：参与抽奖

指令：/抽奖次数
说明：查询剩余次数

指令：/抽奖活动
说明：查看活动规则和奖品

指令：/生图 图片描述
说明：生成图片`
		for _, custom := range c.CustomCommands {
			if custom.Enabled {
				text += "\n\n指令：" + custom.Command + "\n说明：自定义回复"
			}
		}
	case "渠道状态":
		return s.sendChannelStatus(ctx, c, *j, arg)
	case "绑定":
		if arg == "" {
			text = "用法：/绑定 邮箱或绑定码"
			break
		}
		var bound bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM qq_bot_bindings WHERE app_id=$1 AND member_id=$2)`, c.AppID, e.Author.MemberID).Scan(&bound)
		if err != nil {
			text = "绑定状态查询失败，请稍后重试。"
		} else if bound {
			text = "当前 QQ 已绑定账号。"
		} else if strings.Contains(arg, "@") {
			var pending *preparedBindingEmail
			text, pending = s.prepareEmailBinding(ctx, c, e, arg)
			if pending != nil {
				// A slow SMTP server must not delay the user's QQ acknowledgement.
				if err := s.send(ctx, c, *j, text, nil, seq); err != nil {
					return err
				}
				s.deliverBindingEmail(ctx, e, pending)
				return nil
			}
		} else {
			text = s.claimBinding(ctx, c, e, arg)
		}
	case "签到":
		var err error
		text, err = s.reward(ctx, c, *j, "checkin")
		if err != nil {
			text = "签到失败，请稍后重试。"
		}
	case "抽奖":
		var err error
		text, err = s.drawLottery(ctx, c, *j)
		if err != nil {
			text = "抽奖处理异常，请查询 /抽奖次数。"
		}
	case "抽奖次数", "我的抽奖", "抽奖活动":
		var err error
		text, err = s.lotteryInfo(ctx, c, *j, cmd == "抽奖活动")
		if err != nil {
			text = "抽奖信息查询失败，请稍后重试。"
		}
	case "邀请奖励", "邀请活动":
		var err error
		text, err = s.inviteInfo(ctx, c, *j)
		if err != nil {
			text = "邀请活动查询失败，请稍后重试。"
		}
	case "我的邀请":
		var err error
		text, err = s.myInvites(ctx, c, *j)
		if err != nil {
			text = "邀请信息查询失败，请稍后重试。"
		}
	case "我的奖励":
		var err error
		text, err = s.myRewards(ctx, c, *j)
		if err != nil {
			text = "奖励信息查询失败，请稍后重试。"
		}
	case "我的额度":
		var balance float64
		err := s.db.QueryRowContext(ctx, `SELECT u.balance FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.deleted_at IS NULL AND u.status='active'`, c.AppID, j.MemberID).Scan(&balance)
		if errors.Is(err, sql.ErrNoRows) {
			text = "请先使用 /绑定 绑定网站账号。"
		} else if err != nil {
			text = "账户信息查询失败，请稍后重试。"
		} else {
			text = fmt.Sprintf("账户额度：$%.2f", balance)
		}
	default:
		if reply, ok := customCommandReply(c.CustomCommands, cmd, arg); ok {
			text = reply
			break
		}
		if cmd != "" && cmd != "生图" && cmd != "画图" {
			text = "未知指令，请发送 /帮助。"
			break
		}
		kind := "chat"
		prompt := strings.TrimSpace(mentionPrefix.ReplaceAllString(strings.TrimSpace(e.Content), ""))
		if prompt == "" && (hasImages || parts != "" || e.referenceID() != "") {
			prompt = "请分析这条消息的内容。"
		}
		if cmd == "生图" || cmd == "画图" {
			kind = "image"
			prompt = arg
			if !c.ImageEnabled {
				text = "生图功能未启用。"
			}
			if prompt == "" {
				text = "用法：/生图 图片描述"
			}
		}
		if text != "" {
			break
		}
		if prompt == "" || len([]rune(prompt)) > 6000 {
			text = "内容长度须为 1～6000 字。"
			break
		}
		if err := s.reserveUsage(ctx, c, *j, kind); err != nil {
			text = "服务暂时不可用，请稍后重试。"
			if errors.Is(err, errFeatureUnavailable) || errors.Is(err, errUsageCooldown) || errors.Is(err, errUsageLimit) {
				text = err.Error()
			}
			break
		}
		j.Kind = kind
		if err := s.send(ctx, c, *j, "正在思考中", nil, seq); err != nil {
			return err
		}
		seq++
		var err error
		if kind == "image" {
			img, err = s.generateImage(ctx, c, prompt)
		} else {
			messages := s.history(ctx, c, *j)
			var input Message
			input, err = s.chatMessage(ctx, e, prompt, ref)
			if err == nil {
				messages = append(messages, input)
				text, j.InputTokens, j.OutputTokens, err = s.chat(ctx, c, messages)
			} else {
				text = err.Error()
			}
			if err == nil {
				s.saveHistory(ctx, c, *j, append(messages, Message{Role: "assistant", Content: text}))
			}
		}
		if err != nil {
			// Record upstream failure distinctly from delivery failures. Never expose raw upstream bodies.
			if text == "" {
				text = "回复生成失败，请稍后重试。"
			}
			if kind == "image" {
				text = "图片生成失败，请稍后重试。"
			}
			j.Error = err.Error()
			_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET error=$2 WHERE id=$1`, j.ID, j.Error)
			if sendErr := s.send(ctx, c, *j, text, nil, seq); sendErr != nil {
				return sendErr
			}
			return fmt.Errorf("模型调用失败：%s", j.Error)
		}
		// Persist before sending to QQ. Delivery retries only reuse this saved result.
		if _, err = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET result_text=$2,result_image=$3,image_type=$4,input_tokens=$5,output_tokens=$6 WHERE id=$1`, j.ID, text, img, "image/jpeg", j.InputTokens, j.OutputTokens); err != nil {
			return errors.New("生成成功但结果保存失败；未自动重新生成")
		}
	}
	if len(img) > 0 {
		if err := s.send(ctx, c, *j, "图片已生成。", nil, seq); err != nil {
			return err
		}
		return s.send(ctx, c, *j, text, img, seq+1)
	}
	chunks := splitReply(text, 5-seq+1)
	for _, chunk := range chunks {
		if err := s.send(ctx, c, *j, chunk, nil, seq); err != nil {
			return err
		}
		seq++
	}
	return nil
}
func (s *Service) history(ctx context.Context, c Config, j Job) []Message {
	messages := []Message{{Role: "system", Content: c.SystemPrompt}}
	if c.HistoryTurns == 0 {
		return messages
	}
	var raw []byte
	if s.db.QueryRowContext(ctx, `SELECT messages FROM qq_bot_conversations WHERE app_id=$1 AND group_id=$2 AND member_id=$3 AND updated_at>NOW()-INTERVAL '30 minutes'`, c.AppID, j.GroupID, j.MemberID).Scan(&raw) != nil {
		return messages
	}
	var old []Message
	if json.Unmarshal(raw, &old) != nil {
		return messages
	}
	if len(old) > c.HistoryTurns*2 {
		old = old[len(old)-c.HistoryTurns*2:]
	}
	return append(messages, old...)
}
func (s *Service) saveHistory(ctx context.Context, c Config, j Job, messages []Message) {
	if c.HistoryTurns == 0 {
		return
	}
	var kept []Message
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			if len(m.Images) > 0 {
				m.Content = limitText(m.Content, 2400)
				m.Content += "\n（本条附带图片，历史记录不保存图片；需要复查细节时请重新发送。）"
				m.Images = nil
			}
			m.Content = limitText(m.Content, 2500)
			kept = append(kept, m)
		}
	}
	if len(kept) > c.HistoryTurns*2 {
		kept = kept[len(kept)-c.HistoryTurns*2:]
	}
	raw, _ := json.Marshal(kept)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO qq_bot_conversations(app_id,group_id,member_id,messages) VALUES($1,$2,$3,$4) ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET messages=EXCLUDED.messages,updated_at=NOW()`, c.AppID, j.GroupID, j.MemberID, string(raw))
}

// QQ allows five passive messages per incoming message. Split the reply within
// the remaining slots, accounting for any messages already sent.
func splitReply(text string, maxChunks int) []string {
	r := []rune(text)
	const width = 1500
	if len(r) > width*maxChunks {
		r = append(r[:width*maxChunks-50], []rune("\n（内容过长，已截断。）")...)
	}
	var chunks []string
	for len(r) > 0 {
		n := min(len(r), width)
		chunks = append(chunks, string(r[:n]))
		r = r[n:]
	}
	if len(chunks) == 0 {
		return []string{"暂无内容。"}
	}
	return chunks
}
