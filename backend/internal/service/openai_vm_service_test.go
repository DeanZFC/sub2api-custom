package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

const vmTestSecret = "bridge-secret-at-least-32-characters"

type vmTestEncryptor struct{}

func (vmTestEncryptor) Encrypt(s string) (string, error) { return "encrypted:" + s, nil }
func (vmTestEncryptor) Decrypt(s string) (string, error) {
	return strings.TrimPrefix(s, "encrypted:"), nil
}

type vmTestAccounts struct {
	AccountRepository
	a *Account
}

func (r *vmTestAccounts) GetByID(context.Context, int64) (*Account, error) { c := *r.a; return &c, nil }

type vmTestProxies struct {
	ProxyRepository
	p   *Proxy
	err error
}

func (r *vmTestProxies) GetByID(context.Context, int64) (*Proxy, error) { return r.p, r.err }

type vmTestRepo struct {
	mu         sync.Mutex
	c          OpenAIVMConfig
	b          *OpenAIVMBinding
	account    *Account
	err        error
	reserves   int
	restores   int
	beforeLock func()
}

func (r *vmTestRepo) GetConfig(context.Context) (*OpenAIVMConfig, error)    { c := r.c; return &c, nil }
func (r *vmTestRepo) SaveConfig(_ context.Context, c *OpenAIVMConfig) error { r.c = *c; return nil }
func (r *vmTestRepo) GetBinding(context.Context, int64) (*OpenAIVMBinding, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.b == nil {
		return nil, nil
	}
	b := *r.b
	return &b, nil
}
func (r *vmTestRepo) CountBindings(context.Context) (int, error) {
	if r.b != nil {
		return 1, nil
	}
	return 0, nil
}
func (r *vmTestRepo) WithLock(_ context.Context, _ int64, fn func() error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.beforeLock != nil {
		r.beforeLock()
		r.beforeLock = nil
	}
	return fn()
}
func (r *vmTestRepo) Reserve(_ context.Context, id int64, runtime string, p *Proxy) (*OpenAIVMBinding, error) {
	r.reserves++
	p.ID = 900
	r.b = &OpenAIVMBinding{AccountID: id, ManagedProxyID: p.ID, OriginalProxyID: r.account.ProxyID, RuntimeType: runtime, Status: "provisioning", CreatedAt: time.Now()}
	r.account.ProxyID = &p.ID
	r.account.Proxy = p
	r.account.Extra = map[string]any{"openai_vm_bound": true}
	b := *r.b
	return &b, nil
}
func (r *vmTestRepo) SetState(_ context.Context, _ int64, state, slot, message string) error {
	r.b.Status = state
	if slot != "" {
		r.b.SlotID = slot
	}
	r.b.LastError = message
	return nil
}
func (r *vmTestRepo) Restore(context.Context, int64) error {
	r.restores++
	r.account.ProxyID = r.b.OriginalProxyID
	r.account.Extra = nil
	r.b = nil
	return nil
}
func vmTestService(t *testing.T, h http.HandlerFunc) (*OpenAIVMService, *vmTestRepo, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	a := &Account{ID: 7, Name: "test", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "account-at", "refresh_token": "never-export-refresh", "chatgpt_account_id": "chatgpt-owner"}}
	r := &vmTestRepo{account: a, c: OpenAIVMConfig{Enabled: true, BaseURL: server.URL, ProxyURL: server.URL, EncryptedSecret: "encrypted:" + vmTestSecret, SecretConfigured: true}}
	s := NewOpenAIVMService(r, &vmTestAccounts{a: a}, &vmTestProxies{}, vmTestEncryptor{})
	return s, r, server
}

