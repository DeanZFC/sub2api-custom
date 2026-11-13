package ipv6egress

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAddresses struct {
	mu        sync.Mutex
	owned     map[string]bool
	removeErr bool
}

type interruptedAddresses struct {
	*fakeAddresses
	beforeAdd func(string) error
}

func (f *interruptedAddresses) Ensure(ctx context.Context, address string, restore bool) (bool, error) {
	if err := f.beforeAdd(address); err != nil {
		return false, err
	}
	if _, err := f.fakeAddresses.Ensure(ctx, address, restore); err != nil {
		return false, err
	}
	return false, context.Canceled
}

func (f *fakeAddresses) Ensure(_ context.Context, addr string, restore bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.owned[addr] && !restore {
		return false, errors.New("already exists")
	}
	f.owned[addr] = true
	return true, nil
}
func (f *fakeAddresses) Remove(_ context.Context, addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.removeErr {
		return errors.New("remove failed")
	}
	delete(f.owned, addr)
	return nil
}
func (f *fakeAddresses) has(addr string) bool { f.mu.Lock(); defer f.mu.Unlock(); return f.owned[addr] }

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Prefix: "2001:db8:205:b900::/56", Interface: "eth0", ProxyListen: "127.0.0.1:11880", ControlListen: "127.0.0.1:11881", AdvertiseHost: "127.0.0.1", ControlToken: strings.Repeat("x", 40), StateDir: t.TempDir(), ProbeURL: "https://example.com/", AllowedClients: []string{"127.0.0.1/32", "192.0.2.0/24"}, AllowedHosts: []string{"example.com", "upstream.example"}}
}
func newTestAgent(t *testing.T) (*Agent, *fakeAddresses) {
	t.Helper()
	f := &fakeAddresses{owned: map[string]bool{}}
	a, err := Open(testConfig(t), true, Dependencies{Addresses: f, Probe: func(context.Context, string) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, f
}

func TestAllocationSurvivesRetirementAndRestart(t *testing.T) {
	cfg := testConfig(t)
	f := &fakeAddresses{owned: map[string]bool{}}
	dep := Dependencies{Addresses: f, Probe: func(context.Context, string) error { return nil }}
	a, err := Open(cfg, true, dep)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "1" || first.Address != "2001:db8:205:b900::1" {
		t.Fatalf("unexpected first allocation: %+v", first)
	}
	if _, err = a.Activate(first.ID); err != nil {
		t.Fatal(err)
	}
	retired, err := a.Retire(context.Background(), first.ID)
	if err != nil || retired.State != "retired" {
		t.Fatalf("retirement: %+v %v", retired, err)
	}
	if f.has(first.Address) {
		t.Fatal("retired address remains configured")
	}
	if _, err = a.Activate(first.ID); err == nil {
		t.Fatal("retired address reactivated")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(cfg, false, dep)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	second, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != "2" {
		t.Fatalf("counter reused after restart: %s", second.ID)
	}
	if _, err = Open(cfg, true, dep); err == nil {
		t.Fatal("concurrent or destructive initialization accepted")
	}
	b, err := os.ReadFile(filepath.Join(cfg.StateDir, "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var d diskState
	if err = json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Leases) != 1 || d.Leases["1"] != nil {
		t.Fatalf("retired history retained: %s", b)
	}
}

func TestRetirementDrainsExistingConnections(t *testing.T) {
	a, f := newTestAgent(t)
	lease, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Activate(lease.ID); err != nil {
		t.Fatal(err)
	}
	_, source, release, ok := a.acquire(lease.Username, lease.Password)
	if !ok || source != lease.Address {
		t.Fatal("fixed source unavailable")
	}
	result, err := a.Retire(context.Background(), lease.ID)
	if err != nil || result.State != "retiring" {
		t.Fatalf("retirement did not wait: %+v %v", result, err)
	}
	if !f.has(lease.Address) {
		t.Fatal("address removed under active connection")
	}
	if _, _, _, ok = a.acquire(lease.Username, lease.Password); ok {
		t.Fatal("retiring lease accepted a new connection")
	}
	if _, err = a.Activate(lease.ID); err == nil {
		t.Fatal("retiring lease reactivated")
	}
	release()
	release()
	if f.has(lease.Address) {
		t.Fatal("address not removed after drain")
	}
	result, err = a.Retire(context.Background(), lease.ID)
	if err != nil || result.State != "retired" {
		t.Fatal("DELETE is not idempotent")
	}
}

func TestFailedProvisioningBurnsSequenceAndOrphansExpire(t *testing.T) {
	a, f := newTestAgent(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	a.probe = func(context.Context, string) error { return errors.New("unreachable") }
	if _, err := a.Create(context.Background()); err == nil {
		t.Fatal("failed probe accepted")
	}
	if f.has("2001:db8:205:b900::1") {
		t.Fatal("failed provision leaked address")
	}
	a.probe = func(context.Context, string) error { return nil }
	l, err := a.Create(context.Background())
	if err != nil || l.ID != "2" {
		t.Fatalf("failed allocation reused: %+v %v", l, err)
	}
	now = now.Add(pendingLifetime + time.Second)
	a.Reconcile(context.Background())
	if _, err = a.Activate(l.ID); err == nil {
		t.Fatal("orphan reactivated")
	}
	if f.has(l.Address) {
		t.Fatal("orphan address not reclaimed")
	}
}

func TestInterruptedAddressCommandRetainsCleanupOwnership(t *testing.T) {
	a, f := newTestAgent(t)
	a.addresses = &interruptedAddresses{fakeAddresses: f, beforeAdd: a.persistAddressIntent}
	if _, err := a.Create(context.Background()); err == nil {
		t.Fatal("interrupted kernel operation accepted")
	}
	if f.has("2001:db8:205:b900::1") {
		t.Fatal("address survived failed command despite durable ownership intent")
	}
	a.addresses = f
	l, err := a.Create(context.Background())
	if err != nil || l.ID != "2" {
		t.Fatalf("failed command reused its address: %+v %v", l, err)
	}
}

func TestCleanupFailureRetainedUntilRetry(t *testing.T) {
	a, f := newTestAgent(t)
	l, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.removeErr = true
	r, err := a.Retire(context.Background(), l.ID)
	if err != nil || r.State != "retiring" {
		t.Fatal("cleanup was forgotten")
	}
	if a.Status().Pending != 1 {
		t.Fatal("missing pending cleanup")
	}
	f.removeErr = false
	a.Reconcile(context.Background())
	if a.Status().Pending != 0 || f.has(l.Address) {
		t.Fatal("cleanup retry failed")
	}
}

func TestConcurrentReservationsNeverRepeat(t *testing.T) {
	a, _ := newTestAgent(t)
	var wg sync.WaitGroup
	ids := make(chan string, 40)
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l, e := a.Create(context.Background()); ids <- l.ID; errs <- e }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
	if a.Status().Issued != "40" {
		t.Fatal("counter lost updates")
	}
}

func TestCounterGuardPreventsPartialRestoreAndMissingStateFails(t *testing.T) {
	cfg := testConfig(t)
	dep := Dependencies{Addresses: &fakeAddresses{owned: map[string]bool{}}, Probe: func(context.Context, string) error { return nil }}
	a, err := Open(cfg, true, dep)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join(cfg.StateDir, "counter.json"))
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Retire(context.Background(), l.ID)
	_ = a.Close()
	if err = os.WriteFile(filepath.Join(cfg.StateDir, "counter.json"), old, 0600); err != nil {
		t.Fatal(err)
	}
	a, err = Open(cfg, false, dep)
	if err != nil {
		t.Fatal(err)
	}
	l, err = a.Create(context.Background())
	if err != nil || l.ID != "2" {
		t.Fatal("guard allowed counter rollback")
	}
	_ = a.Close()
	if err = os.Remove(filepath.Join(cfg.StateDir, "leases.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(cfg, false, dep); err == nil {
		t.Fatal("missing state silently reset")
	}
	if _, err = Open(cfg, true, dep); err == nil {
		t.Fatal("partial state allowed destructive initialize")
	}
}

func TestRestoredActiveLeaseRetainsSourceAndCredentials(t *testing.T) {
	cfg := testConfig(t)
	f := &fakeAddresses{owned: map[string]bool{}}
	dep := Dependencies{Addresses: f, Probe: func(context.Context, string) error { return nil }}
	a, err := Open(cfg, true, dep)
	if err != nil {
		t.Fatal(err)
	}
	l, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = a.Activate(l.ID)
	_ = a.Close()
	f.owned = map[string]bool{}
	a, err = Open(cfg, false, dep)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	if _, _, _, ok := a.acquire(l.Username, l.Password); ok {
		t.Fatal("lease served before address restored")
	}
	a.Reconcile(context.Background())
	_, ip, release, ok := a.acquire(l.Username, l.Password)
	if !ok || ip != l.Address {
		t.Fatal("restored lease changed identity")
	}
	release()
}

type fakeResolver struct {
	ips     []net.IP
	network string
}

func (f *fakeResolver) LookupIP(_ context.Context, network, _ string) ([]net.IP, error) {
	f.network = network
	return f.ips, nil
}

func TestNetworkOnlyDialsPermittedIPv6WithFixedSource(t *testing.T) {
	src := "2001:db8:205:b900::5"
	resolver := &fakeResolver{ips: []net.IP{net.ParseIP("::1"), net.ParseIP("fd00::1"), net.ParseIP("192.0.2.1"), net.ParseIP(src), net.ParseIP("2606:4700:4700::1111")}}
	n := Network{Prefix: netip.MustParsePrefix("2001:db8:205:b900::/56"), Resolver: resolver}
	calls := 0
	n.Dial = func(_ context.Context, network, target, source string) (net.Conn, error) {
		calls++
		if network != "tcp6" || source != src || target != "[2606:4700:4700::1111]:443" {
			t.Fatalf("wrong dial: %s %s %s", network, target, source)
		}
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	c, err := n.Connect(context.Background(), "upstream.example:443", src)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if calls != 1 || resolver.network != "ip6" {
		t.Fatal("not restricted to IPv6")
	}
	for _, target := range []string{"127.0.0.1:443", "[::1]:443", "[fd00::1]:443", "[2001:db8:205:b900::8]:443", "upstream.example:22", "[64:ff9b::7f00:1]:443"} {
		if _, err = n.Connect(context.Background(), target, src); err == nil {
			t.Fatalf("unsafe destination allowed: %s", target)
		}
	}
	resolver.ips = []net.IP{net.ParseIP("192.0.2.1")}
	if _, err = n.Connect(context.Background(), "ipv4-only.example:443", src); err == nil {
		t.Fatal("IPv4 fallback allowed")
	}
	if calls != 1 {
		t.Fatal("unsafe target dialed")
	}
	n.LocalAddresses = []netip.Addr{netip.MustParseAddr("2606:4700:4700::1111")}
	if _, err = n.Connect(context.Background(), "[2606:4700:4700::1111]:443", src); err == nil {
		t.Fatal("host's additional public IPv6 allowed as upstream")
	}
}

func TestControlAuthenticationAndProxyHeaderIsolation(t *testing.T) {
	a, _ := newTestAgent(t)
	unauth := httptest.NewRecorder()
	a.ControlHandler().ServeHTTP(unauth, httptest.NewRequest("GET", "/v1/status", nil))
	if unauth.Code != 401 {
		t.Fatal("control API is public")
	}
	r := httptest.NewRequest("GET", "/v1/status", nil)
	r.Header.Set("Authorization", "Bearer "+a.config.ControlToken)
	status := httptest.NewRecorder()
	a.ControlHandler().ServeHTTP(status, r)
	if status.Code != 200 || strings.Contains(status.Body.String(), a.config.ControlToken) {
		t.Fatal("bad status response")
	}
	l, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var gotSource, gotRequest string
	a.network.Resolver = &fakeResolver{ips: []net.IP{net.ParseIP("2606:4700:4700::1111")}}
	a.network.Dial = func(_ context.Context, network, target, source string) (net.Conn, error) {
		gotSource = source
		c, s := net.Pipe()
		go func() {
			defer func() { _ = s.Close() }()
			b := make([]byte, 8192)
			n, _ := s.Read(b)
			gotRequest = string(b[:n])
			_, _ = io.WriteString(s, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
		}()
		return c, nil
	}
	r = httptest.NewRequest("GET", "http://upstream.example/trace", nil)
	r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(l.Username+":"+l.Password)))
	r.Header.Set("Connection", "x-private")
	r.Header.Set("X-Private", "hidden")
	w := httptest.NewRecorder()
	a.ProxyHandler().ServeHTTP(w, r)
	if w.Code != 200 || gotSource != l.Address {
		t.Fatalf("proxy failed: %d %s", w.Code, gotSource)
	}
	if strings.Contains(gotRequest, "Proxy-Authorization") || strings.Contains(gotRequest, l.Password) || strings.Contains(gotRequest, "X-Private") {
		t.Fatal("proxy credential or hop header leaked")
	}
	if fmt.Sprint(w.Body) != "ok" {
		t.Fatal("upstream body changed")
	}
}

func TestConnectTunnelKeepsInFlightBytesWhileRetiring(t *testing.T) {
	a, addresses := newTestAgent(t)
	l, err := a.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Activate(l.ID); err != nil {
		t.Fatal(err)
	}
	upstreamReady := make(chan net.Conn, 1)
	a.network.Resolver = &fakeResolver{ips: []net.IP{net.ParseIP("2606:4700:4700::1111")}}
	a.network.Dial = func(_ context.Context, network, target, source string) (net.Conn, error) {
		if network != "tcp6" || source != l.Address {
			return nil, errors.New("wrong source family")
		}
		client, server := net.Pipe()
		upstreamReady <- server
		return client, nil
	}
	server := httptest.NewServer(a.ProxyHandler())
	defer server.Close()
	c, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	auth := base64.StdEncoding.EncodeToString([]byte(l.Username + ":" + l.Password))
	_, err = fmt.Fprintf(c, "CONNECT upstream.example:443 HTTP/1.1\r\nHost: upstream.example:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(c)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT failed: %v", err)
	}
	upstream := <-upstreamReady
	defer func() { _ = upstream.Close() }()
	r, err := a.Retire(context.Background(), l.ID)
	if err != nil || r.State != "retiring" {
		t.Fatal("active tunnel did not retain address")
	}
	if !addresses.has(l.Address) {
		t.Fatal("active CONNECT address removed")
	}
	written := make(chan error, 1)
	go func() { _, e := io.WriteString(upstream, "stream-after-retirement"); written <- e }()
	got := make([]byte, len("stream-after-retirement"))
	if _, err = io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "stream-after-retirement" {
		t.Fatal("stream bytes changed")
	}
	if err = <-written; err != nil {
		t.Fatal(err)
	}
	_ = upstream.Close()
	_ = c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = a.WaitDrained(ctx); err != nil {
		t.Fatal(err)
	}
	// Completion calls cleanup before releasing the server handler.
	a.Reconcile(context.Background())
	if addresses.has(l.Address) {
		t.Fatal("drained tunnel address leaked")
	}
}
