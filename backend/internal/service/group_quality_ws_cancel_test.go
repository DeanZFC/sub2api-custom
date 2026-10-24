package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForwardOpenAIWSV2_GroupFallbackCancellationStopsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name             string
		cause            error
		cancelDuringRead bool
		writeErr         error
		wantDisconnect   bool
	}{
		{name: "lease lost during upstream read", cause: ErrGroupQualityFallbackLeaseLost, cancelDuringRead: true},
		{name: "client canceled after output", cause: context.Canceled, wantDisconnect: true},
		{name: "downstream write failed before context cancellation", writeErr: errors.New("client connection closed"), wantDisconnect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "fallback-request", GroupID: 8})
			defer cancelGroupQualityTraffic(ctx, context.Canceled)
			bindGroupQualityTrafficLease(ctx, nil, 9101, "gpt-5.5", "fallback-lease")
			bindGroupQualityFallbackStop(ctx, "fallback-lease", func() {})
			require.True(t, GroupQualityFallbackActive(ctx))
			writer := &groupQualityFallbackCancelWriter{cancelOnFirstWriteResponseWriter: &cancelOnFirstWriteResponseWriter{}, writeErr: tc.writeErr}
			cancel := func() { cancelGroupQualityTraffic(ctx, tc.cause) }
			if !tc.cancelDuringRead && tc.cause != nil {
				writer.cancel = cancel
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")
			captureConn := &groupQualityFallbackCancelConn{openAIWSCancelSafeConn: &openAIWSCancelSafeConn{openAIWSCaptureConn: &openAIWSCaptureConn{
				events: [][]byte{
					[]byte(`{"type":"response.created","response":{"id":"resp_fallback_cancel","model":"gpt-5.5","usage":{"input_tokens":3,"output_tokens":1}}}`),
					[]byte(`{"type":"response.output_text.delta","delta":"partial"}`),
					[]byte(`{"type":"response.completed","response":{"id":"resp_fallback_cancel","model":"gpt-5.5","usage":{"input_tokens":100,"output_tokens":50}}}`),
				},
				readDelays: []time.Duration{0, 0, 50 * time.Millisecond},
			}}}
			if tc.cancelDuringRead {
				captureConn.cancelPendingRead = cancel
			}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
			cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			pool := newOpenAIWSConnPool(cfg)
			t.Cleanup(pool.Close)
			pool.setClientDialerForTest(&openAIWSClientConnCancelDialer{conn: captureConn})
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 9101, Name: "fallback-cancel", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool}}
			type forwarded struct {
				result *OpenAIForwardResult
				err    error
			}
			done := make(chan forwarded, 1)
			go func() {
				result, err := svc.Forward(ctx, c, account, []byte(`{"model":"gpt-5.5","stream":true,"input":[{"type":"input_text","text":"hello"}]}`))
				done <- forwarded{result: result, err: err}
			}()
			var got forwarded
			select {
			case got = <-done:
			case <-time.After(time.Second):
				_ = captureConn.Close()
				t.Fatal("fallback cancellation must stop without a detached drain or retry loop")
			}
			require.ErrorIs(t, got.err, context.Canceled)
			if tc.cause != nil {
				require.ErrorIs(t, got.err, tc.cause)
			}
			var fallbackErr *openAIWSFallbackError
			require.False(t, errors.As(got.err, &fallbackErr), "local cancellation must not trigger upstream fallback")
			require.NotNil(t, got.result, "keep known usage even on a canceled fallback")
			require.Equal(t, tc.wantDisconnect, got.result.ClientDisconnect)
			require.Equal(t, "resp_fallback_cancel", got.result.RequestID)
			require.Equal(t, 3, got.result.Usage.InputTokens)
			require.Equal(t, 1, got.result.Usage.OutputTokens)
			require.Empty(t, got.result.UpstreamTerminalEvent)
			require.Empty(t, c.GetString(OpsUpstreamErrorMessageKey))
			_, hasUpstreamErrors := c.Get(OpsUpstreamErrorsKey)
			require.False(t, hasUpstreamErrors, "lease loss says nothing about upstream account health")
			require.NotContains(t, writer.body.String(), "response.failed")
			captureConn.mu.Lock()
			closed, remaining := captureConn.closed, len(captureConn.events)
			captureConn.mu.Unlock()
			require.True(t, closed, "close the upstream owned by the canceled fallback request")
			require.Equal(t, 1, remaining, "do not consume the delayed terminal event after cancellation")
		})
	}
}

type groupQualityFallbackCancelWriter struct {
	*cancelOnFirstWriteResponseWriter
	writeErr error
}

func (w *groupQualityFallbackCancelWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.cancelOnFirstWriteResponseWriter.Write(p)
}

type groupQualityFallbackCancelConn struct {
	*openAIWSCancelSafeConn
	cancelPendingRead func()
}

func (c *groupQualityFallbackCancelConn) ReadMessage(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	pending := len(c.events) == 1
	c.mu.Unlock()
	if pending && c.cancelPendingRead != nil {
		c.cancelPendingRead()
	}
	return c.openAIWSCancelSafeConn.ReadMessage(ctx)
}
