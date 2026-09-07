//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type codexRetryFrameScript struct {
	mu      sync.Mutex
	scripts [][]string
	queue   []string
	writes  [][]byte
}

func (s *codexRetryFrameScript) WriteFrame(_ context.Context, _ coderws.MessageType, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := len(s.writes)
	s.writes = append(s.writes, append([]byte(nil), payload...))
	if i >= len(s.scripts) {
		i = len(s.scripts) - 1
	}
	s.queue = append(s.queue, s.scripts[i]...)
	return nil
}

func (s *codexRetryFrameScript) ReadFrame(_ context.Context) (coderws.MessageType, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return coderws.MessageText, nil, io.EOF
	}
	value := s.queue[0]
	s.queue = s.queue[1:]
	return coderws.MessageText, []byte(value), nil
}

func (s *codexRetryFrameScript) Close() error { return nil }

func TestCodexPreOutputRetryWS(t *testing.T) {
	failure := []string{
		`{"type":"response.created","response":{"id":"failed-attempt"}}`,
		`{"type":"error","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}`,
		codexRetryTestFailure,
	}
	success := []string{
		`{"type":"response.created","response":{"id":"success-attempt"}}`,
		`{"type":"response.output_text.delta","delta":"hello"}`,
		`{"type":"response.completed","response":{"id":"success-attempt","usage":{"input_tokens":10,"output_tokens":5}}}`,
	}
	settings := codexRetryTestSettings(t).GetCodexPreOutputRetrySettingsCached(context.Background())
	script := &codexRetryFrameScript{scripts: [][]string{failure, success, failure, success}}
	conn := &codexRetryWSFrameConn{inner: script, settings: func(context.Context) CodexPreOutputRetrySettings { return settings }}
	for turn := 0; turn < 2; turn++ {
		request := []byte(`{"type":"response.create","model":"gpt-5.5","previous_response_id":"previous-turn","input":[]}`)
		require.NoError(t, conn.WriteFrame(context.Background(), coderws.MessageText, request))
		for {
			_, payload, err := conn.ReadFrame(context.Background())
			require.NoError(t, err)
			require.NotContains(t, string(payload), "failed-attempt")
			require.NotContains(t, string(payload), "overloaded")
			if gjson.GetBytes(payload, "type").String() == "response.completed" {
				break
			}
		}
		require.Len(t, script.writes, (turn+1)*2)
		require.Equal(t, request, script.writes[turn*2+1], "continuation context must survive a retry")
	}
}

func TestCodexPreOutputRetryWSLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		prefix  []string
		calls   int
	}{
		{"exhausted", true, nil, 3},
		{"disabled", false, nil, 1},
		{"after_text", true, []string{`{"type":"response.output_text.delta","delta":"partial"}`}, 1},
		{"after_tool", true, []string{`{"type":"response.function_call_arguments.delta","delta":"{}"}`}, 1},
		{"buffer_limit", true, []string{`{"type":"response.created","padding":"` + strings.Repeat("x", codexRetryWSBufferLimit) + `"}`}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := codexRetryTestSettings(t).GetCodexPreOutputRetrySettingsCached(context.Background())
			settings.Enabled = tc.enabled
			script := &codexRetryFrameScript{scripts: [][]string{append(tc.prefix, codexRetryTestFailure)}}
			conn := &codexRetryWSFrameConn{inner: script, settings: func(context.Context) CodexPreOutputRetrySettings { return settings }}
			require.NoError(t, conn.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
			for {
				_, payload, err := conn.ReadFrame(context.Background())
				require.NoError(t, err)
				if gjson.GetBytes(payload, "type").String() == "response.failed" {
					break
				}
			}
			require.Len(t, script.writes, tc.calls)
		})
	}
}

func TestCodexPreOutputRetryWSAuthoritativeFailure(t *testing.T) {
	settings := codexRetryTestSettings(t).GetCodexPreOutputRetrySettingsCached(context.Background())
	for _, terminal := range []string{"", `{"type":"response.failed","response":{"error":{"code":"invalid_request","message":"fix input"}}}`} {
		script := &codexRetryFrameScript{scripts: [][]string{{`{"type":"error","error":{"code":"server_is_overloaded"}}`}}}
		if terminal != "" {
			script.scripts[0] = append(script.scripts[0], terminal)
		}
		conn := &codexRetryWSFrameConn{inner: script, settings: func(context.Context) CodexPreOutputRetrySettings { return settings }}
		require.NoError(t, conn.WriteFrame(context.Background(), coderws.MessageText, []byte(`{"type":"response.create"}`)))
		_, payload, err := conn.ReadFrame(context.Background())
		require.NoError(t, err)
		require.Equal(t, "error", gjson.GetBytes(payload, "type").String())
		require.Len(t, script.writes, 1)
	}
}

