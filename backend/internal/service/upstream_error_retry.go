package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/tidwall/gjson"
)

const SettingKeyUpstreamErrorRetry = "upstream_error_retry"

// UpstreamErrorRetrySettings is an independent, DB-backed gateway policy.
// MaxRetries counts additional attempts across the entire client request.
type UpstreamErrorRetrySettings struct {
	Enabled    bool   `json:"enabled"`
	MaxRetries int    `json:"max_retries"`
	DelayMS    int    `json:"delay_ms"`
	Errors     string `json:"errors"`
}

func defaultUpstreamErrorRetrySettings() UpstreamErrorRetrySettings {
	return UpstreamErrorRetrySettings{MaxRetries: 3, DelayMS: 1000}
}

func normalizeUpstreamErrorRetrySettings(v UpstreamErrorRetrySettings) (UpstreamErrorRetrySettings, error) {
	invalid := func(message string) (UpstreamErrorRetrySettings, error) {
		return v, infraerrors.BadRequest("INVALID_UPSTREAM_ERROR_RETRY", message)
	}
	if v.MaxRetries < 1 || v.MaxRetries > 10 {
		return invalid("upstream_error_retry.max_retries must be between 1 and 10")
	}
	if v.DelayMS < 100 || v.DelayMS > 10000 {
		return invalid("upstream_error_retry.delay_ms must be between 100 and 10000")
	}
	if len(v.Errors) > 32*1024 {
		return invalid("upstream_error_retry.errors must not exceed 32 KiB")
	}
	lines := make([]string, 0)
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(v.Errors, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[strings.ToLower(line)] {
			continue
		}
		if len(line) > 512 || strings.ContainsAny(line, "\r\x00") {
			return invalid("each error must be a single line of at most 512 bytes")
		}
		if code, err := strconv.Atoi(line); err == nil {
			if code < 400 || code > 599 || code == http.StatusTooManyRequests {
				return invalid("HTTP error codes must be 400–599, excluding 429 (handled by the built-in rate-limit policy)")
			}
		}
		seen[strings.ToLower(line)] = true
		lines = append(lines, line)
	}
	if len(lines) > 100 {
		return invalid("at most 100 error rules are allowed")
	}
	if v.Enabled && len(lines) == 0 {
		return invalid("at least one error rule is required when upstream error retry is enabled")
	}
	v.Errors = strings.Join(lines, "\n")
	return v, nil
}

func parseUpstreamErrorRetrySettings(raw string) UpstreamErrorRetrySettings {
	v := defaultUpstreamErrorRetrySettings()
	if strings.TrimSpace(raw) == "" {
		return v
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return defaultUpstreamErrorRetrySettings()
	}
	if normalized, err := normalizeUpstreamErrorRetrySettings(v); err == nil {
		return normalized
	}
	// A malformed stored policy must never create an unbounded retry loop.
	return defaultUpstreamErrorRetrySettings()
}

type upstreamErrorRetryPolicy struct {
	settings UpstreamErrorRetrySettings
	statuses map[int]bool
	keywords []string
}

func compileUpstreamErrorRetryPolicy(v UpstreamErrorRetrySettings) *upstreamErrorRetryPolicy {
	p := &upstreamErrorRetryPolicy{settings: v, statuses: make(map[int]bool)}
	for _, line := range strings.Split(v.Errors, "\n") {
		line = strings.ToLower(strings.TrimSpace(line))
		if code, err := strconv.Atoi(line); err == nil {
			p.statuses[code] = true
		} else if line != "" {
			p.keywords = append(p.keywords, line)
		}
	}
	return p
}

