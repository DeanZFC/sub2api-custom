package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type OpenAIIPv6Service struct {
	repo        OpenAIIPv6Repository
	accounts    AccountRepository
	concurrency *ConcurrencyService
	scheduler   SchedulerCache
	proxies     ProxyRepository
	client      *http.Client
}

func NewOpenAIIPv6Service(repo OpenAIIPv6Repository, accounts AccountRepository, concurrency *ConcurrencyService, scheduler SchedulerCache, proxies ProxyRepository) *OpenAIIPv6Service {
	return &OpenAIIPv6Service{repo: repo, accounts: accounts, concurrency: concurrency, scheduler: scheduler, proxies: proxies, client: &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type ipv6Config struct{ control, proxy, token string }

func loadIPv6Config() (ipv6Config, error) {
	var c ipv6Config
	var err error
	c.control, err = validateOpenAIVMEndpoint(os.Getenv("OPENAI_IPV6_EGRESS_CONTROL_URL"))
	if err != nil {
		return c, errors.New("IPv6 control endpoint is missing or invalid")
	}
	raw := strings.TrimSpace(os.Getenv("OPENAI_IPV6_EGRESS_PROXY_ADDRESS"))
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	c.proxy, err = validateOpenAIVMEndpoint(raw)
	if err != nil {
		return c, errors.New("IPv6 proxy endpoint is missing or invalid")
	}
	b, err := os.ReadFile(os.Getenv("OPENAI_IPV6_EGRESS_CONTROL_TOKEN_FILE")) //nolint:gosec // The path is trusted operator configuration, never request input.
	if err != nil {
		return c, errors.New("IPv6 control token file is unavailable")
	}
	c.token = strings.TrimSpace(string(b))
	if len(c.token) < 32 || strings.ContainsAny(c.token, "\r\n") {
		return c, errors.New("IPv6 control token must contain at least 32 characters")
	}
	return c, nil
}
func ipv6Eligible(a *Account) bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsShadow() && !a.IsOpenAIAgentIdentity() && !a.IsOpenAIVMBound() && !a.IsExcelBPSEnabled()
}

type OpenAIIPv6Status struct {
	Binding    *OpenAIIPv6Binding `json:"binding"`
	Eligible   bool               `json:"eligible"`
	Configured bool               `json:"configured"`
}

func (s *OpenAIIPv6Service) Status(ctx context.Context, id int64) (*OpenAIIPv6Status, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	b, err := s.repo.GetBinding(ctx, openAIVMOwnerID(a))
	if err != nil {
		return nil, err
	}
	_, cfgErr := loadIPv6Config()
	return &OpenAIIPv6Status{b, ipv6Eligible(a), cfgErr == nil}, nil
}
func (s *OpenAIIPv6Service) drained(ctx context.Context, id int64) error {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if !ipv6Eligible(a) {
		return infraerrors.BadRequest("OPENAI_IPV6_INELIGIBLE", "Fixed IPv6 requires an OpenAI OAuth parent account without VM, Agent Identity, or Excel BPS")
	}
	shadows, err := s.accounts.ListShadowsByParent(ctx, id)
	if err != nil {
		return err
	}
	all := append(shadows, a)
	ids := make([]int64, 0, len(all))
	for _, v := range all {
		if v.Status != StatusDisabled && v.Status != "inactive" {
			return infraerrors.Conflict("OPENAI_IPV6_DRAIN_REQUIRED", "Disable the account and all shadows, then wait for active requests to finish")
		}
		ids = append(ids, v.ID)
	}
	if s.concurrency == nil || s.concurrency.cache == nil {
		return infraerrors.Conflict("OPENAI_IPV6_DRAIN_UNAVAILABLE", "Cannot verify active requests; retry when concurrency tracking is available")
	}
	counts, err := s.concurrency.GetAccountConcurrencyBatch(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		n, ok := counts[id]
		if !ok || n != 0 {
			return infraerrors.Conflict("OPENAI_IPV6_DRAIN_REQUIRED", "Active requests remain; wait before changing IPv6 egress")
		}
	}
	return nil
}
func (s *OpenAIIPv6Service) remote(ctx context.Context, c ipv6Config, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.control+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return errors.New("IPv6 control request failed; retry the same operation")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("IPv6 control rejected the operation; check the egress service")
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(out)
	}
	return nil
}
func ipv6Proxy(c ipv6Config, leaseID string) *Proxy {
	u, _ := url.Parse(c.proxy)
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if u.Port() != "" {
		port, _ = strconv.Atoi(u.Port())
	}
	return &Proxy{Protocol: u.Scheme, Host: u.Hostname(), Port: port, Username: "v6-" + leaseID, Status: StatusActive, FallbackMode: FallbackModeNone}
}
func (s *OpenAIIPv6Service) Bind(ctx context.Context, id int64) (*OpenAIIPv6Binding, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var result *OpenAIIPv6Binding
	err := s.repo.WithLock(ctx, id, func() error {
		if err := s.drained(ctx, id); err != nil {
			return err
		}
		c, err := loadIPv6Config()
		if err != nil {
			return err
		}
		b, err := s.repo.GetBinding(ctx, id)
		if err != nil {
			return err
		}
		if b != nil && b.Status == "ready" {
			result = b
			return nil
		}
		if b != nil && (b.Status == "deleting" || b.Status == "delete_failed") {
			return infraerrors.Conflict("OPENAI_IPV6_DELETING", "Retry detach before creating another IPv6 binding")
		}
		if b == nil {
			var random [16]byte
			if _, err = rand.Read(random[:]); err != nil {
				return err
			}
			leaseID := "sub2-" + hex.EncodeToString(random[:])
			b, err = s.repo.Reserve(ctx, id, leaseID, c.control, ipv6Proxy(c, leaseID))
			if err != nil {
				return err
			}
		}
		p, lookupErr := s.proxies.GetByID(ctx, b.ManagedProxyID)
		expectedProxy := ipv6Proxy(c, b.LeaseID)
		if lookupErr != nil || p == nil || p.Host != expectedProxy.Host || p.Port != expectedProxy.Port || p.Protocol != expectedProxy.Protocol {
			return infraerrors.Conflict("OPENAI_IPV6_CONFIG_CHANGED", "Restore the original proxy endpoint before retrying")
		}
		if b.ControlURL != c.control {
			return infraerrors.Conflict("OPENAI_IPV6_CONFIG_CHANGED", "Restore the original control endpoint before retrying or detaching")
		}
		var l struct {
			ID       string `json:"id"`
			Address  string `json:"address"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err = s.remote(ctx, c, http.MethodPost, "/v1/leases", map[string]string{"id": b.LeaseID}, &l); err == nil {
			ip := net.ParseIP(l.Address)
			if l.ID != b.LeaseID || ip == nil || ip.To4() != nil || l.Username != "v6-"+b.LeaseID || len(l.Password) < 32 {
				err = errors.New("IPv6 service returned an invalid lease")
			}
		}
		if err == nil {
			err = s.repo.Activate(ctx, id, l.Address, l.Password)
		}
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			stateErr := s.repo.SetState(cleanup, id, "error", "Provisioning incomplete; retry reuses the reserved lease")
			s.refresh(cleanup, id)
			return errors.Join(err, stateErr)
		}
		s.refresh(ctx, id)
		result, err = s.repo.GetBinding(ctx, id)
		return err
	})
	return result, err
}
func (s *OpenAIIPv6Service) Detach(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	return s.repo.WithLock(ctx, id, func() error {
		if err := s.drained(ctx, id); err != nil {
			return err
		}
		b, err := s.repo.GetBinding(ctx, id)
		if err != nil || b == nil {
			return err
		}
		c, err := loadIPv6Config()
		if err != nil {
			return err
		}
		if c.control != b.ControlURL {
			return infraerrors.Conflict("OPENAI_IPV6_CONFIG_CHANGED", "Restore the original control endpoint before detaching")
		}
		if err = s.repo.SetState(ctx, id, "deleting", ""); err != nil {
			return err
		}
		if err = s.remote(ctx, c, http.MethodDelete, "/v1/leases/"+b.LeaseID, nil, nil); err == nil {
			err = s.repo.Restore(ctx, id)
		}
		if err != nil {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			return errors.Join(err, s.repo.SetState(cleanup, id, "delete_failed", "Detach incomplete; retry cleanup before enabling this account"))
		}
		s.refresh(ctx, id)
		return nil
	})
}
func (s *OpenAIIPv6Service) refresh(ctx context.Context, id int64) {
	if s.scheduler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if a, err := s.accounts.GetByID(ctx, id); err == nil && a != nil {
		_ = s.scheduler.SetAccount(ctx, a)
	}
	if shadows, err := s.accounts.ListShadowsByParent(ctx, id); err == nil {
		for _, a := range shadows {
			_ = s.scheduler.SetAccount(ctx, a)
		}
	}
}
func validateOpenAIIPv6Proxy(a *Account, p *Proxy) error {
	if !a.IsOpenAIIPv6Bound() {
		return nil
	}
	if p == nil || a.ProxyID == nil || p.ID != *a.ProxyID || !strings.HasPrefix(p.Username, "v6-sub2-") || len(p.Password) < 32 || !p.IsActive() || p.IsExpired(time.Now()) || p.FallbackMode != FallbackModeNone {
		return errors.New("fixed IPv6 proxy unavailable; fallback is disabled")
	}
	return nil
}
func validateOpenAIIPv6Route(a *Account, route string) error {
	if !a.IsOpenAIIPv6Bound() {
		return nil
	}
	if err := validateOpenAIIPv6Proxy(a, a.Proxy); err != nil {
		return err
	}
	if route != a.Proxy.URL() {
		return errors.New("fixed IPv6 egress conflicts with the selected route; refresh tickets on the bound exit")
	}
	return nil
}

// Authoritative binding checks also reject stale scheduler snapshots after a
// route change. No request may use the previous direct/proxy route meanwhile.
func (s *OpenAIIPv6Service) CheckRoute(ctx context.Context, a *Account, route string) error {
	if s == nil || a == nil || a.Platform != PlatformOpenAI {
		return nil
	}
	b, err := s.repo.GetBinding(ctx, openAIVMOwnerID(a))
	if err != nil {
		return err
	}
	if b == nil {
		if a.IsOpenAIIPv6Bound() {
			return errors.New("IPv6 binding changed; refresh the account route")
		}
		return nil
	}
	if b.Status != "ready" || !a.IsOpenAIIPv6Bound() || a.ProxyID == nil || *a.ProxyID != b.ManagedProxyID {
		return errors.New("IPv6 binding is not ready or the account route is stale")
	}
	p, err := s.proxies.GetByID(ctx, b.ManagedProxyID)
	if err != nil {
		return errors.New("IPv6 proxy lookup failed")
	}
	if err = validateOpenAIIPv6Proxy(a, p); err != nil {
		return err
	}
	if p.URL() != route {
		return errors.New("IPv6 route mismatch; fallback is disabled")
	}
	return nil
}
