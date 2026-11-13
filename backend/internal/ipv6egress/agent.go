package ipv6egress

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ovhipv6"
)

type Dependencies struct {
	Addresses AddressManager
	Network   Network
	Probe     func(context.Context, string) error
	Now       func() time.Time
}
type runtimeLease struct {
	*storedLease
	connections int
	busy        bool
	ready       bool
}

type Agent struct {
	mu        sync.Mutex
	config    Config
	store     *store
	issued    string
	leases    map[string]*runtimeLease
	addresses AddressManager
	network   Network
	probe     func(context.Context, string) error
	now       func() time.Time
	fatal     error
}

func Open(c Config, initialize bool, dep Dependencies) (*Agent, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	s, issued, d, err := openStore(c.StateDir, c.Prefix, initialize)
	if err != nil {
		return nil, err
	}
	a := &Agent{config: c, store: s, issued: issued, leases: make(map[string]*runtimeLease), addresses: dep.Addresses, network: dep.Network, probe: dep.Probe, now: dep.Now}
	if a.addresses == nil {
		a.addresses = LinuxAddresses{Interface: c.Interface, BeforeAdd: a.persistAddressIntent}
	}
	if a.now == nil {
		a.now = time.Now
	}
	a.network.Prefix = netip.MustParsePrefix(c.Prefix)
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if prefix, err := netip.ParsePrefix(address.String()); err == nil {
				a.network.LocalAddresses = append(a.network.LocalAddresses, prefix.Addr())
			}
		}
	} else {
		_ = s.unlock()
		return nil, errors.New("cannot discover local addresses for egress validation")
	}
	if a.probe == nil {
		a.probe = a.probeAddress
	}
	for id, l := range d.Leases {
		a.leases[id] = &runtimeLease{storedLease: l}
	}
	return a, nil
}

func (a *Agent) persistAddressIntent(address string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, lease := range a.leases {
		if lease.Address == address {
			lease.Configured = true
			return a.saveLocked()
		}
	}
	return errors.New("address has no durable lease")
}

// Close is called after servers and reconciliation have stopped.
func (a *Agent) Close() error { return a.store.unlock() }

// WaitDrained includes hijacked CONNECT tunnels, which http.Server.Shutdown
// does not wait for. The supervisor may stop accepting traffic without cutting
// existing model streams or WebSocket sessions.
func (a *Agent) WaitDrained(ctx context.Context) error {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		a.mu.Lock()
		count := 0
		for _, lease := range a.leases {
			count += lease.connections
			if lease.busy {
				count++
			}
		}
		a.mu.Unlock()
		if count == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (a *Agent) saveLocked() error {
	leases := make(map[string]*storedLease, len(a.leases))
	for id, l := range a.leases {
		leases[id] = l.storedLease
	}
	if err := a.store.saveLeases(leases); err != nil {
		a.fatal = err
		return err
	}
	return nil
}

func (a *Agent) Status() ovhipv6.Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := ovhipv6.Status{Configured: true, Available: a.fatal == nil, Prefix: a.config.Prefix, Issued: a.issued}
	for _, l := range a.leases {
		if l.State == "active" {
			r.Bound++
		}
		if l.State == "retiring" {
			r.Pending++
		}
	}
	if a.fatal != nil {
		r.Error = "IPv6 allocator storage is unavailable; new connections are blocked"
	}
	return r
}

func (a *Agent) Create(ctx context.Context) (ovhipv6.Lease, error) {
	a.mu.Lock()
	if a.fatal != nil {
		a.mu.Unlock()
		return ovhipv6.Lease{}, errors.New("allocator unavailable")
	}
	seq, err := ovhipv6.Next(a.issued)
	if err != nil {
		a.mu.Unlock()
		return ovhipv6.Lease{}, err
	}
	addr, err := ovhipv6.Address(a.config.Prefix, seq)
	if err != nil {
		a.mu.Unlock()
		return ovhipv6.Lease{}, err
	}
	// Persist the consumed sequence first. Later failures may create gaps but can
	// never send this address back into the allocation pool.
	if err = a.store.saveCounter(seq); err != nil {
		a.fatal = err
		a.mu.Unlock()
		return ovhipv6.Lease{}, err
	}
	a.issued = seq
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		a.mu.Unlock()
		return ovhipv6.Lease{}, err
	}
	_, port, _ := net.SplitHostPort(a.config.ProxyListen)
	pn, _ := strconv.Atoi(port)
	l := &runtimeLease{storedLease: &storedLease{Lease: ovhipv6.Lease{ID: seq, Address: addr.String(), Username: "ovh6_" + seq, Password: base64.RawURLEncoding.EncodeToString(secret), State: "provisioning", Host: a.config.AdvertiseHost, Port: pn}, CreatedAt: a.now()}, busy: true}
	a.leases[seq] = l
	if err = a.saveLocked(); err != nil {
		l.busy = false
		delete(a.leases, seq)
		a.mu.Unlock()
		return ovhipv6.Lease{}, err
	}
	a.mu.Unlock()
	owned, err := a.addresses.Ensure(ctx, l.Address, false)
	a.mu.Lock()
	// The kernel command can be interrupted after applying the address. Preserve
	// the durable pre-command ownership intent so cleanup still removes it.
	l.Configured = owned || l.Configured
	if saveErr := a.saveLocked(); saveErr != nil {
		err = saveErr
	}
	a.mu.Unlock()
	if err == nil {
		err = a.probe(ctx, l.Address)
	}
	a.mu.Lock()
	l.busy = false
	if err != nil || ctx.Err() != nil || l.State == "retiring" {
		log.Printf("IPv6 allocation %s did not become ready: %v", seq, err)
		l.State = "retiring"
		_ = a.saveLocked()
		a.mu.Unlock()
		a.cleanup(context.Background(), seq)
		return ovhipv6.Lease{}, errors.New("IPv6 provisioning or connectivity check failed; address retired")
	}
	l.State = "pending"
	l.ready = true
	err = a.saveLocked()
	out := l.Lease
	a.mu.Unlock()
	if err != nil {
		return ovhipv6.Lease{}, err
	}
	return out, nil
}

