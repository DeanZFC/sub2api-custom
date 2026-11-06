package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWSSemanticFirstTokenForwarders(t *testing.T) {
	cases := []struct {
		name, event string
		want        bool
	}{
		{"terminal_only", `{"type":"response.completed","response":{"id":"resp_synthetic","usage":{"output_tokens":1}}}`, false},
		{"created", `{"type":"response.created"}`, false},
		{"typed_reasoning_done", `{"type":"response.output_item.done","item":{"type":"reasoning"}}`, true},
		{"reasoning_content_part", `{"type":"response.content_part.added","part":{"type":"reasoning_text"}}`, true},
		{"summary_content_part", `{"type":"response.content_part.done","part":{"type":"summary_text"}}`, true},
		{"untyped_item", `{"type":"response.output_item.added","item":{"id":"synthetic"}}`, false},
		{"typed_reasoning", `{"type":"response.output_item.added","item":{"type":"reasoning","summary":[]}}`, true},
		{"reasoning_part", `{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":""}}`, true},
		{"reasoning_delta", `{"type":"response.reasoning_text.delta","delta":"thinking"}`, true},
		{"text", `{"type":"response.output_text.delta","delta":"hello"}`, true},
		{"body", `{"type":"response.output_text.done","text":"hello"}`, true},
		{"tool", `{"type":"response.function_call_arguments.delta","delta":"{}"}`, true},
		{"metadata", `{"type":"response.metadata"}`, false},
		{"heartbeat", `{"type":"keepalive"}`, false},
		{"empty_delta", `{"type":"response.output_text.delta","delta":""}`, false},
		{"object_delta", `{"type":"response.output_text.delta","delta":{}}`, false},
		{"empty_message", `{"type":"response.output_item.added","item":{"type":"message","content":[]}}`, false},
	}
	for _, path := range []string{"ws_to_sse", "http_bridge"} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				events := []string{`{"type":"response.created","response":{"id":"resp_synthetic"}}`, tc.event, `{"type":"response.completed","response":{"id":"resp_synthetic","usage":{"input_tokens":1,"output_tokens":1}}}`}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				var result *OpenAIForwardResult
				var err error
				if path == "ws_to_sse" {
					conn := &openAIWSCaptureConn{}
					for _, event := range events {
						conn.events = append(conn.events, []byte(event))
					}
					s := wsSSETestService(t, &wsSSETestDialer{conn: conn}, &httpUpstreamRecorder{})
					result, err = forwardSemanticWSAdapter(t, s, c)
					require.Contains(t, rec.Body.String(), tc.event)
				} else {
					body := "data: " + strings.Join(events, "\n\ndata: ") + "\n\n"
					s := &OpenAIGatewayService{cfg: newOpenAIWSV2TestConfig(), httpUpstream: &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}}
					payload := []byte(`{"type":"response.create","model":"gpt-5.1","input":"synthetic"}`)
					result, err = s.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, "sk-test", payload, len(payload), "gpt-5.1", "", "", "", "", 1, func([]byte) error { return nil })
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				if tc.want {
					require.NotNil(t, result.FirstTokenMs, "semantic event must establish first token")
				} else {
					require.Nil(t, result.FirstTokenMs, "metadata or empty payload must leave first token unknown")
				}
			})
		}
	}
}

const wsSSETestRequest = `{"model":"gpt-5.1","stream":true,"instructions":"test","input":[{"role":"user","content":"hello"}]}`

func wsSSETestAccount() *Account {
	return &Account{
		ID: 99101, Name: "ws-sse-test", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-oauth-token"},
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
		},
	}
}

type wsSSETestDialer struct {
	conn   openAIWSClientConn
	status int
	err    error
	calls  atomic.Int32
}

func (d *wsSSETestDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	d.calls.Add(1)
	return d.conn, d.status, http.Header{}, d.err
}

func wsSSETestService(t *testing.T, d *wsSSETestDialer, upstream HTTPUpstream) *OpenAIGatewayService {
	t.Helper()
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 2
	cfg.Gateway.OpenAIWS.EventFlushBatchSize = 100
	cfg.Gateway.OpenAIWS.EventFlushIntervalMS = 60000
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(d)
	t.Cleanup(pool.Close)
	return &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
}

// Exercise the existing WS-to-SSE adapter directly. HTTP ingress transport
// policy is intentionally covered separately by the protocol routing tests.
func forwardSemanticWSAdapter(t *testing.T, s *OpenAIGatewayService, c *gin.Context) (*OpenAIForwardResult, error) {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(wsSSETestRequest), &body))
	recovered := false
	return s.forwardOpenAIWSV2(context.Background(), c, wsSSETestAccount(), body, "", "", "test-oauth-token",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, false, true,
		"gpt-5.1", "gpt-5.1", time.Now(), 1, "", &recovered)
}
