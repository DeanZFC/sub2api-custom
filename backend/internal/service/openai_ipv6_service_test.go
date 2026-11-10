package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type ipv6TestRepo struct {
	OpenAIIPv6Repository
	b        *OpenAIIPv6Binding
	p        *Proxy
	account  *Account
	reserves int
}

func (r *ipv6TestRepo) GetBinding(context.Context, int64) (*OpenAIIPv6Binding, error) {
	return r.b, nil
}
func (r *ipv6TestRepo) WithLock(_ context.Context, _ int64, fn func() error) error { return fn() }
func (r *ipv6TestRepo) Reserve(_ context.Context, id int64, lease, control string, p *Proxy) (*OpenAIIPv6Binding, error) {
	r.reserves++
	p.ID = 70
	r.p = p
	r.b = &OpenAIIPv6Binding{AccountID: id, LeaseID: lease, ManagedProxyID: p.ID, ControlURL: control, Status: "provisioning"}
	r.account.ProxyID = &p.ID
	r.account.Proxy = p
	r.account.Extra = map[string]any{"openai_ipv6_bound": true}
	return r.b, nil
}
func (r *ipv6TestRepo) Activate(_ context.Context, _ int64, address, password string) error {
	r.b.Status = "ready"
	r.b.Address = address
	r.p.Password = password
	return nil
}
func (r *ipv6TestRepo) SetState(_ context.Context, _ int64, status, message string) error {
	r.b.Status = status
	r.b.LastError = message
	return nil
}
func (r *ipv6TestRepo) Restore(context.Context, int64) error {
	r.b = nil
	r.account.ProxyID = nil
	r.account.Proxy = nil
	delete(r.account.Extra, "openai_ipv6_bound")
	return nil
}

type ipv6TestAccounts struct {
	AccountRepository
	a       *Account
	shadows []*Account
}

func (r *ipv6TestAccounts) GetByID(context.Context, int64) (*Account, error) { return r.a, nil }
func (r *ipv6TestAccounts) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return r.shadows, nil
}

type ipv6TestProxies struct {
	ProxyRepository
	repo *ipv6TestRepo
}

