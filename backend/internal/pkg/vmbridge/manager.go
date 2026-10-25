package vmbridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	stateProvisioning = "provisioning"
	stateReady        = "ready"
	stateFailed       = "failed"
	stateDeleting     = "deleting"
)

// Error is deliberately safe for a public response: it contains no upstream
// error, credential, URL, response body, or provider implementation detail.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string                           { return e.Message }
func bridgeError(status int, code, message string) error { return &Error{status, code, message} }

type Config struct {
	Secret       string
	StateDir     string
	DrainTimeout time.Duration
	DialTimeout  time.Duration
}
type Status struct {
	AccountID     int64     `json:"account_id"`
	Name          string    `json:"name"`
	State         string    `json:"state"`
	Ready         bool      `json:"ready"`
	RuntimeType   string    `json:"runtime_type"`
	VMID          string    `json:"vm_id,omitempty"`
	KeyID         string    `json:"key_id,omitempty"`
	ActiveLeases  int       `json:"active_leases"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastErrorCode string    `json:"last_error_code,omitempty"`
}

// persistentAccount intentionally cannot contain access or refresh tokens.
type persistentAccount struct {
	AccountID        int64     `json:"account_id"`
	Name             string    `json:"name"`
	RuntimeType      string    `json:"runtime_type"`
	ChatGPTAccountID string    `json:"chatgpt_account_id"`
	UpstreamProxyURL string    `json:"upstream_proxy_url"`
	State            string    `json:"state"`
	Resources        Resources `json:"resources"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	LastErrorCode    string    `json:"last_error_code,omitempty"`
}
type accountEntry struct {
	op     chan struct{}
	data   persistentAccount
	active int
	idle   chan struct{}
}
type Manager struct {
	mu       sync.Mutex
	accounts map[int64]*accountEntry
	provider Provider
	store    *stateStore
	cfg      Config
}

func New(cfg Config, provider Provider) (*Manager, error) {
	if len(cfg.Secret) < 32 || strings.TrimSpace(cfg.Secret) != cfg.Secret || strings.ContainsAny(cfg.Secret, "\r\n") {
		return nil, errors.New("bridge secret must contain at least 32 bytes without surrounding whitespace")
	}
	if provider == nil {
		return nil, errors.New("provider is required")
	}
	if cfg.DrainTimeout <= 0 {
		cfg.DrainTimeout = 30 * time.Second
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 15 * time.Second
	}
	store, err := newStateStore(cfg.StateDir, cfg.Secret)
	if err != nil {
		return nil, err
	}
	states, err := store.load()
	if err != nil {
		return nil, err
	}
	m := &Manager{accounts: make(map[int64]*accountEntry), provider: provider, store: store, cfg: cfg}
	for _, state := range states {
		m.accounts[state.AccountID] = newEntry(state)
	}
	return m, nil
}
func newEntry(data persistentAccount) *accountEntry {
	idle := make(chan struct{})
	close(idle)
	return &accountEntry{op: make(chan struct{}, 1), data: data, idle: idle}
}

