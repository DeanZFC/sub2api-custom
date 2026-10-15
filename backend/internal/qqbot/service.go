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
)

type Service struct {
	db              *sql.DB
	encryptor       Encryptor
	encryptionReady bool
	invalidate      func(context.Context, int64)
	bindingEmail    BindingEmailDelivery
	http            *http.Client
	tokenURL        string
	qqURL           string
	tokenMu         sync.Mutex
	accessToken     string
	tokenKey        string
	tokenExpires    time.Time
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

func New(db *sql.DB, encryptor Encryptor, ready bool, invalidate func(context.Context, int64)) *Service {
	if invalidate == nil {
		invalidate = func(context.Context, int64) {}
	}
	return &Service{db: db, encryptor: encryptor, encryptionReady: ready, invalidate: invalidate, http: &http.Client{Timeout: 210 * time.Second, CheckRedirect: noRedirect}, tokenURL: "https://bots.qq.com/app/getAppAccessToken", qqURL: "https://api.sgroup.qq.com"}
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
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET status='expired',error='排队超过回复时限，请重新发送',finished_at=NOW(),payload='{}' WHERE status='queued' AND created_at<NOW()-INTERVAL '5 minutes' AND event_type='GROUP_AT_MESSAGE_CREATE'`)
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET result_image=NULL,result_text='',payload='{}' WHERE created_at<NOW()-INTERVAL '24 hours' AND (result_image IS NOT NULL OR result_text<>'' OR payload<>'{}'::jsonb)`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_bind_requests WHERE expires_at<NOW()`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_email_bind_requests WHERE expires_at<NOW()`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_email_limits WHERE day<CURRENT_DATE-2`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_conversations WHERE updated_at<NOW()-INTERVAL '1 day'`)
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
	if err == nil && (!c.Enabled || c.AppID != j.AppID || !c.allows(j.GroupID)) {
		err = ErrDisabled
	}
	if err != nil {
		s.finish(j, "failed", err.Error())
		return
	}
	var e Event
	if err = json.Unmarshal(j.Payload, &e); err != nil {
		s.finish(j, "failed", "事件格式不正确")
		return
	}
	if j.EventType != "GROUP_AT_MESSAGE_CREATE" {
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
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET status=$2,error=$3,finished_at=NOW(),payload='{}' WHERE id=$1`, j.ID, status, limitText(detail, 250))
}
func (s *Service) memberEvent(ctx context.Context, c Config, j Job, e Event) error {
	switch j.EventType {
	case "GROUP_JOIN_REQUEST":
		if e.ApplySource != "invited" || e.InvitedBy == "" || e.InvitedBy == e.MemberID {
			return nil
		}
		applied, err := time.Parse(time.RFC3339, e.ApplyAt)
		if err != nil {
			return errors.New("invalid invitation time")
		}
		// Signed events can arrive out of order. A delayed invitation may
		// complete an earlier join, but cannot be attached after that join.
		_, err = s.db.ExecContext(ctx, `INSERT INTO qq_bot_members(app_id,group_id,member_id,inviter_id,invited_at) VALUES($1,$2,$3,$4,$5)
        ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET inviter_id=EXCLUDED.inviter_id,invited_at=EXCLUDED.invited_at
        WHERE qq_bot_members.inviter_id='' AND (qq_bot_members.joined_at IS NULL OR EXCLUDED.invited_at<=qq_bot_members.joined_at)`, c.AppID, e.GroupID, e.MemberID, e.InvitedBy, applied)
		if err != nil {
			return err
		}
		_, err = s.reward(ctx, c, j, "invite")
		return err
	case "GROUP_MEMBER_ADD", "GROUP_MEMBER_REMOVE":
		var ts int64
		if json.Unmarshal(e.Timestamp, &ts) != nil || ts <= 0 {
			return errors.New("invalid membership time")
		}
		at := time.Unix(ts, 0)
		if j.EventType == "GROUP_MEMBER_ADD" {
			_, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_members(app_id,group_id,member_id,joined_at,last_membership_at) VALUES($1,$2,$3,$4,$4)
            ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET joined_at=COALESCE(qq_bot_members.joined_at,EXCLUDED.joined_at),left_at=NULL,last_membership_at=EXCLUDED.last_membership_at
            WHERE qq_bot_members.last_membership_at IS NULL OR EXCLUDED.last_membership_at>qq_bot_members.last_membership_at`, c.AppID, e.GroupID, e.MemberID, at)
			if err != nil {
				return err
			}
			_, err = s.reward(ctx, c, j, "invite")
			return err
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_members(app_id,group_id,member_id,left_at,last_membership_at) VALUES($1,$2,$3,$4,$4)
        ON CONFLICT(app_id,group_id,member_id) DO UPDATE SET left_at=EXCLUDED.left_at,last_membership_at=EXCLUDED.last_membership_at
        WHERE qq_bot_members.last_membership_at IS NULL OR EXCLUDED.last_membership_at>=qq_bot_members.last_membership_at`, c.AppID, e.GroupID, e.MemberID, at)
		return err
	}
	return nil
}

func (s *Service) SettleInvites(ctx context.Context, member string) error {
	c, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !c.Enabled || c.InviteReward <= 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_id,member_id FROM qq_bot_members WHERE app_id=$1 AND (member_id=$2 OR inviter_id=$2) AND joined_at IS NOT NULL AND left_at IS NULL AND inviter_id<>'' LIMIT 100`, c.AppID, member)
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
	cmd, arg := command(e.Content)
	text := ""
	var img []byte
	seq := 1
	switch cmd {
	case "帮助", "help":
		text = "CTAV 群助手（免费使用，费用由站长承担）\n直接 @我 提问；生图 描述；新对话；查看结果 任务号\n绑定 邮箱（收到邮件后确认）或绑定 绑定码；签到；我的额度；我的奖励；邀请奖励\n绑定码请在网站个人资料页生成，并回网站确认绑定。"
	case "绑定":
		if arg == "" {
			text = "发送：@机器人 绑定 你的注册邮箱，收到邮件后打开链接确认。也可在网站个人资料页生成绑定码后发送：@机器人 绑定 绑定码。"
		} else if strings.Contains(arg, "@") {
			if validBindingEmail(arg) {
				// A slow SMTP server must not delay the user's QQ acknowledgement.
				if err := s.send(ctx, c, *j, emailBindingAccepted, nil, seq); err != nil {
					return err
				}
				seq++
			}
			text = s.requestEmailBinding(ctx, c, e, arg)
			if text == emailBindingAccepted {
				return nil
			}
		} else {
			text = s.claimBinding(ctx, c, e, arg)
		}
	case "签到":
		var err error
		text, err = s.reward(ctx, c, *j, "checkin")
		if err != nil {
			text = "签到入账失败，请稍后重试。"
		}
	case "邀请奖励":
		var err error
		text, err = s.reward(ctx, c, *j, "invite")
		if err != nil {
			text = "奖励入账失败，请稍后重试。"
		}
	case "我的额度", "我的奖励":
		var balance, total float64
		err := s.db.QueryRowContext(ctx, `SELECT u.balance,COALESCE((SELECT SUM(amount) FROM qq_bot_rewards WHERE user_id=u.id),0) FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.deleted_at IS NULL AND u.status='active'`, c.AppID, j.MemberID).Scan(&balance, &total)
		if err != nil {
			text = "请先绑定网站账号。"
		} else {
			text = fmt.Sprintf("账户额度：$%.4f\n累计群活动奖励：$%.4f\n群内 AI 由站长免费提供，不扣你的余额。", balance, total)
		}
	case "新对话", "清空":
		if _, err := s.db.ExecContext(ctx, `DELETE FROM qq_bot_conversations WHERE app_id=$1 AND group_id=$2 AND member_id=$3`, c.AppID, j.GroupID, j.MemberID); err != nil {
			text = "清空失败，请重试。"
		} else {
			text = "已开启新对话。"
		}
	case "查看结果":
		old, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM qq_bot_jobs WHERE id=$1 AND app_id=$2 AND group_id=$3 AND member_id=$4 AND created_at>NOW()-INTERVAL '24 hours'`, arg, c.AppID, j.GroupID, j.MemberID))
		if err != nil {
			text = "未找到你的任务，或结果已超过 24 小时保留期。"
		} else if old.Status == "queued" || old.Status == "running" {
			text = "任务仍在处理中，请稍后查询。"
		} else if len(old.Image) > 0 {
			img = old.Image
		} else if old.ResultText != "" {
			text = old.ResultText
		} else {
			text = "任务没有可领取的结果，请查看失败提示后重新发起。"
		}
	default:
		kind := "chat"
		prompt := strings.TrimSpace(e.Content)
		if cmd == "生图" || cmd == "画图" {
			kind = "image"
			prompt = arg
			if !c.ImageEnabled {
				text = "生图功能尚未开启。"
			}
			if prompt == "" {
				text = "请发送：@机器人 生图 图片描述"
			}
		}
		if text != "" {
			break
		}
		if prompt == "" || len([]rune(prompt)) > 6000 {
			text = "请输入 1 到 6000 字的内容。"
			break
		}
		if err := s.reserveUsage(ctx, c, *j, kind); err != nil {
			text = err.Error()
			break
		}
		j.Kind = kind
		if err := s.send(ctx, c, *j, "正在处理，任务号："+j.ID+"。结果保留 24 小时，可发送“查看结果 "+j.ID+"”。", nil, 1); err != nil {
			return err
		}
		seq = 2
		var err error
		if kind == "image" {
			img, err = s.generateImage(ctx, c, prompt)
		} else {
			messages := s.history(ctx, c, *j)
			messages = append(messages, Message{Role: "user", Content: prompt})
			text, j.InputTokens, j.OutputTokens, err = s.chat(ctx, c, messages)
			if err == nil {
				s.saveHistory(ctx, c, *j, append(messages, Message{Role: "assistant", Content: text}))
			}
		}
		if err != nil {
			// Record upstream failure distinctly from delivery failures. Never expose raw upstream bodies.
			text = err.Error()
			j.Error = text
			_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET error=$2 WHERE id=$1`, j.ID, text)
			if sendErr := s.send(ctx, c, *j, text, nil, seq); sendErr != nil {
				return sendErr
			}
			return fmt.Errorf("模型调用失败：%s", text)
		}
		// Persist before sending to QQ. Delivery retries only reuse this saved result.
		if _, err = s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET result_text=$2,result_image=$3,image_type=$4,input_tokens=$5,output_tokens=$6 WHERE id=$1`, j.ID, text, img, "image/jpeg", j.InputTokens, j.OutputTokens); err != nil {
			return errors.New("生成成功但结果保存失败；未自动重新生成")
		}
	}
	if len(img) > 0 {
		return s.send(ctx, c, *j, text, img, seq)
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

// QQ allows five passive messages per incoming message. The first status reply
// consumes one slot; split the remaining text without ever exceeding that limit.
func splitReply(text string, maxChunks int) []string {
	r := []rune(text)
	const width = 1500
	if len(r) > width*maxChunks {
		r = append(r[:width*maxChunks-50], []rune("\n（回复过长，已截断；请让机器人分段解释。）")...)
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
