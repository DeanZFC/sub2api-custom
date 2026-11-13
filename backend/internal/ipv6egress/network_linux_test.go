//go:build linux && integration

package ipv6egress

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

// Run only in a disposable network namespace with CAP_NET_ADMIN. It creates a
// dedicated dummy interface and never changes existing interfaces or routes.
func TestLinuxDeprecatedLeasePreservesDefaultAndAllowsBind(t *testing.T) {
	if os.Getenv("IPV6_EGRESS_KERNEL_TEST") != "1" {
		t.Skip("requires isolated Linux network namespace and explicit kernel-test opt-in")
	}
	const primary, lease, destination = "2001:db8:7a::1", "2001:db8:7a::20", "2001:db8:7a::1234"
	a := LinuxAddresses{Interface: "egresstest0"}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	run := func(args ...string) []byte {
		t.Helper()
		b, err := a.run(ctx, args...)
		if err != nil {
			t.Fatalf("ip %q failed: %v", args, err)
		}
		return b
	}
	run("link", "add", a.Interface, "type", "dummy")
	t.Cleanup(func() { _, _ = a.run(context.Background(), "link", "del", a.Interface) })
	run("link", "set", a.Interface, "up")
	run("-6", "address", "add", primary+"/64", "dev", a.Interface)
	for {
		info, err := a.lookup(ctx, primary)
		if err != nil {
			t.Fatal(err)
		}
		if info != nil && !info.Tentative {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("primary DAD did not finish")
		case <-time.After(50 * time.Millisecond):
		}
	}
	source := func() string {
		t.Helper()
		var routes []struct {
			PreferredSource string `json:"prefsrc"`
			Source          string `json:"src"`
		}
		b := run("-j", "-6", "route", "get", destination)
		if err := json.Unmarshal(b, &routes); err != nil || len(routes) != 1 {
			t.Fatalf("invalid route result: %s %v", b, err)
		}
		if routes[0].PreferredSource != "" {
			return routes[0].PreferredSource
		}
		return routes[0].Source
	}
	if got := source(); got != primary {
		t.Fatalf("unexpected initial source: %q", got)
	}
	if owned, err := a.Ensure(ctx, lease, false); err != nil || !owned {
		t.Fatalf("lease provisioning failed: %v %v", owned, err)
	}
	assertDeprecated := func() {
		t.Helper()
		var interfaces []struct {
			Addresses []struct {
				Local     string `json:"local"`
				Preferred int64  `json:"preferred_life_time"`
				Valid     int64  `json:"valid_life_time"`
			} `json:"addr_info"`
		}
		b := run("-j", "-6", "address", "show", "dev", a.Interface)
		if err := json.Unmarshal(b, &interfaces); err != nil {
			t.Fatal(err)
		}
		for _, iface := range interfaces {
			for _, address := range iface.Addresses {
				if address.Local == lease {
					if address.Preferred != 0 || address.Valid != 4294967295 {
						t.Fatalf("wrong lease lifetimes: %+v", address)
					}
					return
				}
			}
		}
		t.Fatal("lease missing")
	}
	assertDeprecated()
	if got := source(); got != primary {
		t.Fatalf("new lease changed default source to %q", got)
	}
	// Exercise migration of an existing owned /128 from an older agent version.
	run("-6", "address", "change", lease+"/128", "dev", a.Interface, "preferred_lft", "forever", "valid_lft", "forever")
	if owned, err := a.Ensure(ctx, lease, true); err != nil || !owned {
		t.Fatalf("lease restore failed: %v %v", owned, err)
	}
	assertDeprecated()
	if got := source(); got != primary {
		t.Fatalf("restored lease changed default source to %q", got)
	}
	listener, err := net.Listen("tcp6", net.JoinHostPort(primary, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err == nil {
			defer peer.Close()
			_, err = io.WriteString(peer, "source-bound")
		}
		accepted <- err
	}()
	conn, err := systemDial(ctx, "tcp6", listener.Addr().String(), lease)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if got := conn.LocalAddr().(*net.TCPAddr).IP.String(); got != lease {
		t.Fatalf("explicit bind used %q", got)
	}
	b := make([]byte, len("source-bound"))
	if _, err = io.ReadFull(conn, b); err != nil || string(b) != "source-bound" {
		t.Fatalf("deprecated source did not carry TCP: %q %v", b, err)
	}
	if err = <-accepted; err != nil {
		t.Fatal(err)
	}
}
