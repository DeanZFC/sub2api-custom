package vmbridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testSecret = "test-bridge-secret-32-bytes-minimum-for-tests"

type testProvider struct {
	check     func(context.Context, Resources) error
	provision func(context.Context, ProvisionRequest) (Resources, error)
	identity  func(context.Context, Resources, Identity) error
	delete    func(context.Context, Resources) error
	forward   func(context.Context, Resources, *http.Request) (*http.Response, error)
}

func (p *testProvider) CheckReady(ctx context.Context, r Resources) error {
	if p.check != nil {
		return p.check(ctx, r)
	}
	return nil
}

func (p *testProvider) Provision(ctx context.Context, in ProvisionRequest) (Resources, error) {
	if p.provision != nil {
		return p.provision(ctx, in)
	}
	return Resources{VMID: fmt.Sprintf("vm-%d", in.AccountID), KeyID: fmt.Sprintf("key-%d", in.AccountID), APIKey: fmt.Sprintf("private-key-%d", in.AccountID)}, nil
}
func (p *testProvider) UpdateIdentity(ctx context.Context, r Resources, i Identity) error {
	if p.identity != nil {
		return p.identity(ctx, r, i)
	}
	return nil
}
func (p *testProvider) Delete(ctx context.Context, r Resources) error {
	if p.delete != nil {
		return p.delete(ctx, r)
	}
	return nil
}
func (p *testProvider) Forward(ctx context.Context, r Resources, req *http.Request) (*http.Response, error) {
	if p.forward != nil {
		return p.forward(ctx, r, req)
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
func newTestManager(t *testing.T, p Provider) *Manager {
	t.Helper()
	m, err := New(Config{Secret: testSecret, StateDir: t.TempDir(), DrainTimeout: 30 * time.Millisecond, DialTimeout: time.Second}, p)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func testInput(id int64) ProvisionRequest {
	return ProvisionRequest{AccountID: id, Name: fmt.Sprintf("account %d", id), AccessToken: "access-token-never-persist", ChatGPTAccountID: fmt.Sprintf("chatgpt-%d", id), RuntimeType: "docker"}
}
func provisionAccount(t *testing.T, m *Manager, id int64) {
	t.Helper()
	status, _, err := m.Provision(context.Background(), testInput(id))
	if err != nil || !status.Ready {
		t.Fatalf("provision: %+v, %v", status, err)
	}
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want code %s, got %v", code, err)
	}
}
func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func TestProvisionReusesBoundAccountAndUpdatesAccessToken(t *testing.T) {
	p := &testProvider{}
	var creates atomic.Int32
	var updates atomic.Int32
	p.provision = func(_ context.Context, in ProvisionRequest) (Resources, error) {
		creates.Add(1)
		return Resources{VMID: "one-slot", KeyID: "one-key", APIKey: "scoped-key"}, nil
	}
	p.identity = func(_ context.Context, resources Resources, identity Identity) error {
		if resources.VMID != "one-slot" || identity.AccessToken != "rotated-token" {
			t.Error("unexpected identity synchronization")
		}
		updates.Add(1)
		return nil
	}
	m := newTestManager(t, p)
	status, created, err := m.Provision(context.Background(), testInput(1))
	if err != nil || !created || !status.Ready {
		t.Fatalf("initial provision: %+v %v %v", status, created, err)
	}
	in := testInput(1)
	in.AccessToken = "rotated-token"
	status, created, err = m.Provision(context.Background(), in)
	if err != nil || created || status.VMID != "one-slot" {
		t.Fatalf("reuse: %+v %v %v", status, created, err)
	}
	if creates.Load() != 1 || updates.Load() != 1 {
		t.Fatal("provision was not idempotent")
	}
	in.ChatGPTAccountID = "different-account"
	_, _, err = m.Provision(context.Background(), in)
	requireCode(t, err, "binding_conflict")
	in = testInput(2)
	in.UpstreamProxyURL = "http://127.0.0.1:8080"
	_, _, err = m.Provision(context.Background(), in)
	requireCode(t, err, "invalid_proxy")
	_, err = m.UpdateIdentity(context.Background(), 1, Identity{AccessToken: "token", ChatGPTAccountID: "wrong-account"})
	requireCode(t, err, "identity_mismatch")
}

func TestStatusReflectsExternalSlotReadinessWithoutMutatingBinding(t *testing.T) {
	available := false
	p := &testProvider{check: func(_ context.Context, r Resources) error {
		if r.VMID != "vm-1" || r.KeyID != "key-1" {
			t.Error("readiness check escaped account binding")
		}
		if !available {
			return errors.New("external slot unavailable")
		}
		return nil
	}}
	m := newTestManager(t, p)
	provisionAccount(t, m, 1)
	status, err := m.StatusContext(context.Background(), 1)
	if err != nil || status.Ready || status.State != "unavailable" || status.LastErrorCode != "upstream_not_ready" {
		t.Fatalf("unavailable status was hidden: %+v %v", status, err)
	}
	cached, err := m.Status(1)
	if err != nil || !cached.Ready || cached.State != stateReady {
		t.Fatal("temporary read probe mutated durable lifecycle state")
	}
	available = true
	status, err = m.StatusContext(context.Background(), 1)
	if err != nil || !status.Ready || status.State != stateReady {
		t.Fatal("status did not recover after slot returned")
	}
}

func TestEncryptedStateNeverContainsOAuthTokens(t *testing.T) {
	m := newTestManager(t, &testProvider{})
	in := testInput(7)
	in.UpstreamProxyURL = "socks5://proxy-user:proxy-secret@127.0.0.1:1080"
	if _, _, err := m.Provision(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.cfg.StateDir, "account-7.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{in.AccessToken, "private-key-7", "proxy-secret", in.ChatGPTAccountID} {
		if strings.Contains(string(data), secret) {
			t.Fatal("credential or identity leaked in plaintext state")
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state file is not private")
	}
	var envelope sealedState
	if json.Unmarshal(data, &envelope) != nil {
		t.Fatal("bad envelope")
	}
	sealed, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := m.store.aead.Open(nil, sealed[:m.store.aead.NonceSize()], sealed[m.store.aead.NonceSize():], []byte("7"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), in.AccessToken) || strings.Contains(string(plain), "access_token") || strings.Contains(string(plain), "refresh_token") {
		t.Fatal("OAuth token was persisted inside encrypted state")
	}
	p := &testProvider{forward: func(_ context.Context, r Resources, _ *http.Request) (*http.Response, error) {
		if r.VMID != "vm-7" || r.APIKey != "private-key-7" {
			t.Error("restart lost account scoped resources")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
	}}
	restarted, err := New(m.cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	response, err := restarted.Forward(context.Background(), 7, &http.Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	wrong := m.cfg
	wrong.Secret = "different-bridge-secret-long-enough-for-test"
	if _, err := New(wrong, p); err == nil {
		t.Fatal("wrong encryption key was accepted")
	}
	status, _ := m.Status(7)
	statusJSON, _ := json.Marshal(status)
	for _, secret := range []string{in.AccessToken, "private-key-7", "proxy-secret", in.ChatGPTAccountID} {
		if strings.Contains(string(statusJSON), secret) {
			t.Fatal("status exposed credential or private identity")
		}
	}
}

func TestFailedProvisionRetainsPartialResourcesForRetryAndDelete(t *testing.T) {
	var calls atomic.Int32
	var deleted Resources
	p := &testProvider{provision: func(context.Context, ProvisionRequest) (Resources, error) {
		if calls.Add(1) == 1 {
			return Resources{VMID: "partial-slot", KeyID: "partial-key"}, errors.New("secret upstream detail")
		}
		return Resources{}, errors.New("retry failed early")
	}, delete: func(_ context.Context, r Resources) error { deleted = r; return nil }}
	m := newTestManager(t, p)
	for i := 0; i < 2; i++ {
		_, _, err := m.Provision(context.Background(), testInput(1))
		requireCode(t, err, "provision_failed")
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("provider details leaked")
		}
	}
	if err := m.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if deleted.VMID != "partial-slot" || deleted.KeyID != "partial-key" {
		t.Fatalf("lost cleanup resources: %+v", deleted)
	}
}

func TestFailedProvisionCanRetryToReady(t *testing.T) {
	var calls atomic.Int32
	m := newTestManager(t, &testProvider{provision: func(context.Context, ProvisionRequest) (Resources, error) {
		if calls.Add(1) == 1 {
			return Resources{VMID: "slot"}, errors.New("temporary failure")
		}
		return Resources{VMID: "slot", KeyID: "key", APIKey: "secret"}, nil
	}})
	_, _, err := m.Provision(context.Background(), testInput(1))
	requireCode(t, err, "provision_failed")
	status, created, err := m.Provision(context.Background(), testInput(1))
	if err != nil || created || !status.Ready || status.VMID != "slot" {
		t.Fatalf("retry failed: %+v %v %v", status, created, err)
	}
}

func TestDeleteBusyStaysDrainingAndRetriesOnlyBoundResources(t *testing.T) {
	var deletes atomic.Int32
	m := newTestManager(t, &testProvider{delete: func(_ context.Context, r Resources) error {
		if r.VMID != "vm-1" || r.KeyID != "key-1" {
			t.Error("delete crossed account boundary")
		}
		deletes.Add(1)
		return nil
	}})
	provisionAccount(t, m, 1)
	provisionAccount(t, m, 2)
	response, err := m.Forward(context.Background(), 1, &http.Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = m.Delete(context.Background(), 1)
	requireCode(t, err, "account_busy")
	status, _ := m.Status(1)
	if status.State != stateDeleting || status.Ready || status.ActiveLeases != 1 || deletes.Load() != 0 {
		t.Fatalf("unsafe drain: %+v deletes=%d", status, deletes.Load())
	}
	_, err = m.Forward(context.Background(), 1, &http.Request{}, nil)
	requireCode(t, err, "account_not_ready")
	_, _, _, _, err = m.acquire(1)
	requireCode(t, err, "account_not_ready")
	other, err := m.Forward(context.Background(), 2, &http.Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Body.Close()
	restarted, err := New(m.cfg, &testProvider{})
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := restarted.Status(1)
	if pending.State != stateDeleting {
		t.Fatal("draining state was not durable")
	}
	_ = response.Body.Close()
	_ = response.Body.Close()
	if err = m.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err = m.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 1 {
		t.Fatal("idempotent deletion called provider again")
	}
	_, err = m.Status(1)
	requireCode(t, err, "account_not_found")
	if status, err = m.Status(2); err != nil || !status.Ready {
		t.Fatal("other account was affected")
	}
}

func TestDeleteDeadlineIncludesWaitingForUpstreamHeaders(t *testing.T) {
	started := make(chan struct{})
	ended := make(chan struct{})
	p := &testProvider{forward: func(ctx context.Context, r Resources, _ *http.Request) (*http.Response, error) {
		if r.VMID == "vm-2" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("other"))}, nil
		}
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	m := newTestManager(t, p)
	provisionAccount(t, m, 1)
	provisionAccount(t, m, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { defer close(ended); _, _ = m.Forward(ctx, 1, &http.Request{}, nil) }()
	waitSignal(t, started)
	other, err := m.Forward(context.Background(), 2, &http.Request{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Body.Close()
	start := time.Now()
	err = m.Delete(context.Background(), 1)
	requireCode(t, err, "account_busy")
	if time.Since(start) > time.Second {
		t.Fatal("delete blocked on account mutex")
	}
	rejectCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	_, err = m.Forward(rejectCtx, 1, &http.Request{}, nil)
	requireCode(t, err, "account_not_ready")
	cancel()
	waitSignal(t, ended)
	if err = m.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteDuringProvisionCannotRestoreReady(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	ended := make(chan struct{})
	var deleted Resources
	m := newTestManager(t, &testProvider{provision: func(context.Context, ProvisionRequest) (Resources, error) {
		close(started)
		<-finish
		return Resources{VMID: "late-slot", KeyID: "late-key", APIKey: "private"}, nil
	}, delete: func(_ context.Context, r Resources) error { deleted = r; return nil }})
	go func() {
		defer close(ended)
		_, _, err := m.Provision(context.Background(), testInput(1))
		requireCode(t, err, "account_draining")
	}()
	waitSignal(t, started)
	err := m.Delete(context.Background(), 1)
	requireCode(t, err, "account_busy")
	close(finish)
	waitSignal(t, ended)
	status, _ := m.Status(1)
	if status.State != stateDeleting || status.Ready {
		t.Fatal("provision restored draining account")
	}
	if err = m.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if deleted.VMID != "late-slot" || deleted.KeyID != "late-key" {
		t.Fatal("late resource creation escaped cleanup")
	}
}
