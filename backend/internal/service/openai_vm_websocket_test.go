package service

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type vmCapturedDial struct {
	url     string
	headers http.Header
	proxy   string
}

type vmCapturingWSDialer struct {
	conn  openAIWSClientConn
	calls chan vmCapturedDial
}

func (d *vmCapturingWSDialer) Dial(ctx context.Context, endpoint string, headers http.Header, proxy string) (openAIWSClientConn, int, http.Header, error) {
	d.calls <- vmCapturedDial{url: endpoint, headers: headers.Clone(), proxy: proxy}
	return d.conn, http.StatusSwitchingProtocols, http.Header{"X-Request-Id": {"ws-handshake"}}, nil
}

func TestOpenAIVMWebSocketRouteAuthoritativeAndFailClosed(t *testing.T) {
	vm, repo, server := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") })
	route, handled, err := vm.websocketRoute(context.Background(), repo.account, "token", nil)
	require.NoError(t, err)
	require.False(t, handled)
	require.Nil(t, route)
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	route, handled, err = vm.websocketRoute(context.Background(), repo.account, "token", http.Header{"Authorization": {"Bearer caller"}, "Cookie": {"private"}, "X-VM2API-Access-Token": {"spoofed"}, "X-Codex-Turn-State": {"original"}})
	require.NoError(t, err)
	require.True(t, handled)
	require.Equal(t, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/accounts/7/responses", route.url)
	require.Equal(t, "Bearer "+vmTestSecret, route.headers.Get("Authorization"))
	require.Equal(t, "token", route.headers.Get("X-VM2API-Access-Token"))
	require.Equal(t, "chatgpt-owner", route.headers.Get("X-VM2API-ChatGPT-Account-ID"))
	require.Equal(t, "original", route.headers.Get("X-Codex-Turn-State"))
	require.Empty(t, route.headers.Get("Cookie"))
	repo.b.Status = "deleting"
	_, handled, err = vm.websocketRoute(context.Background(), repo.account, "token", nil)
	require.True(t, handled)
	require.ErrorContains(t, err, "direct WebSocket fallback is disabled")
	repo.b = nil
	repo.account.Extra = map[string]any{"openai_vm_bound": true}
	_, handled, err = vm.websocketRoute(context.Background(), repo.account, "token", nil)
	require.True(t, handled)
	require.ErrorContains(t, err, "transitioning")
}

func TestOpenAIVMWebSocketNativeInterruptContinuationUsageAndAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	control, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, repo, bridge := vmTestService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("VM WebSocket must not use an HTTP inference bridge")
	})
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	// A managed proxy must not be used for the bridge's own WS connection.
	proxyID := int64(91)
	repo.account.ProxyID = &proxyID
	repo.account.Proxy = &Proxy{ID: proxyID, Protocol: "http", Host: "127.0.0.1", Port: 1}
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 5
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.openaiVM, svc.accountRepo = vm, vm.accounts
	dialer := &vmCapturingWSDialer{conn: upstream, calls: make(chan vmCapturedDial, 1)}
	svc.openaiWSPassthroughDialer = dialer
	type completion struct {
		turn   int
		result *OpenAIForwardResult
		err    error
	}
	turns := make(chan completion, 4)
	checks := make(chan int, 4)
	leases := make(chan int, 4)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, control, svc, repo.account, func(c *gin.Context) *OpenAIWSIngressHooks {
		c.Request.Header.Set("X-OpenAI-Internal-Codex-Responses-Lite", "true")
		c.Request.Header.Set("Session_id", "original-session")
		c.Request.Header.Set("X-Codex-Turn-State", "foreign-account-state")
		svc.noteOpenAICodexTurnStateProvenance(c, &Account{ID: 99})
		return &OpenAIWSIngressHooks{
			BeforeRequest: func(turn int, payload []byte, model string) error {
				checks <- turn
				if turn == 3 {
					return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "quota denied", errors.New("quota denied"))
				}
				return nil
			},
			BeforeTurn: func(turn int) error { leases <- turn; return nil },
			AfterTurn:  func(turn int, result *OpenAIForwardResult, err error) { turns <- completion{turn, result, err} },
		}
	})
	defer server.Close()
	first := `{"type":"response.create","model":"gpt-5.4","previous_response_id":"resp_previous","input":[{"type":"function_call_output","call_id":"call_original","output":"keep me"}],"store":true,"max_output_tokens":128,"parallel_tool_calls":true,"client_metadata":{"custom":"unchanged"}}`
	client := dialPassthroughLifecycleClientWithPayload(t, server, first)
	defer func() { _ = client.CloseNow() }()
	sent := requirePassthroughUpstreamWrite(t, upstream, 2*time.Second)
	require.JSONEq(t, first, string(sent))
	call := <-dialer.calls
	require.Equal(t, "ws"+strings.TrimPrefix(bridge.URL, "http")+"/v1/accounts/7/responses", call.url)
	require.Empty(t, call.proxy)
	require.Equal(t, "Bearer "+vmTestSecret, call.headers.Get("Authorization"))
	require.Equal(t, "sk-test", call.headers.Get("X-VM2API-Access-Token"))
	require.Equal(t, "true", call.headers.Get("X-OpenAI-Internal-Codex-Responses-Lite"))
	require.Empty(t, call.headers.Get("X-Codex-Turn-State"))
	// No upstream event has been sent. Interrupt must already be readable.
	require.NoError(t, client.Write(control, coderws.MessageText, []byte(`{"type":"response.interrupt"}`)))
	require.Equal(t, "response.interrupt", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_one","model":"gpt-5.4","usage":{"input_tokens":10,"output_tokens":4,"input_tokens_details":{"cached_tokens":3}}}}`)
	_, err := readPassthroughLifecycleFrame(t, client, 2*time.Second)
	require.NoError(t, err)
	select {
	case done := <-turns:
		require.NoError(t, done.err)
		require.Equal(t, 1, done.turn)
		require.EqualValues(t, 10, done.result.Usage.InputTokens)
		require.EqualValues(t, 4, done.result.Usage.OutputTokens)
		require.EqualValues(t, 3, done.result.Usage.CacheReadInputTokens)
		require.Equal(t, "resp_one", done.result.ResponseID)
	case <-time.After(2 * time.Second):
		t.Fatal("first turn usage callback missing")
	}
	second := `{"type":"response.create","model":"gpt-5.4","previous_response_id":"resp_one","input":[{"type":"function_call_output","call_id":"call_second","output":"continue"}],"tools":[{"type":"image_generation","size":"1024x1024"}]}`
	require.NoError(t, client.Write(control, coderws.MessageText, []byte(second)))
	require.JSONEq(t, second, string(requirePassthroughUpstreamWrite(t, upstream, 2*time.Second)))
	require.Equal(t, 2, <-checks)
	require.Equal(t, 2, <-leases)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_two","model":"gpt-5.4","output":[{"id":"img_1","type":"image_generation_call","status":"completed","result":"ZmFrZQ==","size":"1024x1024"}],"usage":{"input_tokens":20,"output_tokens":9}}}`)
	_, err = readPassthroughLifecycleFrame(t, client, 2*time.Second)
	require.NoError(t, err)
	select {
	case done := <-turns:
		require.NoError(t, done.err)
		require.Equal(t, 2, done.turn)
		require.EqualValues(t, 20, done.result.Usage.InputTokens)
		require.EqualValues(t, 9, done.result.Usage.OutputTokens)
		require.Equal(t, 1, done.result.ImageCount)
		require.NotEmpty(t, done.result.BillingModel)
	case <-time.After(2 * time.Second):
		t.Fatal("second turn image usage callback missing")
	}
	require.NoError(t, client.Write(control, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.4","input":"blocked"}`)))
	require.Equal(t, 3, <-checks)
	_, err = readPassthroughLifecycleFrame(t, client, 2*time.Second)
	require.Error(t, err)
	select {
	case err := <-serverErr:
		require.ErrorContains(t, err, "quota denied")
	case <-time.After(3 * time.Second):
		t.Fatal("authorization failure did not close WS")
	}
	select {
	case payload := <-upstream.writes:
		t.Fatalf("denied turn reached upstream: %s", payload)
	default:
	}
	select {
	case turn := <-leases:
		t.Fatalf("denied turn acquired lease: %d", turn)
	default:
	}
}

func TestOpenAIVMWSImagePermission(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set("api_key", &APIKey{Group: &Group{AllowImageGeneration: false}})
	_, err := openAIVMWSImageBillingConfig(c, []byte(`{"tools":[{"type":"image_generation"}]}`), "gpt-5.4")
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
}

func TestOpenAIVMWebSocketRejectsRedirectAndEnvironmentProxy(t *testing.T) {
	var leaked atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); w.WriteHeader(500) }))
	defer destination.Close()
	vm, repo, bridge := vmTestService(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) })
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	route, handled, err := vm.websocketRoute(context.Background(), repo.account, "account-at", nil)
	require.NoError(t, err)
	require.True(t, handled)
	transport, ok := route.client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy, "managed bridge must not consult HTTP_PROXY")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, status, _, err := newDefaultOpenAIWSClientDialer().Dial(withOpenAIVMWSHTTPClient(ctx, route.client), "ws"+strings.TrimPrefix(bridge.URL, "http"), route.headers, "")
	require.Error(t, err)
	require.Equal(t, http.StatusFound, status)
	require.Zero(t, leaked.Load(), "redirect target must not receive bridge credentials")
}

