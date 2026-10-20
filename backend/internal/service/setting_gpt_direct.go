package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const SettingKeyGPTDirectConfig = "gpt_direct_config"

// GPTDirectConfig is administrator-owned. Only Public() may be sent to users.
type GPTDirectConfig struct {
	Enabled              bool   `json:"enabled"`
	BaseURL              string `json:"base_url"`
	TenantSlug           string `json:"tenant_slug"`
	PurchaseURL          string `json:"purchase_url"`
	ButtonText           string `json:"button_text"`
	Description          string `json:"description"`
	SupportURL           string `json:"support_url"` // Legacy key; accepts contact text or an HTTP(S) link.
	LotteryRewardEnabled bool   `json:"lottery_reward_enabled"`
	LotteryChances       int    `json:"lottery_chances"`
}

type GPTDirectPublicConfig struct {
	ReceiptScope         string `json:"receipt_scope"`
	Enabled              bool   `json:"enabled"`
	PurchaseURL          string `json:"purchase_url"`
	ButtonText           string `json:"button_text"`
	Description          string `json:"description"`
	SupportURL           string `json:"support_url"`
	LotteryRewardEnabled bool   `json:"lottery_reward_enabled"`
	LotteryChances       int    `json:"lottery_chances"`
}

func (v GPTDirectConfig) Public() GPTDirectPublicConfig {
	scope := sha256.Sum256([]byte(v.BaseURL + ":" + v.TenantSlug))
	return GPTDirectPublicConfig{
		ReceiptScope: fmt.Sprintf("%x", scope[:12]),
		Enabled:      v.Enabled && v.BaseURL != "", PurchaseURL: v.PurchaseURL,
		ButtonText: v.ButtonText, Description: v.Description, SupportURL: v.SupportURL,
		LotteryRewardEnabled: v.LotteryRewardEnabled, LotteryChances: v.LotteryChances,
	}
}

func parseGPTDirectConfig(raw string) (GPTDirectConfig, error) {
	// Defaults only seed a previously unconfigured installation. Saved settings,
	// including explicitly cleared links, always take precedence.
	if raw == "" {
		return GPTDirectConfig{
			BaseURL:        "https://ctai.chengtingkj.org",
			TenantSlug:     "ctai",
			PurchaseURL:    "https://wzyp.cn/shop/ctai/mgitov",
			LotteryChances: 1,
		}, nil
	}
	v := GPTDirectConfig{TenantSlug: "ctai", LotteryChances: 1}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return GPTDirectConfig{}, errors.New("invalid GPT direct settings")
	}
	return v, nil
}

func gptDirectEnabled(raw string) bool {
	v, err := parseGPTDirectConfig(raw)
	return err == nil && v.Public().Enabled
}

func (s *SettingService) GetGPTDirectConfig(ctx context.Context) (GPTDirectConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyGPTDirectConfig)
	if errors.Is(err, ErrSettingNotFound) {
		err = nil
	}
	if err != nil {
		return GPTDirectConfig{}, err
	}
	return parseGPTDirectConfig(raw)
}

var gptDirectTenantPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

func (s *SettingService) SaveGPTDirectConfig(ctx context.Context, v GPTDirectConfig) (GPTDirectConfig, error) {
	v.BaseURL = strings.TrimRight(strings.TrimSpace(v.BaseURL), "/")
	v.TenantSlug = strings.ToLower(strings.TrimSpace(v.TenantSlug))
	if v.TenantSlug == "" {
		v.TenantSlug = "ctai"
	}
	v.PurchaseURL = strings.TrimSpace(v.PurchaseURL)
	v.SupportURL = strings.TrimSpace(v.SupportURL)
	v.ButtonText = strings.TrimSpace(v.ButtonText)
	v.Description = strings.TrimSpace(v.Description)
	if v.LotteryChances < 0 || v.LotteryChances > 1000 || (v.LotteryRewardEnabled && v.LotteryChances < 1) {
		return v, errors.New("GPT direct lottery chances must be between 1 and 1000 when enabled")
	}
	if !gptDirectTenantPattern.MatchString(v.TenantSlug) || len([]rune(v.ButtonText)) > 40 || len([]rune(v.Description)) > 2000 {
		return v, errors.New("invalid GPT direct settings")
	}
	if len([]rune(v.SupportURL)) > 2048 {
		return v, errors.New("GPT direct support content must not exceed 2048 characters")
	}
	for _, address := range []string{v.BaseURL, v.PurchaseURL} {
		if address == "" {
			continue
		}
		u, err := url.Parse(address)
		if err != nil || len(address) > 2048 || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
			return v, errors.New("invalid GPT direct URL")
		}
	}
	if v.BaseURL != "" {
		u, _ := url.Parse(v.BaseURL)
		if u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return v, errors.New("GPT direct service URL must be an origin without path, query or fragment")
		}
	}
	if v.Enabled && (v.BaseURL == "" || v.PurchaseURL == "") {
		return v, errors.New("GPT direct service URL and purchase URL are required")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v, err
	}
	if err = s.settingRepo.Set(ctx, SettingKeyGPTDirectConfig, string(raw)); err != nil {
		return v, err
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return v, nil
}
