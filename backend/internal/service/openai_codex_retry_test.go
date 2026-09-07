//go:build unit

package service

import (
	"context"
	"errors"
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
