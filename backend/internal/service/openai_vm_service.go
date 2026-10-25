package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// OpenAIVMService is an optional adapter. The external bridge owns VM resources;
// Sub2API retains OAuth refresh, scheduling, accounting and stream parsing.
type OpenAIVMService struct {
	repo        OpenAIVMRepository
	accounts    AccountRepository
	proxies     ProxyRepository
	encryptor   SecretEncryptor
	accessToken func(context.Context, *Account) (string, error)
	client      *http.Client
	scheduler   SchedulerCache
}

func NewOpenAIVMService(repo OpenAIVMRepository, accounts AccountRepository, proxies ProxyRepository, encryptor SecretEncryptor) *OpenAIVMService {
	return &OpenAIVMService{repo: repo, accounts: accounts, proxies: proxies, encryptor: encryptor, client: &http.Client{
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 95 * time.Second, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (s *OpenAIVMService) GetConfig(ctx context.Context) (*OpenAIVMConfig, error) {
	return s.repo.GetConfig(ctx)
}

func validateOpenAIVMEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", infraerrors.BadRequest("OPENAI_VM_INVALID_URL", "Use an HTTP(S) bridge origin without credentials, path or query")
	}
	// A shared credential must not cross an untrusted network in cleartext.
	ip := net.ParseIP(u.Hostname())
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", infraerrors.BadRequest("OPENAI_VM_INVALID_URL", "Bridge port must be between 1 and 65535")
		}
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" && (ip == nil || (!ip.IsLoopback() && !ip.IsPrivate())) {
		return "", infraerrors.BadRequest("OPENAI_VM_TLS_REQUIRED", "Public bridge endpoints require HTTPS; HTTP is limited to loopback/private IPs")
	}
	u.Path = ""
	return u.String(), nil
}

func (s *OpenAIVMService) SaveConfig(ctx context.Context, in OpenAIVMConfigInput) (*OpenAIVMConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var result *OpenAIVMConfig
	err := s.repo.WithLock(ctx, 0, func() error {
		old, err := s.repo.GetConfig(ctx)
		if err != nil {
			return err
		}
		next := *old
		next.Enabled = in.Enabled
		if strings.TrimSpace(in.BaseURL) != "" {
			next.BaseURL, err = validateOpenAIVMEndpoint(in.BaseURL)
			if err != nil {
				return err
			}
		} else {
			next.BaseURL = ""
		}
		if strings.TrimSpace(in.ProxyURL) != "" {
			next.ProxyURL, err = validateOpenAIVMEndpoint(in.ProxyURL)
			if err != nil {
				return err
			}
		} else {
			next.ProxyURL = next.BaseURL
		}
		secret := strings.TrimSpace(in.Secret)
		changedSecret := false
		if secret != "" {
			if len(secret) < 32 || strings.ContainsAny(secret, "\r\n") {
				return infraerrors.BadRequest("OPENAI_VM_SECRET_WEAK", "Bridge secret must contain at least 32 characters")
			}
			prior, decErr := s.decrypt(old)
			if decErr != nil && old.EncryptedSecret != "" {
				return decErr
			}
			changedSecret = secret != prior
			if changedSecret {
				if s.encryptor == nil {
					return errors.New("bridge encryption unavailable")
				}
				next.EncryptedSecret, err = s.encryptor.Encrypt(secret)
				if err != nil {
					return err
				}
			}
		}
		if next.Enabled && (next.BaseURL == "" || next.ProxyURL == "" || next.EncryptedSecret == "") {
			return infraerrors.BadRequest("OPENAI_VM_CONFIG_REQUIRED", "Bridge URL, proxy URL and secret are required")
		}
		changed := old.Enabled != next.Enabled || old.BaseURL != next.BaseURL || old.ProxyURL != next.ProxyURL || changedSecret
		if changed {
			n, err := s.repo.CountBindings(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				return infraerrors.Conflict("OPENAI_VM_BINDINGS_EXIST", "Detach every VM slot before changing or disabling bridge configuration")
			}
		}
		next.SecretConfigured = next.EncryptedSecret != ""
		if err = s.repo.SaveConfig(ctx, &next); err != nil {
			return err
		}
		result = &next
		return nil
	})
	return result, err
}
func (s *OpenAIVMService) decrypt(c *OpenAIVMConfig) (string, error) {
	if c.EncryptedSecret == "" {
		return "", nil
	}
	if s.encryptor == nil {
		return "", errors.New("bridge encryption unavailable")
	}
	return s.encryptor.Decrypt(c.EncryptedSecret)
}
func (s *OpenAIVMService) configured(ctx context.Context) (*OpenAIVMConfig, string, error) {
	c, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, "", err
	}
	if !c.Enabled || c.BaseURL == "" || c.ProxyURL == "" || c.EncryptedSecret == "" {
		return nil, "", infraerrors.Conflict("OPENAI_VM_NOT_CONFIGURED", "OpenAI VM bridge is not configured and enabled")
	}
	key, err := s.decrypt(c)
	return c, key, err
}

