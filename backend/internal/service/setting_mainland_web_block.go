package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/mainlandip"
)

const SettingKeyMainlandWebBlockEnabled = "mainland_web_block_enabled"

const mainlandWebBlockCacheTTL = 5 * time.Second

type cachedMainlandWebBlock struct {
	enabled   bool
	expiresAt time.Time
}

func (s *SettingService) mainlandWebBlockConfigDefault() bool {
	return s != nil && s.cfg != nil && s.cfg.Security.MainlandWebBlock.Enabled
}

func (s *SettingService) parseMainlandWebBlockEnabled(settings map[string]string) bool {
	if value, ok := settings[SettingKeyMainlandWebBlockEnabled]; ok {
		return value == "true"
	}
	return s.mainlandWebBlockConfigDefault()
}

// IsMainlandWebBlockEnabled uses a short process-local cache. Successful admin
// writes publish immediately; other instances observe database changes within
// the TTL. Transient read errors retain the last known value.
func (s *SettingService) IsMainlandWebBlockEnabled(ctx context.Context) bool {
	if s == nil || s.settingRepo == nil {
		return s.mainlandWebBlockConfigDefault()
	}
	if cached := s.mainlandWebBlockCache.Load(); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.enabled
	}
	s.mainlandWebBlockMu.Lock()
	defer s.mainlandWebBlockMu.Unlock()
	if cached := s.mainlandWebBlockCache.Load(); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.enabled
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyMainlandWebBlockEnabled)
	enabled := s.mainlandWebBlockConfigDefault()
	ttl := mainlandWebBlockCacheTTL
	switch {
	case err == nil:
		enabled = raw == "true"
	case errors.Is(err, ErrSettingNotFound):
		// A deployment default applies only until an administrator persists a value.
	default:
		slog.Warn("mainland_web_block.settings_read_failed", "error", err)
		ttl = time.Second
		if cached := s.mainlandWebBlockCache.Load(); cached != nil {
			enabled = cached.enabled
		}
	}
	s.mainlandWebBlockCache.Store(&cachedMainlandWebBlock{enabled: enabled, expiresAt: time.Now().Add(ttl)})
	return enabled
}

func (s *SettingService) publishMainlandWebBlockEnabled(enabled bool) {
	s.mainlandWebBlockMu.Lock()
	defer s.mainlandWebBlockMu.Unlock()
	s.mainlandWebBlockCache.Store(&cachedMainlandWebBlock{enabled: enabled, expiresAt: time.Now().Add(mainlandWebBlockCacheTTL)})
}

// ValidateMainlandWebBlockEnabled checks local country data before any admin
// settings writes. Disabled or omitted switches never require a data file.
func (s *SettingService) ValidateMainlandWebBlockEnabled(enabled bool) error {
	if !enabled {
		return nil
	}
	var err error
	if s != nil && s.cfg != nil && strings.TrimSpace(s.cfg.Security.MainlandWebBlock.CIDRFile) != "" {
		_, err = mainlandip.Load(strings.TrimSpace(s.cfg.Security.MainlandWebBlock.CIDRFile))
	} else {
		_, err = mainlandip.Default()
	}
	if err != nil {
		return infraerrors.BadRequest("INVALID_MAINLAND_WEB_BLOCK_DATA", fmt.Sprintf("Cannot enable mainland web access restriction: %v", err))
	}
	return nil
}

// SetMainlandWebBlockEnabled commits this switch only after the rest of the
// admin settings request has completed successfully, then publishes immediately.
// The single-key write prevents an error from a later payment/settings step
// from making a failed admin save unexpectedly activate a regional restriction.
func (s *SettingService) SetMainlandWebBlockEnabled(ctx context.Context, enabled bool) error {
	if err := s.ValidateMainlandWebBlockEnabled(enabled); err != nil {
		return err
	}
	s.mainlandWebBlockMu.Lock()
	defer s.mainlandWebBlockMu.Unlock()
	if err := s.settingRepo.SetMultiple(ctx, map[string]string{SettingKeyMainlandWebBlockEnabled: strconv.FormatBool(enabled)}); err != nil {
		return err
	}
	s.mainlandWebBlockCache.Store(&cachedMainlandWebBlock{enabled: enabled, expiresAt: time.Now().Add(mainlandWebBlockCacheTTL)})
	return nil
}