func TestCodexPreOutputRetryHTTPBridge(t *testing.T) {
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		codexRetryTestResponse(200, "data: "+codexRetryTestFailure+"\n\n"),
		codexRetryTestResponse(200, codexRetryTestSuccess),
	}}
	svc := newOpenAIImageGenerationControlTestService(upstream)
	svc.settingService = codexRetryTestSettings(t)
	c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
	var received [][]byte
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(c.Request.Context(), c, newOpenAIImageGenerationControlTestAccount(), "test-token", payload, len(payload), "gpt-5.5", "", "", "", "", 2, func(message []byte) error {
		received = append(received, append([]byte(nil), message...))
		return nil
	})
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.NotEmpty(t, received)
	for _, message := range received {
		require.NotContains(t, string(message), "overloaded")
	}
	require.NotNil(t, result.FirstTokenMs)
	require.GreaterOrEqual(t, *result.FirstTokenMs, 100)
}

func TestCodexPreOutputRetryHTTPBridgeExhaustionPreservesError(t *testing.T) {
	for _, tc := range []struct {
		name, payload, eventType string
		status                   int
	}{
		{"http", `{"error":{"code":"auth_unavailable","type":"upstream_unavailable","message":"last error: server_is_overloaded","details":{"attempt":"last"}},"trace":"original"}`, "", 503},
		{"sse", codexRetryTestFailure, "response.failed", 200},
		{"sse_event_header", `{"error":{"code":"server_is_overloaded","message":"Original message","param":null}}`, "error", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.payload
			if tc.eventType != "" {
				body = "event: " + tc.eventType + "\ndata: " + body + "\n\n"
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				codexRetryTestResponse(503, `{"error":{"code":"server_is_overloaded","message":"earlier-attempt-one"}}`),
				codexRetryTestResponse(503, `{"error":{"code":"server_is_overloaded","message":"earlier-attempt-two"}}`),
				codexRetryTestResponse(tc.status, body),
			}}
			svc := newOpenAIImageGenerationControlTestService(upstream)
			svc.settingService = codexRetryTestSettings(t)
			c, _ := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
			var received [][]byte
			request := []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)
			_, err := svc.proxyOpenAIWSHTTPBridgeTurn(c.Request.Context(), c, newOpenAIImageGenerationControlTestAccount(), "test-token", request, len(request), "gpt-5.5", "", "", "", "", 1, func(message []byte) error {
				received = append(received, append([]byte(nil), message...))
				return nil
			})
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "the delivered failure must not trigger another failover")
			require.Len(t, upstream.requests, 3)
			require.Len(t, received, 1)
			if tc.eventType == "response.failed" {
				require.Equal(t, tc.payload, string(received[0]))
			} else {
				require.Equal(t, "error", gjson.GetBytes(received[0], "type").String())
				require.Equal(t, gjson.Get(tc.payload, "error").Raw, gjson.GetBytes(received[0], "error").Raw)
				require.Equal(t, gjson.Get(tc.payload, "trace").String(), gjson.GetBytes(received[0], "trace").String())
			}
			require.NotContains(t, string(received[0]), "earlier-attempt")
		})
	}
}

type codexRetryStagedConn struct {
	*stagedPassthroughConn
}

func (c *codexRetryStagedConn) WriteJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, payload)
}

func codexRetryWSIngressTestService(t *testing.T, mode string) (*OpenAIGatewayService, *Account, *codexRetryStagedConn) {
	t.Helper()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 3
	upstream := &codexRetryStagedConn{newStagedPassthroughConn()}
	svc := newPassthroughLifecycleService(cfg, upstream.stagedPassthroughConn)
	svc.settingService = codexRetryTestSettings(t)
	svc.openaiWSPassthroughDialer = &stagedPassthroughDialer{conn: upstream}
	svc.openaiWSPool = newOpenAIWSConnPool(cfg)
	svc.openaiWSPool.setClientDialerForTest(&stagedPassthroughDialer{conn: upstream})
	t.Cleanup(svc.openaiWSPool.Close)
	account := passthroughLifecycleAccount()
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
	return svc, account, upstream
}

