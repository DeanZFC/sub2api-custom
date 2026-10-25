package vm2api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/vmbridge"
	"github.com/coder/websocket"
)

func TestForwardKeepsVm2apiKernelHeaders(t *testing.T) {
	headers := make(http.Header)
	for _, name := range inferenceHeaders {
		headers.Set(name, "value-"+name)
	}
	for _, name := range []string{"Authorization", "Cookie", "Proxy-Authorization", "X-Kin-Vm", "X-Kin-Debug", vmbridge.AccessTokenHeader, vmbridge.AccountIdentityHeader} {
		headers.Set(name, "secret-do-not-forward")
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		for _, name := range inferenceHeaders {
			if r.Header.Get(name) != headers.Get(name) {
				t.Errorf("lost header %s", name)
			}
		}
		if r.Header.Get("Authorization") != "Bearer scoped-key" {
			t.Error("not using scoped key")
		}
		for name, values := range r.Header {
			for _, value := range values {
				if strings.Contains(value, "secret-do-not-forward") {
					t.Errorf("leaked %s", name)
				}
			}
		}
		w.WriteHeader(204)
	})
	resp, err := c.ForwardResponses(context.Background(), "scoped-key", strings.NewReader(`{}`), headers)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}

func TestForwardSessionAliasesAndFixedPublicEndpoints(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/responses/compact", "/v1/images/generations", "/v1/images/edits", "/v1/alpha/search"} {
		t.Run(path, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != path || r.URL.RawQuery != "" {
					t.Error("endpoint changed")
				}
				if r.Header.Get("Session-Id") != "session" || r.Header.Get("X-Conversation-Id") != "conversation" {
					t.Error("session aliases missing")
				}
				b, _ := io.ReadAll(r.Body)
				if string(b) != "opaque-body" {
					t.Error("body changed")
				}
				w.WriteHeader(404)
			})
			p, _ := NewProvider(c, "installation")
			req := httptest.NewRequest(http.MethodPost, "http://ignored.invalid"+path+"?vm_id=other", strings.NewReader("opaque-body"))
			req.Header.Set("Session_id", "session")
			req.Header.Set("Conversation_id", "conversation")
			resp, err := p.Forward(context.Background(), vmbridge.Resources{APIKey: "scoped-key"}, req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 404 {
				t.Fatal("upstream error changed")
			}
		})
	}
}

func TestWebSocketUsesOnlySlotCredentialAndPreservesFrames(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer scoped-key" || r.URL.Path != "/v1/responses" || r.Header.Get(vmbridge.AccessTokenHeader) != "" {
			t.Error("wrong upgrade credentials or path")
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"responses"}})
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			typ, payload, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if conn.Write(ctx, typ, payload) != nil {
				return
			}
		}
	})
	p, _ := NewProvider(c, "installation")
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := p.Forward(r.Context(), vmbridge.Resources{APIKey: "scoped-key"}, r)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 101 {
			t.Errorf("upgrade=%d", resp.StatusCode)
			return
		}
		duplex, ok := resp.Body.(io.ReadWriteCloser)
		if !ok {
			t.Error("upgrade is not writable")
			return
		}
		client, buf, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = client.Close() }()
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		_ = resp.Header.Write(buf)
		_, _ = buf.WriteString("\r\n")
		_ = buf.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(duplex, buf.Reader); close(done) }()
		_, _ = io.Copy(client, duplex)
		_ = client.Close()
		_ = duplex.Close()
		<-done
	}))
	defer bridge.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(bridge.URL, "http")+"/v1/responses", &websocket.DialOptions{Subprotocols: []string{"responses"}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	if conn.Subprotocol() != "responses" {
		t.Fatal("subprotocol dropped")
	}
	for _, payload := range []string{`{"type":"response.create"}`, `{"type":"response.interrupt"}`} {
		if conn.Write(ctx, websocket.MessageText, []byte(payload)) != nil {
			t.Fatal("write failed")
		}
		_, got, err := conn.Read(ctx)
		if err != nil || string(got) != payload {
			t.Fatalf("frame changed: %s %v", got, err)
		}
	}
}
