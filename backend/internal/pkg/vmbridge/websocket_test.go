package vmbridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestInferenceEndpointMappingWithoutPayloadRewrite(t *testing.T) {
	for _, endpoint := range []string{"responses", "responses/compact", "images/generations", "images/edits", "alpha/search"} {
		t.Run(endpoint, func(t *testing.T) {
			payload := `{ "previous_response_id":"resp_1", "input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}] }`
			m := newTestManager(t, &testProvider{forward: func(_ context.Context, _ Resources, r *http.Request) (*http.Response, error) {
				b, _ := io.ReadAll(r.Body)
				if r.URL.Path != "/v1/"+endpoint || string(b) != payload {
					t.Fatal("changed endpoint or payload")
				}
				return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"unsupported by upstream"}`))}, nil
			}})
			provisionAccount(t, m, 1)
			w := httptest.NewRecorder()
			m.Handler().ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/accounts/1/"+endpoint, payload))
			if w.Code != 404 || !strings.Contains(w.Body.String(), "unsupported by upstream") {
				t.Fatal("upstream status lost")
			}
		})
	}
}

func TestWebSocketInterruptPassesDuringActiveTurnAndDrains(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			typ, body, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if err = conn.Write(ctx, typ, body); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	m := newTestManager(t, &testProvider{forward: func(ctx context.Context, _ Resources, r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" || r.Header.Get(AccessTokenHeader) != "" {
			t.Error("bridge credential escaped")
		}
		r.URL, _ = url.Parse(upstream.URL + "/v1/responses")
		return http.DefaultClient.Do(r.WithContext(ctx))
	}})
	provisionAccount(t, m, 1)
	bridge := httptest.NewServer(m.Handler())
	defer bridge.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(bridge.URL, "http")+"/v1/accounts/1/responses", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + testSecret}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	for _, payload := range []string{`{"type":"response.create","response":{"previous_response_id":"resp_1"}}`, `{"type":"response.interrupt"}`} {
		if err = conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		_, got, err := conn.Read(ctx)
		if err != nil || string(got) != payload {
			t.Fatalf("websocket payload changed: %s %v", got, err)
		}
	}
	status, _ := m.Status(1)
	if status.ActiveLeases != 1 {
		t.Fatal("missing websocket lease")
	}
	requireCode(t, m.Delete(ctx, 1), "account_busy")
	_ = conn.CloseNow()
	for deadline := time.Now().Add(time.Second); ; {
		status, _ = m.Status(1)
		if status.ActiveLeases == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("websocket lease did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	if err = m.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
}
