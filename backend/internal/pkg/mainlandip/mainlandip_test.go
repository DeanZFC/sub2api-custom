package mainlandip

import (
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestParseCompactsAndMatchesBoundaries(t *testing.T) {
	set, err := Parse(strings.NewReader(`# country prefixes
203.0.113.128/25
203.0.113.0/24
203.0.113.0/24
203.0.114.16/28
2001:db8:1::/48
2001:db8::/32
2001:db9::/48

`))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.ipv4) != 2 || len(set.ipv6) != 2 {
		t.Fatalf("unexpected compacted prefix counts: IPv4=%d IPv6=%d", len(set.ipv4), len(set.ipv6))
	}
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"203.0.112.255", false},
		{"203.0.113.0", true},
		{"203.0.113.255", true},
		{"203.0.114.0", false},
		{"203.0.114.15", false},
		{"203.0.114.16", true},
		{"203.0.114.31", true},
		{"203.0.114.32", false},
		{"::ffff:203.0.113.20", true},
		{"2001:db7:ffff:ffff:ffff:ffff:ffff:ffff", false},
		{"2001:db8::", true},
		{"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", true},
		{"2001:db9::", true},
		{"2001:db9:0:ffff:ffff:ffff:ffff:ffff", true},
		{"2001:db9:1::", false},
		{"2001:db8::1%eth0", false},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			if got := set.Contains(netip.MustParseAddr(tc.ip)); got != tc.want {
				t.Errorf("Contains(%s)=%v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestParseMasksHostBits(t *testing.T) {
	set, err := Parse(strings.NewReader("203.0.113.9/24\n2001:db8::1/32\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !set.Contains(netip.MustParseAddr("203.0.113.255")) || !set.Contains(netip.MustParseAddr("2001:db8:1::1")) {
		t.Fatal("host bits were not masked")
	}
}

func TestParseRejectsInvalidData(t *testing.T) {
	for _, input := range []string{
		"", "# only comments\n", "203.0.113.0/24\n", "2001:db8::/32\n",
		"203.0.113.0/24\n2001:db8::/32\nnot-a-cidr\n",
		"0.0.0.0/0\n2001:db8::/32\n",
		"203.0.113.0/24\n::/0\n",
		"::ffff:203.0.113.0/120\n2001:db8::/32\n",
		"203.0.113.0/33\n2001:db8::/32\n",
		"203.0.113.0/24\n2001:db8::/129\n",
		"203.0.113.0/24 extra\n2001:db8::/32\n",
		"<html>upstream error</html>",
		strings.Repeat("#", 2048),
	} {
		if _, err := Parse(strings.NewReader(input)); err == nil {
			t.Errorf("Parse accepted invalid input %q", input)
		}
	}
	if _, err := Parse(nil); err == nil {
		t.Fatal("Parse accepted nil reader")
	}
	if _, err := Parse(brokenReader{}); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("reader error not propagated: %v", err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestContainsExcludesLocalAddresses(t *testing.T) {
	set, err := Parse(strings.NewReader("10.0.0.0/8\n127.0.0.0/8\n169.254.0.0/16\n192.168.0.0/16\n224.0.0.0/4\n0.0.0.0/32\n255.255.255.255/32\n::/128\n::1/128\nfc00::/7\nfe80::/10\nff00::/8\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"10.1.2.3", "127.0.0.1", "169.254.1.1", "192.168.1.1", "224.0.0.1", "0.0.0.0", "255.255.255.255", "::", "::1", "fc00::1", "fe80::1", "ff02::1", "::ffff:127.0.0.1"} {
		if set.Contains(netip.MustParseAddr(value)) {
			t.Errorf("local address matched: %s", value)
		}
	}
	if set.Contains(netip.Addr{}) || (*Set)(nil).Contains(netip.MustParseAddr("223.5.5.5")) {
		t.Fatal("invalid address or nil set matched")
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cn.zone")
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted missing file")
	}
	if err := os.WriteFile(path, []byte("203.0.113.0/24\n2001:db8::/32\n"), 0600); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !set.Contains(netip.MustParseAddr("203.0.113.1")) {
		t.Fatal("loaded set depends on open file")
	}
}

func TestDefaultCountrySamples(t *testing.T) {
	set, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.ipv4) < 100 || len(set.ipv6) < 100 {
		t.Fatal("bundled data appears incomplete")
	}
	for _, tc := range []struct {
		ip   string
		want bool
	}{
		{"223.5.5.5", true},
		{"::ffff:223.5.5.5", true},
		{"2400:3200::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2606:4700:4700::1111", false},
	} {
		if got := set.Contains(netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("bundled Contains(%s)=%v, want %v", tc.ip, got, tc.want)
		}
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			other, err := Default()
			if err != nil || other != set || !other.Contains(netip.MustParseAddr("223.5.5.5")) {
				t.Errorf("concurrent Default failed: %v", err)
			}
		}()
	}
	workers.Wait()
}
