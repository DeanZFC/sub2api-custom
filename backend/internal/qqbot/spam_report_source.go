package qqbot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

func (c Config) reportsSpam(group string) bool {
	if !c.Enabled || !c.Moderation.ReportEnabled || !c.allows(group) {
		return false
	}
	for _, g := range c.Moderation.Groups {
		if group == g {
			return true
		}
	}
	return false
}

// Model and credential changes invalidate a verdict as well as policy changes.
func spamReportPolicy(c Config) string {
	raw, _ := json.Marshal([]any{"spam-report-v1", c.Enabled, c.AppID, c.Sandbox, c.AllowedGroups,
		c.Moderation, c.GroupAdmins, c.BaseURL, c.APIKey, c.ChatModel, c.ChatProtocol})
	mac := hmac.New(sha256.New, []byte(c.AppSecret))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

type spamReportSource struct {
	MessageID string
	MemberID  string
	Content   string
	SentAt    time.Time
}

func spamReportTextEligible(e Event) bool {
	return !e.Author.Bot && e.Author.MemberRole == "member" && e.MessageType == 0 &&
		e.MessageReference.MessageID == "" && e.MessageScene.value("ref_msg_idx") == "" &&
		len(e.Attachments) == 0 && len(e.MsgElements) == 0 &&
		strings.TrimSpace(e.Content) != "" && utf8.ValidString(e.Content) &&
		len(e.Content) <= 24000 && utf8.RuneCountInString(e.Content) <= 6000 && !containsRewardCode(e.Content)
}

// Capture only signed original callbacks. Quote bodies never establish authorship.
// This path runs before history, AI chat and ambient-message routing gates.
func (s *Service) rememberSpamReportSource(ctx context.Context, snapshot Config, e Event) error {
	if !snapshot.reportsSpam(e.GroupID) || e.ID == "" || len(e.ID) > 1024 ||
		e.Author.MemberID == "" || len(e.Author.MemberID) > 128 || len(e.Author.MemberRole) > 32 {
		return nil
	}
	var stamp string
	if json.Unmarshal(e.Timestamp, &stamp) != nil {
		return nil
	}
	sent, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || time.Since(sent) > 120*time.Second || sent.After(time.Now().Add(5*time.Second)) {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lockConfig(ctx, tx)
	if err != nil {
		return err
	}
	if c.AppID != snapshot.AppID || c.Sandbox != snapshot.Sandbox || !c.reportsSpam(e.GroupID) {
		return nil
	}
	// Bound this evidence buffer even for unmentioned, high-volume group traffic.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,306))`, c.AppID+":"+e.GroupID); err != nil {
		return err
	}
	eligible := spamReportTextEligible(e) && !c.moderationExempt(e.GroupID, e.Author.MemberID)
	ciphertext := ""
	mac := hmac.New(sha256.New, []byte(c.AppSecret))
	_, _ = mac.Write([]byte(e.Content))
	contentHash := hex.EncodeToString(mac.Sum(nil))
	if eligible {
		if !s.encryptionReady || s.encryptor == nil {
			return errors.New("举报原消息证据加密不可用")
		}
		ciphertext, err = s.encryptor.Encrypt(e.Content)
		if err != nil {
			return errors.New("举报原消息证据加密失败")
		}
	}
	// QQ may deliver the same message through both event types. Conflicting
	// immutable metadata fails closed; duplicates cannot refresh the expiry.
	_, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_spam_report_sources
 (app_id,sandbox,group_id,message_id,ref_idx,member_id,member_role,is_bot,eligible,content_encrypted,policy_hash,sent_at,content_hash)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
 ON CONFLICT(app_id,sandbox,group_id,message_id) DO UPDATE SET
 member_role=CASE WHEN EXCLUDED.member_role<>'member' THEN EXCLUDED.member_role ELSE qq_bot_spam_report_sources.member_role END,
 is_bot=qq_bot_spam_report_sources.is_bot OR EXCLUDED.is_bot,
 eligible=
 qq_bot_spam_report_sources.eligible AND EXCLUDED.eligible
 AND qq_bot_spam_report_sources.member_id=EXCLUDED.member_id
 AND qq_bot_spam_report_sources.member_role=EXCLUDED.member_role
 AND qq_bot_spam_report_sources.ref_idx=EXCLUDED.ref_idx
 AND qq_bot_spam_report_sources.sent_at=EXCLUDED.sent_at
	 AND qq_bot_spam_report_sources.content_hash=EXCLUDED.content_hash
 AND qq_bot_spam_report_sources.policy_hash=EXCLUDED.policy_hash`, c.AppID, c.Sandbox, e.GroupID, e.ID,
		e.MessageScene.value("msg_idx"), e.Author.MemberID, e.Author.MemberRole, e.Author.Bot, eligible, ciphertext, spamReportPolicy(c), sent, contentHash)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM qq_bot_spam_report_sources WHERE app_id=$1 AND sandbox=$2 AND group_id=$3
 AND (sent_at<clock_timestamp()-INTERVAL '120 seconds' OR message_id IN
 (SELECT message_id FROM qq_bot_spam_report_sources WHERE app_id=$1 AND sandbox=$2 AND group_id=$3
 ORDER BY sent_at DESC,message_id DESC OFFSET 2000))`, c.AppID, c.Sandbox, e.GroupID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

type spamReportQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Service) spamReportSource(ctx context.Context, c Config, group, reference string) (*spamReportSource, error) {
	return s.loadSpamReportSource(ctx, s.db, c, group, reference)
}

func (s *Service) loadSpamReportSource(ctx context.Context, db spamReportQueryer, c Config, group, reference string) (*spamReportSource, error) {
	if !c.reportsSpam(group) || reference == "" || len(reference) > 1024 {
		return nil, nil
	}
	// Resolve ambiguity before checking eligibility. An invalid colliding record
	// must not cause an apparently unique match against a different sender.
	rows, err := db.QueryContext(ctx, `SELECT s.message_id,s.member_id,s.content_encrypted,s.sent_at,
 s.eligible AND NOT s.is_bot AND s.member_role='member' AND s.policy_hash=$5
 AND NOT EXISTS(SELECT 1 FROM qq_bot_spam_report_sources newer WHERE newer.app_id=s.app_id
 AND newer.sandbox=s.sandbox AND newer.group_id=s.group_id AND newer.member_id=s.member_id
 AND newer.sent_at>=s.sent_at AND (newer.member_role<>'member' OR newer.is_bot))
 FROM qq_bot_spam_report_sources s WHERE s.app_id=$1 AND s.sandbox=$2 AND s.group_id=$3
 AND (s.message_id=$4 OR s.ref_idx=$4) AND s.sent_at>clock_timestamp()-INTERVAL '115 seconds'
 AND s.sent_at<=clock_timestamp()+INTERVAL '5 seconds' LIMIT 2`, c.AppID, c.Sandbox, group, reference, spamReportPolicy(c))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var source spamReportSource
	var encrypted string
	var eligible bool
	count := 0
	for rows.Next() {
		if err = rows.Scan(&source.MessageID, &source.MemberID, &encrypted, &source.SentAt, &eligible); err != nil {
			return nil, err
		}
		count++
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if count != 1 || !eligible || c.moderationExempt(group, source.MemberID) {
		return nil, nil
	}
	if s.encryptor == nil {
		return nil, errors.New("举报证据解密不可用")
	}
	source.Content, err = s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, errors.New("举报证据解密失败")
	}
	if strings.TrimSpace(source.Content) == "" {
		return nil, nil
	}
	return &source, nil
}

func (s *Service) confirmSpamReport(ctx context.Context, snapshot Config, reportID string, source spamReportSource, category, reason string) (bool, error) {
	if category != "advertising" && category != "phishing" && category != "scam" {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lockConfig(ctx, tx)
	if err != nil {
		return false, err
	}
	if c.AppID != snapshot.AppID || c.Sandbox != snapshot.Sandbox || spamReportPolicy(c) != spamReportPolicy(snapshot) {
		return false, nil
	}
	var group, status, policy, message, member string
	err = tx.QueryRowContext(ctx, `SELECT group_id,status,policy_hash,message_id,member_id FROM qq_bot_spam_reports
 WHERE id=$1 AND app_id=$2 AND sandbox=$3 FOR UPDATE`, reportID, c.AppID, c.Sandbox).Scan(&group, &status, &policy, &message, &member)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != "evaluating" || policy != spamReportPolicy(c) || message != source.MessageID || member != source.MemberID ||
		!c.reportsSpam(group) || c.moderationExempt(group, member) || time.Until(source.SentAt.Add(115*time.Second)) < 5*time.Second {
		return false, nil
	}
	current, err := s.loadSpamReportSource(ctx, tx, c, group, message)
	if err != nil {
		return false, err
	}
	if current == nil || current.MemberID != source.MemberID || current.Content != source.Content || !current.SentAt.Equal(source.SentAt) {
		return false, nil
	}
	incident := randomID()
	if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_moderation_incidents
 (id,app_id,sandbox,group_id,member_id,policy_hash,reason,mute_seconds) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		incident, c.AppID, c.Sandbox, group, member, policy, "spam_"+category, c.Moderation.ReportMuteMinutes*60); err != nil {
		return false, err
	}
	// Recall must be confirmed before the executor may mute. Never activate the
	// anti-flood cooldown: no other messages were judged by this report.
	if _, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_moderation_operations(incident_id,kind,message_id,expires_at)
 VALUES($1,'recall',$2,$3),($1,'mute','',$3)`, incident, message, source.SentAt.Add(115*time.Second)); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_spam_reports SET status='queued',category=$2,reason=$3,incident_id=$4,finished_at=NOW() WHERE id=$1`, reportID, category, limitText(reason, 250), incident); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Service) maintainSpamReports(ctx context.Context) {
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_spam_report_sources WHERE sent_at<NOW()-INTERVAL '120 seconds'`)
	_, _ = s.db.ExecContext(ctx, `UPDATE qq_bot_spam_reports SET status='failed',reason='审核中断或超时，未采取处罚',finished_at=NOW()
 WHERE status='evaluating' AND created_at<NOW()-INTERVAL '2 minutes'`)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM qq_bot_spam_reports WHERE created_at<NOW()-INTERVAL '30 days'`)
}

