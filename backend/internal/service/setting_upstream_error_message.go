package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const SettingKeyUpstreamErrorMessage = "upstream_error_message"

const maxUpstreamErrorMessageChars = 500

type cachedUpstreamErrorMessage struct {
	message   string
	expiresAt time.Time
}

func normalizeUpstreamErrorMessage(message string) (string, error) {
	message = strings.TrimSpace(message)
	if utf8.RuneCountInString(message) > maxUpstreamErrorMessageChars {
		return "", infraerrors.BadRequest("INVALID_UPSTREAM_ERROR_MESSAGE", "upstream_error_message must not exceed 500 characters")
	}
	return message, nil
}

// GetUpstreamErrorMessage returns the administrator's display-only override.
// An empty string keeps default error handling. The cache keeps stream emission
// off the database hot path; writers publish the new value immediately.
func (s *SettingService) GetUpstreamErrorMessage(ctx context.Context) string {
	if s == nil || s.settingRepo == nil {
		return ""
	}
	if cached := s.upstreamErrorMessageCache.Load(); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.message
	}
	s.upstreamErrorMessageMu.Lock()
	defer s.upstreamErrorMessageMu.Unlock()
	if cached := s.upstreamErrorMessageCache.Load(); cached != nil && time.Now().Before(cached.expiresAt) {
		return cached.message
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyUpstreamErrorMessage)
	ttl := 60 * time.Second
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		slog.Warn("upstream_error_message.settings_read_failed", "error", err)
		ttl = 5 * time.Second
		// Preserve an existing override through a transient DB outage. Dropping it
		// here would unexpectedly disclose upstream details after it was enabled.
		if cached := s.upstreamErrorMessageCache.Load(); cached != nil {
			raw = cached.message
		}
	}
	message, err := normalizeUpstreamErrorMessage(raw)
	if err != nil {
		message = ""
	}
	s.upstreamErrorMessageCache.Store(&cachedUpstreamErrorMessage{message: message, expiresAt: time.Now().Add(ttl)})
	return message
}

func (s *SettingService) publishUpstreamErrorMessage(message string) {
	message, _ = normalizeUpstreamErrorMessage(message)
	s.upstreamErrorMessageMu.Lock()
	defer s.upstreamErrorMessageMu.Unlock()
	s.upstreamErrorMessageCache.Store(&cachedUpstreamErrorMessage{message: message, expiresAt: time.Now().Add(60 * time.Second)})
}
