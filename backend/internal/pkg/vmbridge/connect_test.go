package vmbridge

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func proxyAuthorization(id string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+ProxyPassword(testSecret, 1)))
}

func TestConnectRejectsUnscopedAuthenticationAndTargets(t *testing.T) {
	m := newTestManager(t, &testProvider{})
	provisionAccount(t, m, 1)
	for _, tc := range []struct {
		target, auth string
		status       int
	}{
		{"127.0.0.1:443", proxyAuthorization("1"), 403},
		{"api.openai.com:80", proxyAuthorization("1"), 403},
		{"api.openai.com.evil.test:443", proxyAuthorization("1"), 403},
		{"api.openai.com.:443", proxyAuthorization("1"), 403},
		{"api.openai.com:443", "", 407},
		{"api.openai.com:443", proxyAuthorization("01"), 407},
		{"api.openai.com:443", proxyAuthorization("2"), 407},
		{"api.openai.com:443", "Bearer " + testSecret, 407},
	} {
		r := httptest.NewRequest(http.MethodConnect, "http://bridge", nil)
		r.Host = tc.target
		r.Header.Set("Proxy-Authorization", tc.auth)
		w := httptest.NewRecorder()
		m.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("target %s status %d want %d", tc.target, w.Code, tc.status)
		}
	}
}

func TestConfiguredProxyFailureNeverFallsBackToDirect(t *testing.T) {
	// The destination is reachable. A configured failing proxy must still fail.
	destination, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = destination.Close() }()
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := dead.Addr().String()
	_ = dead.Close()
	for _, scheme := range []string{"socks5", "http"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, err := dialAccountTunnel(ctx, destination.Addr().String(), scheme+"://"+deadAddress)
		cancel()
		if conn != nil {
			_ = conn.Close()
			t.Fatal("configured proxy silently fell back to direct")
		}
		if err == nil {
			t.Fatal("expected upstream failure")
		}
	}
}

func TestHTTPConnectPreservesBufferedTunnelBytesAndAuthentication(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "api.openai.com:443" || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("user:pass")) {
			t.Error("incorrect upstream CONNECT")
			w.WriteHeader(400)
			return
		}
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\nprefetched")
		_ = rw.Flush()
		_, _ = io.Copy(c, rw.Reader)
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	u.User = url.UserPassword("user", "pass")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialAccountTunnel(ctx, "api.openai.com:443", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	prefetched := make([]byte, len("prefetched"))
	if _, err = io.ReadFull(conn, prefetched); err != nil || string(prefetched) != "prefetched" {
		t.Fatalf("buffered bytes lost: %q %v", prefetched, err)
	}
	_, _ = io.WriteString(conn, "ping")
	echo := make([]byte, 4)
	if _, err = io.ReadFull(conn, echo); err != nil || string(echo) != "ping" {
		t.Fatal("tunnel did not forward bytes")
	}
}

func TestProxyHandshakeCancellation(t *testing.T) {
	accepted := make(chan struct{})
	closed := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		close(accepted)
		_, _ = io.Copy(io.Discard, c)
		close(closed)
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := dialAccountTunnel(ctx, "api.openai.com:443", upstream.URL)
		if conn != nil {
			_ = conn.Close()
		}
		if err == nil {
			t.Error("canceled handshake succeeded")
		}
	}()
	waitSignal(t, accepted)
	cancel()
	waitSignal(t, done)
	waitSignal(t, closed)
}

type socksObservation struct{ user, password, target string }

// This server accepts one authenticated SOCKS5 tunnel and echoes opaque bytes.
func startSOCKSEcho(t *testing.T) (string, <-chan socksObservation, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	observed := make(chan socksObservation, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		header := make([]byte, 2)
		if _, err = io.ReadFull(conn, header); err != nil || header[0] != 5 {
			return
		}
		methods := make([]byte, int(header[1]))
		if _, err = io.ReadFull(conn, methods); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 2})
		if _, err = io.ReadFull(conn, header); err != nil || header[0] != 1 {
			return
		}
		user := make([]byte, int(header[1]))
		if _, err = io.ReadFull(conn, user); err != nil {
			return
		}
		n := make([]byte, 1)
		if _, err = io.ReadFull(conn, n); err != nil {
			return
		}
		password := make([]byte, int(n[0]))
		if _, err = io.ReadFull(conn, password); err != nil {
			return
		}
		_, _ = conn.Write([]byte{1, 0})
		request := make([]byte, 4)
		if _, err = io.ReadFull(conn, request); err != nil || request[0] != 5 || request[1] != 1 || request[3] != 3 {
			return
		}
		if _, err = io.ReadFull(conn, n); err != nil {
			return
		}
		host := make([]byte, int(n[0]))
		if _, err = io.ReadFull(conn, host); err != nil {
			return
		}
		port := make([]byte, 2)
		if _, err = io.ReadFull(conn, port); err != nil {
			return
		}
		observed <- socksObservation{string(user), string(password), fmt.Sprintf("%s:%d", host, binary.BigEndian.Uint16(port))}
		_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
		_, _ = io.Copy(conn, conn)
	}()
	return "socks5://proxy-user:proxy-pass@" + listener.Addr().String(), observed, finished
}

func TestConnectUsesAccountProxyAndKeepsLeaseUntilTunnelCloses(t *testing.T) {
	proxyURL, observed, finished := startSOCKSEcho(t)
	m := newTestManager(t, &testProvider{})
	in := testInput(1)
	in.UpstreamProxyURL = proxyURL
	if _, _, err := m.Provision(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)
	conn, err := net.DialTimeout("tcp", serverURL.Host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = fmt.Fprintf(conn, "CONNECT api.openai.com:443 HTTP/1.1\r\nHost: api.openai.com:443\r\nProxy-Authorization: %s\r\n\r\n", proxyAuthorization("1"))
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v %v", response, err)
	}
	select {
	case actual := <-observed:
		if actual.user != "proxy-user" || actual.password != "proxy-pass" || actual.target != "api.openai.com:443" {
			t.Fatal("account exit or destination changed")
		}
	case <-time.After(time.Second):
		t.Fatal("no upstream SOCKS request")
	}
	_, _ = io.WriteString(conn, "opaque tls bytes")
	echo := make([]byte, len("opaque tls bytes"))
	if _, err = io.ReadFull(reader, echo); err != nil || string(echo) != "opaque tls bytes" {
		t.Fatal("CONNECT modified payload")
	}
	status, _ := m.Status(1)
	if status.ActiveLeases != 1 {
		t.Fatal("CONNECT has no active lease")
	}
	err = m.Delete(context.Background(), 1)
	requireCode(t, err, "account_busy")
	r := httptest.NewRequest(http.MethodConnect, "http://bridge", nil)
	r.Host = "api.openai.com:443"
	r.Header.Set("Proxy-Authorization", proxyAuthorization("1"))
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "account_not_ready") {
		t.Fatal("draining account accepted another tunnel")
	}
	_ = conn.Close()
	waitSignal(t, finished)
	deadline := time.Now().Add(time.Second)
	for {
		status, _ = m.Status(1)
		if status.ActiveLeases == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed tunnel leaked lease")
		}
		time.Sleep(time.Millisecond)
	}
	if err = m.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
}