// lock observes cancellation even while another request waits for upstream
// headers. Deletion marks the binding draining before waiting for this lock.
func (e *accountEntry) lock(ctx context.Context) error {
	select {
	case e.op <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-e.op
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *accountEntry) unlock() { <-e.op }
func statusOf(e *accountEntry) Status {
	d := e.data
	return Status{AccountID: d.AccountID, Name: d.Name, State: d.State, Ready: d.State == stateReady, RuntimeType: d.RuntimeType, VMID: d.Resources.VMID, KeyID: d.Resources.KeyID, ActiveLeases: e.active, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, LastErrorCode: d.LastErrorCode}
}
func (m *Manager) Status(id int64) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.accounts[id]
	if e == nil {
		return Status{}, bridgeError(404, "account_not_found", "account binding does not exist")
	}
	return statusOf(e), nil
}
func (m *Manager) StatusContext(ctx context.Context, id int64) (Status, error) {
	m.mu.Lock()
	e := m.accounts[id]
	if e == nil {
		m.mu.Unlock()
		return Status{}, bridgeError(404, "account_not_found", "account binding does not exist")
	}
	status, resources := statusOf(e), e.data.Resources
	m.mu.Unlock()
	if status.Ready {
		if checker, ok := m.provider.(ReadinessChecker); ok {
			checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := checker.CheckReady(checkCtx, resources)
			cancel()
			if err != nil {
				status.State = "unavailable"
				status.Ready = false
				status.LastErrorCode = "upstream_not_ready"
			}
		}
	}
	return status, nil
}
func validIdentity(id Identity) bool {
	return strings.TrimSpace(id.AccessToken) != "" && len(id.AccessToken) <= 32768 && strings.TrimSpace(id.ChatGPTAccountID) != "" && len(id.ChatGPTAccountID) <= 256 && !strings.ContainsAny(id.AccessToken+id.ChatGPTAccountID, "\r\n")
}
func (m *Manager) Provision(ctx context.Context, in ProvisionRequest) (Status, bool, error) {
	if in.AccountID <= 0 || strings.TrimSpace(in.Name) == "" || len(in.Name) > 256 || !validIdentity(Identity{in.AccessToken, in.ChatGPTAccountID}) {
		return Status{}, false, bridgeError(400, "invalid_account", "account id, name, access token and ChatGPT account id are required")
	}
	if in.RuntimeType == "" {
		in.RuntimeType = "docker"
	}
	if in.RuntimeType != "docker" && in.RuntimeType != "kvm" {
		return Status{}, false, bridgeError(400, "invalid_runtime", "runtime_type must be docker or kvm")
	}
	proxy, err := parseUpstreamProxy(in.UpstreamProxyURL)
	if err != nil || (proxy != nil && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") {
		return Status{}, false, bridgeError(400, "invalid_proxy", "upstream proxy must use SOCKS5 or SOCKS5H, or be empty for the runtime host exit")
	}
	m.mu.Lock()
	e := m.accounts[in.AccountID]
	created := e == nil
	if e == nil {
		now := time.Now().UTC()
		e = newEntry(persistentAccount{AccountID: in.AccountID, Name: in.Name, RuntimeType: in.RuntimeType, ChatGPTAccountID: in.ChatGPTAccountID, UpstreamProxyURL: in.UpstreamProxyURL, State: stateProvisioning, CreatedAt: now, UpdatedAt: now})
		m.accounts[in.AccountID] = e
	}
	m.mu.Unlock()
	if err := e.lock(ctx); err != nil {
		return Status{}, false, bridgeError(408, "operation_cancelled", "account operation was cancelled")
	}
	defer e.unlock()
	m.mu.Lock()
	if m.accounts[in.AccountID] != e {
		m.mu.Unlock()
		return Status{}, false, bridgeError(409, "binding_changed", "account binding changed; retry")
	}
	if e.data.ChatGPTAccountID != in.ChatGPTAccountID || e.data.UpstreamProxyURL != in.UpstreamProxyURL || e.data.RuntimeType != in.RuntimeType {
		m.mu.Unlock()
		return Status{}, false, bridgeError(409, "binding_conflict", "account is already bound with different identity or routing")
	}
	if e.data.State == stateDeleting {
		m.mu.Unlock()
		return Status{}, false, bridgeError(409, "account_draining", "account binding is being removed")
	}
	ready := e.data.State == stateReady
	resources := e.data.Resources
	if !ready {
		e.data.State = stateProvisioning
		e.data.LastErrorCode = ""
		e.data.UpdatedAt = time.Now().UTC()
		if err := m.store.save(e.data); err != nil {
			if created {
				delete(m.accounts, in.AccountID)
			}
			m.mu.Unlock()
			return Status{}, false, bridgeError(500, "state_persist_failed", "cannot persist account binding")
		}
	}
	m.mu.Unlock()
	if ready {
		if err := m.provider.UpdateIdentity(ctx, resources, Identity{in.AccessToken, in.ChatGPTAccountID}); err != nil {
			return Status{}, false, bridgeError(502, "identity_update_failed", "upstream identity update failed")
		}
		status, err := m.Status(in.AccountID)
		return status, false, err
	}
	newResources, err := m.provider.Provision(ctx, in)
	m.mu.Lock()
	defer m.mu.Unlock()
	// A failed retry may fail before reporting its resources. Retain the last
	// known partial binding so a later delete can still clean it up.
	resources = mergeResources(resources, newResources)
	e.data.Resources = resources
	e.data.UpdatedAt = time.Now().UTC()
	if e.data.State == stateDeleting {
		if m.store.save(e.data) != nil {
			return statusOf(e), created, bridgeError(500, "state_persist_failed", "cannot persist account binding")
		}
		return statusOf(e), created, bridgeError(409, "account_draining", "account binding is being removed")
	}
	if err != nil || resources.VMID == "" || resources.APIKey == "" {
		e.data.State = stateFailed
		e.data.LastErrorCode = "provision_failed"
		if m.store.save(e.data) != nil {
			return statusOf(e), created, bridgeError(500, "state_persist_failed", "cannot persist account binding")
		}
		return statusOf(e), created, bridgeError(502, "provision_failed", "upstream provisioning failed")
	}
	e.data.State = stateReady
	e.data.LastErrorCode = ""
	if m.store.save(e.data) != nil {
		e.data.State = stateFailed
		return statusOf(e), created, bridgeError(500, "state_persist_failed", "cannot persist account binding")
	}
	return statusOf(e), created, nil
}