func TestOpenAIVMLifecycleRetainsHostCredentialAndRestores(t *testing.T) {
	var posts, deletes int
	s, r, _ := vmTestService(t, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "Bearer "+vmTestSecret, req.Header.Get("Authorization"))
		require.Equal(t, "/v1/accounts/7", req.URL.Path)
		if req.Method == http.MethodDelete {
			deletes++
			w.WriteHeader(204)
			return
		}
		posts++
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "never-export-refresh")
		require.NotContains(t, string(body), "refresh_token")
		require.Contains(t, string(body), "account-at")
		_, _ = io.WriteString(w, `{"vm_id":"slot-7","ready":true}`)
	})
	b, err := s.Create(context.Background(), 7, "docker")
	require.NoError(t, err)
	require.Equal(t, "ready", b.Status)
	require.True(t, r.account.IsOpenAIVMBound())
	require.NotNil(t, r.account.ProxyID)
	_, err = s.Create(context.Background(), 7, "docker")
	require.NoError(t, err)
	require.Equal(t, 1, r.reserves)
	require.Equal(t, 2, posts)
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "managed_proxy")
	require.NotContains(t, string(raw), vmTestSecret)
	require.NoError(t, s.Delete(context.Background(), 7))
	require.Nil(t, r.b)
	require.Nil(t, r.account.ProxyID)
	require.False(t, r.account.IsOpenAIVMBound())
	require.Equal(t, 1, r.restores)
	require.NoError(t, s.Delete(context.Background(), 7))
	require.Equal(t, 1, deletes)
	require.Equal(t, "never-export-refresh", r.account.GetCredential("refresh_token"))
}