// Only inspect error fields in JSON: echoed prompts/metadata are not errors.
// Plain-text error bodies are accepted for reverse proxies such as nginx.
func (p *upstreamErrorRetryPolicy) matches(status int, body []byte) bool {
	if p == nil || !p.settings.Enabled || status < 400 || status > 599 || status == http.StatusTooManyRequests {
		return false
	}
	if upstreamErrorRetryHasUsage(body) {
		return false
	}
	if p.statuses[status] {
		return true
	}
	if len(p.keywords) == 0 {
		return false
	}
	var texts []string
	if gjson.ValidBytes(body) {
		for _, path := range []string{"error.code", "error.type", "error.message", "response.error.code", "response.error.type", "response.error.message", "detail.code", "detail.message", "code", "type", "message"} {
			if v := gjson.GetBytes(body, path); v.Type == gjson.String {
				texts = append(texts, strings.ToLower(v.String()))
			}
		}
		for _, path := range []string{"error", "detail"} {
			if v := gjson.GetBytes(body, path); v.Type == gjson.String {
				texts = append(texts, strings.ToLower(v.String()))
			}
		}
	} else {
		texts = []string{strings.ToLower(string(body))}
	}
	for _, text := range texts {
		for _, keyword := range p.keywords {
			if strings.Contains(text, keyword) {
				return true
			}
		}
	}
	return false
}

type cachedUpstreamErrorRetry struct {
	policy    *upstreamErrorRetryPolicy
	expiresAt time.Time
}

func (s *SettingService) upstreamErrorRetryPolicy(ctx context.Context) *upstreamErrorRetryPolicy {
	if s == nil || s.settingRepo == nil {
		return compileUpstreamErrorRetryPolicy(defaultUpstreamErrorRetrySettings())
	}
	if v := s.upstreamErrorRetryCache.Load(); v != nil && time.Now().Before(v.expiresAt) {
		return v.policy
	}
	// Serialize refresh and publication after writes, so an old DB read cannot
	// overwrite a newly saved policy. The hot path is an atomic load.
	s.upstreamErrorRetryMu.Lock()
	defer s.upstreamErrorRetryMu.Unlock()
	if v := s.upstreamErrorRetryCache.Load(); v != nil && time.Now().Before(v.expiresAt) {
		return v.policy
	}
	dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyUpstreamErrorRetry)
	ttl := 60 * time.Second
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		ttl = 5 * time.Second
		slog.Warn("upstream_error_retry.settings_read_failed", "error", err)
		raw = ""
	}
	p := compileUpstreamErrorRetryPolicy(parseUpstreamErrorRetrySettings(raw))
	s.upstreamErrorRetryCache.Store(&cachedUpstreamErrorRetry{policy: p, expiresAt: time.Now().Add(ttl)})
	return p
}

func (s *SettingService) publishUpstreamErrorRetrySettings(v *UpstreamErrorRetrySettings) {
	if v == nil {
		return
	}
	s.upstreamErrorRetryMu.Lock()
	defer s.upstreamErrorRetryMu.Unlock()
	s.upstreamErrorRetryCache.Store(&cachedUpstreamErrorRetry{
		policy: compileUpstreamErrorRetryPolicy(*v), expiresAt: time.Now().Add(60 * time.Second),
	})
}

type upstreamErrorRetryContextKey struct{}

// One shared budget follows the request through HTTP transport retries,
// protocol-level failovers and account switches. It also retains the original
// client context when a forwarding path detaches its billing/drain context.
type upstreamErrorRetryState struct {
	clientCtx context.Context
	settings  *SettingService
	once      sync.Once
	policy    *upstreamErrorRetryPolicy
	mu        sync.Mutex
	used      int
}

func WithUpstreamErrorRetry(ctx context.Context, settings *SettingService) context.Context {
	if settings == nil || upstreamErrorRetryFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, upstreamErrorRetryContextKey{}, &upstreamErrorRetryState{clientCtx: ctx, settings: settings})
}

func upstreamErrorRetryFromContext(ctx context.Context) *upstreamErrorRetryState {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(upstreamErrorRetryContextKey{}).(*upstreamErrorRetryState)
	return v
}

func (s *upstreamErrorRetryState) getPolicy() *upstreamErrorRetryPolicy {
	s.once.Do(func() { s.policy = s.settings.upstreamErrorRetryPolicy(s.clientCtx) })
	return s.policy
}