// Reverting a configuration must not revive a previously invalidated verdict.
// This runs under SaveConfig's exclusive lock, including sandbox changes when
// automatic anti-flood enforcement itself is disabled.
func resetSpamReports(ctx context.Context, tx *sql.Tx, old, current Config) error {
	if (!old.Moderation.ReportEnabled && !current.Moderation.ReportEnabled) || spamReportPolicy(old) == spamReportPolicy(current) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE qq_bot_spam_reports r SET status='cancelled',reason='审核配置或白名单已变更，取消未执行处罚',finished_at=NOW()
 WHERE app_id=$1 AND (status='evaluating' OR (status='queued' AND EXISTS
 (SELECT 1 FROM qq_bot_moderation_operations o WHERE o.incident_id=r.incident_id AND o.status IN ('queued','started'))))`, old.AppID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE qq_bot_moderation_operations o SET status='cancelled',error='举报配置或白名单已变更，取消处罚',finished_at=NOW()
 FROM qq_bot_moderation_incidents i WHERE i.id=o.incident_id AND i.app_id=$1
 AND i.reason IN ('spam_advertising','spam_phishing','spam_scam') AND o.status='queued'`, old.AppID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM qq_bot_spam_report_sources WHERE app_id=$1`, old.AppID)
	return err
}