func (s *OpenAIVMService) bridgeJSON(ctx context.Context, c *OpenAIVMConfig, key, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return infraerrors.New(http.StatusBadGateway, "OPENAI_VM_BRIDGE_UNREACHABLE", "VM bridge request failed; the account remains blocked until retry or detach")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if method == http.MethodDelete && resp.StatusCode == http.StatusNotFound {
			return nil
		}
		if resp.StatusCode == http.StatusConflict {
			return infraerrors.Conflict("OPENAI_VM_BRIDGE_BUSY", "VM bridge is draining active requests; retry cleanup after they finish")
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return infraerrors.BadRequest("OPENAI_VM_BRIDGE_AUTH", "VM bridge rejected its shared secret")
		}
		return infraerrors.Newf(http.StatusBadGateway, "OPENAI_VM_BRIDGE_FAILED", "VM bridge returned HTTP %d; inspect the bridge status and retry", resp.StatusCode)
	}
	if out != nil {
		if err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(out); err != nil {
			return infraerrors.New(502, "OPENAI_VM_BRIDGE_PROTOCOL", "VM bridge returned an invalid response")
		}
	}
	return nil
}
func (s *OpenAIVMService) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, key, err := s.configured(ctx)
	if err != nil {
		return err
	}
	var health struct {
		Status string `json:"status"`
	}
	if err = s.bridgeJSON(ctx, c, key, http.MethodGet, "/v1/health", nil, &health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return infraerrors.New(502, "OPENAI_VM_BRIDGE_PROTOCOL", "VM bridge health check failed")
	}
	return nil
}

type OpenAIVMAccountStatus struct {
	Binding    *OpenAIVMBinding `json:"binding"`
	Eligible   bool             `json:"eligible"`
	Configured bool             `json:"configured"`
}

func openAIVMEligible(a *Account) bool {
	return a != nil && a.IsOpenAIOAuth() && !a.IsShadow() && !a.IsOpenAIAgentIdentity()
}
func (s *OpenAIVMService) Status(ctx context.Context, id int64) (*OpenAIVMAccountStatus, error) {
	a, err := s.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	b, err := s.repo.GetBinding(ctx, openAIVMOwnerID(a))
	if err != nil {
		return nil, err
	}
	c, key, cerr := s.configured(ctx)
	result := &OpenAIVMAccountStatus{Binding: b, Eligible: openAIVMEligible(a), Configured: cerr == nil}
	if b != nil && b.Status == "ready" {
		var remote struct {
			Ready bool   `json:"ready"`
			State string `json:"state"`
		}
		if cerr != nil {
			b.Status = "error"
			b.LastError = "Bridge configuration unavailable"
		} else if err = s.bridgeJSON(ctx, c, key, http.MethodGet, fmt.Sprintf("/v1/accounts/%d", b.AccountID), nil, &remote); err != nil {
			b.Status = "error"
			b.LastError = err.Error()
		} else if !remote.Ready {
			b.Status = "error"
			b.LastError = "Bridge slot is not ready; retry or detach"
		}
	}
	return result, nil
}

