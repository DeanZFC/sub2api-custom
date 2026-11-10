package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *leaseStore {
	t.Helper()
	s, e := newStore(filepath.Join(t.TempDir(), "leases.json"), netip.MustParsePrefix("2001:db8:1000::/64"), 10)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func auth(l lease) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(l.Username+":"+l.Password))
}
func TestLeasePersistenceAndRevocation(t *testing.T) {
	s := testStore(t)
	l, e := s.reserve("account-1")
	if e != nil {
		t.Fatal(e)
	}
	same, e := s.reserve(l.ID)
	if e != nil || same != l {
		t.Fatal("retry changed lease")
	}
	restored, e := newStore(s.path, s.prefix, s.limit)
	if e != nil {
		t.Fatal(e)
	}
	same, e = restored.reserve(l.ID)
	if e != nil || same != l {
		t.Fatal("restart changed lease")
	}
	p := &proxy{store: s, conns: map[net.Conn]struct{}{}}
	fail := func(context.Context, string) error { return errors.New("synthetic failure") }
	if p.release(context.Background(), l.ID, fail) == nil {
		t.Fatal("expected failure")
	}
	if _, ok := s.authenticate(auth(l)); ok {
		t.Fatal("revoked lease authenticated")
	}
	if _, e = s.reserve(l.ID); e == nil {
		t.Fatal("revoked lease was resurrected")
	}
	restored, e = newStore(s.path, s.prefix, s.limit)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := restored.authenticate(auth(l)); ok {
		t.Fatal("revocation lost on restart")
	}
	for i := 0; i < 2; i++ {
		if e = p.release(context.Background(), l.ID, func(context.Context, string) error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if len(s.leases) != 0 {
		t.Fatal("lease not removed")
	}
}
func TestControlAuthAndIdempotentRelease(t *testing.T) {
	s := testStore(t)
	p := &proxy{store: s, ensure: func(context.Context, string) error { return nil }, conns: map[net.Conn]struct{}{}}
	h := controlHandler("synthetic-control-secret", s, p, func(context.Context, string) error { return nil })
	for _, route := range []struct{ method, path string }{{"GET", "/health"}, {"POST", "/v1/leases"}, {"DELETE", "/v1/leases/a"}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(route.method, route.path, strings.NewReader(`{"id":"a"}`)))
		if w.Code != 401 {
			t.Fatal(route, w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/leases", strings.NewReader(`{"id":"a"}`))
		req.Header.Set("Authorization", "Bearer synthetic-control-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var l lease
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &l) != nil || l.ID != "a" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("DELETE", "/v1/leases/a", nil)
		req.Header.Set("Authorization", "Bearer synthetic-control-secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
}
func TestProxyRejectsUnauthorizedAndNonPublicIPv6(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "fd00::1", "fe80::1", "2001:db8::1", "::ffff:8.8.8.8"} {
		if publicIPv6(net.ParseIP(ip)) {
			t.Fatal(ip)
		}
	}
	if !publicIPv6(net.ParseIP("2606:4700::1111")) {
		t.Fatal("public IPv6 rejected")
	}
	s := testStore(t)
	l, e := s.reserve("a")
	if e != nil {
		t.Fatal(e)
	}
	p := &proxy{store: s, clients: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, allowed: map[string]bool{"chatgpt.com": true}}
	req := httptest.NewRequest("CONNECT", "https://evil.example:443", nil)
	req.Host = "evil.example:443"
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Proxy-Authorization", auth(l))
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestConnectBindsSourceAndRevocationClosesTunnel(t *testing.T) {
	s := testStore(t)
	l, err := s.reserve("tunnel")
	if err != nil {
		t.Fatal(err)
	}
	dialed := make(chan []string, 1)
	p := &proxy{store: s, clients: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, allowed: map[string]bool{"chatgpt.com": true}, maxTunnels: 5, conns: map[net.Conn]struct{}{}, ensure: func(context.Context, string) error { return nil }, lookup: func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("2606:4700::1111")}, nil
	}, dial: func(_ context.Context, network, target, source string) (net.Conn, error) {
		dialed <- []string{network, target, source}
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	}}
	server := httptest.NewServer(p)
	defer server.Close()
	c, err := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprintf(c, "CONNECT chatgpt.com:443 HTTP/1.1\r\nHost: chatgpt.com:443\r\nProxy-Authorization: %s\r\n\r\nping", auth(l))
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(c)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "200") {
		t.Fatal(line, err)
	}
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	echo := make([]byte, 4)
	if _, err = io.ReadFull(reader, echo); err != nil || string(echo) != "ping" {
		t.Fatal(string(echo), err)
	}
	route := <-dialed
	if route[0] != "tcp6" || route[1] != "[2606:4700::1111]:443" || route[2] != l.Address {
		t.Fatal(route)
	}
	if err = p.release(context.Background(), l.ID, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.ReadByte(); err == nil {
		t.Fatal("revoked tunnel remained open")
	}
}
