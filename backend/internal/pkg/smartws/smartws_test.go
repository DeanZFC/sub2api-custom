package smartws

import (
	"context"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testPool(t *testing.T) *Pool {
	t.Helper()
	p := newPool(poolConfig{capacity: 8, perKey: 4, perAccount: 8, hardCapacity: 8, wait: 100 * time.Millisecond, maxWaiters: 8, accountWaiters: 4, maxWaitingBytes: 1 << 20, idle: time.Minute, lifetime: time.Minute, handshake: time.Second, allowed: func(*url.URL) bool { return true }, dialer: &websocket.Dialer{HandshakeTimeout: time.Second}, dialRate: 1000, dialBurst: 100, dialConcurrent: 8})
	t.Cleanup(p.Close)
	return p
}
func testRequest(t *testing.T, target, session string) *http.Request {
	t.Helper()
	req, err := http.NewRequest("POST", target, strings.NewReader(`{"model":"test","stream":true,"input":[]}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer synthetic-token")
	req.Header.Set("session_id", session)
	req.Header.Set("ChatGPT-Account-Id", "synthetic-account")
	return req
}
func TestPoolReuseAndIdentityIsolation(t *testing.T) {
	var opened, dispatched atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		opened.Add(1)
		defer func() { _ = c.Close() }()
		for {
			_, _, err = c.ReadMessage()
			if err != nil {
				return
			}
			id := fmt.Sprint(dispatched.Add(1))
			for _, event := range []string{fmt.Sprintf(`{"type":"response.created","response":{"id":"%s"}}`, id), fmt.Sprintf(`{"type":"response.completed","response":{"id":"%s","status":"completed"}}`, id)} {
				if c.WriteMessage(websocket.TextMessage, []byte(event)) != nil {
					return
				}
			}
		}
	}))
	defer server.Close()
	p := testPool(t)
	for _, scope := range []string{"group1/user1/key1", "group1/user1/key1", "group1/user2/key2"} {
		resp, handled, err := p.Try(testRequest(t, server.URL, "session-a"), "", scope)
		require.NoError(t, err)
		require.True(t, handled)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "response.completed")
		require.NoError(t, resp.Body.Close())
	}
	require.EqualValues(t, 2, opened.Load())
	require.EqualValues(t, 3, dispatched.Load())
}
func TestFallbackOnlyBeforeDispatch(t *testing.T) {
	unsupported := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(426) }))
	defer unsupported.Close()
	p := testPool(t)
	resp, handled, err := p.Try(testRequest(t, unsupported.URL, "s"), "", "scope")
	require.NoError(t, err)
	require.False(t, handled)
	require.Nil(t, resp)
	var sent atomic.Int64
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer func() { _ = c.Close() }()
		if _, _, e = c.ReadMessage(); e == nil {
			sent.Add(1)
		}
	}))
	defer broken.Close()
	resp, handled, err = p.Try(testRequest(t, broken.URL, "s"), "", "scope")
	require.NoError(t, err)
	require.True(t, handled)
	_, err = io.ReadAll(resp.Body)
	var dispatch *SentError
	require.ErrorAs(t, err, &dispatch)
	require.EqualValues(t, 1, sent.Load())
}
func TestPoolBudgetAndEligibility(t *testing.T) {
	p := testPool(t)
	req := testRequest(t, "http://127.0.0.1:1", "s")
	ctx, release := httputil.WithRequestBodyMemoryBudget(context.Background(), httputil.NewBodyMemoryBudget(1))
	defer release()
	_, handled, err := p.Try(req.WithContext(ctx), "", "scope")
	require.True(t, handled)
	require.True(t, errors.Is(err, httputil.ErrRequestBodyMemoryBudget))
	for _, body := range []string{`{"stream":true,"previous_response_id":"old"}`, `{"stream":true,"stream":false}`, `{"stream":false}`} {
		_, ok := prepareMessage([]byte(body))
		require.False(t, ok)
	}
	req.ContentLength = 1
	_, handled, err = p.Try(req, "", "scope")
	require.NoError(t, err)
	require.False(t, handled)
}
func TestAffinityIncludesProxyAndHeaders(t *testing.T) {
	a := http.Header{"Authorization": {"Bearer a"}, "Session_id": {"one"}}
	b := a.Clone()
	b.Set("Authorization", "Bearer b")
	require.NotEqual(t, headersKey(a, false), headersKey(b, false))
	require.NotEqual(t, digest("url", "proxy-a", "scope", headersKey(a, false)), digest("url", "proxy-b", "scope", headersKey(a, false)))
}
