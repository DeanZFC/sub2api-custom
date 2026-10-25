package vm2api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/vmbridge"
)

// Provider adapts the external control plane to the separately deployed bridge.
// Namespace must be an immutable installation ID. The bridge serializes
// account operations; sharing a namespace does not enable active-active use.
type Provider struct {
	client    *Client
	namespace string
}

var _ vmbridge.Provider = (*Provider)(nil)
var _ vmbridge.ReadinessChecker = (*Provider)(nil)

func NewProvider(client *Client, namespace string) (*Provider, error) {
	if client == nil || strings.TrimSpace(namespace) == "" {
		return nil, errors.New("vm2api: client and installation namespace required")
	}
	return &Provider{client: client, namespace: namespace}, nil
}
func keyName(slotID string) string { return slotID + "-oauth" }

func (p *Provider) Provision(ctx context.Context, in vmbridge.ProvisionRequest) (vmbridge.Resources, error) {
	var resources vmbridge.Resources
	if in.AccountID <= 0 || strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.AccessToken) == "" || strings.TrimSpace(in.ChatGPTAccountID) == "" {
		return resources, errors.New("vm2api: account identity required")
	}
	runtime := RuntimeType(in.RuntimeType)
	if runtime == "" {
		runtime = RuntimeDocker
	}
	if runtime != RuntimeDocker && runtime != RuntimeKVM {
		return resources, errors.New("vm2api: unsupported runtime")
	}
	if strings.TrimSpace(in.UpstreamProxyURL) != "" {
		if _, err := parseSocksProxy(in.UpstreamProxyURL); err != nil {
			return resources, err
		}
	}
	slotID := DeterministicSlotID(p.namespace, in.AccountID)
	_, attempted, err := p.client.EnsureSlot(ctx, CreateSlotRequest{ID: slotID, Name: in.Name, RuntimeType: runtime, Start: false})
	if attempted || err == nil {
		resources.VMID = slotID
	}
	if err != nil {
		return resources, err
	}
	// Keep a retry or partially provisioned slot out of scheduling until every
	// required resource and the imported identity have been verified.
	if err = p.client.SetSchedulable(ctx, slotID, false); err != nil {
		return resources, err
	}
	var proxy *Proxy
	if strings.TrimSpace(in.UpstreamProxyURL) == "" {
		proxy, err = p.client.EnsureLocalProxy(ctx)
	} else {
		proxy, err = p.client.ImportProxy(ctx, ProxyImportRequest{URL: in.UpstreamProxyURL})
	}
	if proxy != nil {
		resources.ProxyID = proxy.ID
	}
	if err != nil {
		return resources, err
	}
	if _, err = p.client.BindProxy(ctx, proxy.ID, slotID); err != nil {
		return resources, err
	}
	if _, err = p.client.ImportAccessToken(ctx, slotID, AccessTokenCredential{AccessToken: in.AccessToken, ChatGPTAccountID: in.ChatGPTAccountID}); err != nil {
		return resources, err
	}
	if _, err = p.client.StartSlot(ctx, slotID); err != nil {
		return resources, err
	}
	key, err := p.client.EnsureSingleSlotKey(ctx, keyName(slotID), slotID)
	if key != nil {
		resources.KeyID = key.ID
		resources.APIKey = key.Key
	}
	if err != nil {
		return resources, err
	}
	if err = p.client.SetSchedulable(ctx, slotID, true); err != nil {
		return resources, err
	}
	vm, err := p.client.GetSlot(ctx, slotID)
	if err != nil {
		return resources, err
	}
	if vm.Platform != "openai" || vm.Status != "running" || !vm.Schedulable || !vm.HasToken {
		return resources, &APIError{Operation: "provision", Code: "slot_not_ready"}
	}
	return resources, nil
}