func TestOpenAIVMWebSocketIgnoresEnvironmentProxy(t *testing.T) {
	// net/http caches environment proxy configuration. Use a child process so
	// another test cannot make this regression accidentally pass.
	if os.Getenv("SUB2API_VM_PROXY_TEST_CHILD") != "1" {
		executable, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(executable, "-test.run=^TestOpenAIVMWebSocketIgnoresEnvironmentProxy$", "-test.timeout=10s")
		cmd.Env = append(os.Environ(), "SUB2API_VM_PROXY_TEST_CHILD=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return
	}
	var leaked atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("ALL_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	vm, repo, bridge := vmTestService(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+vmTestSecret, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
	})
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	repo.c.BaseURL = "http://bridge-vm-test.invalid"
	baseTransport, ok := vm.client.Transport.(*http.Transport)
	require.True(t, ok)
	transport := baseTransport.Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, bridge.Listener.Addr().String())
	}
	vm.client.Transport = transport
	defer transport.CloseIdleConnections()
	route, _, err := vm.websocketRoute(context.Background(), repo.account, "private-at", nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, status, _, err := newDefaultOpenAIWSClientDialer().Dial(withOpenAIVMWSHTTPClient(ctx, route.client), route.url, route.headers, "")
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Zero(t, leaked.Load(), "HTTP_PROXY must not receive VM authentication headers")
}