func openAIVMProxy(c *OpenAIVMConfig, key string, id int64) (*Proxy, error) {
	u, err := url.Parse(c.ProxyURL)
	if err != nil {
		return nil, err
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid bridge proxy port")
		}
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = fmt.Fprintf(mac, "account:%d", id)
	return &Proxy{Protocol: u.Scheme, Host: u.Hostname(), Port: port, Username: strconv.FormatInt(id, 10), Password: hex.EncodeToString(mac.Sum(nil)), Status: StatusActive, FallbackMode: FallbackModeNone}, nil
}
func (s *OpenAIVMService) Create(ctx context.Context, id int64, runtime string) (*OpenAIVMBinding, error) {
	return s.create(ctx, id, runtime, nil)
}

func (s *OpenAIVMService) create(ctx context.Context, id int64, runtime string, expected *time.Time) (*OpenAIVMBinding, error) {
	// Leave time for cancellation cleanup before the repository lock expires.
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	if runtime == "" {
		runtime = "docker"
	}
	if runtime != "docker" && runtime != "kvm" {
		return nil, infraerrors.BadRequest("OPENAI_VM_RUNTIME", "Runtime must be docker or kvm")
	}
	var result *OpenAIVMBinding
	err := s.repo.WithLock(ctx, id, func() error {
		a, err := s.accounts.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if !openAIVMEligible(a) {
			return infraerrors.BadRequest("OPENAI_VM_INELIGIBLE", "Only OpenAI OAuth parent accounts with bearer credentials support VM slots")
		}
		c, key, err := s.configured(ctx)
		if err != nil {
			return err
		}
		b, err := s.repo.GetBinding(ctx, id)
		if err != nil {
			return err
		}
		if expected != nil && (b == nil || !b.CreatedAt.Equal(*expected)) {
			return infraerrors.Conflict("OPENAI_VM_BINDING_CHANGED", "VM binding changed; refresh before retrying")
		}
		if b != nil && (b.Status == "deleting" || b.Status == "delete_failed") {
			return infraerrors.Conflict("OPENAI_VM_DELETING", "Retry cleanup before creating another slot")
		}
		token := a.GetOpenAIAccessToken()
		// A failed provision still needs its existing AT to bootstrap the proxy
		// before the host can refresh. Refresh tokens never leave Sub2API.
		if b == nil && s.accessToken != nil {
			token, err = s.accessToken(ctx, a)
			if err != nil {
				return err
			}
			a, err = s.accounts.GetByID(ctx, id)
			if err != nil {
				return err
			}
		}
		if token == "" {
			return infraerrors.BadRequest("OPENAI_VM_TOKEN_REQUIRED", "Account access token is missing")
		}
		if strings.TrimSpace(a.GetCredential("chatgpt_account_id")) == "" {
			return infraerrors.BadRequest("OPENAI_VM_IDENTITY_REQUIRED", "Refresh or reauthorize this account to obtain its ChatGPT account ID before creating a VM slot")
		}
		if b == nil {
			if len(a.ProxyIDs) > 1 {
				return infraerrors.BadRequest("OPENAI_VM_PROXY_POOL", "Select one SOCKS5 proxy before creating a dedicated slot")
			}
			if a.ProxyID != nil {
				p, err := s.proxies.GetByID(ctx, *a.ProxyID)
				if err != nil {
					return err
				}
				if p == nil || !p.IsActive() || p.IsExpired(time.Now()) {
					return infraerrors.BadRequest("OPENAI_VM_PROXY_UNAVAILABLE", "Original outbound proxy is unavailable")
				}
				if p.Protocol != "socks5" && p.Protocol != "socks5h" {
					return infraerrors.BadRequest("OPENAI_VM_PROXY_PROTOCOL", "vm2api slots require a single SOCKS5 proxy or an explicitly direct account")
				}
			}
		}
		if b == nil {
			p, err := openAIVMProxy(c, key, id)
			if err != nil {
				return err
			}
			b, err = s.repo.Reserve(ctx, id, runtime, p)
			if err != nil {
				return err
			}
		}
		s.refreshSnapshots(ctx, id)
		result = b
		fail := func(cause error) error {
			cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			stateErr := s.repo.SetState(cleanCtx, id, "error", "", cause.Error())
			return errors.Join(cause, stateErr)
		}
		if err = s.repo.SetState(ctx, id, "provisioning", "", ""); err != nil {
			return err
		}
		originalProxy := ""
		if b.OriginalProxyID != nil {
			p, err := s.proxies.GetByID(ctx, *b.OriginalProxyID)
			if err != nil || p == nil {
				return fail(errors.New("original outbound proxy is unavailable"))
			}
			if !p.IsActive() || p.IsExpired(time.Now()) {
				return fail(errors.New("original outbound proxy is inactive or expired"))
			}
			originalProxy = p.URL()
		}
		input := struct {
			Name             string `json:"name"`
			AccessToken      string `json:"access_token"`
			ChatGPTAccountID string `json:"chatgpt_account_id"`
			UpstreamProxyURL string `json:"upstream_proxy_url"`
			RuntimeType      string `json:"runtime_type"`
		}{a.Name, token, a.GetCredential("chatgpt_account_id"), originalProxy, b.RuntimeType}
		var remote struct {
			VMID  string `json:"vm_id"`
			Ready bool   `json:"ready"`
		}
		if err = s.bridgeJSON(ctx, c, key, http.MethodPost, fmt.Sprintf("/v1/accounts/%d", id), input, &remote); err != nil {
			return fail(err)
		}
		if !remote.Ready || remote.VMID == "" {
			return fail(errors.New("VM bridge did not confirm a ready dedicated slot"))
		}
		if err = s.repo.SetState(ctx, id, "ready", remote.VMID, ""); err != nil {
			return err
		}
		result, err = s.repo.GetBinding(ctx, id)
		return err
	})
	return result, err
}

