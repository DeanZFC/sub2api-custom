package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const CodexPreOutputRetryReason GatewayFailureReason = "codex_pre_output_retry"
const codexRetrySettingsContextKey = "codex_pre_output_retry_settings"

// Match only error fields: successful output and echoed request content must
// never turn a response into a replayable failure.
func (v CodexPreOutputRetrySettings) matches(payload []byte, message string) bool {
	if !v.Enabled {
		return false
	}
	fields := []string{message}
	if gjson.ValidBytes(payload) {
		fields = nil
		for _, path := range []string{
			"error.message", "error.code", "error.type",
			"response.error.message", "response.error.code", "response.error.type",
			"message", "code",
		} {
			if field := gjson.GetBytes(payload, path); field.Type == gjson.String {
				fields = append(fields, field.String())
			}
		}
	} else if len(payload) > 0 {
		fields = append(fields, string(payload))
	}
	for _, field := range fields {
		field = strings.ToLower(field)
		for _, keyword := range v.Keywords {
			keyword = strings.ToLower(strings.TrimSpace(keyword))
			if keyword != "" && strings.Contains(field, keyword) {
				return true
			}
		}
	}
	return false
}

func (s *OpenAIGatewayService) newCodexPreOutputRetryError(c *gin.Context, account *Account, status int, headers http.Header, payload []byte, message string) *UpstreamFailoverError {
	if c == nil || account == nil || account.Platform != PlatformOpenAI {
		return nil
	}
	raw, ok := c.Get(codexRetrySettingsContextKey)
	settings, valid := raw.(CodexPreOutputRetrySettings)
	if !ok || !valid || !settings.matches(payload, message) {
		return nil
	}
	message = sanitizeUpstreamErrorMessage(strings.TrimSpace(message))
	if message == "" {
		message = "Upstream request failed before output"
	}
	if status < 400 {
		status = http.StatusBadGateway
	}
	return &UpstreamFailoverError{
		StatusCode: status, ResponseHeaders: headers.Clone(), ResponseBody: append([]byte(nil), payload...),
		Reason: CodexPreOutputRetryReason, Scope: GatewayFailureScopeRequest,
		RequestScopedTransient: true, NextAccountAction: NextAccountStop,
		SafeToFailoverAfterWrite: true,
		ClientStatusCode:         status, ClientMessage: message,
	}
}

func (s *OpenAIGatewayService) withCodexPreOutputRetry(ctx context.Context, c *gin.Context, account *Account, forward func() (*OpenAIForwardResult, error)) (*OpenAIForwardResult, error) {
	if s == nil || s.settingService == nil || c == nil || account == nil || account.Platform != PlatformOpenAI {
		return forward()
	}
	settings := s.settingService.GetCodexPreOutputRetrySettingsCached(ctx)
	if !settings.Enabled {
		return forward()
	}
	previous, existed := c.Get(codexRetrySettingsContextKey)
	c.Set(codexRetrySettingsContextKey, settings)
	defer func() {
		if existed {
			c.Set(codexRetrySettingsContextKey, previous)
		} else {
			c.Set(codexRetrySettingsContextKey, nil)
		}
	}()
	started := time.Now()
	initialHeaders := c.Writer.Header().Clone()
	for retries := 0; ; retries++ {
		attemptStart := time.Now()
		result, err := forward()
		if result != nil {
			overhead := attemptStart.Sub(started)
			result.Duration += overhead
			if result.FirstTokenMs != nil {
				ms := *result.FirstTokenMs + int(overhead.Milliseconds())
				result.FirstTokenMs = &ms
			}
		}
		var retryErr *UpstreamFailoverError
		if !errors.As(err, &retryErr) || retryErr.Reason != CodexPreOutputRetryReason {
			return result, err
		}
		if !c.Writer.Written() {
			clear(c.Writer.Header())
			for key, values := range initialHeaders {
				c.Writer.Header()[key] = append([]string(nil), values...)
			}
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if !settings.canRetry(retries, started) {
			return result, err
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
			UpstreamStatusCode: retryErr.StatusCode, UpstreamRequestID: retryErr.ResponseHeaders.Get("x-request-id"),
			Kind: "retry", Message: retryErr.ClientMessage,
		})
		if err := settings.wait(ctx); err != nil {
			return result, err
		}
	}
}

func (v CodexPreOutputRetrySettings) canRetry(retries int, started time.Time) bool {
	return v.Enabled && retries < v.MaxRetries &&
		time.Since(started)+time.Duration(v.RetryIntervalMs)*time.Millisecond < time.Duration(v.MaxRetryWindowSeconds)*time.Second
}

func (v CodexPreOutputRetrySettings) wait(ctx context.Context) error {
	timer := time.NewTimer(time.Duration(v.RetryIntervalMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
