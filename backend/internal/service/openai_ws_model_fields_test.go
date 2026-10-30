package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSPassthroughRejectsRawModelConflictsBeforeNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		payload string
		reason  string
	}{
		{"input", `{"type":"response.create","model":"gpt-6-astra","model":"gpt-6.1-sol","input":"hello"}`, "model"},
		{"messages", `{"type":"response.create","model":"gpt-6-astra","model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}]}`, "model"},
		{"messages_reverse", `{"type":"response.create","model":"gpt-6.1-sol","model":"gpt-6-astra","messages":[{"role":"user","content":"hello"}]}`, "model"},
		{"same_model", `{"type":"response.create","model":"gpt-6-astra","model":"gpt-6-astra","input":"hello"}`, "model"},
		{"session_model", `{"type":"session.update","session":{"model":"gpt-6-astra","model":"gpt-6.1-sol"}}`, "model"},
		{"session_case_variant", `{"type":"session.update","session":{"model":"gpt-6-astra","Model":"gpt-6.1-sol"}}`, "model"},
		{"session_escaped_model", `{"type":"session.update","session":{"model":"gpt-6-astra","m\u006fdel":null}}`, "model"},
		{"session_containers", `{"type":"session.update","session":{"model":"gpt-6-astra"},"session":{"model":"gpt-6.1-sol"}}`, "session"},
		{"session_case_containers", `{"type":"session.update","session":{"model":"gpt-6-astra"},"Session":null}`, "session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			controlCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			var beforeRequestCalls atomic.Int32
			var successfulTurns atomic.Int32
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, passthroughLifecycleAccount(), func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{
					BeforeRequest: func(int, []byte, string) error { beforeRequestCalls.Add(1); return nil },
					AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
						if result != nil && err == nil {
							successfulTurns.Add(1)
						}
					},
				}
			})
			defer server.Close()
			client := dialPassthroughLifecycleClient(t, server)
			defer func() { _ = client.CloseNow() }()
			requirePassthroughUpstreamWrite(t, upstream, time.Second)
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_model_guard","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)
			_, err := readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)

			writeCtx, cancelWrite := context.WithTimeout(context.Background(), time.Second)
			err = client.Write(writeCtx, coderws.MessageText, []byte(tt.payload))
			cancelWrite()
			require.NoError(t, err)
			_, err = readPassthroughLifecycleFrame(t, client, time.Second)
			var clientClose coderws.CloseError
			require.ErrorAs(t, err, &clientClose)
			require.Equal(t, coderws.StatusPolicyViolation, clientClose.Code)
			wantReason := "duplicate or case-conflicting " + tt.reason + " fields are not allowed"
			require.Equal(t, wantReason, clientClose.Reason)
			select {
			case err := <-serverErr:
				var closeErr *OpenAIWSClientCloseError
				require.ErrorAs(t, err, &closeErr)
				require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
			case <-time.After(time.Second):
				t.Fatal("ambiguous websocket frame did not terminate passthrough")
			}
			select {
			case payload := <-upstream.writes:
				t.Fatalf("ambiguous frame reached upstream: %s", payload)
			default:
			}
			// Validation precedes the admission hook and cannot create a billable turn.
			// The only completed turn is the initial valid request.
			require.Zero(t, beforeRequestCalls.Load())
			require.Equal(t, int32(1), successfulTurns.Load())
		})
	}
}

func TestOpenAIWSNativeIngressRejectsRawModelConflictsBeforeNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, payload := range []string{
		`{"type":"response.create","model":"gpt-6-astra","model":"gpt-6.1-sol","input":"hello"}`,
		`{"type":"response.create","model":"gpt-6-astra","model":"gpt-6.1-sol","messages":[{"role":"user","content":"hello"}]}`,
		`{"type":"response.create","model":"gpt-6.1-sol","model":"gpt-6-astra","messages":[{"role":"user","content":"hello"}]}`,
		`{"type":"response.create","model":"gpt-6-astra","model":"gpt-6-astra","input":"hello"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			controlCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			upstream := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_native_model_guard","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
			dialer := &openAIWSCaptureDialer{conn: upstream}
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			pool.setClientDialerForTest(dialer)
			svc := &OpenAIGatewayService{
				cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(),
				openaiWSPool: pool,
			}
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
			var beforeRequestCalls atomic.Int32
			var successfulTurns atomic.Int32
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
				return &OpenAIWSIngressHooks{
					BeforeRequest: func(int, []byte, string) error { beforeRequestCalls.Add(1); return nil },
					AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
						if result != nil && err == nil {
							successfulTurns.Add(1)
						}
					},
				}
			})
			defer server.Close()
			client := dialPassthroughLifecycleClient(t, server)
			defer func() { _ = client.CloseNow() }()
			_, err := readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)
			writeCtx, cancelWrite := context.WithTimeout(context.Background(), time.Second)
			err = client.Write(writeCtx, coderws.MessageText, []byte(payload))
			cancelWrite()
			require.NoError(t, err)
			select {
			case err := <-serverErr:
				var closeErr *OpenAIWSClientCloseError
				require.ErrorAs(t, err, &closeErr)
				require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
				require.Equal(t, "duplicate or case-conflicting model fields are not allowed", closeErr.Reason())
			case <-time.After(time.Second):
				t.Fatal("ambiguous websocket frame did not terminate native ingress")
			}
			require.Len(t, upstream.writes, 1, "ambiguous frame must not reach upstream")
			require.Zero(t, beforeRequestCalls.Load())
			require.Equal(t, int32(1), successfulTurns.Load())
		})
	}
}