func (a *Agent) Activate(id string) (ovhipv6.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.leases[id]
	if a.fatal != nil || l == nil || !l.ready || l.busy || (l.State != "pending" && l.State != "active") {
		return ovhipv6.Lease{}, errors.New("lease unavailable or permanently retired")
	}
	if l.State == "pending" && a.now().Sub(l.CreatedAt) >= pendingLifetime {
		l.State = "retiring"
		_ = a.saveLocked()
		return ovhipv6.Lease{}, errors.New("pending lease expired")
	}
	l.State = "active"
	if err := a.saveLocked(); err != nil {
		return ovhipv6.Lease{}, err
	}
	out := l.Lease
	out.Password = ""
	return out, nil
}

func (a *Agent) knownRetiredLocked(id string) bool {
	n, ok := new(big.Int).SetString(id, 10)
	if !ok || n.Sign() <= 0 || n.String() != id {
		return false
	}
	max, _ := new(big.Int).SetString(a.issued, 10)
	return n.Cmp(max) <= 0
}

func (a *Agent) Retire(ctx context.Context, id string) (ovhipv6.Lease, error) {
	a.mu.Lock()
	l := a.leases[id]
	if l == nil {
		ok := a.knownRetiredLocked(id)
		a.mu.Unlock()
		if ok {
			return ovhipv6.Lease{ID: id, State: "retired"}, nil
		}
		return ovhipv6.Lease{}, errors.New("unknown lease")
	}
	l.State = "retiring"
	l.ready = false
	if err := a.saveLocked(); err != nil {
		a.mu.Unlock()
		return ovhipv6.Lease{}, err
	}
	a.mu.Unlock()
	a.cleanup(ctx, id)
	a.mu.Lock()
	defer a.mu.Unlock()
	if l = a.leases[id]; l != nil {
		return ovhipv6.Lease{ID: id, Address: l.Address, State: "retiring"}, nil
	}
	return ovhipv6.Lease{ID: id, State: "retired"}, nil
}

func (a *Agent) cleanup(ctx context.Context, id string) {
	a.mu.Lock()
	l := a.leases[id]
	if l == nil || l.State != "retiring" || l.busy || l.connections > 0 {
		a.mu.Unlock()
		return
	}
	l.busy = true
	owned, address := l.Configured, l.Address
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var err error
	if owned {
		err = a.addresses.Remove(ctx, address)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	l.busy = false
	if err != nil {
		log.Printf("IPv6 lease %s cleanup will retry: %v", id, err)
		return
	}
	delete(a.leases, id)
	if err = a.saveLocked(); err != nil {
		a.leases[id] = l
	}
}

// Reconcile restores live addresses after a host reboot and removes abandoned
// pre-commit allocations. It never resurrects a retired lease.
func (a *Agent) Reconcile(ctx context.Context) {
	a.mu.Lock()
	ids := make([]string, 0, len(a.leases))
	for id := range a.leases {
		ids = append(ids, id)
	}
	a.mu.Unlock()
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		a.mu.Lock()
		l := a.leases[id]
		if l == nil || l.busy {
			a.mu.Unlock()
			continue
		}
		if l.State == "provisioning" || (l.State == "pending" && a.now().Sub(l.CreatedAt) >= pendingLifetime) {
			l.State = "retiring"
			l.ready = false
			_ = a.saveLocked()
		}
		if l.State == "retiring" {
			a.mu.Unlock()
			a.cleanup(ctx, id)
			continue
		}
		if l.ready {
			a.mu.Unlock()
			continue
		}
		l.busy = true
		address := l.Address
		a.mu.Unlock()
		step, cancel := context.WithTimeout(ctx, 8*time.Second)
		owned, err := a.addresses.Ensure(step, address, true)
		cancel()
		a.mu.Lock()
		l.busy = false
		l.Configured = owned || l.Configured
		l.ready = err == nil
		_ = a.saveLocked()
		a.mu.Unlock()
	}
}

func (a *Agent) acquire(username, password string) (string, string, func(), bool) {
	const prefix = "ovh6_"
	if len(username) <= len(prefix) || username[:len(prefix)] != prefix {
		return "", "", nil, false
	}
	id := username[len(prefix):]
	a.mu.Lock()
	l := a.leases[id]
	if a.fatal != nil || l == nil || !l.ready || (l.State != "pending" && l.State != "active") || subtle.ConstantTimeCompare([]byte(l.Password), []byte(password)) != 1 {
		a.mu.Unlock()
		return "", "", nil, false
	}
	if l.State == "pending" && a.now().Sub(l.CreatedAt) >= pendingLifetime {
		a.mu.Unlock()
		return "", "", nil, false
	}
	l.connections++
	address := l.Address
	a.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			a.mu.Lock()
			l.connections--
			retiring := l.State == "retiring"
			a.mu.Unlock()
			if retiring {
				a.cleanup(context.Background(), id)
			}
		})
	}
	return id, address, release, true
}

func (a *Agent) probeAddress(ctx context.Context, address string) error {
	tr := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, target string) (net.Conn, error) {
		return a.network.Connect(ctx, target, address)
	}, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.config.ProbeURL, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("IPv6 TLS probe failed")
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 500 {
		return fmt.Errorf("IPv6 probe HTTP %d", res.StatusCode)
	}
	return nil
}
