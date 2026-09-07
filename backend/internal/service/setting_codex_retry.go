package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const SettingKeyCodexPreOutputRetry = "codex_pre_output_retry"

type CodexPreOutputRetrySettings struct {
	Enabled               bool     `json:"enabled"`
	MaxRetries            int      `json:"max_retries"`
	RetryIntervalMs       int      `json:"retry_interval_ms"`
	MaxRetryWindowSeconds int      `json:"max_retry_window_seconds"`
	Keywords              []string `json:"keywords"`
}

func DefaultCodexPreOutputRetrySettings() CodexPreOutputRetrySettings {
	return CodexPreOutputRetrySettings{
		MaxRetries: 3, RetryIntervalMs: 1000, MaxRetryWindowSeconds: 30,
		Keywords: []string{"servers are currently overloaded", "server_is_overloaded", "slow_down"},
	}
}

func (v CodexPreOutputRetrySettings) Validate() error {
	if v.MaxRetries < 1 || v.MaxRetries > 10 {
		return fmt.Errorf("max_retries must be between 1 and 10")
	}
	if v.RetryIntervalMs < 100 || v.RetryIntervalMs > 10000 {
		return fmt.Errorf("retry_interval_ms must be between 100 and 10000")
	}
	if v.MaxRetryWindowSeconds < 1 || v.MaxRetryWindowSeconds > 300 {
		return fmt.Errorf("max_retry_window_seconds must be between 1 and 300")
	}
	if len(v.Keywords) > 50 || (v.Enabled && len(v.Keywords) == 0) {
		return fmt.Errorf("keywords must contain 1 to 50 entries when enabled")
	}
	for _, keyword := range v.Keywords {
		if strings.TrimSpace(keyword) == "" || len(keyword) > 256 {
			return fmt.Errorf("each keyword must contain 1 to 256 bytes")
		}
	}
	return nil
}

type cachedCodexPreOutputRetry struct {
	settings CodexPreOutputRetrySettings
	expires  time.Time
}

func (s *SettingService) GetCodexPreOutputRetrySettings(ctx context.Context) (CodexPreOutputRetrySettings, error) {
	value := DefaultCodexPreOutputRetrySettings()
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyCodexPreOutputRetry)
	if errors.Is(err, ErrSettingNotFound) || (err == nil && strings.TrimSpace(raw) == "") {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return DefaultCodexPreOutputRetrySettings(), fmt.Errorf("invalid codex retry settings: %w", err)
	}
	if err := value.Validate(); err != nil {
		return DefaultCodexPreOutputRetrySettings(), err
	}
	return value, nil
}

func (s *SettingService) SetCodexPreOutputRetrySettings(ctx context.Context, value CodexPreOutputRetrySettings) error {
	if err := value.Validate(); err != nil {
		return err
	}
	keywords := make([]string, 0, len(value.Keywords))
	seen := make(map[string]bool)
	for _, keyword := range value.Keywords {
		keyword = strings.TrimSpace(keyword)
		if key := strings.ToLower(keyword); !seen[key] {
			seen[key] = true
			keywords = append(keywords, keyword)
		}
	}
	value.Keywords = keywords
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyCodexPreOutputRetry, string(raw)); err != nil {
		return err
	}
	s.codexRetryCache.Store(&cachedCodexPreOutputRetry{settings: value, expires: time.Now().Add(time.Minute)})
	return nil
}

func (s *SettingService) GetCodexPreOutputRetrySettingsCached(ctx context.Context) CodexPreOutputRetrySettings {
	if s == nil || s.settingRepo == nil {
		return DefaultCodexPreOutputRetrySettings()
	}
	if cached := s.codexRetryCache.Load(); cached != nil && time.Now().Before(cached.expires) {
		return cached.settings
	}
	result, _, _ := s.codexRetrySF.Do(SettingKeyCodexPreOutputRetry, func() (any, error) {
		if cached := s.codexRetryCache.Load(); cached != nil && time.Now().Before(cached.expires) {
			return cached.settings, nil
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		value, err := s.GetCodexPreOutputRetrySettings(dbCtx)
		ttl := time.Minute
		if err != nil {
			ttl = 5 * time.Second
			if cached := s.codexRetryCache.Load(); cached != nil {
				value = cached.settings
			}
		}
		entry := &cachedCodexPreOutputRetry{settings: value, expires: time.Now().Add(ttl)}
		// A concurrent admin save wins over an in-flight cache refresh.
		prior := s.codexRetryCache.Load()
		if prior == nil || time.Now().After(prior.expires) {
			s.codexRetryCache.CompareAndSwap(prior, entry)
		}
		return s.codexRetryCache.Load().settings, nil
	})
	if value, ok := result.(CodexPreOutputRetrySettings); ok {
		return value
	}
	return DefaultCodexPreOutputRetrySettings()
}
