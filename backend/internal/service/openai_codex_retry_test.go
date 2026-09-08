//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const codexRetryTestFailure = `{"type":"response.failed","response":{"id":"failed-attempt","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`
const codexRetryTestSuccess = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"success-attempt\"}}\n\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"success-attempt\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}}\n\n"

func codexRetryTestSettings(t *testing.T) *SettingService {
	t.Helper()
	s := NewSettingService(&panelRateLimitSettingRepo{}, &config.Config{})
	value := DefaultCodexPreOutputRetrySettings()
	value.Enabled, value.RetryIntervalMs, value.MaxRetries = true, 100, 2
	require.NoError(t, s.SetCodexPreOutputRetrySettings(context.Background(), value))
	return s
}

func codexRetryTestResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestCodexPreOutputRetryForward(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, httpError := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{true: "passthrough", false: "normal"}[passthrough], map[bool]string{true: "http", false: "sse"}[httpError]}, "/"), func(t *testing.T) {
				failure := codexRetryTestResponse(200, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"failed-attempt\"}}\n\n"+"data: "+codexRetryTestFailure+"\n\n")
				failure.Header.Set("X-Request-Id", "failed-header")
				if httpError {
					failure = codexRetryTestResponse(503, `{"error":{"message":"Our servers are currently overloaded. Please try again later."}}`)
				}
				upstream := &httpUpstreamRecorder{responses: []*http.Response{failure, codexRetryTestResponse(200, codexRetryTestSuccess)}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				svc.settingService = codexRetryTestSettings(t)
				c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "pool_mode_retry_count": 0}
				result, err := svc.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`))
				require.NoError(t, err)
				require.Len(t, upstream.requests, 2)
				require.Equal(t, upstream.bodies[0], upstream.bodies[1])
				require.NotContains(t, recorder.Body.String(), "failed-attempt")
				require.NotContains(t, recorder.Body.String(), "overloaded")
				require.NotEqual(t, "failed-header", recorder.Header().Get("X-Request-Id"))
				require.Contains(t, recorder.Body.String(), "hello")
				require.Equal(t, 5, result.Usage.OutputTokens)
				require.NotNil(t, result.FirstTokenMs)
				require.GreaterOrEqual(t, *result.FirstTokenMs, 100)
			})
		}
	}
}

func TestCodexPreOutputRetryBoundsAndOutput(t *testing.T) {
	for _, tc := range []struct {
		name, prefix string
		enabled      bool
		calls        int
	}{
		{"exhausted", "", true, 3},
		{"disabled", "", false, 1},
		{"text already sent", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", true, 1},
		{"tool already sent", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_1\",\"name\":\"run\",\"arguments\":\"{}\"}}\n\n", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			for range 3 {
				upstream.responses = append(upstream.responses, codexRetryTestResponse(200, tc.prefix+"data: "+codexRetryTestFailure+"\n\n"))
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.settingService = codexRetryTestSettings(t)
			settings := svc.settingService.GetCodexPreOutputRetrySettingsCached(context.Background())
			settings.Enabled = tc.enabled
			require.NoError(t, svc.settingService.SetCodexPreOutputRetrySettings(context.Background(), settings))
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			_, err := svc.Forward(c.Request.Context(), c, newOpenAIImageGenerationControlTestAccount(), []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`))
			require.Error(t, err)
			require.Len(t, upstream.requests, tc.calls)
			if tc.name == "exhausted" {
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.False(t, failure.ShouldRetryNextAccount())
				require.False(t, failure.ShouldReportAccountScheduleFailure())
				require.Empty(t, recorder.Body.String())
			}
		})
	}
}

