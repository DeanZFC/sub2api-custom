package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
)

const configKey = "qq_bot_config"

type Encryptor interface {
	Encrypt(string) (string, error)
	Decrypt(string) (string, error)
}
type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) loadConfig(ctx context.Context, db queryRower) (Config, error) {
	c := DefaultConfig()
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, configKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, fmt.Errorf("QQ 配置读取失败")
	}
	for _, p := range []*string{&c.AppSecret, &c.APIKey, &c.ImageAPIKey} {
		if *p != "" {
			*p, err = s.encryptor.Decrypt(*p)
			if err != nil {
				return c, fmt.Errorf("QQ 凭据解密失败，请检查固定加密密钥")
			}
		}
	}
	return c, nil
}
func (s *Service) Config(ctx context.Context) (Config, error) { return s.loadConfig(ctx, s.db) }

// Authorize writes against the current settings, then keep them stable until
// commit. Always take this lock before any binding, reward or usage lock.
func (s *Service) lockConfig(ctx context.Context, tx *sql.Tx) (Config, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(71427101)`); err != nil {
		return Config{}, err
	}
	return s.loadConfig(ctx, tx)
}

func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("地址必须是完整的 HTTP(S) URL，不能包含密码、查询参数或片段")
	}
	return nil
}
func validateConfig(c Config) error {
	if c.ChatProtocol != "responses" && c.ChatProtocol != "chat_completions" {
		return errors.New("聊天接口模式不正确")
	}
	for _, u := range []string{c.BaseURL, c.ImageBaseURL} {
		if u != "" {
			if err := validateURL(u); err != nil {
				return err
			}
		}
	}
	for _, n := range []int{c.DailyChatLimit, c.DailyImageLimit, c.TotalDailyChatLimit, c.TotalDailyImageLimit, c.InviteDailyLimit} {
		if n < 0 || n > 100000 {
			return errors.New("每日次数必须在 0 到 100000 之间；0 表示禁止")
		}
	}
	if c.MaxOutputTokens < 64 || c.MaxOutputTokens > 8192 || c.HistoryTurns < 0 || c.HistoryTurns > 20 || c.CooldownSeconds < 0 || c.CooldownSeconds > 3600 {
		return errors.New("输出长度、历史轮数或冷却时间超出范围")
	}
	for _, n := range []float64{c.CheckinReward, c.InviteReward} {
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1000 || math.Abs(n*1e8-math.Round(n*1e8)) > 0.001 {
			return errors.New("奖励需为 0 到 1000 之间的数值，最多 8 位小数；0 表示关闭")
		}
	}
	if len(c.SystemPrompt) > 8000 || len(c.ChatModel) > 200 || len(c.ImageModel) > 200 || len(c.AllowedGroups) > 100 {
		return errors.New("配置内容过长")
	}
	for _, g := range c.AllowedGroups {
		if len(g) < 1 || len(g) > 128 || strings.ContainsAny(g, " \r\n\t") {
			return errors.New("群 OpenID 格式不正确")
		}
	}
	if c.ImageSize != "1024x1024" && c.ImageSize != "1536x1024" && c.ImageSize != "1024x1536" && c.ImageSize != "auto" {
		return errors.New("图片尺寸不正确")
	}
	if c.Enabled && (c.AppID == "" || c.AppSecret == "" || c.BaseURL == "" || c.APIKey == "" || c.ChatModel == "" || len(c.AllowedGroups) == 0) {
		return errors.New("启用前请填写 QQ 凭据、中转地址、API Key、聊天模型和允许使用的群")
	}
	if c.Enabled && c.ImageEnabled && c.ImageModel == "" {
		return errors.New("请填写生图模型")
	}
	if c.ImageBaseURL != "" && c.ImageAPIKey == "" {
		return errors.New("使用独立生图地址时，请填写独立生图 API Key")
	}
	return nil
}

func (s *Service) SaveConfig(ctx context.Context, c Config) (Config, error) {
	return s.saveConfig(ctx, func(Config) Config { return c })
}

// SaveBasicConfig preserves activity settings under the same lock as invitation edits.
func (s *Service) SaveBasicConfig(ctx context.Context, c Config) (Config, error) {
	return s.saveConfig(ctx, func(old Config) Config {
		c.InviteReward = old.InviteReward
		c.InviteDailyLimit = old.InviteDailyLimit
		return c
	})
}

func (s *Service) saveConfig(ctx context.Context, update func(Config) Config) (Config, error) {
	var c Config
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(71427101)`); err != nil {
		return c, err
	}
	old, err := s.loadConfig(ctx, tx)
	if err != nil {
		return c, err
	}
	c = update(old)
	c.AppID = strings.TrimSpace(c.AppID)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.ImageBaseURL = strings.TrimRight(strings.TrimSpace(c.ImageBaseURL), "/")
	if old.AppID != "" && old.AppID != c.AppID {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM qq_bot_bindings WHERE app_id=$1`, old.AppID).Scan(&n); err != nil {
			return c, err
		}
		if n > 0 {
			return c, errors.New("已有账号绑定，不能直接更换机器人 AppID")
		}
		if c.AppSecret == "" && !c.ClearAppSecret {
			return c, errors.New("更换 AppID 时请同时填写新的 AppSecret")
		}
	}
	if old.BaseURL != "" && old.BaseURL != c.BaseURL && c.APIKey == "" && !c.ClearAPIKey {
		return c, errors.New("修改中转地址时请重新填写 API Key")
	}
	if old.ImageBaseURL != "" && old.ImageBaseURL != c.ImageBaseURL && c.ImageAPIKey == "" && !c.ClearImageAPIKey {
		return c, errors.New("修改生图地址时请重新填写或清除生图 API Key")
	}
	merge := func(dst *string, prev string, clear bool) {
		if clear {
			*dst = ""
		} else if *dst == "" {
			*dst = prev
		}
	}
	merge(&c.AppSecret, old.AppSecret, c.ClearAppSecret)
	merge(&c.APIKey, old.APIKey, c.ClearAPIKey)
	merge(&c.ImageAPIKey, old.ImageAPIKey, c.ClearImageAPIKey)
	c.ClearAppSecret = false
	c.ClearAPIKey = false
	c.ClearImageAPIKey = false
	if err = validateConfig(c); err != nil {
		return c, err
	}
	stored := c
	for _, p := range []*string{&stored.AppSecret, &stored.APIKey, &stored.ImageAPIKey} {
		if *p != "" {
			if !s.encryptionReady {
				return c, errors.New("请先配置固定 TOTP_ENCRYPTION_KEY，再保存机器人密钥")
			}
			*p, err = s.encryptor.Encrypt(*p)
			if err != nil {
				return c, errors.New("凭据加密失败")
			}
		}
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return c, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, configKey, string(raw))
	if err != nil {
		return c, err
	}
	if err = tx.Commit(); err != nil {
		return c, err
	}
	return c.Public(), nil
}
