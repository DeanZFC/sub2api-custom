package qqbot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

// BindingEmailDelivery uses the site's existing account lookup and SMTP service.
// Install it before starting workers.
type BindingEmailDelivery interface {
	FrontendURL(context.Context) string
	LookupAccount(context.Context, string) (int64, string, error)
	SendEmail(context.Context, string, string, string) error
}

func (s *Service) SetBindingEmailDelivery(delivery BindingEmailDelivery) { s.bindingEmail = delivery }

var ErrEmailBindingInvalid = errors.New("绑定链接无效、已过期或已使用，请回群内重新发送绑定指令")

const emailBindingAccepted = "已收到邮箱绑定请求。请前往邮箱（含垃圾邮件）查收确认邮件，收到后请在 10 分钟内打开链接确认绑定。若邮箱未注册、已绑定或请求过于频繁，可能不会发送邮件；也可使用网站绑定码。请勿将确认链接发到群里。"

func validBindingEmail(raw string) bool {
	address, err := mail.ParseAddress(raw)
	return err == nil && address.Address == raw && len(raw) <= 254 && !strings.ContainsAny(raw, "\r\n\t ")
}

func emailBindingURL(base, token string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if err := validateURL(base); err != nil {
		return "", err
	}
	u, _ := url.Parse(base)
	// A fragment keeps the token out of HTTP access logs and Referer headers.
	u.Path += "/qqbot/bind"
	u.Fragment = "token=" + token
	return u.String(), nil
}

func bindingEmailBody(link string, e Event) string {
	return fmt.Sprintf(`<!doctype html><html><body style="font-family:sans-serif;line-height:1.7;max-width:600px;margin:32px auto;padding:16px">
<h2>确认绑定 QQ 群账号</h2><p>有人在 QQ 群中请求将以下 QQ 身份绑定到你的本站账号：</p>
<p>昵称：%s<br>QQ 身份 OpenID：%s<br>群 OpenID：%s</p>
<p><a href="%s">查看并确认绑定</a></p><p>链接 10 分钟内有效，只能使用一次。打开页面后还需点击确认，不需要登录网站。</p>
<p>仅在你本人发起请求且以上身份属于你时确认。不要转发链接；如果不是你发起，请忽略这封邮件，账号不会被绑定。</p></body></html>`,
		html.EscapeString(e.Author.Username), html.EscapeString(e.Author.MemberID), html.EscapeString(e.GroupID), html.EscapeString(link))
}

// Reserve attempts transactionally, across instances. Unknown addresses and
// delivery failures still consume the member's quota to prevent email flooding.
func (s *Service) reserveBindingEmails(ctx context.Context, app string, scopes []string, limits []int, cooldowns []int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for i, scope := range scopes {
		var count int
		err = tx.QueryRowContext(ctx, `INSERT INTO qq_bot_email_limits(app_id,scope,day,count) VALUES($1,$2,$3,1)
ON CONFLICT(app_id,scope,day) DO UPDATE SET count=qq_bot_email_limits.count+1,last_sent_at=NOW()
WHERE qq_bot_email_limits.count<$4 AND qq_bot_email_limits.last_sent_at<=NOW()-($5*INTERVAL '1 second') RETURNING count`,
			app, scope, day(time.Now()), limits[i], cooldowns[i]).Scan(&count)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) requestEmailBinding(ctx context.Context, c Config, e Event, email string) string {
	if !validBindingEmail(email) {
		return "邮箱格式不正确，请发送：@机器人 绑定 你的注册邮箱。"
	}
	if !c.Enabled || !c.allows(e.GroupID) || e.Author.MemberID == "" {
		return "该群暂不提供绑定服务。"
	}
	if s.bindingEmail == nil {
		return "邮箱绑定暂未配置，请使用网站个人资料页的绑定码。"
	}
	token := randomID() + randomID()
	link, err := emailBindingURL(s.bindingEmail.FrontendURL(ctx), token)
	if err != nil {
		return "网站地址尚未配置，请联系站长或使用网站绑定码。"
	}
	if err = s.reserveBindingEmails(ctx, c.AppID, []string{"member:" + e.Author.MemberID, "all"}, []int{5, 200}, []int{60, 0}); err != nil {
		return emailBindingAccepted
	}
	uid, recipient, err := s.bindingEmail.LookupAccount(ctx, email)
	if err != nil || uid <= 0 || !validBindingEmail(recipient) {
		return emailBindingAccepted
	}
	// Apply the recipient limit to the canonical registered email, not an alias
	// supplied in the group command. Never reveal account existence in QQ replies.
	if err = s.reserveBindingEmails(ctx, c.AppID, []string{"email:" + codeHash(strings.ToLower(recipient))}, []int{3}, []int{60}); err != nil {
		return emailBindingAccepted
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return emailBindingAccepted
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,273))`, c.AppID); err != nil {
		return emailBindingAccepted
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO qq_bot_email_bind_requests(token_hash,app_id,member_id,group_id,nickname,user_id,email,expires_at)
SELECT $1,$2,$3,$4,$5,u.id,u.email,NOW()+INTERVAL '10 minutes' FROM users u
WHERE u.id=$6 AND u.email=$7 AND u.status='active' AND u.deleted_at IS NULL
AND NOT EXISTS(SELECT 1 FROM qq_bot_bindings b WHERE b.app_id=$2 AND (b.user_id=u.id OR b.member_id=$3))
ON CONFLICT(app_id,member_id) DO UPDATE SET token_hash=EXCLUDED.token_hash,group_id=EXCLUDED.group_id,nickname=EXCLUDED.nickname,
user_id=EXCLUDED.user_id,email=EXCLUDED.email,expires_at=EXCLUDED.expires_at,created_at=NOW()`,
		codeHash(token), c.AppID, e.Author.MemberID, e.GroupID, limitText(e.Author.Username, 100), uid, recipient)
	if err != nil {
		return emailBindingAccepted
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return emailBindingAccepted
	}
	if err = tx.Commit(); err != nil {
		return emailBindingAccepted
	}
	if err = s.bindingEmail.SendEmail(ctx, recipient, "确认绑定 QQ 群账号", bindingEmailBody(link, e)); err != nil {
		slog.Warn("QQ bot binding email delivery failed; check SMTP settings")
		// Even if SMTP delivery is uncertain, an unsuccessful send cannot leave
		// a usable confirmation token behind. Do not retry automatically.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(cleanup, `DELETE FROM qq_bot_email_bind_requests WHERE token_hash=$1`, codeHash(token))
	}
	return emailBindingAccepted
}