func TestCodexPreOutputRetryWSIngress(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			svc, account, upstream := codexRetryWSIngressTestService(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			results := make(chan *OpenAIForwardResult, 2)
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
					if err == nil && result != nil {
						results <- result
					}
				}}
			})
			defer server.Close()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer client.CloseNow()
			readWrite := func() []byte {
				select {
				case payload := <-upstream.writes:
					return payload
				case <-ctx.Done():
					t.Fatal("upstream request did not arrive")
					return nil
				}
			}
			for turn := 1; turn <= 2; turn++ {
				previous := ""
				if turn > 1 {
					previous = `,"previous_response_id":"resp_success_1"`
				}
				request := []byte(`{"type":"response.create","model":"gpt-5.5","store":true,"input":"hello"` + previous + `}`)
				require.NoError(t, client.Write(ctx, coderws.MessageText, request))
				first := readWrite()
				upstream.Send(`{"type":"response.created","response":{"id":"failed-attempt"}}`)
				upstream.Send(`{"type":"error","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded."}}`)
				upstream.Send(codexRetryTestFailure)
				require.Equal(t, first, readWrite())
				if turn > 1 {
					require.Equal(t, "resp_success_1", gjson.GetBytes(first, "previous_response_id").String())
				}
				upstream.Send(fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_success_%d"}}`, turn))
				upstream.Send(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":"resp_success_%d","delta":"hello"}`, turn))
				upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_success_%d","usage":{"input_tokens":10,"output_tokens":5}}}`, turn))
				for _, eventType := range []string{"response.created", "response.output_text.delta", "response.completed"} {
					_, message, err := client.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, eventType, gjson.GetBytes(message, "type").String())
					require.NotContains(t, string(message), "failed-attempt")
				}
				select {
				case result := <-results:
					require.Equal(t, fmt.Sprintf("resp_success_%d", turn), result.RequestID)
					require.Equal(t, 5, result.Usage.OutputTokens)
					require.NotNil(t, result.FirstTokenMs)
					require.GreaterOrEqual(t, *result.FirstTokenMs, 100)
				case <-ctx.Done():
					t.Fatal("turn result did not arrive")
				}
			}
			_ = client.CloseNow()
			cancel()
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("ingress did not stop")
			}
		})
	}
}

func TestCodexPreOutputRetryWSFailuresExhausted(t *testing.T) {
	for _, code := range []string{"rate_limit_exceeded", "server_is_overloaded"} {
		for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
			t.Run(code+"/"+mode, func(t *testing.T) {
				svc, account, upstream := codexRetryWSIngressTestService(t, mode)
				repo := &openAIWSIngressCapacityShedRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}}}
				svc.accountRepo = repo
				svc.rateLimitService = &RateLimitService{accountRepo: repo}
				settings := svc.settingService.GetCodexPreOutputRetrySettingsCached(context.Background())
				settings.Keywords = []string{code}
				require.NoError(t, svc.settingService.SetCodexPreOutputRetrySettings(context.Background(), settings))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
				defer server.Close()
				client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
				require.NoError(t, err)
				defer client.CloseNow()
				require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)))
				errorEvent := fmt.Sprintf(`{"type":"error","error":{"code":%q,"message":"Original upstream failure","param":null}}`, code)
				failedEvent := fmt.Sprintf(`{"type":"response.failed","response":{"id":"resp_limited","error":{"code":%q,"message":"Original upstream failure","details":{"attempt":"last"}}}}`, code)
				for attempt := 0; attempt <= settings.MaxRetries; attempt++ {
					select {
					case <-upstream.writes:
					case <-ctx.Done():
						t.Fatal("upstream request did not arrive")
					}
					upstream.Send(errorEvent)
					upstream.Send(failedEvent)
				}
				for _, expected := range []string{errorEvent, failedEvent} {
					_, payload, err := client.Read(ctx)
					require.NoError(t, err, "exhaustion must deliver the final failure, not request another account retry")
					require.Equal(t, expected, string(payload))
				}
				require.Empty(t, upstream.writes)
				_ = client.CloseNow()
				cancel()
				select {
				case err := <-serverErr:
					var failover *UpstreamFailoverError
					require.False(t, errors.As(err, &failover))
				case <-time.After(time.Second):
					t.Fatal("ingress did not stop")
				}
			})
		}
	}
}

func TestCodexPreOutputRetryWSClientCancellation(t *testing.T) {
	settings := codexRetryTestSettings(t).GetCodexPreOutputRetrySettingsCached(context.Background())
	upstream := newStagedPassthroughConn()
	conn := &codexRetryWSFrameConn{inner: upstream, settings: func(context.Context) CodexPreOutputRetrySettings { return settings }}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.WriteFrame(ctx, coderws.MessageText, []byte(`{"type":"response.create"}`)))
	<-upstream.writes
	upstream.Send(`{"type":"response.created","response":{"id":"failed-attempt"}}`)
	upstream.Send(codexRetryTestFailure)
	done := make(chan error, 1)
	go func() {
		_, _, err := conn.ReadFrame(ctx)
		done <- err
	}()
	require.Eventually(t, func() bool {
		conn.mu.Lock()
		defer conn.mu.Unlock()
		return conn.turn != nil && len(conn.turn.buffer) > 0
	}, time.Second, time.Millisecond)
	require.NoError(t, conn.WriteFrame(ctx, coderws.MessageText, []byte(`{"type":"response.cancel"}`)))
	<-upstream.writes
	require.NoError(t, <-done)
	require.Empty(t, upstream.writes, "a canceled turn must not be replayed")
}
