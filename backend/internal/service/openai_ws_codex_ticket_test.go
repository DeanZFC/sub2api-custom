package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newCodexTicketWSTestPool(t *testing.T) (*openAIWSConnPool, *openAIWSCountingDialer) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	pool := newOpenAIWSConnPool(cfg)
	dialer := &openAIWSCountingDialer{}
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	return pool, dialer
}

func TestOpenAIWSCodexTicketRotationControlsSocketReuse(t *testing.T) {
	for _, changed := range []string{"ticket", "cookie", "unmanaged"} {
		t.Run(changed, func(t *testing.T) {
			pool, dialer := newCodexTicketWSTestPool(t)
			var generation atomic.Int32
			req := openAIWSAcquireRequest{
				Account: ticketTestAccount(41),
				WSURL:   "wss://chatgpt.com/backend-api/codex/responses",
				CodexTicketHeadersFactory: func(_ context.Context, h http.Header) (http.Header, bool, error) {
					if h == nil {
						h = make(http.Header)
					}
					state, cookie := fakeCodexTicketState(292), "__cflb=one; __oailb=one"
					if generation.Load() > 0 {
						if changed != "cookie" {
							state = strings.Replace(state, "B", "C", 1)
						}
						if changed != "ticket" {
							cookie = "__cflb=two; __oailb=two"
						}
					}
					h.Set(openAICodexTurnStateHeader, state)
					h.Set("Cookie", cookie)
					return h, changed != "unmanaged", nil
				},
			}
			first, err := pool.Acquire(context.Background(), req)
			require.NoError(t, err)
			firstID := first.ConnID()
			first.Release()
			same, err := pool.Acquire(context.Background(), req)
			require.NoError(t, err)
			require.True(t, same.Reused())
			require.Equal(t, firstID, same.ConnID())
			same.Release()
			generation.Store(1)
			next, err := pool.Acquire(context.Background(), req)
			require.NoError(t, err)
			defer next.Release()
			if changed == "unmanaged" {
				require.True(t, next.Reused())
				require.Equal(t, firstID, next.ConnID())
				require.Equal(t, 1, dialer.DialCount())
			} else {
				require.False(t, next.Reused())
				require.NotEqual(t, firstID, next.ConnID())
				require.Equal(t, 2, dialer.DialCount())
			}
		})
	}
}

func TestOpenAIWSCodexTicketDialRefreshUsesActualHandshake(t *testing.T) {
	pool, _ := newCodexTicketWSTestPool(t)
	var calls atomic.Int32
	updated := http.Header{}
	updated.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	updated.Set("Cookie", "__cflb=fresh; __oailb=fresh")
	req := openAIWSAcquireRequest{
		Account: ticketTestAccount(41),
		WSURL:   "wss://chatgpt.com/backend-api/codex/responses",
		CodexTicketHeadersFactory: func(_ context.Context, _ http.Header) (http.Header, bool, error) {
			h := updated.Clone()
			if calls.Add(1) == 1 {
				h.Set("Cookie", "__cflb=old; __oailb=old")
			}
			return h, true, nil
		},
	}
	lease, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, normalizeOpenAIWSHandshakeCompatibility(req.Account, updated, true), lease.conn.handshakeCompatibility)
	require.GreaterOrEqual(t, calls.Load(), int32(3), "refresh before selection, dial, and returning a lease")
	lease.Release()
	second, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	require.True(t, second.Reused(), "connection must remember refreshed dial credentials")
	second.Release()
}

func TestOpenAIWSCodexTicketExpiresWhileWaiting(t *testing.T) {
	pool, dialer := newCodexTicketWSTestPool(t)
	var expired atomic.Bool
	req := openAIWSAcquireRequest{
		Account: ticketTestAccount(41),
		WSURL:   "wss://chatgpt.com/backend-api/codex/responses",
		CodexTicketHeadersFactory: func(_ context.Context, _ http.Header) (http.Header, bool, error) {
			if expired.Load() {
				return nil, false, ErrOpenAICodexTicketUnavailable
			}
			h := http.Header{}
			h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
			h.Set("Cookie", "__cflb=valid; __oailb=valid")
			return h, true, nil
		},
	}
	lease, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	req.PreferredConnID, req.ForcePreferredConn = lease.ConnID(), true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		waiting, err := pool.Acquire(ctx, req)
		if waiting != nil {
			waiting.Release()
		}
		finished <- err
	}()
	require.Eventually(t, func() bool { return lease.conn.waiters.Load() == 1 }, time.Second, time.Millisecond)
	expired.Store(true)
	lease.Release()
	require.ErrorIs(t, <-finished, ErrOpenAICodexTicketUnavailable)
	require.Equal(t, 1, dialer.DialCount())
}