func (s *upstreamErrorRetryState) claim(status int, body []byte) (time.Duration, bool) {
	if s == nil || s.clientCtx.Err() != nil || !s.getPolicy().matches(status, body) {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used >= s.policy.settings.MaxRetries {
		return 0, false
	}
	s.used++
	slog.Info("gateway.upstream_error_retry", "upstream_status", status, "retry", s.used, "max_retries", s.policy.settings.MaxRetries)
	return time.Duration(s.policy.settings.DelayMS) * time.Millisecond, true
}

func (s *upstreamErrorRetryState) wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.clientCtx.Err(); err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.clientCtx.Done():
		return s.clientCtx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.clientCtx.Err()
	}
}

// TryConfiguredUpstreamErrorRetry is called only after the protocol's existing
// no-output/partial-usage guard. It leaves all built-in failover metadata intact.
// claimed=true with err!=nil means cancellation while waiting, not exhaustion.
func TryConfiguredUpstreamErrorRetry(ctx context.Context, failure *UpstreamFailoverError) (claimed bool, err error) {
	if ctx == nil || failure == nil || !failure.ShouldRetryNextAccount() || failure.IsCredentialFailure() || failure.ConfiguredRetryUnsafe || failure.StatusCode == http.StatusTooManyRequests || ctx.Err() != nil {
		return false, nil
	}
	s := upstreamErrorRetryFromContext(ctx)
	if delay, ok := s.claim(failure.StatusCode, failure.ResponseBody); ok {
		return true, s.wait(ctx, delay)
	}
	return false, nil
}

func marshalUpstreamErrorRetrySettings(v *UpstreamErrorRetrySettings) (string, error) {
	normalized, err := normalizeUpstreamErrorRetrySettings(*v)
	if err != nil {
		return "", err
	}
	*v = normalized
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal upstream error retry: %w", err)
	}
	return string(data), nil
}

// A failed response can still report consumed tokens or generated output. Never
// add a replay to such a response; the existing partial-usage path must own it.
func upstreamErrorRetryHasUsage(body []byte) bool {
	for _, path := range []string{"usage.input_tokens", "usage.output_tokens", "usage.prompt_tokens", "usage.completion_tokens", "usage.total_tokens", "response.usage.input_tokens", "response.usage.output_tokens", "response.output.#", "usageMetadata.totalTokenCount"} {
		if gjson.GetBytes(body, path).Int() > 0 {
			return true
		}
	}
	return false
}

// Stream errors arrive under HTTP 200. Match their semantic status and error
// text before the protocol reader releases its initial event buffer.
func configuredOpenAIStreamRetryFailure(ctx context.Context, payload []byte, message string, usage *OpenAIUsage) *UpstreamFailoverError {
	state := upstreamErrorRetryFromContext(ctx)
	if state == nil || ctx.Err() != nil || state.clientCtx.Err() != nil || openAIUsageHasTokens(usage) {
		return nil
	}
	code := openAIStreamFailedEventErrorCode(payload)
	errType := firstNonEmpty(gjson.GetBytes(payload, "response.error.type").String(), gjson.GetBytes(payload, "error.type").String())
	if isOpenAIWSRateLimitError(code, errType, message) {
		return nil
	}
	status := openAIStreamFailedEventSemanticStatus(payload, message)
	if status != http.StatusTooManyRequests {
		for _, path := range []string{"response.error.status_code", "error.status_code", "status_code"} {
			if code := int(gjson.GetBytes(payload, path).Int()); code >= 400 && code <= 599 {
				status = code
				break
			}
		}
	}
	if status == http.StatusTooManyRequests {
		return nil
	}
	if !state.getPolicy().matches(status, payload) {
		return nil
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.used >= state.policy.settings.MaxRetries {
		return nil
	}
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: append([]byte(nil), payload...), RequestScopedTransient: true, ConfiguredRetry: true}
}
