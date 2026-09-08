package service

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const CodexPreOutputRetryReason GatewayFailureReason = "codex_pre_output_retry"
const codexRetrySettingsContextKey = "codex_pre_output_retry_settings"
const codexRetryStartedContextKey = "codex_pre_output_retry_started"

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
	if c.Request != nil {
		payload = s.redactAgentIdentitySensitiveBody(c.Request.Context(), account, payload)
	}
	return &UpstreamFailoverError{
		StatusCode: status, ResponseHeaders: headers.Clone(), ResponseBody: append([]byte(nil), payload...),
		Reason: CodexPreOutputRetryReason, Scope: GatewayFailureScopeRequest,
		RequestScopedTransient: true, NextAccountAction: NextAccountStop,
		SafeToFailoverAfterWrite: true,
		ClientStatusCode:         status, ClientMessage: message,
	}
}

func (s *OpenAIGatewayService) newCodexPreOutputRetrySSEError(c *gin.Context, account *Account, resp *http.Response, payload []byte, eventType, message string) *UpstreamFailoverError {
	err := s.newCodexPreOutputRetryError(c, account, openAIStreamFailureStatus(payload, message), resp.Header, payload, message)
	if err != nil {
		err.ClientStatusCode = resp.StatusCode
		err.ResponseSSEEvent = eventType
	}
	return err
}

// WriteCodexRetryExhaustedResponse returns the last upstream failure without
// replacing its error fields or converting an SSE failure into an HTTP error.
func WriteCodexRetryExhaustedResponse(c *gin.Context, failure *UpstreamFailoverError, streamStarted bool) {
	if StopOpenAICompactSSEKeepaliveCommitted(c) {
		streamStarted = true
	}
	MarkResponseCommitted(c)
	setOpsUpstreamError(c, failure.StatusCode, failure.ClientMessage, "")
	writeOpenAIPassthroughErrorHeaders(c.Writer.Header(), failure.ResponseHeaders)
	if failure.ResponseSSEEvent == "" && !streamStarted && !c.Writer.Written() {
		contentType := failure.ResponseHeaders.Get("Content-Type")
		if contentType == "" {
			contentType = "application/json; charset=utf-8"
			if !gjson.ValidBytes(failure.ResponseBody) {
				contentType = "text/plain; charset=utf-8"
			}
		}
		c.Header("Content-Type", contentType)
		c.Data(failure.ClientStatusCode, contentType, failure.ResponseBody)
		return
	}

	eventType := failure.ResponseSSEEvent
	payload := failure.ResponseBody
	if eventType == "" {
		// A keepalive may already have committed HTTP 200. Only the transport
		// envelope changes in that case; retain the upstream error object.
		eventType = "error"
		var err error
		payload, err = codexRetryFailureEvent(failure)
		if err != nil {
			_ = c.Error(err)
			return
		}
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	if failure.ResponseSSEEvent != "" && !c.Writer.Written() {
		c.Status(failure.ClientStatusCode)
	}
	c.SSEvent(eventType, string(payload))
	c.Writer.Flush()
}

func codexRetryFailureEvent(failure *UpstreamFailoverError) ([]byte, error) {
	payload := failure.ResponseBody
	if failure.ResponseSSEEvent != "" {
		if gjson.GetBytes(payload, "type").String() != "" {
			return payload, nil
		}
		return sjson.SetBytes(payload, "type", failure.ResponseSSEEvent)
	}
	if gjson.ValidBytes(payload) && gjson.GetBytes(payload, "error").Exists() {
		return sjson.SetBytes(payload, "type", "error")
	}
	var upstreamError any = map[string]string{"message": string(payload)}
	if gjson.ValidBytes(payload) {
		upstreamError = json.RawMessage(payload)
	}
	return json.Marshal(map[string]any{"type": "error", "error": upstreamError})
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
	previousStart, startExisted := c.Get(codexRetryStartedContextKey)
	c.Set(codexRetryStartedContextKey, started)
	defer func() {
		if startExisted {
			c.Set(codexRetryStartedContextKey, previousStart)
		} else {
			c.Set(codexRetryStartedContextKey, nil)
		}
	}()
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
		delay := settings.retryDelay(retries, retryErr.ResponseHeaders, time.Now())
		if !settings.canRetryAfter(retries, started, delay) {
			return result, err
		}
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
			ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
			UpstreamStatusCode: retryErr.StatusCode, UpstreamRequestID: retryErr.ResponseHeaders.Get("x-request-id"),
			Kind: "retry", Message: retryErr.ClientMessage,
		})
		if err := waitCodexRetry(ctx, delay); err != nil {
			return result, err
		}
		// A delayed timer must not start another attempt outside the window.
		if !settings.canRetry(retries, started) {
			return result, retryErr
		}
	}
}

func (v CodexPreOutputRetrySettings) canRetry(retries int, started time.Time) bool {
	return v.canRetryAfter(retries, started, 0)
}

func (v CodexPreOutputRetrySettings) canRetryAfter(retries int, started time.Time, delay time.Duration) bool {
	return v.Enabled && retries < v.MaxRetries &&
		time.Since(started)+delay < time.Duration(v.MaxRetryWindowSeconds)*time.Second
}

func (v CodexPreOutputRetrySettings) retryDelay(retries int, headers http.Header, now time.Time) time.Duration {
	delay := time.Duration(v.RetryIntervalMs) * time.Millisecond
	if v.ExponentialBackoff {
		// A 100 ms base otherwise spends all retries during the same overload.
		if delay < time.Second {
			delay = time.Second
		}
		for attempt := 0; attempt < retries && delay < 10*time.Second; attempt++ {
			delay *= 2
		}
		delay = min(delay, 10*time.Second)
		// Positive jitter never shortens the base delay or Retry-After.
		delay += time.Duration(rand.Int64N(int64(delay/5) + 1))
	}
	if retryAfter := codexRetryAfter(headers.Get("Retry-After"), now); retryAfter > delay {
		delay = retryAfter
	}
	return delay
}

func codexRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if seconds, err := strconv.ParseUint(raw, 10, 64); err == nil {
		// Longer waits cannot fit in the maximum configurable 300 s window.
		return time.Duration(min(seconds, uint64(301))) * time.Second
	}
	if date, err := http.ParseTime(raw); err == nil && date.After(now) {
		return min(date.Sub(now), 301*time.Second)
	}
	return 0
}

func waitCodexRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
