package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"html"
	"sort"
	"strings"
)

type qqBotSiteAI struct {
	keys     *APIKeyService
	groups   GroupRepository
	users    UserRepository
	accounts AccountRepository
	settings *SettingService
	email    *EmailService
	baseURL  string
}

func (p *qqBotSiteAI) Options(ctx context.Context, owner int64, c qqbot.Config) (qqbot.SiteAIOptions, error) {
	out := qqbot.SiteAIOptions{Groups: []qqbot.SiteAIGroup{}, FrontendURL: p.settings.GetFrontendURL(ctx)}
	mail, mailErr := p.email.GetSMTPConfig(ctx)
	out.EmailConfigured = mailErr == nil && mail != nil && mail.Host != "" && mail.From != ""
	user, err := p.users.GetByID(ctx, owner)
	if err != nil {
		return out, err
	}
	groups, err := p.groups.ListActive(ctx)
	if err != nil {
		return out, err
	}
	for _, g := range groups {
		if g.Platform != PlatformOpenAI && g.Platform != PlatformComposite {
			continue
		}
		if _, e := p.keys.loadBindableAPIKeyGroup(ctx, user, g.ID); e != nil {
			continue
		}
		models := []string{}
		seen := map[string]bool{}
		add := func(m string) {
			m = strings.TrimSpace(m)
			if m != "" && !strings.ContainsAny(m, "*?") && !seen[m] && g.ModelAllowlist.Allows(m) {
				seen[m] = true
				models = append(models, m)
			}
		}
		for _, m := range g.ModelAllowlist.Models {
			add(m)
		}
		accounts, e := p.accounts.ListSchedulableByGroupID(ctx, g.ID)
		if e != nil {
			return out, e
		}
		for _, a := range accounts {
			for m := range a.GetModelMapping() {
				add(m)
			}
		}
		for _, m := range defaultModelsListCandidateIDs(g.Platform) {
			add(m)
		}
		sort.Strings(models)
		out.Groups = append(out.Groups, qqbot.SiteAIGroup{ID: g.ID, Name: g.Name, Models: models})
	}
	if c.SiteAPIKeyID > 0 {
		if key, e := p.keys.GetByID(ctx, c.SiteAPIKeyID); e == nil && key.UserID == c.SiteOwnerID {
			out.CurrentQuota = key.Quota
			out.CurrentUsed = key.QuotaUsed
		}
	}
	return out, nil
}
func (p *qqBotSiteAI) Prepare(ctx context.Context, tx *sql.Tx, owner, groupID int64, quota float64, old qqbot.Config) (qqbot.SiteAIConnection, error) {
	var out qqbot.SiteAIConnection
	if groupID <= 0 {
		return out, errors.New("请选择本站分组")
	}
	user, err := p.users.GetByID(ctx, owner)
	if err != nil {
		return out, err
	}
	group, err := p.keys.loadBindableAPIKeyGroup(ctx, user, groupID)
	if err != nil {
		return out, err
	}
	if !group.IsActive() || (group.Platform != PlatformOpenAI && group.Platform != PlatformComposite) {
		return out, errors.New("本站 AI 当前支持 OpenAI 或合成分组")
	}
	out.BaseURL = p.baseURL
	out.GroupID = groupID
	out.OwnerID = owner
	if old.AIConnection == "site" && old.SiteGroupID == groupID && old.SiteAPIKeyID > 0 {
		key, e := p.keys.GetByID(ctx, old.SiteAPIKeyID)
		if e != nil {
			return out, errors.New("机器人专用 Key 不存在，请重新配置本站 AI")
		}
		if key.UserID != old.SiteOwnerID || key.Key != APIKeyStoredValue(old.APIKey) || key.GroupID == nil || *key.GroupID != groupID || old.APIKey == "" {
			return out, errors.New("机器人专用 Key 身份不匹配")
		}
		if key.Status != StatusActive || key.IsExpired() || key.IsQuotaExhausted() {
			return out, errors.New("机器人专用 Key 已停用或额度耗尽，请在 API Key 管理恢复或调整额度")
		}
		if quota != key.Quota {
			return out, errors.New("已有专用 Key 的总额度请在 API Key 管理调整，不会通过重新保存重置用量")
		}
		out.OwnerID = key.UserID
		out.KeyID = key.ID
		out.Key = old.APIKey
		return out, nil
	}
	// The dedicated key and encrypted bot configuration commit in the same SQL transaction.
	// A validation failure or process crash cannot leave an active orphan key.
	if err := p.keys.checkAPIKeyCreateLimits(ctx, owner); err != nil {
		return out, err
	}
	raw, err := p.keys.GenerateKey()
	if err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name,group_id,status,quota) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		owner, APIKeyStoredValue(raw), html.EscapeString("QQ bot "+old.AppID), groupID, StatusActive, quota).Scan(&out.KeyID); err != nil {
		return out, err
	}
	out.Key = raw
	return out, nil
}
func (p *qqBotSiteAI) Revoke(ctx context.Context, keyID, owner int64) error {
	return p.keys.Delete(ctx, keyID, owner)
}
func qqBotInternalBaseURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/v1", port) }