func (s *OpenAIVMService) Delete(ctx context.Context, id int64) error {
	return s.delete(ctx, id, nil)
}

func (s *OpenAIVMService) delete(ctx context.Context, id int64, expected *time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	return s.repo.WithLock(ctx, id, func() error {
		b, err := s.repo.GetBinding(ctx, id)
		if err == nil && expected != nil && (b == nil || !b.CreatedAt.Equal(*expected)) {
			return infraerrors.Conflict("OPENAI_VM_BINDING_CHANGED", "VM binding changed; refresh before retrying")
		}
		if err != nil || b == nil {
			return err
		}
		c, key, err := s.configured(ctx)
		if err != nil {
			return err
		}
		if b.OriginalProxyID != nil {
			p, err := s.proxies.GetByID(ctx, *b.OriginalProxyID)
			if err != nil || p == nil || !p.IsActive() || p.IsExpired(time.Now()) {
				return infraerrors.Conflict("OPENAI_VM_ORIGINAL_PROXY_UNAVAILABLE", "Restore the original proxy before detaching this VM slot")
			}
		}
		if err = s.repo.SetState(ctx, id, "deleting", "", ""); err != nil {
			return err
		}
		err = s.bridgeJSON(ctx, c, key, http.MethodDelete, fmt.Sprintf("/v1/accounts/%d", id), nil, nil)
		if err == nil {
			err = s.repo.Restore(ctx, id)
			if err == nil {
				s.refreshSnapshots(ctx, id)
			}
		}
		if err != nil {
			cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return errors.Join(err, s.repo.SetState(cleanCtx, id, "delete_failed", "", err.Error()))
		}
		return nil
	})
}
func (s *OpenAIVMService) Retry(ctx context.Context, id int64) (*OpenAIVMBinding, error) {
	b, err := s.repo.GetBinding(ctx, id)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, infraerrors.NotFound("OPENAI_VM_NOT_BOUND", "Account has no VM binding")
	}
	if b.Status == "deleting" || b.Status == "delete_failed" {
		return nil, s.delete(ctx, id, &b.CreatedAt)
	}
	return s.create(ctx, id, b.RuntimeType, &b.CreatedAt)
}

