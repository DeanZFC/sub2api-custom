package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type setupContextKey struct{}

func managedSetupAllowed(ctx context.Context) bool {
	ok, _ := ctx.Value(setupContextKey{}).(bool)
	return ok
}

type SiteAIGroup struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
}
type SiteAIOptions struct {
	Groups          []SiteAIGroup `json:"groups"`
	FrontendURL     string        `json:"frontend_url"`
	EmailConfigured bool          `json:"email_configured"`
	CurrentQuota    float64       `json:"current_quota"`
	CurrentUsed     float64       `json:"current_used"`
}
type SiteAIConnection struct {
	BaseURL, Key            string
	KeyID, OwnerID, GroupID int64
	Created                 bool
}
type SiteAIProvider interface {
	Options(context.Context, int64, Config) (SiteAIOptions, error)
	Prepare(context.Context, *sql.Tx, int64, int64, float64, Config) (SiteAIConnection, error)
	Revoke(context.Context, int64, int64) error
}

func (s *Service) SetSiteAIProvider(p SiteAIProvider) { s.siteAI = p }
func (s *Service) SetupOptions(ctx context.Context, owner int64) (SiteAIOptions, error) {
	if s.siteAI == nil {
		return SiteAIOptions{}, errors.New("本站 AI 服务尚未就绪")
	}
	c, err := s.Config(ctx)
	if err != nil {
		return SiteAIOptions{}, err
	}
	return s.siteAI.Options(ctx, owner, c)
}

type SetupRequest struct {
	Config            Config            `json:"config"`
	Versions          map[string]string `json:"versions"`
	Sections          []string          `json:"sections"`
	RegenerateSiteKey bool              `json:"regenerate_site_key"`
	SiteQuota         float64           `json:"site_quota"`
}