func TestCodexPreOutputRetryExhaustionPreservesLastResponse(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, tc := range []struct {
			name, contentType, payload, eventType string
			status                                int
		}{
			{"http_json", "application/json", `{"error":{"code":"auth_unavailable","type":"upstream_unavailable","message":"last upstream error: server_is_overloaded","details":{"attempt":"last"}},"trace":"upstream-trace"}`, "", 503},
			{"http_text", "text/plain", "Our servers are currently overloaded.\nPlease try again later.\n", "", 529},
			{"sse_failed", "text/event-stream", codexRetryTestFailure, "response.failed", 200},
			{"sse_error", "text/event-stream", `{"error":{"code":"server_is_overloaded","message":"Original upstream message","param":null}}`, "error", 200},
		} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, tc.name), func(t *testing.T) {
				body := tc.payload
				if tc.eventType != "" {
					body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"buffered-preamble\"}}\n\n" +
						"event: " + tc.eventType + "\ndata: " + tc.payload + "\n\n"
				}
				lastResponse := codexRetryTestResponse(tc.status, body)
				lastResponse.Header.Set("Content-Type", tc.contentType)
				lastResponse.Header.Set("Retry-After", "2")
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					codexRetryTestResponse(503, `{"error":{"code":"server_is_overloaded","message":"earlier-attempt-one"}}`),
					codexRetryTestResponse(503, `{"error":{"code":"server_is_overloaded","message":"earlier-attempt-two"}}`),
					lastResponse,
				}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				svc.settingService = codexRetryTestSettings(t)
				c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "pool_mode_retry_count": 0}
				_, err := svc.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`))
				var failure *UpstreamFailoverError
				require.ErrorAs(t, err, &failure)
				require.Len(t, upstream.requests, 3)
				require.Equal(t, tc.payload, string(failure.ResponseBody))
				require.Equal(t, tc.eventType, failure.ResponseSSEEvent)
				require.Empty(t, recorder.Body.String())

				WriteCodexRetryExhaustedResponse(c, failure, false)
				require.Equal(t, tc.status, recorder.Code)
				require.Equal(t, tc.contentType, recorder.Header().Get("Content-Type"))
				require.Equal(t, "2", recorder.Header().Get("Retry-After"))
				if tc.eventType == "" {
					require.Equal(t, tc.payload, recorder.Body.String())
				} else {
					require.Contains(t, recorder.Body.String(), tc.payload)
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "data:"))
				}
				require.NotContains(t, recorder.Body.String(), "earlier-attempt")
				require.NotContains(t, recorder.Body.String(), "buffered-preamble")
			})
		}
	}
}

func TestCodexPreOutputRetryWindowExhaustionPreservesResponse(t *testing.T) {
	payload := `{"error":{"code":"server_is_overloaded","message":"Original failure"}}`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{codexRetryTestResponse(503, payload)}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.settingService = codexRetryTestSettings(t)
	settings := svc.settingService.GetCodexPreOutputRetrySettingsCached(context.Background())
	settings.MaxRetryWindowSeconds, settings.RetryIntervalMs = 1, 1000
	require.NoError(t, svc.settingService.SetCodexPreOutputRetrySettings(context.Background(), settings))
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	_, err := svc.Forward(c.Request.Context(), c, newOpenAIImageGenerationControlTestAccount(), []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`))
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Len(t, upstream.requests, 1)
	WriteCodexRetryExhaustedResponse(c, failure, false)
	require.Equal(t, 503, recorder.Code)
	require.Equal(t, payload, recorder.Body.String())
}

func TestCodexPreOutputRetryPreservesCredentialRedaction(t *testing.T) {
	c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	c.Set(codexRetrySettingsContextKey, codexRetryTestSettings(t).GetCodexPreOutputRetrySettingsCached(context.Background()))
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"auth_mode": OpenAIAuthModeAgentIdentity, "access_token": "private-upstream-credential",
	}}
	failure := (&OpenAIGatewayService{}).newCodexPreOutputRetryError(c, account, 503, nil,
		[]byte(`{"error":{"code":"server_is_overloaded","message":"private-upstream-credential"}}`), "server_is_overloaded")
	require.NotNil(t, failure)
	WriteCodexRetryExhaustedResponse(c, failure, false)
	require.Equal(t, 503, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"server_is_overloaded"`)
	require.NotContains(t, recorder.Body.String(), "private-upstream-credential")
}

func TestCodexPreOutputRetrySettings(t *testing.T) {
	svc := codexRetryTestSettings(t)
	settings := svc.GetCodexPreOutputRetrySettingsCached(context.Background())
	settings.Keywords = []string{" custom_BUSY ", "CUSTOM_busy"}
	require.NoError(t, svc.SetCodexPreOutputRetrySettings(context.Background(), settings))
	settings = svc.GetCodexPreOutputRetrySettingsCached(context.Background())
	require.Equal(t, []string{"custom_BUSY"}, settings.Keywords)
	require.True(t, settings.matches([]byte(`{"response":{"error":{"code":"CUSTOM_BUSY"}}}`), ""))
	require.False(t, settings.matches([]byte(`{"error":{"message":"invalid"},"input":"custom_BUSY"}`), "custom_BUSY"))
	require.False(t, settings.matches([]byte(`{"response":{"output":[{"text":"custom_BUSY"}]}}`), ""))
	require.False(t, settings.canRetry(0, time.Now().Add(-time.Minute)))
	settings.MaxRetries = 11
	require.Error(t, svc.SetCodexPreOutputRetrySettings(context.Background(), settings))
	settings.MaxRetries, settings.Keywords = 1, nil
	require.Error(t, settings.Validate())
	settings.Enabled = false
	require.NoError(t, svc.SetCodexPreOutputRetrySettings(context.Background(), settings))
	require.False(t, svc.GetCodexPreOutputRetrySettingsCached(context.Background()).Enabled)
}

func TestCodexPreOutputRetryCancellation(t *testing.T) {
	svc := &OpenAIGatewayService{settingService: codexRetryTestSettings(t)}
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	account := newOpenAIImageGenerationControlTestAccount()
	ctx, cancel := context.WithCancel(c.Request.Context())
	calls := 0
	_, err := svc.withCodexPreOutputRetry(ctx, c, account, func() (*OpenAIForwardResult, error) {
		calls++
		cancel()
		return nil, svc.newCodexPreOutputRetryError(c, account, 503, nil, []byte(codexRetryTestFailure), "overloaded")
	})
	require.True(t, errors.Is(err, context.Canceled))
	require.Equal(t, 1, calls)
}

func TestCodexBufferedRetryAfterPartialOutput(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, prefix := range []string{
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"discarded-text\"}\n\n",
			"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"discarded-tool\",\"call_id\":\"discarded-call\",\"name\":\"run\",\"arguments\":\"{}\"}}\n\n",
		} {
			t.Run(fmt.Sprintf("passthrough=%t/prefix=%s", passthrough, prefix), func(t *testing.T) {
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					codexRetryTestResponse(200, prefix+"data: "+codexRetryTestFailure+"\n\n"),
					codexRetryTestResponse(200, codexRetryTestSuccess),
				}}
				svc := newOpenAIImageGenerationControlTestService(upstream)
				svc.settingService = codexRetryTestSettings(t)
				settings := svc.settingService.GetCodexPreOutputRetrySettingsCached(context.Background())
				settings.BufferUntilComplete = true
				require.NoError(t, svc.settingService.SetCodexPreOutputRetrySettings(context.Background(), settings))
				c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				account := newOpenAIImageGenerationControlTestAccount()
				account.Extra = map[string]any{"openai_passthrough": passthrough, "pool_mode_retry_count": 0}
				result, err := svc.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`))
				require.NoError(t, err)
				require.Len(t, upstream.requests, 2)
				require.Equal(t, upstream.bodies[0], upstream.bodies[1])
				require.NotContains(t, recorder.Body.String(), "discarded-")
				require.NotContains(t, recorder.Body.String(), "failed-attempt")
				require.NotContains(t, recorder.Body.String(), "overloaded")
				require.Equal(t, 1, strings.Count(recorder.Body.String(), `"delta":"hello"`))
				require.Equal(t, 5, result.Usage.OutputTokens)
			})
		}
	}
}