func mergeResources(old, next Resources) Resources {
	if next.VMID != "" {
		old.VMID = next.VMID
	}
	if next.KeyID != "" {
		old.KeyID = next.KeyID
	}
	if next.APIKey != "" {
		old.APIKey = next.APIKey
	}
	if next.ProxyID != "" {
		old.ProxyID = next.ProxyID
	}
	return old
}

func (m *Manager) entry(id int64) (*accountEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.accounts[id]
	if e == nil {
		return nil, bridgeError(404, "account_not_found", "account binding does not exist")
	}
	return e, nil
}
func (m *Manager) UpdateIdentity(ctx context.Context, id int64, identity Identity) (Status, error) {
	if !validIdentity(identity) {
		return Status{}, bridgeError(400, "invalid_identity", "access token and ChatGPT account id are required")
	}
	e, err := m.entry(id)
	if err != nil {
		return Status{}, err
	}
	if err := e.lock(ctx); err != nil {
		return Status{}, bridgeError(408, "operation_cancelled", "account operation was cancelled")
	}
	defer e.unlock()
	m.mu.Lock()
	if m.accounts[id] != e || e.data.State != stateReady {
		m.mu.Unlock()
		return Status{}, bridgeError(409, "account_not_ready", "account is not ready")
	}
	if identity.ChatGPTAccountID != e.data.ChatGPTAccountID {
		m.mu.Unlock()
		return Status{}, bridgeError(409, "identity_mismatch", "ChatGPT account id does not match binding")
	}
	resources := e.data.Resources
	m.mu.Unlock()
	if err = m.provider.UpdateIdentity(ctx, resources, identity); err != nil {
		return Status{}, bridgeError(502, "identity_update_failed", "upstream identity update failed")
	}
	return m.Status(id)
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	drainCtx, cancel := context.WithTimeout(ctx, m.cfg.DrainTimeout)
	defer cancel()
	e, err := m.entry(id)
	if err != nil {
		var be *Error
		if errors.As(err, &be) && be.Status == 404 {
			return nil
		}
		return err
	}
	m.mu.Lock()
	if m.accounts[id] != e {
		m.mu.Unlock()
		return nil
	}
	previous := e.data.State
	e.data.State = stateDeleting
	e.data.UpdatedAt = time.Now().UTC()
	if m.store.save(e.data) != nil {
		e.data.State = previous
		m.mu.Unlock()
		return bridgeError(500, "state_persist_failed", "cannot persist account binding")
	}
	m.mu.Unlock()
	// Fail closed immediately. A timed-out drain retains the deleting state,
	// resources and credentials, and a later DELETE resumes the cleanup.
	if err := e.lock(drainCtx); err != nil {
		return bridgeError(409, "account_busy", "account has active requests; deletion remains pending")
	}
	defer e.unlock()
	m.mu.Lock()
	if m.accounts[id] != e {
		m.mu.Unlock()
		return nil
	}
	idle := e.idle
	resources := e.data.Resources
	m.mu.Unlock()
	select {
	case <-idle:
	case <-drainCtx.Done():
		return bridgeError(409, "account_busy", "account has active requests; deletion remains pending")
	}
	if err = m.provider.Delete(ctx, resources); err != nil {
		m.mu.Lock()
		e.data.State = stateDeleting
		e.data.LastErrorCode = "delete_failed"
		_ = m.store.save(e.data)
		m.mu.Unlock()
		return bridgeError(502, "delete_failed", "upstream cleanup failed; retry account deletion")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store.remove(id) != nil {
		return bridgeError(500, "state_persist_failed", "cannot remove account binding")
	}
	delete(m.accounts, id)
	return nil
}

func (m *Manager) acquire(id int64) (*accountEntry, Resources, string, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.accounts[id]
	if e == nil || e.data.State != stateReady {
		return nil, Resources{}, "", nil, bridgeError(409, "account_not_ready", "account is not ready")
	}
	if e.active == 0 {
		e.idle = make(chan struct{})
	}
	e.active++
	var once sync.Once
	release := func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			e.active--
			if e.active == 0 {
				close(e.idle)
			}
		})
	}
	return e, e.data.Resources, e.data.UpstreamProxyURL, release, nil
}