// SaveSetup merges selected modules under the existing global configuration lock.
// The enable switch commits with its prerequisites; secrets never return to clients.
func (s *Service) SaveSetup(ctx context.Context, owner int64, req SetupRequest) (Config, error) {
	if len(req.Sections) == 0 || len(req.Sections) > len(configSections) {
		return Config{}, errors.New("请选择需要保存的配置模块")
	}
	if math.IsNaN(req.SiteQuota) || math.IsInf(req.SiteQuota, 0) || req.SiteQuota < 0 || req.SiteQuota > 1000000 {
		return Config{}, errors.New("API Key 总额度须在 0～1000000 USD 之间")
	}
	ctx = context.WithValue(ctx, setupContextKey{}, true)
	var created SiteAIConnection
	var previous Config
	var needsActivation bool
	result, err := s.saveConfigTransaction(ctx, func(tx *sql.Tx, old Config) (Config, error) {
		previous = old
		needsActivation = !old.Enabled
		next := old
		seen := map[string]bool{}
		for _, section := range req.Sections {
			if _, ok := configSections[section]; !ok || seen[section] {
				return old, errors.New("配置模块无效或重复")
			}
			seen[section] = true
			if req.Versions[section] == "" || req.Versions[section] != sectionVersion(old, section) {
				return old, ErrConfigConflict
			}
			var err error
			next, err = mergeConfigSection(next, req.Config, section)
			if err != nil {
				return old, err
			}
		}
		if seen["chat"] {
			if req.Config.AIConnection == "site" {
				if s.siteAI == nil || owner <= 0 {
					return old, errors.New("本站 AI 服务尚未就绪")
				}
				if !s.encryptionReady {
					return old, errors.New("请先配置固定 TOTP_ENCRYPTION_KEY")
				}
				provision := old
				provision.AppID = next.AppID
				if req.RegenerateSiteKey {
					provision.SiteAPIKeyID = 0
				}
				conn, e := s.siteAI.Prepare(ctx, tx, owner, req.Config.SiteGroupID, req.SiteQuota, provision)
				if e != nil {
					return old, e
				}
				if conn.Created {
					created = conn
				}
				next.BaseURL, next.APIKey = conn.BaseURL, conn.Key
				next.ClearAPIKey = false
				next.SiteAPIKeyID, next.SiteGroupID, next.SiteOwnerID = conn.KeyID, conn.GroupID, conn.OwnerID
			} else {
				next.AIConnection = "external"
				next.SiteAPIKeyID = 0
				next.SiteGroupID = 0
				next.SiteOwnerID = 0
				// Changing providers must never carry the previous site's credential.
				if old.AIConnection == "site" && strings.TrimSpace(next.APIKey) == "" {
					return old, errors.New("切换外部服务须填写该服务的 API Key")
				}
			}
		}
		if seen["groups"] {
			reconcileRemovedGroups(&next)
		}
		return next, nil
	}, "", func(tx *sql.Tx, c Config) error {
		if c.Enabled && needsActivation {
			if err := validateSetupActivation(ctx, tx, c); err != nil {
				return err
			}
		}
		// Include dependent scope changes in history, not just requested modules.
		history := []ConfigChange{}
		if _, err := readActivityConfig(ctx, tx, configHistoryKey, &history); err != nil {
			return err
		}
		for section := range configSections {
			if sectionVersion(previous, section) != sectionVersion(c, section) {
				history = append([]ConfigChange{{At: time.Now(), Section: section, Before: publicSection(previous, section), After: publicSection(c, section)}}, history...)
			}
		}
		if len(history) > 30 {
			history = history[:30]
		}
		raw, err := json.Marshal(history)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, configHistoryKey, string(raw))
		return err
	})
	if err != nil && created.Created {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if e := s.siteAI.Revoke(cleanup, created.KeyID, created.OwnerID); e != nil {
			return result, fmt.Errorf("%w；新建专用 Key 清理失败，请在 API Key 管理撤销 ID %d", err, created.KeyID)
		}
	}
	if err == nil && previous.SiteAPIKeyID > 0 && previous.SiteAPIKeyID != result.SiteAPIKeyID && s.siteAI != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if e := s.siteAI.Revoke(cleanup, previous.SiteAPIKeyID, previous.SiteOwnerID); e != nil {
			result.SetupWarning = fmt.Sprintf("配置已保存，旧专用 Key 撤销失败，请在 API Key 管理撤销 ID %d", previous.SiteAPIKeyID)
		}
	}
	return result, err
}
func reconcileRemovedGroups(c *Config) {
	filter := func(ids []string) []string {
		out := []string{}
		for _, id := range ids {
			if c.allows(id) {
				out = append(out, id)
			}
		}
		return out
	}
	c.Autonomous.Groups = filter(c.Autonomous.Groups)
	c.Moderation.Groups = filter(c.Moderation.Groups)
	// Preserve explicit notification scope; never turn a removed last group into all groups.
	if len(c.PriceNotifications.Groups) > 0 {
		c.PriceNotifications.Groups = filter(c.PriceNotifications.Groups)
		if len(c.PriceNotifications.Groups) == 0 {
			c.PriceNotifications.Enabled = false
		}
	}
	if len(c.HealthNotifications.Groups) > 0 {
		c.HealthNotifications.Groups = filter(c.HealthNotifications.Groups)
		if len(c.HealthNotifications.Groups) == 0 {
			c.HealthNotifications.Enabled = false
		}
	}
	policies := []GroupPolicy{}
	for _, p := range c.GroupPolicies {
		if c.allows(p.GroupID) {
			policies = append(policies, p)
		}
	}
	c.GroupPolicies = policies
	admins := []GroupAdmin{}
	for _, a := range c.GroupAdmins {
		if c.allows(a.GroupID) {
			admins = append(admins, a)
		}
	}
	c.GroupAdmins = admins
	exemptions := []ModerationExemption{}
	for _, e := range c.Moderation.Exemptions {
		if c.allows(e.GroupID) {
			exemptions = append(exemptions, e)
		}
	}
	c.Moderation.Exemptions = exemptions
	if !c.Autonomous.InheritGroups && len(c.Autonomous.Groups) == 0 {
		c.Autonomous.Enabled = false
	}
	if !c.Moderation.InheritGroups && len(c.Moderation.Groups) == 0 {
		c.Moderation.Enabled = false
	}
}
