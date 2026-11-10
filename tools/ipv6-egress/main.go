// ipv6-egress is an authenticated CONNECT proxy with persistent IPv6 leases.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type config struct {
	Listen         string   `json:"listen"`
	ControlListen  string   `json:"control_listen"`
	ControlToken   string   `json:"control_token"`
	Prefix         string   `json:"prefix"`
	Interface      string   `json:"interface"`
	StateFile      string   `json:"state_file"`
	AllowedHosts   []string `json:"allowed_hosts"`
	AllowedClients []string `json:"allowed_clients"`
	MaxLeases      int      `json:"max_leases"`
	MaxTunnels     int      `json:"max_tunnels"`
}

type lease struct {
	Revoked   bool      `json:"revoked,omitempty"`
	ID        string    `json:"id"`
	Address   string    `json:"address"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
}

type leaseStore struct {
	mu     sync.RWMutex
	leases map[string]lease
	users  map[string]lease
	path   string
	prefix netip.Prefix
	limit  int
}

var leaseID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,96}$`)

func newStore(path string, prefix netip.Prefix, limit int) (*leaseStore, error) {
	s := &leaseStore{path: path, prefix: prefix, limit: limit, leases: map[string]lease{}, users: map[string]lease{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []lease
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, err
	}
	addresses := map[string]bool{}
	for _, l := range entries {
		addr, err := netip.ParseAddr(l.Address)
		if err != nil || !prefix.Contains(addr) || addr == prefix.Addr() || !leaseID.MatchString(l.ID) || l.Username != "v6-"+l.ID || len(l.Password) < 32 || addresses[l.Address] {
			return nil, errors.New("invalid or duplicate persisted IPv6 lease")
		}
		if _, ok := s.leases[l.ID]; ok {
			return nil, errors.New("duplicate lease identity")
		}
		addresses[l.Address] = true
		s.leases[l.ID] = l
		if !l.Revoked {
			s.users[l.Username] = l
		}
	}
	return s, nil
}

// reserve writes and fsyncs a lease before it can be used. Retries and restarts
// keep the same address, even when address provisioning failed previously.
func (s *leaseStore) reserve(id string) (lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !leaseID.MatchString(id) {
		return lease{}, errors.New("invalid lease id")
	}
	if l, ok := s.leases[id]; ok {
		if l.Revoked {
			return lease{}, errors.New("lease is being released")
		}
		return l, nil
	}
	if len(s.leases) >= s.limit {
		return lease{}, errors.New("lease capacity reached")
	}
	var address string
	for {
		b := s.prefix.Addr().As16()
		if _, err := rand.Read(b[8:]); err != nil {
			return lease{}, err
		}
		// Use a nonzero, non-anycast IID; locally allocated leases never use ::.
		b[8] &= 0x7f
		b[15] |= 1
		address = netip.AddrFrom16(b).String()
		duplicate := false
		for _, v := range s.leases {
			if v.Address == address {
				duplicate = true
				break
			}
		}
		if !duplicate {
			break
		}
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return lease{}, err
	}
	l := lease{ID: id, Address: address, Username: "v6-" + id, Password: base64.RawURLEncoding.EncodeToString(secret), CreatedAt: time.Now().UTC()}
	s.leases[id] = l
	if err := s.saveLocked(); err != nil {
		delete(s.leases, id)
		return lease{}, err
	}
	s.users[l.Username] = l
	return l, nil
}

func (s *leaseStore) saveLocked() error {
	entries := make([]lease, 0, len(s.leases))
	for _, l := range s.leases {
		entries = append(entries, l)
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".leases-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func secureEqual(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}

func (s *leaseStore) authenticate(header string) (lease, bool) {
	if !strings.HasPrefix(header, "Basic ") {
		return lease{}, false
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	if err != nil {
		return lease{}, false
	}
	u, p, ok := strings.Cut(string(b), ":")
	if !ok {
		return lease{}, false
	}
	s.mu.RLock()
	l, exists := s.users[u]
	s.mu.RUnlock()
	valid := secureEqual(p, l.Password)
	return l, exists && valid
}

type addressManager struct {
	iface string
	locks sync.Map
	ready sync.Map
}

func (m *addressManager) ensure(ctx context.Context, address string) error {
	if value, ok := m.ready.Load(address); ok && time.Since(value.(time.Time)) < 5*time.Minute {
		return nil
	}

	lock, _ := m.locks.LoadOrStore(address, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	// Inspect exact local addresses: never replace an existing primary address.
	for attempt := 0; attempt < 35; attempt++ {
		cmd := exec.CommandContext(ctx, "/usr/sbin/ip", "-j", "-6", "addr", "show", "dev", m.iface)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("inspect IPv6 addresses: %w", err)
		}
		var links []struct {
			Addresses []struct {
				Local     string `json:"local"`
				Tentative bool   `json:"tentative"`
				Failed    bool   `json:"dadfailed"`
			} `json:"addr_info"`
		}
		if err = json.Unmarshal(out, &links); err != nil {
			return err
		}
		found := false
		for _, link := range links {
			for _, a := range link.Addresses {
				if a.Local == address {
					found = true
					if a.Failed {
						return errors.New("IPv6 duplicate address detection failed")
					}
					if !a.Tentative {
						m.ready.Store(address, time.Now())
						return nil
					}
				}
			}
		}
		if !found {
			cmd = exec.CommandContext(ctx, "/usr/sbin/ip", "-6", "addr", "add", address+"/128", "dev", m.iface, "preferred_lft", "0")
			if err = cmd.Run(); err != nil {
				return fmt.Errorf("add IPv6 address: %w", err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("IPv6 address did not become ready")
}

type proxy struct {
	store      *leaseStore
	allowed    map[string]bool
	clients    []netip.Prefix
	ensure     func(context.Context, string) error
	lookup     func(context.Context, string) ([]net.IP, error)
	dial       func(context.Context, string, string, string) (net.Conn, error)
	active     atomic.Int64
	maxTunnels int64
	mu         sync.Mutex
	conns      map[net.Conn]struct{}
	leaseConns map[string]map[net.Conn]struct{}
	closing    bool
}

func publicIPv6(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	return a.Is6() && a.IsGlobalUnicast() && !a.IsPrivate() && netip.MustParsePrefix("2000::/3").Contains(a) && !netip.MustParsePrefix("2001:db8::/32").Contains(a)
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	addr, parseErr := netip.ParseAddr(remote)
	allowedClient := false
	if err == nil && parseErr == nil {
		for _, prefix := range p.clients {
			if prefix.Contains(addr.Unmap()) {
				allowedClient = true
				break
			}
		}
	}
	if !allowedClient {
		slog.Warn("egress_client_denied", "remote", r.RemoteAddr)
		http.Error(w, "client denied", http.StatusForbidden)
		return
	}
	l, ok := p.store.authenticate(r.Header.Get("Proxy-Authorization"))
	if !ok {
		w.Header().Set("Proxy-Authenticate", `Basic realm="ipv6-egress"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	host, port, err := net.SplitHostPort(r.Host)
	host = strings.ToLower(host)
	if err != nil || port != "443" || !p.allowed[host] || r.ContentLength > 0 || len(r.TransferEncoding) > 0 {
		http.Error(w, "target denied", http.StatusForbidden)
		return
	}
	if p.active.Add(1) > p.maxTunnels {
		p.active.Add(-1)
		http.Error(w, "tunnel capacity reached", http.StatusServiceUnavailable)
		return
	}
	defer p.active.Add(-1)
	// Serialize admission with revocation, including slow address setup/dials.
	p.store.mu.RLock()
	locked := true
	defer func() {
		if locked {
			p.store.mu.RUnlock()
		}
	}()
	current, exists := p.store.leases[l.ID]
	if !exists || current.Revoked {
		http.Error(w, "lease revoked", 407)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err = p.ensure(ctx, l.Address); err != nil {
		slog.Warn("egress_address_unavailable", "lease", l.ID)
		http.Error(w, "IPv6 address unavailable", http.StatusBadGateway)
		return
	}
	ips, err := p.lookup(ctx, host)
	if err != nil {
		http.Error(w, "IPv6 DNS failed", http.StatusBadGateway)
		return
	}
	var upstream net.Conn
	for _, ip := range ips {
		if !publicIPv6(ip) {
			continue
		}
		attempt, stop := context.WithTimeout(ctx, 4*time.Second)
		upstream, err = p.dial(attempt, "tcp6", net.JoinHostPort(ip.String(), port), l.Address)
		stop()
		if err == nil {
			break
		}
	}
	if upstream == nil {
		slog.Warn("egress_connect_failed", "lease", l.ID, "source_ip", l.Address, "target", host)
		http.Error(w, "IPv6 upstream unavailable", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "tunneling unavailable", 500)
		return
	}
	client, buffer, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	p.conns[client] = struct{}{}
	p.conns[upstream] = struct{}{}
	if p.leaseConns == nil {
		p.leaseConns = map[string]map[net.Conn]struct{}{}
	}
	if p.leaseConns[l.ID] == nil {
		p.leaseConns[l.ID] = map[net.Conn]struct{}{}
	}
	p.leaseConns[l.ID][client] = struct{}{}
	p.leaseConns[l.ID][upstream] = struct{}{}
	p.mu.Unlock()
	p.store.mu.RUnlock()
	locked = false
	defer func() {
		p.mu.Lock()
		delete(p.conns, client)
		delete(p.conns, upstream)
		delete(p.leaseConns[l.ID], client)
		delete(p.leaseConns[l.ID], upstream)
		if len(p.leaseConns[l.ID]) == 0 {
			delete(p.leaseConns, l.ID)
		}
		p.mu.Unlock()
	}()
	if _, err = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buffer.Flush(); err != nil {
		return
	}
	_ = client.SetDeadline(time.Time{})
	slog.Info("egress_connected", "lease", l.ID, "source_ip", l.Address, "target", host)
	// Consume bytes already buffered after CONNECT, and preserve TCP half-close.
	done := make(chan struct{}, 1)
	go func() {
		_, copyErr := io.Copy(upstream, buffer)
		if c, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
		if copyErr != nil {
			_ = client.Close()
			_ = upstream.Close()
		}
		done <- struct{}{}
	}()
	_, _ = io.Copy(client, upstream)
	// The upstream has finished its response. End a client that never half-closes.
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

func main() {
	configPath := flag.String("config", "/etc/sub2api-ipv6/config.json", "configuration path")
	flag.Parse()
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(*configPath); err != nil {
		slog.Error("egress_stopped", "error", err)
		os.Exit(1)
	}
}

func run(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cfg config
	if err = json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	prefix, err := netip.ParsePrefix(cfg.Prefix)
	if err != nil || !prefix.Addr().Is6() || prefix.Bits() != 64 || prefix != prefix.Masked() {
		return errors.New("a canonical IPv6 /64 prefix is required")
	}
	if len(cfg.ControlToken) < 32 || cfg.Interface == "" || cfg.StateFile == "" || cfg.Listen == "" || cfg.ControlListen == "" {
		return errors.New("incomplete egress configuration")
	}
	if cfg.MaxLeases <= 0 {
		cfg.MaxLeases = 10000
	}
	if cfg.MaxTunnels <= 0 {
		cfg.MaxTunnels = 20000
	}
	s, err := newStore(cfg.StateFile, prefix, cfg.MaxLeases)
	if err != nil {
		return err
	}
	m := &addressManager{iface: cfg.Interface}
	p := &proxy{store: s, allowed: map[string]bool{}, ensure: m.ensure, maxTunnels: int64(cfg.MaxTunnels), conns: map[net.Conn]struct{}{}}
	for _, h := range cfg.AllowedHosts {
		p.allowed[strings.ToLower(h)] = true
	}
	for _, c := range cfg.AllowedClients {
		v, e := netip.ParsePrefix(c)
		if e != nil {
			return e
		}
		p.clients = append(p.clients, v)
	}
	p.lookup = func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip6", host)
	}
	p.dial = func(ctx context.Context, network, target, source string) (net.Conn, error) {
		d := net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
		return d.DialContext(ctx, network, target)
	}
	control := controlHandler(cfg.ControlToken, s, p, m.remove)
	proxyServer := &http.Server{Addr: cfg.Listen, Handler: p, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	controlServer := &http.Server{Addr: cfg.ControlListen, Handler: control, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	pl, err := net.Listen("tcp4", cfg.Listen)
	if err != nil {
		return err
	}
	defer pl.Close()
	cl, err := net.Listen("tcp4", cfg.ControlListen)
	if err != nil {
		return err
	}
	defer cl.Close()
	errs := make(chan error, 2)
	go func() { errs <- proxyServer.Serve(pl) }()
	go func() { errs <- controlServer.Serve(cl) }()
	slog.Info("egress_ready", "listen", cfg.Listen, "leases", len(s.leases))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err = <-errs:
	}
	// Stop admissions while allowing active tunnels to drain for up to 60 seconds.
	shutdown, done := context.WithTimeout(context.Background(), 60*time.Second)
	defer done()
	_ = proxyServer.Shutdown(shutdown)
	_ = controlServer.Shutdown(shutdown)
	for p.active.Load() > 0 && shutdown.Err() == nil {
		time.Sleep(100 * time.Millisecond)
	}
	p.mu.Lock()
	p.closing = true
	for c := range p.conns {
		_ = c.Close()
	}
	p.mu.Unlock()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Revocation is persisted before disconnecting tunnels. Tombstones survive an
// address-removal failure and make DELETE safely retryable after restart.
func (p *proxy) release(ctx context.Context, id string, remove func(context.Context, string) error) error {
	s := p.store
	s.mu.Lock()
	l, ok := s.leases[id]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	old := l
	l.Revoked = true
	s.leases[id] = l
	if err := s.saveLocked(); err != nil {
		s.leases[id] = old
		s.mu.Unlock()
		return err
	}
	delete(s.users, l.Username)
	p.mu.Lock()
	for c := range p.leaseConns[id] {
		_ = c.Close()
	}
	p.mu.Unlock()
	s.mu.Unlock()
	if err := remove(ctx, l.Address); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.leases, id)
	if err := s.saveLocked(); err != nil {
		s.leases[id] = l
		return err
	}
	return nil
}
func (m *addressManager) remove(ctx context.Context, address string) error {
	lock, _ := m.locks.LoadOrStore(address, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	m.ready.Delete(address)
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	// iproute2 output is diagnostic only; do not expose it to the API.
	raw, err := exec.CommandContext(ctx, "/usr/sbin/ip", "-j", "-6", "addr", "show", "dev", m.iface).Output()
	if err != nil {
		return errors.New("address inspection failed")
	}
	var devices []struct {
		Addresses []struct {
			Local string `json:"local"`
		} `json:"addr_info"`
	}
	if json.Unmarshal(raw, &devices) != nil {
		return errors.New("invalid address inspection")
	}
	for _, d := range devices {
		for _, a := range d.Addresses {
			if a.Local == address {
				return exec.CommandContext(ctx, "/usr/sbin/ip", "-6", "addr", "del", address+"/128", "dev", m.iface).Run()
			}
		}
	}
	return nil
}
func controlHandler(token string, s *leaseStore, p *proxy, remove func(context.Context, string) error) http.Handler {
	control := http.NewServeMux()
	control.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		s.mu.RLock()
		n := len(s.leases)
		s.mu.RUnlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "leases": n, "active_tunnels": p.active.Load()})
	})
	control.HandleFunc("POST /v1/leases", func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		var request struct {
			ID string `json:"id"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if json.NewDecoder(r.Body).Decode(&request) != nil || !leaseID.MatchString(request.ID) {
			http.Error(w, "invalid lease id", 400)
			return
		}
		l, e := s.reserve(request.ID)
		if e != nil {
			http.Error(w, "lease reservation failed", 503)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		s.mu.RLock()
		current, exists := s.leases[l.ID]
		if !exists || current.Revoked {
			s.mu.RUnlock()
			http.Error(w, "lease revoked", 409)
			return
		}
		e = p.ensure(ctx, l.Address)
		s.mu.RUnlock()
		if e != nil {
			slog.Warn("lease_not_ready", "lease", l.ID)
			http.Error(w, "IPv6 lease not ready", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(l)
	})
	control.HandleFunc("DELETE /v1/leases/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !secureEqual(r.Header.Get("Authorization"), "Bearer "+token) {
			http.Error(w, "unauthorized", 401)
			return
		}
		id := r.PathValue("id")
		if !leaseID.MatchString(id) {
			http.Error(w, "invalid lease id", 400)
			return
		}
		if err := p.release(r.Context(), id, remove); err != nil {
			http.Error(w, "release incomplete; retry", 503)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return control
}