func TestOpenAIVMWebSocketQueueAndInterruptBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, repo, _ := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP call") })
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 5
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.openaiVM, svc.accountRepo = vm, vm.accounts
	done := make(chan *OpenAIForwardResult, 3)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, repo.account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
			if err == nil {
				done <- result
			}
		}}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer func() { _ = client.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	second := `{"type":"response.create","model":"gpt-5.1","input":"queued"}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(second)))
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.interrupt"}`)))
	require.Equal(t, "response.interrupt", gjson.GetBytes(requirePassthroughUpstreamWrite(t, upstream, time.Second), "type").String(), "queued create must not block interrupt")
	upstream.Send(`{"type":"response.interrupt.accepted"}`)
	ack, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"response.interrupt.accepted"}`, string(ack))
	select {
	case result := <-done:
		require.Empty(t, result.ResponseID)
	case <-time.After(time.Second):
		t.Fatal("interrupt did not release turn accounting")
	}
	require.JSONEq(t, second, string(requirePassthroughUpstreamWrite(t, upstream, time.Second)))
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_queued","model":"gpt-5.1","usage":{"input_tokens":8,"output_tokens":6}}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	select {
	case result := <-done:
		require.EqualValues(t, 8, result.Usage.InputTokens)
		require.EqualValues(t, 6, result.Usage.OutputTokens)
	case <-time.After(time.Second):
		t.Fatal("queued turn usage missing")
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("session did not close")
	}
}

func TestOpenAIVMWebSocketInvalidFramesDoNotConsumeNextTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, repo, _ := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP call") })
	repo.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	upstream := newStagedPassthroughConn()
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 5
	svc := newPassthroughLifecycleService(cfg, upstream)
	svc.openaiVM, svc.accountRepo = vm, vm.accounts
	checked := make(chan int, 4)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, repo.account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{BeforeRequest: func(turn int, _ []byte, _ string) error { checked <- turn; return nil }}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1"}`)
	defer func() { _ = client.CloseNow() }()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_initial","usage":{"input_tokens":1,"output_tokens":1}}}`)
	_, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	for _, invalid := range []string{`{"type":"session.update","session":{"model":"forbidden"}}`, `{"type":"response.create","stream":false}`, `{invalid`} {
		require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(invalid)))
		reply, err := readPassthroughLifecycleFrame(t, client, time.Second)
		require.NoError(t, err)
		require.Equal(t, "error", gjson.GetBytes(reply, "type").String())
	}
	second := `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_missing"}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(second)))
	require.JSONEq(t, second, string(requirePassthroughUpstreamWrite(t, upstream, time.Second)))
	require.Equal(t, 2, <-checked)
	upstream.Send(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"previous_response_not_found","message":"Unknown previous response"}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	third := `{"type":"response.create","model":"gpt-5.1","input":"recover"}`
	require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(third)))
	require.JSONEq(t, third, string(requirePassthroughUpstreamWrite(t, upstream, time.Second)))
	require.Equal(t, 3, <-checked)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_recovered","usage":{"input_tokens":2,"output_tokens":1}}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("session did not close")
	}
	select {
	case turn := <-checked:
		t.Fatalf("invalid frame incorrectly admitted as turn %d", turn)
	default:
	}
}