func (p *Provider) UpdateIdentity(ctx context.Context, resources vmbridge.Resources, in vmbridge.Identity) error {
	if strings.TrimSpace(in.ChatGPTAccountID) == "" {
		return errors.New("vm2api: ChatGPT account ID required")
	}
	_, err := p.client.ImportAccessToken(ctx, resources.VMID, AccessTokenCredential{AccessToken: in.AccessToken, ChatGPTAccountID: in.ChatGPTAccountID})
	return err
}

// CheckReady is an optional vmbridge.ReadinessChecker implementation. It
// validates current provider state without changing the bridge's persisted
// lifecycle state, so a later health probe can recover after an operator starts
// or schedules the slot again.
func (p *Provider) CheckReady(ctx context.Context, resources vmbridge.Resources) error {
	if err := validID(resources.VMID); err != nil {
		return err
	}
	if strings.TrimSpace(resources.KeyID) == "" {
		return &APIError{Operation: "check_ready", Code: "key_missing"}
	}
	vm, err := p.client.GetSlot(ctx, resources.VMID)
	if err != nil {
		return err
	}
	if vm.Platform != "openai" || (vm.Family != "" && vm.Family != "codex") || vm.Status != "running" || !vm.Schedulable || !vm.HasToken {
		return &APIError{Operation: "check_ready", Code: "slot_not_ready"}
	}
	keys, err := p.client.ListKeys(ctx)
	if err != nil {
		return err
	}
	for i := range keys {
		key := &keys[i]
		if key.ID != resources.KeyID {
			continue
		}
		if !singleSlotKey(key, resources.VMID) || (key.Status != "" && key.Status != "active") {
			return &APIError{Operation: "check_ready", Code: "key_scope_mismatch"}
		}
		return nil
	}
	return &APIError{Operation: "check_ready", StatusCode: http.StatusNotFound, Code: "key_missing"}
}

// Delete revokes access before stopping the slot. Failed stop/revocation leaves
// the VM record recoverable: vm2api may otherwise delete local metadata even
// when runtime destruction fails. Proxy rows may be shared and are not deleted.
func (p *Provider) Delete(ctx context.Context, resources vmbridge.Resources) error {
	var keyErr error
	if resources.KeyID != "" {
		keyErr = p.client.RevokeKey(ctx, resources.KeyID)
	}
	if resources.VMID == "" {
		return keyErr
	}
	keyErr = errors.Join(keyErr, p.client.RevokeSlotKeys(ctx, keyName(resources.VMID), resources.VMID))
	stopErr := p.client.StopSlot(ctx, resources.VMID)
	if err := errors.Join(keyErr, stopErr); err != nil {
		return err
	}
	if err := p.client.DeleteSlot(ctx, resources.VMID); err != nil {
		return err
	}
	_, err := p.client.GetSlot(ctx, resources.VMID)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return &APIError{Operation: "delete_slot", Code: "slot_still_exists"}
}

func (p *Provider) Forward(ctx context.Context, resources vmbridge.Resources, request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, errors.New("vm2api: request required")
	}
	switch {
	case request.Method == http.MethodPost && request.URL.Path == "/v1/responses":
		return p.client.ForwardResponses(ctx, resources.APIKey, request.Body, request.Header)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/responses":
		return p.client.forward(ctx, http.MethodGet, "/v1/responses", "responses_websocket", resources.APIKey, nil, request.Header)
	case request.Method == http.MethodPost && (request.URL.Path == "/v1/responses/compact" || request.URL.Path == "/v1/images/generations" || request.URL.Path == "/v1/images/edits" || request.URL.Path == "/v1/alpha/search"):
		// Preserve the pinned upstream's response even for unsupported endpoints
		// (for example compact returns 404 on v1.3.225). Never bypass its kernel.
		return p.client.forward(ctx, http.MethodPost, request.URL.Path, "inference", resources.APIKey, request.Body, request.Header)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/models":
		return p.client.ForwardModels(ctx, resources.APIKey)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/usage":
		return p.client.ForwardUsage(ctx, resources.APIKey)
	default:
		return nil, errors.New("vm2api: unsupported public endpoint")
	}
}