func TestOpenAIVMFailedCreateNeverFallsBackAndCanRetry(t *testing.T) {
	fail := true
	s, r, _ := vmTestService(t, func(w http.ResponseWriter, req *http.Request) {
		if fail {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, vmTestSecret+" account-at")
			return
		}
		_, _ = io.WriteString(w, `{"vm_id":"slot-7","ready":true}`)
	})
	_, err := s.Create(context.Background(), 7, "docker")
	require.Error(t, err)
	require.Equal(t, "error", r.b.Status)
	require.NotContains(t, r.b.LastError, vmTestSecret)
	req := httptest.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{}`))
	req.RequestURI = ""
	_, handled, err := s.RoundTrip(req, r.account)
	require.True(t, handled)
	require.Error(t, err)
	fail = false
	b, err := s.Retry(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, "ready", b.Status)
	require.Equal(t, 1, r.reserves)
}

func TestOpenAIVMDeletionBusyRetainsBindingUntilRetry(t *testing.T) {
	busy := true
	s, r, _ := vmTestService(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete {
			if busy {
				w.WriteHeader(409)
			} else {
				w.WriteHeader(204)
			}
			return
		}
		_, _ = io.WriteString(w, `{"vm_id":"slot-7","ready":true}`)
	})
	_, err := s.Create(context.Background(), 7, "docker")
	require.NoError(t, err)
	require.Error(t, s.Delete(context.Background(), 7))
	require.Equal(t, "delete_failed", r.b.Status)
	require.Equal(t, 0, r.restores)
	require.True(t, r.account.IsOpenAIVMBound())
	busy = false
	b, err := s.Retry(context.Background(), 7)
	require.NoError(t, err)
	require.Nil(t, b)
	require.Nil(t, r.b)
}

func TestOpenAIVMStaleRetryCannotDeleteNewBinding(t *testing.T) {
	calls := 0
	s, r, _ := vmTestService(t, func(w http.ResponseWriter, req *http.Request) { calls++; w.WriteHeader(204) })
	r.b = &OpenAIVMBinding{AccountID: 7, Status: "delete_failed", CreatedAt: time.Now()}
	r.beforeLock = func() { r.b = &OpenAIVMBinding{AccountID: 7, Status: "ready", CreatedAt: time.Now().Add(time.Second)} }
	_, err := s.Retry(context.Background(), 7)
	require.ErrorContains(t, err, "changed")
	require.Equal(t, 0, calls)
	require.Equal(t, "ready", r.b.Status)
}

func TestOpenAIVMConfigurationSecretAndBindingGuard(t *testing.T) {
	s, r, _ := vmTestService(t, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "/v1/health", req.URL.Path)
		require.Equal(t, "Bearer "+vmTestSecret, req.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	require.NoError(t, s.Test(context.Background()))
	c, err := s.SaveConfig(context.Background(), OpenAIVMConfigInput{Enabled: true, BaseURL: r.c.BaseURL, ProxyURL: r.c.ProxyURL})
	require.NoError(t, err)
	require.Equal(t, r.c.EncryptedSecret, c.EncryptedSecret)
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	require.NotContains(t, string(raw), vmTestSecret)
	require.Contains(t, string(raw), `"secret_configured":true`)
	r.b = &OpenAIVMBinding{AccountID: 7}
	_, err = s.SaveConfig(context.Background(), OpenAIVMConfigInput{Enabled: false, BaseURL: r.c.BaseURL, ProxyURL: r.c.ProxyURL})
	require.ErrorContains(t, err, "Detach")
	require.True(t, r.c.Enabled)
	_, err = s.SaveConfig(context.Background(), OpenAIVMConfigInput{Enabled: true, BaseURL: r.c.BaseURL, ProxyURL: r.c.ProxyURL, Secret: strings.Repeat("x", 32)})
	require.ErrorContains(t, err, "Detach")
	_, err = s.SaveConfig(context.Background(), OpenAIVMConfigInput{Enabled: true, BaseURL: r.c.BaseURL, ProxyURL: r.c.ProxyURL, Secret: vmTestSecret})
	require.NoError(t, err)
}

func TestOpenAIVMRoutingPreservesSSEAndCancellation(t *testing.T) {
	canceled := make(chan struct{})
	s, r, _ := vmTestService(t, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "/v1/accounts/7/responses", req.URL.Path)
		require.Equal(t, "Bearer "+vmTestSecret, req.Header.Get("Authorization"))
		require.Equal(t, "fresh-at", req.Header.Get("X-VM2API-Access-Token"))
		require.Equal(t, "identity-7", req.Header.Get("X-VM2API-ChatGPT-Account-ID"))
		require.Empty(t, req.Header.Get("Cookie"))
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, `{"model":"test","stream":true}`, string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		flusher.Flush()
		<-req.Context().Done()
		close(canceled)
	})
	r.b = &OpenAIVMBinding{AccountID: 7, Status: "ready"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"test","stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer fresh-at")
	req.Header.Set("Chatgpt-Account-Id", "identity-7")
	req.Header.Set("Cookie", "should-not-cross")
	parent := int64(7)
	shadow := &Account{ID: 8, ParentAccountID: &parent, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	resp, handled, err := s.RoundTrip(req, shadow)
	require.NoError(t, err)
	require.True(t, handled)
	defer func() { _ = resp.Body.Close() }()
	first := make([]byte, len("data: first\n\n"))
	_, err = io.ReadFull(resp.Body, first)
	require.NoError(t, err)
	require.Equal(t, "data: first\n\n", string(first))
	require.Equal(t, "Bearer fresh-at", req.Header.Get("Authorization"))
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("bridge stream did not cancel")
	}
}

func TestOpenAIVMUnboundAndDatabaseFailure(t *testing.T) {
	s, r, _ := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network call") })
	req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	_, handled, err := s.RoundTrip(req, r.account)
	require.NoError(t, err)
	require.False(t, handled)
	r.err = errors.New("database unavailable")
	_, handled, err = s.RoundTrip(req, r.account)
	require.True(t, handled)
	require.Error(t, err)
	apikey := &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	_, handled, err = s.RoundTrip(req, apikey)
	require.NoError(t, err)
	require.False(t, handled)
	r.err = nil
	r.account.Extra = map[string]any{"openai_vm_bound": true}
	_, handled, err = s.RoundTrip(req, r.account)
	require.True(t, handled)
	require.Error(t, err)
}

func TestOpenAIVMManagedProxyLookupFailsClosed(t *testing.T) {
	id := int64(90)
	a := &Account{ID: 7, ProxyID: &id, Extra: map[string]any{"openai_vm_bound": true}}
	_, err := resolveOpenAIAccountProxyURL(context.Background(), &vmTestProxies{err: errors.New("not found")}, a)
	require.ErrorContains(t, err, "direct fallback")
	p, err := openAIVMProxy(&OpenAIVMConfig{ProxyURL: "http://127.0.0.1:8080"}, vmTestSecret, 7)
	require.NoError(t, err)
	p.ID = id
	got, err := resolveOpenAIAccountProxyURL(context.Background(), &vmTestProxies{p: p}, a)
	require.NoError(t, err)
	require.Equal(t, p.URL(), got)
	p.Status = "expired"
	_, err = resolveOpenAIAccountProxyURL(context.Background(), &vmTestProxies{p: p}, a)
	require.Error(t, err)
}

func TestOpenAIVMPrivacyManagedProxyFailureNeverCreatesClient(t *testing.T) {
	for _, operation := range []string{"ensure", "force"} {
		for _, failure := range []string{"lookup_error", "missing_proxy", "inactive_proxy", "missing_proxy_id"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				proxyID := int64(90)
				proxy, err := openAIVMProxy(&OpenAIVMConfig{ProxyURL: "http://127.0.0.1:8097"}, vmTestSecret, 7)
				require.NoError(t, err)
				proxy.ID = proxyID
				account := &Account{
					ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
					ProxyID: &proxyID, Proxy: proxy,
					Credentials: map[string]any{"access_token": "account-at"},
					Extra:       map[string]any{"openai_vm_bound": true},
				}
				proxies := &vmTestProxies{p: proxy}
				switch failure {
				case "lookup_error":
					// A stale cached proxy must not hide an authoritative lookup failure.
					proxies.err = errors.New("database unavailable")
				case "missing_proxy":
					proxies.p = nil
				case "inactive_proxy":
					proxy.Status = "disabled"
				case "missing_proxy_id":
					account.ProxyID = nil
				}
				service := &adminServiceImpl{
					proxyRepo: proxies,
					privacyClientFactory: func(string) (*req.Client, error) {
						t.Fatal("privacy request attempted despite unavailable managed route")
						return nil, errors.New("unexpected privacy client creation")
					},
				}
				if operation == "ensure" {
					require.Empty(t, service.EnsureOpenAIPrivacy(context.Background(), account))
				} else {
					require.Empty(t, service.ForceOpenAIPrivacy(context.Background(), account))
				}
			})
		}
	}
}

func TestOpenAIVMRejectsUnsupportedAccountAndProxy(t *testing.T) {
	s, r, _ := vmTestService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected network call") })
	r.account.Type = AccountTypeAPIKey
	_, err := s.Create(context.Background(), 7, "docker")
	require.Error(t, err)
	require.Zero(t, r.reserves)
	r.account.Type = AccountTypeOAuth
	id := int64(12)
	r.account.ProxyID = &id
	s.proxies = &vmTestProxies{p: &Proxy{ID: id, Protocol: "http", Status: StatusActive}}
	_, err = s.Create(context.Background(), 7, "docker")
	require.ErrorContains(t, err, "SOCKS5")
	require.Zero(t, r.reserves)
	r.account.ProxyIDs = []int64{12, 13}
	_, err = s.Create(context.Background(), 7, "docker")
	require.ErrorContains(t, err, "one SOCKS5")
	require.Zero(t, r.reserves)
}

func TestOpenAIVMEndpointValidation(t *testing.T) {
	for _, raw := range []string{"https://example.com", "http://127.0.0.1:8080", "http://10.0.0.2:8080"} {
		_, err := validateOpenAIVMEndpoint(raw)
		require.NoError(t, err)
	}
	for _, raw := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/path", "https://example.com?secret=x", "file:///etc/passwd"} {
		_, err := validateOpenAIVMEndpoint(raw)
		require.Error(t, err)
	}
}