func TestOpenAIWSCodexTicketFactoryPoliciesAndIsolation(t *testing.T) {
	for _, mode := range []string{"ready", "gateway_off", "account_off", "ungated", "api_key", "fail_open", "fail_open_missing_cookie", "missing_cookie", "other_account"} {
		t.Run(mode, func(t *testing.T) {
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
			account := ticketTestAccount(41)
			model := "gpt-6-astra"
			if mode != "missing_cookie" && mode != "fail_open_missing_cookie" {
				seedCodexTicketCookies(svc, account)
			}
			if mode != "fail_open" {
				svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
					Model: model, State: fakeCodexTicketState(292), Length: 292,
					CapturedAt: time.Now(), ExpiresAt: time.Now().Add(240 * time.Second),
				})
			}
			switch mode {
			case "gateway_off":
				svc.cfg.Gateway.OpenAICodexTicket.Enabled = false
			case "account_off":
				account.Extra = map[string]any{OpenAICodexTicketEnabledExtraKey: false}
			case "ungated":
				model = "gpt-5.5"
			case "api_key":
				account.Type = AccountTypeAPIKey
			case "fail_open", "fail_open_missing_cookie":
				svc.cfg.Gateway.OpenAICodexTicket.FailClosed = false
			case "other_account":
				account = ticketTestAccount(42)
			}
			headers := http.Header{}
			headers.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
			got, managed, err := svc.openAIWSCodexTicketHeadersFactory(account, model)(context.Background(), headers)
			if mode == "missing_cookie" || mode == "other_account" {
				require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
				require.False(t, managed)
				return
			}
			require.NoError(t, err)
			require.Equal(t, mode == "ready", managed)
			if mode == "ready" {
				require.Contains(t, got.Get("Cookie"), "__cflb=test-cflb")
				require.Contains(t, got.Get("Cookie"), "__oailb=test-oailb")
			} else {
				require.Empty(t, got.Get("Cookie"))
			}
		})
	}
}

func TestOpenAIWSCodexTicketClearsRetiredManagedHeaders(t *testing.T) {
	req := openAIWSAcquireRequest{
		Headers: http.Header{
			http.CanonicalHeaderKey(openAICodexTurnStateHeader): {"old-managed-ticket"},
			"Cookie": {"__cflb=old; __oailb=old"},
		},
		codexTicketManaged: true,
		CodexTicketHeadersFactory: func(_ context.Context, h http.Header) (http.Header, bool, error) {
			return h, false, nil
		},
	}
	require.NoError(t, req.refreshCodexTicketHeaders(context.Background()))
	require.Empty(t, req.Headers.Get(openAICodexTurnStateHeader))
	require.Empty(t, req.Headers.Get("Cookie"))
	require.False(t, req.codexTicketManaged)
}

func TestOpenAIWSCodexTicketIngressRotatesOnlyStandaloneTurns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, continuation := range []bool{false, true} {
		name := "full_input_reconnects"
		if continuation {
			name = "tool_continuation_keeps_connection"
		}
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			firstEvent := []byte(`{"type":"response.completed","response":{"id":"resp_ticket_1","model":"gpt-6-astra","output":[{"type":"function_call","name":"tool","call_id":"call_ticket","arguments":"{}"}],"usage":{"input_tokens":1,"output_tokens":1}}}`)
			secondEvent := []byte(`{"type":"response.completed","response":{"id":"resp_ticket_2","model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":1}}}`)
			firstConn := &openAIWSCaptureConn{events: [][]byte{firstEvent, secondEvent}}
			secondConn := &openAIWSCaptureConn{events: [][]byte{secondEvent}}
			dialer := &openAIWSQueueDialer{conns: []openAIWSClientConn{firstConn, secondConn}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			svc := &OpenAIGatewayService{
				cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool,
			}
			account := ticketTestAccount(41)
			account.Concurrency = 1
			account.Extra = map[string]any{
				"openai_oauth_responses_websockets_v2_mode": "ctx_pool",
				"responses_websockets_v2_enabled":           true,
				codexFingerprintModeExtraKey:                "off",
			}
			seedCodexTicketCookies(svc, account)
			storeTicket := func(state string) {
				svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
					Model: "gpt-6-astra", State: state, Length: 292,
					CapturedAt: time.Now(), ExpiresAt: time.Now().Add(240 * time.Second),
				})
			}
			storeTicket(fakeCodexTicketState(292))
			serverErrCh := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					serverErrCh <- err
					return
				}
				defer conn.CloseNow()
				ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ginCtx.Request = r
				_, firstMessage, err := conn.Read(r.Context())
				if err != nil {
					serverErrCh <- err
					return
				}
				proxyErr := svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, account, "tok", firstMessage, nil)
				if proxyErr != nil {
					t.Logf("ingress returned: %v", proxyErr)
				}
				serverErrCh <- proxyErr
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer client.CloseNow()
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"role":"user","content":"hello"}]}`)))
			_, response, err := client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, "resp_ticket_1", gjson.GetBytes(response, "response.id").String())
			storeTicket(strings.Replace(fakeCodexTicketState(292), "B", "C", 1))
			next := `{"type":"response.create","model":"gpt-6-astra","input":[{"role":"user","content":"second complete input"}]}`
			if continuation {
				next = `{"type":"response.create","model":"gpt-6-astra","previous_response_id":"resp_ticket_1","input":[{"type":"function_call_output","call_id":"call_ticket","output":"ok"}]}`
			}
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(next)))
			_, response, err = client.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, "resp_ticket_2", gjson.GetBytes(response, "response.id").String())
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErrCh:
				require.NoError(t, err)
			case <-ctx.Done():
				t.Fatal("ingress did not finish")
			}
			if continuation {
				require.Equal(t, 1, dialer.DialCount())
				require.Len(t, firstConn.writes, 2)
				require.Empty(t, secondConn.writes)
			} else {
				require.Equal(t, 2, dialer.DialCount())
				require.Len(t, firstConn.writes, 1)
				require.Len(t, secondConn.writes, 1)
			}
		})
	}
}
