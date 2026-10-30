// Package mainlandip provides offline membership checks against mainland China
// country IP blocks. Country allocation data is approximate, not a guarantee of
// a visitor's physical location.
package mainlandip

import (
	"bufio"
	"embed"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
)

const maxDataBytes = 16 << 20

// Keep the upstream copyright notice in source distributions and binaries.
//
//go:embed data/cn.zone data/Copyrights.txt
var embeddedData embed.FS

var (
	defaultOnce sync.Once
	defaultSet  *Set
	defaultErr  error
)

// Set is an immutable collection of non-overlapping country prefixes. It is
// safe to share between concurrent requests.
type Set struct {
	ipv4 []netip.Prefix
	ipv6 []netip.Prefix
}

// Parse validates a country CIDR file containing both IPv4 and IPv6 prefixes.
// Blank lines and full-line # comments are accepted; invalid records, default
// routes and files missing either address family are rejected.
func Parse(r io.Reader) (*Set, error) {
	if r == nil {
		return nil, fmt.Errorf("mainland IP data: nil reader")
	}
	limited := &io.LimitedReader{R: r, N: maxDataBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 256), 1024)
	set := &Set{}
	for line := 1; scanner.Scan(); line++ {
		if limited.N == 0 {
			return nil, fmt.Errorf("mainland IP data exceeds %d bytes", maxDataBytes)
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return nil, fmt.Errorf("mainland IP data line %d: invalid CIDR: %w", line, err)
		}
		if prefix.Bits() == 0 || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("mainland IP data line %d: default routes and mapped prefixes are not allowed", line)
		}
		prefix = prefix.Masked()
		if prefix.Addr().Is4() {
			set.ipv4 = append(set.ipv4, prefix)
		} else {
			set.ipv6 = append(set.ipv6, prefix)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read mainland IP data: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("mainland IP data exceeds %d bytes", maxDataBytes)
	}
	if len(set.ipv4) == 0 || len(set.ipv6) == 0 {
		return nil, fmt.Errorf("mainland IP data must contain both IPv4 and IPv6 prefixes")
	}
	set.ipv4 = compact(set.ipv4)
	set.ipv6 = compact(set.ipv6)
	return set, nil
}

// Load reads a validated local replacement dataset. The returned set remains
// valid after the file is closed or atomically replaced.
func Load(path string) (*Set, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open mainland IP data: %w", err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Default loads the bundled country prefixes once. It never uses the network.
func Default() (*Set, error) {
	defaultOnce.Do(func() {
		f, err := embeddedData.Open("data/cn.zone")
		if err != nil {
			defaultErr = fmt.Errorf("open bundled mainland IP data: %w", err)
			return
		}
		defer func() { _ = f.Close() }()
		defaultSet, defaultErr = Parse(f)
	})
	return defaultSet, defaultErr
}

// Contains reports whether a public unicast IP belongs to the dataset. IPv4
// mapped IPv6 addresses are normalized; private, local and invalid IPs do not
// match. Lookup is O(log n) without per-visitor state.
func (s *Set) Contains(addr netip.Addr) bool {
	if s == nil || !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.Zone() != "" {
		return false
	}
	prefixes := s.ipv6
	if addr.Is4() {
		prefixes = s.ipv4
	}
	i := sort.Search(len(prefixes), func(i int) bool {
		return prefixes[i].Addr().Compare(addr) > 0
	}) - 1
	return i >= 0 && prefixes[i].Contains(addr)
}

func compact(prefixes []netip.Prefix) []netip.Prefix {
	sort.Slice(prefixes, func(i, j int) bool {
		if cmp := prefixes[i].Addr().Compare(prefixes[j].Addr()); cmp != 0 {
			return cmp < 0
		}
		return prefixes[i].Bits() < prefixes[j].Bits()
	})
	result := prefixes[:0]
	for _, prefix := range prefixes {
		if len(result) > 0 && result[len(result)-1].Contains(prefix.Addr()) {
			continue
		}
		result = append(result, prefix)
	}
	return result
}