func TestCodexBufferedRetryExhaustionKeepsOnlyLastError(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			for range 3 {
				upstream.responses = append(upstream.responses, codexRetryTestResponse(200,
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"discarded-text\"}\n\n"+"data: "+codexRetryTestFailure+"\n\n"))
			}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.settingService = codexRetryTestSettings(t)
			settings := svc.settingService.GetCodexPreOutputRetrySettingsCached(context.Background())
			settings.BufferUntilComplete = true
			require.NoError(t, svc.settingService.SetCodexPreOutputRetrySettings(context.Background(), settings))
			c, recorder := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			account := newOpenAIImageGenerationControlTestAccount()
			account.Extra = map[string]any{"openai_passthrough": passthrough, "pool_mode_retry_count": 0}
			_, err := svc.Forward(c.Request.Context(), c, account, []byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`))
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Len(t, upstream.requests, 3)
			require.Equal(t, codexRetryTestFailure, string(failure.ResponseBody))
			WriteCodexRetryExhaustedResponse(c, failure, false)
			require.Contains(t, recorder.Body.String(), codexRetryTestFailure)
			require.NotContains(t, recorder.Body.String(), "discarded-text")
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "data:"))
		})
	}
}

func TestCodexRetryBackoffAndRetryAfter(t *testing.T) {
	settings := DefaultCodexPreOutputRetrySettings()
	settings.Enabled, settings.ExponentialBackoff, settings.RetryIntervalMs = true, true, 100
	now := time.Now().UTC().Truncate(time.Second)
	for retries, base := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second} {
		delay := settings.retryDelay(retries, nil, now)
		require.GreaterOrEqual(t, delay, base)
		require.LessOrEqual(t, delay, base+base/5)
	}
	require.Equal(t, 90*time.Second, settings.retryDelay(0, http.Header{"Retry-After": {"90"}}, now))
	require.Equal(t, 90*time.Second, codexRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now))
	require.Equal(t, 301*time.Second, codexRetryAfter("18446744073709551615", now))
	for _, raw := range []string{"", "invalid", "-1", now.Add(-time.Second).Format(http.TimeFormat)} {
		require.Zero(t, codexRetryAfter(raw, now))
	}
	settings.ExponentialBackoff = false
	require.Equal(t, 100*time.Millisecond, settings.retryDelay(3, nil, now))
	require.False(t, settings.canRetryAfter(0, now, 90*time.Second))
	require.False(t, settings.canRetryAfter(0, now.Add(-time.Minute), 0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitCodexRetry(ctx, time.Hour), context.Canceled)
}
