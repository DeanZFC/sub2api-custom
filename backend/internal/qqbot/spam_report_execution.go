package qqbot

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

var errSpamReportRevoked = errors.New("spam report authorization revoked")

// Token refresh can wait on the network or another worker. Only after it
// finishes do we acquire the final authorization locks. Configuration saves
// and incoming role changes cannot pass between this check and the HTTP call.
// The operation was already durably marked started, so even a crash after the
// HTTP call will never cause an automatic replay.
func (s *Service) executeSpamReportOperation(ctx context.Context, snapshot Config, operationID int64) error {
	token, err := s.token(ctx, snapshot)
	if err != nil {
		return err
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
	if c.AppID != snapshot.AppID || c.Sandbox != snapshot.Sandbox || spamReportPolicy(c) != spamReportPolicy(snapshot) {
		return errSpamReportRevoked
	}
	var group, member, message, kind, policy string
	var seconds int
	var expires time.Time
	var recallSucceeded bool
	err = tx.QueryRowContext(ctx, `SELECT i.group_id,i.member_id,r.message_id,o.kind,i.policy_hash,i.mute_seconds,o.expires_at,
 EXISTS(SELECT 1 FROM qq_bot_moderation_operations recall WHERE recall.incident_id=i.id AND recall.kind='recall' AND recall.status='succeeded')
 FROM qq_bot_moderation_operations o JOIN qq_bot_moderation_incidents i ON i.id=o.incident_id
 JOIN qq_bot_spam_reports r ON r.incident_id=i.id AND r.app_id=i.app_id AND r.sandbox=i.sandbox AND r.group_id=i.group_id
 AND r.member_id=i.member_id AND r.policy_hash=i.policy_hash AND r.status='queued'
 WHERE o.id=$1 AND o.status='started' AND i.app_id=$2 AND i.sandbox=$3
 AND (o.kind='mute' OR (o.kind='recall' AND o.message_id=r.message_id))
 AND i.reason IN ('spam_advertising','spam_phishing','spam_scam')`, operationID, c.AppID, c.Sandbox).
		Scan(&group, &member, &message, &kind, &policy, &seconds, &expires, &recallSucceeded)
	if errors.Is(err, sql.ErrNoRows) {
		return errSpamReportRevoked
	}
	if err != nil {
		return err
	}
	if policy != spamReportPolicy(c) || !c.reportsSpam(group) || c.moderationExempt(group, member) ||
		!expires.After(time.Now()) || (kind == "mute" && !recallSucceeded) {
		return errSpamReportRevoked
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,306))`, c.AppID+":"+group); err != nil {
		return err
	}
	source, err := s.loadSpamReportSource(ctx, tx, c, group, message)
	if err != nil {
		return err
	}
	if source == nil || source.MemberID != member || !expires.After(time.Now()) {
		return errSpamReportRevoked
	}
	base := "/v2/groups/" + url.PathEscape(group)
	var raw []byte
	if kind == "mute" {
		until := time.Now().Add(time.Duration(seconds) * time.Second).Format(time.RFC3339)
		raw, err = s.qqRequestMethodWithToken(ctx, c, token, http.MethodPost, base+"/restrict_chat_setting",
			map[string]any{"members": []any{map[string]string{"op": "add", "member_openid": member, "mute_expire_at": until}}})
	} else {
		raw, err = s.qqRequestMethodWithToken(ctx, c, token, http.MethodDelete, base+"/messages/"+url.PathEscape(message), nil)
	}
	if err != nil {
		return err
	}
	return validateSpamReportQQResponse(raw)
}

// QQ documents no response body (or {}) for these operations. An undocumented
// business-error or partial-result payload must not authorize a subsequent mute.
func validateSpamReportQQResponse(raw []byte) error {
	if len(raw) <= 1024 {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			return nil
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(trimmed, &object) == nil && object != nil && len(object) == 0 {
			return nil
		}
	}
	return errors.New("QQ 处罚响应不符合已公布成功格式，结果未确认")
}