// Forward serializes credential synchronization and upstream header acquisition
// for one account. The lease remains held until the streamed body is closed.
func (m *Manager) Forward(ctx context.Context, id int64, request *http.Request, identity *Identity) (*http.Response, error) {
	e, err := m.entry(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	ready := m.accounts[id] == e && e.data.State == stateReady
	m.mu.Unlock()
	if !ready {
		return nil, bridgeError(409, "account_not_ready", "account is not ready")
	}
	if err := e.lock(ctx); err != nil {
		return nil, bridgeError(408, "operation_cancelled", "account operation was cancelled")
	}
	defer e.unlock()
	current, resources, _, release, err := m.acquire(id)
	if err != nil {
		return nil, err
	}
	if current != e {
		release()
		return nil, bridgeError(409, "binding_changed", "account binding changed; retry")
	}
	success := false
	defer func() {
		if !success {
			release()
		}
	}()
	if identity != nil {
		if !validIdentity(*identity) {
			return nil, bridgeError(400, "invalid_identity", "invalid access token identity")
		}
		m.mu.Lock()
		matches := identity.ChatGPTAccountID == e.data.ChatGPTAccountID
		m.mu.Unlock()
		if !matches {
			return nil, bridgeError(409, "identity_mismatch", "ChatGPT account id does not match binding")
		}
		if err = m.provider.UpdateIdentity(ctx, resources, *identity); err != nil {
			return nil, bridgeError(502, "identity_update_failed", "upstream identity update failed")
		}
	}
	response, err := m.provider.Forward(ctx, resources, request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, bridgeError(502, "upstream_unavailable", "account upstream is unavailable")
	}
	if response == nil || response.Body == nil {
		return nil, bridgeError(502, "invalid_upstream_response", "upstream returned an invalid response")
	}
	if response.StatusCode == http.StatusSwitchingProtocols {
		duplex, ok := response.Body.(io.ReadWriteCloser)
		if !ok {
			_ = response.Body.Close()
			return nil, bridgeError(502, "invalid_upstream_upgrade", "upstream returned an invalid upgrade")
		}
		response.Body = &leaseDuplexBody{ReadWriteCloser: duplex, release: release}
	} else {
		response.Body = &leaseBody{ReadCloser: response.Body, release: release}
	}
	success = true
	return response, nil
}

type leaseBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *leaseBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }

type leaseDuplexBody struct {
	io.ReadWriteCloser
	release func()
	once    sync.Once
}

func (b *leaseDuplexBody) Close() error {
	err := b.ReadWriteCloser.Close()
	b.once.Do(b.release)
	return err
}

func ProxyPassword(secret string, accountID int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("account:" + strconv.FormatInt(accountID, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
