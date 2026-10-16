package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (scene MessageScene) value(key string) string {
	for _, item := range scene.Ext {
		name, value, ok := strings.Cut(item, "=")
		if ok && name == key && len(value) <= 1024 {
			return value
		}
	}
	return ""
}

func (e Event) referenceID() string {
	if id := e.MessageScene.value("ref_msg_idx"); id != "" {
		return id
	}
	if len(e.MessageReference.MessageID) <= 1024 {
		return e.MessageReference.MessageID
	}
	return ""
}

type botMessageReference struct {
	Content string
	Image   bool
}

func (s *Service) referencedMessage(ctx context.Context, c Config, group, id string) (*botMessageReference, error) {
	if id == "" || s.db == nil {
		return nil, nil
	}
	var ref botMessageReference
	err := s.db.QueryRowContext(ctx, `SELECT content,is_image FROM qq_bot_message_references
 WHERE app_id=$1 AND group_id=$2 AND (ref_idx=$3 OR message_id=$3)
 AND created_at>NOW()-INTERVAL '24 hours' ORDER BY created_at DESC LIMIT 1`, c.AppID, group, id).Scan(&ref.Content, &ref.Image)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("引用消息查询失败")
	}
	return &ref, nil
}

func (s *Service) rememberSentMessage(ctx context.Context, c Config, group string, raw []byte, content string, image bool) error {
	if s.db == nil {
		return nil
	}
	var sent struct {
		ID  string `json:"id"`
		Ext struct {
			Reference string `json:"ref_idx"`
		} `json:"ext_info"`
	}
	if json.Unmarshal(raw, &sent) != nil {
		return nil // Successful legacy QQ responses can omit message metadata.
	}
	if sent.ID == "" {
		sent.ID = sent.Ext.Reference
	}
	if sent.ID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO qq_bot_message_references(app_id,group_id,message_id,ref_idx,content,is_image)
 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(app_id,group_id,message_id)
 DO UPDATE SET ref_idx=EXCLUDED.ref_idx,content=EXCLUDED.content,is_image=EXCLUDED.is_image,created_at=NOW()`,
		c.AppID, group, sent.ID, sent.Ext.Reference, content, image)
	if err != nil {
		return errors.New("消息已发送，但引用记录保存失败")
	}
	return nil
}
