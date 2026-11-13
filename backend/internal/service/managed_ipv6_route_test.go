package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ovhRouteProxyStub struct {
	ProxyRepository
	proxy  *Proxy
	err    error
	checks int
}

func (r *ovhRouteProxyStub) CheckOVHIPv6Route(context.Context, *Account) error {
	r.checks++
	return r.err
}
func (r *ovhRouteProxyStub) GetByID(context.Context, int64) (*Proxy, error) { return r.proxy, r.err }

func testOVHRoute() (*Account, *Proxy) {
	id := int64(78)
	p := &Proxy{ID: id, Protocol: "socks5", Host: "127.0.0.1", Port: 1080, Username: "ovh6_123", Password: strings.Repeat("p", 43), Status: StatusActive, FallbackMode: FallbackModeNone}
	a := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ProxyID: &id, Proxy: p, Extra: map[string]any{"ovh_ipv6_bound": true}}
	return a, p
}

func TestManagedIPv6RoutePinsOVHTicketAndRejectsDetachedExit(t *testing.T) {
	a, p := testOVHRoute()
	ticket := &openAICodexTicket{HarvestProxyURL: p.URL()}
	require.True(t, codexTicketMatchesIPv6Route(a, ticket))
	ticket.HarvestNodeID = "rotating-node"
	require.False(t, codexTicketMatchesIPv6Route(a, ticket))
	ticket.HarvestNodeID = ""
	require.Error(t, validateOpenAIIPv6Route(a, ""), "fixed egress must not silently become direct")
	require.NoError(t, validateOpenAIIPv6Route(a, p.URL()))
	p.Password = strings.Repeat("q", 43)
	require.False(t, codexTicketMatchesIPv6Route(a, ticket), "a ticket from an earlier proxy credential must not be reused")
	delete(a.Extra, "ovh_ipv6_bound")
	a.ProxyID, a.Proxy = nil, nil
	require.False(t, codexTicketMatchesIPv6Route(a, ticket), "detaching must not revive a ticket bound to the released IPv6")
	ticket.HarvestProxyURL = "socks5://ordinary:1080"
	require.True(t, codexTicketMatchesIPv6Route(a, ticket), "ordinary ticket route semantics remain unchanged")
}

func TestManagedIPv6RouteChecksAuthoritativeOVHBinding(t *testing.T) {
	a, p := testOVHRoute()
	proxies := &ovhRouteProxyStub{proxy: p}
	svc := NewOpenAIIPv6Service(&ipv6TestRepo{}, nil, nil, nil, proxies)
	require.NoError(t, svc.CheckRoute(context.Background(), a, p.URL()))
	require.Equal(t, 1, proxies.checks)
	proxies.err = errors.New("OVH route changed")
	stale := &Account{ID: a.ID, Platform: PlatformOpenAI}
	require.ErrorContains(t, svc.CheckRoute(context.Background(), stale, ""), "OVH route changed")
	_, err := resolveOpenAIAccountProxyURL(context.Background(), proxies, stale)
	require.ErrorContains(t, err, "OVH route changed", "token refresh and harvest must reject unmarked stale snapshots too")
}

func TestManagedIPv6RouteRejectsUnavailableOVHProxy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(*Account, *Proxy)
	}{
		{"missing proxy", func(a *Account, _ *Proxy) { a.Proxy = nil }},
		{"disabled", func(_ *Account, p *Proxy) { p.Status = StatusDisabled }},
		{"expired", func(_ *Account, p *Proxy) { past := time.Now().Add(-time.Hour); p.ExpiresAt = &past }},
		{"weak credential", func(_ *Account, p *Proxy) { p.Password = "" }},
		{"wrong provider", func(_ *Account, p *Proxy) { p.Username = "ordinary" }},
		{"backup", func(_ *Account, p *Proxy) { id := int64(9); p.BackupProxyID = &id }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, p := testOVHRoute()
			tt.mutate(a, p)
			require.Error(t, validateManagedAccountProxy(a, a.Proxy))
		})
	}
}