func (p *ipv6TestProxies) GetByID(context.Context, int64) (*Proxy, error) { return p.repo.p, nil }
func ipv6TestConfig(t *testing.T, url string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte(strings.Repeat("k", 32)), 0600))
	t.Setenv("OPENAI_IPV6_EGRESS_CONTROL_URL", url)
	t.Setenv("OPENAI_IPV6_EGRESS_PROXY_ADDRESS", "127.0.0.1:18080")
	t.Setenv("OPENAI_IPV6_EGRESS_CONTROL_TOKEN_FILE", file)
}
func TestOpenAIIPv6BindRetryDetach(t *testing.T) {
	calls := 0
	firstID := ""
	deleted := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer "+strings.Repeat("k", 32), r.Header.Get("Authorization"))
		if r.Method == "DELETE" {
			deleted++
			require.True(t, strings.HasSuffix(r.URL.Path, firstID))
			w.WriteHeader(204)
			return
		}
		var in map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		calls++
		if firstID == "" {
			firstID = in["id"]
		}
		require.Equal(t, firstID, in["id"])
		if calls == 1 {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": in["id"], "address": "2001:db8::123", "username": "v6-" + in["id"], "password": strings.Repeat("p", 32)})
	}))
	defer server.Close()
	ipv6TestConfig(t, server.URL)
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "inactive"}
	repo := &ipv6TestRepo{account: a}
	svc := NewOpenAIIPv6Service(repo, &ipv6TestAccounts{a: a}, &ConcurrencyService{cache: &ipv6ConcurrencyStub{}}, nil, &ipv6TestProxies{repo: repo})
	_, err := svc.Bind(context.Background(), 1)
	require.Error(t, err)
	require.Equal(t, "error", repo.b.Status)
	require.Equal(t, 1, repo.reserves)
	b, err := svc.Bind(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "ready", b.Status)
	require.Equal(t, firstID, b.LeaseID)
	require.Equal(t, 1, repo.reserves)
	require.NoError(t, svc.CheckRoute(context.Background(), a, a.Proxy.URL()))
	require.Error(t, svc.CheckRoute(context.Background(), &Account{ID: 1, Platform: PlatformOpenAI}, ""))
	require.NoError(t, svc.Detach(context.Background(), 1))
	require.NoError(t, svc.Detach(context.Background(), 1))
	require.Equal(t, 1, deleted)
	require.Nil(t, repo.b)
}
func TestOpenAIIPv6DrainAndRouteFailClosed(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "active"}
	cache := &ipv6ConcurrencyStub{}
	svc := NewOpenAIIPv6Service(nil, &ipv6TestAccounts{a: a}, &ConcurrencyService{cache: cache}, nil, nil)
	require.Error(t, svc.drained(context.Background(), 1))
	a.Status = "inactive"
	cache.concurrency = 1
	require.Error(t, svc.drained(context.Background(), 1))
	cache.concurrency = 0
	cache.concurrencyErr = errors.New("redis unavailable")
	require.Error(t, svc.drained(context.Background(), 1))
	cache.concurrencyErr = nil
	require.NoError(t, svc.drained(context.Background(), 1))
	id := int64(7)
	a.ProxyID = &id
	a.Extra = map[string]any{"openai_ipv6_bound": true}
	require.Error(t, validateOpenAIIPv6Route(a, ""))
	p := &Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 18080, Username: "v6-sub2-test", Password: strings.Repeat("p", 32), Status: StatusActive, FallbackMode: FallbackModeNone}
	a.Proxy = p
	require.NoError(t, validateOpenAIIPv6Route(a, p.URL()))
	require.Error(t, validateOpenAIIPv6Route(a, "http://other:80"))
	_, err := resolveOpenAIAccountProxyURL(context.Background(), &vmTestProxies{err: errors.New("missing")}, a)
	require.Error(t, err)
	expiry := time.Now().Add(-time.Second)
	p.ExpiresAt = &expiry
	require.Error(t, validateOpenAIIPv6Route(a, p.URL()))
}
func TestOpenAIIPv6HarvestIgnoresGlobalPool(t *testing.T) {
	id := int64(7)
	p := &Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 18080, Username: "v6-sub2-test", Password: strings.Repeat("p", 32), Status: StatusActive, FallbackMode: FallbackModeNone}
	a := &Account{ID: 1, ProxyID: &id, Proxy: p, Extra: map[string]any{"openai_ipv6_bound": true}}
	s := &OpenAIGatewayService{proxyRepo: &vmTestProxies{p: p}}
	attempt, ok := s.prepareHarvestAttempt(context.Background(), a, "model", "http://unrelated:80", nil, CodexHarvestControls{})
	require.True(t, ok)
	require.Equal(t, p.URL(), attempt.proxy)
	require.Empty(t, attempt.node.ID)
	manual, err := s.acquireManualHarvestNode(context.Background(), a, "model", "http://unrelated:80", "", nil, true)
	require.NoError(t, err)
	require.Equal(t, p.URL(), manual.proxy)
	_, _, err = s.pinCodexTicketEgressFromHeader(context.Background(), http.Header{}, a, "http://unrelated:80")
	require.Error(t, err)
}

type ipv6ConcurrencyStub struct {
	ConcurrencyCache
	concurrency    int
	concurrencyErr error
}

func (c *ipv6ConcurrencyStub) GetAccountConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	for _, id := range ids {
		out[id] = c.concurrency
	}
	return out, c.concurrencyErr
}

func TestOpenAIIPv6TicketRouteAfterDetach(t *testing.T) {
	id := int64(7)
	p := &Proxy{ID: id, Protocol: "http", Host: "127.0.0.1", Port: 18080, Username: "v6-sub2-test", Password: strings.Repeat("p", 32), Status: StatusActive, FallbackMode: FallbackModeNone}
	a := &Account{ID: 1, ProxyID: &id, Proxy: p, Extra: map[string]any{"openai_ipv6_bound": true}}
	ticket := &openAICodexTicket{HarvestProxyURL: p.URL()}
	require.True(t, codexTicketMatchesIPv6Route(a, ticket))
	ticket.HarvestProxyURL = "http://other:80"
	require.False(t, codexTicketMatchesIPv6Route(a, ticket))
	ticket.HarvestProxyURL = p.URL()
	delete(a.Extra, "openai_ipv6_bound")
	require.False(t, codexTicketMatchesIPv6Route(a, ticket))
	ticket.HarvestProxyURL = "http://other:80"
	require.True(t, codexTicketMatchesIPv6Route(a, ticket))
}