type EmailBindingPreview struct {
	MemberID  string    `json:"member_id"`
	GroupID   string    `json:"group_id"`
	Nickname  string    `json:"nickname"`
	ExpiresAt time.Time `json:"expires_at"`
}

func validBindingToken(token string) bool {
	if len(token) != 48 {
		return false
	}
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Preview never consumes a token. Confirmation is a separate explicit POST.
func (s *Service) PreviewEmailBinding(ctx context.Context, token string) (EmailBindingPreview, error) {
	var p EmailBindingPreview
	if !validBindingToken(token) {
		return p, ErrEmailBindingInvalid
	}
	c, err := s.Config(ctx)
	if err != nil {
		return p, err
	}
	if !c.Enabled {
		return p, ErrDisabled
	}
	err = s.db.QueryRowContext(ctx, `SELECT r.member_id,r.group_id,r.nickname,r.expires_at FROM qq_bot_email_bind_requests r JOIN users u ON u.id=r.user_id
WHERE r.token_hash=$1 AND r.app_id=$2 AND r.expires_at>NOW() AND u.email=r.email AND u.status='active' AND u.deleted_at IS NULL
AND NOT EXISTS(SELECT 1 FROM qq_bot_bindings b WHERE b.app_id=r.app_id AND (b.user_id=r.user_id OR b.member_id=r.member_id))`, codeHash(token), c.AppID).Scan(&p.MemberID, &p.GroupID, &p.Nickname, &p.ExpiresAt)
	if err != nil || !c.allows(p.GroupID) {
		return EmailBindingPreview{}, ErrEmailBindingInvalid
	}
	return p, nil
}

func (s *Service) ConfirmEmailBinding(ctx context.Context, token, member string) (string, error) {
	if !validBindingToken(token) || member == "" {
		return "", ErrEmailBindingInvalid
	}
	c, err := s.Config(ctx)
	if err != nil {
		return "", err
	}
	if !c.Enabled {
		return "", ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	// Serializes concurrent email confirmations; unique binding constraints also
	// protect races with the authenticated website-code flow.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,273))`, c.AppID); err != nil {
		return "", err
	}
	var p EmailBindingPreview
	var uid int64
	err = tx.QueryRowContext(ctx, `SELECT r.user_id,r.member_id,r.group_id,r.nickname,r.expires_at FROM qq_bot_email_bind_requests r JOIN users u ON u.id=r.user_id
WHERE r.token_hash=$1 AND r.app_id=$2 AND r.expires_at>NOW() AND u.email=r.email AND u.status='active' AND u.deleted_at IS NULL FOR UPDATE OF r FOR NO KEY UPDATE OF u`, codeHash(token), c.AppID).Scan(&uid, &p.MemberID, &p.GroupID, &p.Nickname, &p.ExpiresAt)
	if err != nil || p.MemberID != member || !c.allows(p.GroupID) {
		return "", ErrEmailBindingInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_bindings(app_id,member_id,user_id,nickname,group_id) VALUES($1,$2,$3,$4,$5)`, c.AppID, p.MemberID, uid, p.Nickname, p.GroupID)
	if err != nil {
		return "", ErrEmailBindingInvalid
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM qq_bot_email_bind_requests WHERE app_id=$1 AND (user_id=$2 OR member_id=$3)`, c.AppID, uid, p.MemberID); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM qq_bot_bind_requests WHERE user_id=$1 AND app_id=$2`, uid, c.AppID); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return p.MemberID, nil
}
