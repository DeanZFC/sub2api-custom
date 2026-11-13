package ipv6egress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strings"
	"time"
)

type AddressManager interface {
	Ensure(context.Context, string, bool) (bool, error)
	Remove(context.Context, string) error
}

type LinuxAddresses struct {
	Interface string
	BeforeAdd func(string) error
	command   func(context.Context, ...string) ([]byte, error)
}
type addressInfo struct {
	Local     string   `json:"local"`
	PrefixLen int      `json:"prefixlen"`
	Tentative bool     `json:"tentative"`
	DADFailed bool     `json:"dadfailed"`
	Flags     []string `json:"flags"`
}

func (a LinuxAddresses) run(ctx context.Context, args ...string) ([]byte, error) {
	if a.command != nil {
		return a.command(ctx, args...)
	}
	return exec.CommandContext(ctx, "ip", args...).Output()
}

func (a LinuxAddresses) lookup(ctx context.Context, address string) (*addressInfo, error) {
	b, err := a.run(ctx, "-j", "-6", "address", "show", "dev", a.Interface)
	if err != nil {
		return nil, errors.New("cannot inspect IPv6 interface")
	}
	var devices []struct {
		Addresses []addressInfo `json:"addr_info"`
	}
	if err = json.Unmarshal(b, &devices); err != nil {
		return nil, err
	}
	for _, d := range devices {
		for _, v := range d.Addresses {
			if v.Local == address {
				return &v, nil
			}
		}
	}
	return nil, nil
}

func (a LinuxAddresses) Ensure(ctx context.Context, address string, restore bool) (bool, error) {
	current, err := a.lookup(ctx, address)
	if err != nil {
		return false, err
	}
	if current != nil {
		if !restore || current.PrefixLen != 128 {
			return false, errors.New("IPv6 address already exists outside this allocation")
		}
		// Also repair leases created by an earlier version. Deprecation makes
		// source selection prefer the primary while explicit bind still works.
		if _, err = a.run(ctx, "-6", "address", "change", address+"/128", "dev", a.Interface, "preferred_lft", "0", "valid_lft", "forever"); err != nil {
			return true, errors.New("cannot deprecate restored IPv6 address")
		}
	} else {
		// Persist ownership intent after checking for collisions, before the
		// kernel mutation. A crash immediately after ip addr add is recoverable.
		if a.BeforeAdd != nil {
			if err = a.BeforeAdd(address); err != nil {
				return false, err
			}
		}
		// Keep DAD enabled. A zero preferred lifetime prevents another host
		// process from adopting a lease as its automatically selected source.
		if _, err = a.run(ctx, "-6", "address", "add", address+"/128", "dev", a.Interface, "preferred_lft", "0", "valid_lft", "forever"); err != nil {
			return false, errors.New("cannot configure IPv6 address")
		}
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		current, err = a.lookup(ctx, address)
		if err != nil {
			return true, err
		}
		if current != nil {
			tentative, failed := current.Tentative, current.DADFailed
			for _, flag := range current.Flags {
				tentative = tentative || flag == "tentative"
				failed = failed || flag == "dadfailed"
			}
			if failed {
				return true, errors.New("IPv6 duplicate address detection failed")
			}
			if !tentative {
				return true, nil
			}
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-deadline.C:
			return true, errors.New("IPv6 duplicate address detection timed out")
		case <-tick.C:
		}
	}
}

func (a LinuxAddresses) Remove(ctx context.Context, address string) error {
	current, err := a.lookup(ctx, address)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	if current.PrefixLen != 128 {
		return errors.New("refusing to remove an unmanaged interface address")
	}
	if _, err = a.run(ctx, "-6", "address", "del", address+"/128", "dev", a.Interface); err != nil {
		return errors.New("cannot remove IPv6 address")
	}
	return nil
}

type Resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}
type DialFunc func(context.Context, string, string, string) (net.Conn, error)

// Network resolves once and dials the validated IP literal, eliminating DNS
// rebinding between validation and connection. No IPv4 dial is attempted.
type Network struct {
	Prefix         netip.Prefix
	Resolver       Resolver
	Dial           DialFunc
	LocalAddresses []netip.Addr
}

var blockedIPv6 = []netip.Prefix{
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
}

func (n Network) allowed(ip netip.Addr) bool {
	if !ip.Is6() || ip.Is4In6() || !ip.IsGlobalUnicast() || ip.IsPrivate() || n.Prefix.Contains(ip) {
		return false
	}
	for _, p := range blockedIPv6 {
		if p.Contains(ip) {
			return false
		}
	}
	for _, local := range n.LocalAddresses {
		if ip == local {
			return false
		}
	}
	return true
}

func systemDial(ctx context.Context, network, address, source string) (net.Conn, error) {
	d := net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
	return d.DialContext(ctx, network, address)
}

func (n Network) Connect(ctx context.Context, target, source string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil || strings.Contains(host, "%") || (port != "80" && port != "443") {
		return nil, errors.New("only public IPv6 HTTP/HTTPS destinations are allowed")
	}
	if src, err := netip.ParseAddr(source); err != nil || !src.Is6() || src.Is4In6() || !n.Prefix.Contains(src) {
		return nil, errors.New("invalid IPv6 source")
	}
	var addresses []net.IP
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []net.IP{net.IP(literal.AsSlice())}
	} else {
		resolver := n.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		addresses, err = resolver.LookupIP(ctx, "ip6", host)
		if err != nil {
			return nil, errors.New("upstream IPv6 DNS lookup failed")
		}
	}
	dial := n.Dial
	if dial == nil {
		dial = systemDial
	}
	var last error
	for _, ip := range addresses {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || !n.allowed(addr) {
			continue
		}
		conn, err := dial(ctx, "tcp6", net.JoinHostPort(addr.String(), port), source)
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	if last != nil {
		return nil, fmt.Errorf("upstream IPv6 connection failed")
	}
	return nil, errors.New("destination has no permitted IPv6 address")
}