// RouteBound reads the authoritative row even when the scheduler has an older
// snapshot. A missing row with a managed marker fails closed during transitions.
func (s *OpenAIVMService) RouteBound(ctx context.Context, a *Account) (bool, error) {
	if a == nil || !a.IsOpenAIOAuth() {
		return false, nil
	}
	b, err := s.repo.GetBinding(ctx, openAIVMOwnerID(a))
	if err != nil {
		return true, err
	}
	return b != nil || a.IsOpenAIVMBound(), nil
}
func (s *OpenAIVMService) RoundTrip(req *http.Request, a *Account) (*http.Response, bool, error) {
	if a == nil || !a.IsOpenAIOAuth() {
		return nil, false, nil
	}
	b, err := s.repo.GetBinding(req.Context(), openAIVMOwnerID(a))
	if err != nil {
		return nil, true, err
	}
	if b == nil {
		if a.IsOpenAIVMBound() {
			return nil, true, errors.New("VM binding is transitioning; retry with a fresh account snapshot")
		}
		return nil, false, nil
	}
	if b.Status != "ready" {
		return nil, true, errors.New("OpenAI VM slot is not ready; direct fallback is disabled")
	}
	c, key, err := s.configured(req.Context())
	if err != nil {
		return nil, true, err
	}
	forward := req.Clone(req.Context())
	forward.Header = req.Header.Clone()
	forward.Host = ""
	if endpoint := openAIVMPublicEndpoint(req.Method, req.URL.Path); endpoint != "" {
		forward.URL, err = url.Parse(fmt.Sprintf("%s/v1/accounts/%d/%s", c.BaseURL, b.AccountID, endpoint))
		if err != nil {
			return nil, true, err
		}
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" || token == req.Header.Get("Authorization") {
			return nil, true, errors.New("OpenAI VM requires a bearer access token")
		}
		forward.Header.Set("X-VM2API-Access-Token", token)
		identity := req.Header.Get("Chatgpt-Account-Id")
		if identity == "" {
			identity = a.GetCredential("chatgpt_account_id")
		}
		forward.Header.Set("X-VM2API-ChatGPT-Account-ID", identity)
		forward.Header.Set("Authorization", "Bearer "+key)
		forward.Header.Del("Cookie")
		forward.Header.Del("Proxy-Authorization")
		resp, err := s.client.Do(forward)
		if err != nil {
			return nil, true, errors.New("OpenAI VM bridge transport failed")
		}
		return resp, true, nil
	}
	p, err := openAIVMProxy(c, key, b.AccountID)
	if err != nil {
		return nil, true, err
	}
	proxy, err := url.Parse(p.URL())
	if err != nil {
		return nil, true, err
	}
	// Auxiliary requests preserve their native protocol through CONNECT. No
	// idle tunnel is retained by this adapter, allowing safe resource drain.
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 60 * time.Second}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(forward)
	if err != nil {
		return nil, true, errors.New("OpenAI VM auxiliary transport failed")
	}
	return resp, true, nil
}

// validateOpenAIVMProxy prevents legacy best-effort proxy lookups from turning a
// managed account into a direct connection when a proxy row cannot be loaded.
func validateOpenAIVMProxy(a *Account, p *Proxy) error {
	if !a.IsOpenAIVMBound() {
		return nil
	}
	if p == nil || a.ProxyID == nil || p.ID != *a.ProxyID || p.Username != strconv.FormatInt(openAIVMOwnerID(a), 10) || p.Password == "" || !p.IsActive() || p.IsExpired(time.Now()) {
		return errors.New("OpenAI VM managed proxy is unavailable; direct fallback is disabled")
	}
	return nil
}

func resolveOpenAIAccountProxyURL(ctx context.Context, repo ProxyRepository, a *Account) (string, error) {
	var p *Proxy
	if a.ProxyID != nil {
		p = a.Proxy
		if repo != nil {
			loaded, err := repo.GetByID(ctx, *a.ProxyID)
			if err == nil {
				p = loaded
			} else if a.IsOpenAIVMBound() {
				return "", errors.New("OpenAI VM proxy lookup failed; direct fallback is disabled")
			}
		}
	}
	if err := validateOpenAIVMProxy(a, p); err != nil {
		return "", err
	}
	if p != nil {
		return p.URL(), nil
	}
	return "", nil
}

func (s *OpenAIVMService) refreshSnapshots(ctx context.Context, id int64) {
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
