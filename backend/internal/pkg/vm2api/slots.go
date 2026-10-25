package vm2api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type slotWire struct {
	VM         Slot            `json:"vm"`
	StartError json.RawMessage `json:"start_error"`
	ProxyError json.RawMessage `json:"proxy_error"`
	Boot       *struct {
		OK *bool `json:"ok"`
	} `json:"boot"`
}

func hasFailure(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != `""` && s != "false"
}
func slotOutcome(w slotWire, operation string, running bool) (*SlotResult, error) {
	r := &SlotResult{VM: w.VM, StartFailed: hasFailure(w.StartError), ProxyFailed: hasFailure(w.ProxyError)}
	code := ""
	switch {
	case r.StartFailed || (w.Boot != nil && w.Boot.OK != nil && !*w.Boot.OK):
		code = "slot_start_failed"
	case r.ProxyFailed:
		code = "proxy_binding_failed"
	case w.VM.ID == "":
		code = "slot_missing"
	case running && w.VM.Status != "running":
		code = "slot_not_running"
	}
	if code != "" {
		return r, &APIError{Operation: operation, StatusCode: 200, Code: code}
	}
	return r, nil
}

func (c *Client) CreateSlot(ctx context.Context, input CreateSlotRequest) (*SlotResult, error) {
	if err := validID(input.ID); err != nil {
		return nil, err
	}
	runtime := input.RuntimeType
	if runtime == "" {
		runtime = RuntimeDocker
	}
	if runtime != RuntimeDocker && runtime != RuntimeKVM {
		return nil, errors.New("vm2api: unsupported runtime")
	}
	if input.ProxyID != "" {
		if err := validID(input.ProxyID); err != nil {
			return nil, err
		}
	}
	body := struct {
		ID       string      `json:"id"`
		Name     string      `json:"name"`
		Platform string      `json:"platform"`
		Family   string      `json:"family"`
		Runtime  RuntimeType `json:"runtime_type"`
		Start    bool        `json:"start"`
		ProxyID  string      `json:"proxy_id,omitempty"`
	}{input.ID, input.Name, "openai", "codex", runtime, input.Start, input.ProxyID}
	var wire slotWire
	if err := c.control(ctx, http.MethodPost, "/api/panel/vms/create", "create_slot", body, &wire); err != nil {
		return nil, err
	}
	r, err := slotOutcome(wire, "create_slot", input.Start)
	if wire.VM.ID != input.ID {
		return r, &APIError{Operation: "create_slot", Code: "slot_identity_mismatch"}
	}
	return r, err
}

func (c *Client) GetSlot(ctx context.Context, id string) (*Slot, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	var wire slotWire
	if err := c.control(ctx, http.MethodGet, "/api/panel/vms/"+id, "get_slot", nil, &wire); err != nil {
		return nil, err
	}
	if wire.VM.ID != id {
		return nil, &APIError{Operation: "get_slot", Code: "slot_identity_mismatch"}
	}
	return &wire.VM, nil
}

// EnsureSlot recovers a prior create after a lost response. The boolean reports
// a create attempt, including an ambiguous transport error, so cleanup can
// recover its deterministic ID. An incompatible existing slot is never adopted.
func (c *Client) EnsureSlot(ctx context.Context, input CreateSlotRequest) (*SlotResult, bool, error) {
	vm, err := c.GetSlot(ctx, input.ID)
	if err == nil {
		return existingSlot(vm, input), false, compatibleSlot(vm, input)
	}
	if !IsNotFound(err) {
		return nil, false, err
	}
	r, err := c.CreateSlot(ctx, input)
	if IsConflict(err) {
		vm, getErr := c.GetSlot(ctx, input.ID)
		if getErr != nil {
			return nil, false, getErr
		}
		return existingSlot(vm, input), false, compatibleSlot(vm, input)
	}
	return r, true, err
}
func existingSlot(vm *Slot, _ CreateSlotRequest) *SlotResult { return &SlotResult{VM: *vm} }
func compatibleSlot(vm *Slot, input CreateSlotRequest) error {
	runtime := input.RuntimeType
	if runtime == "" {
		runtime = RuntimeDocker
	}
	actual := vm.RuntimeType
	if actual == "" {
		actual = vm.Runtime.Type
	}
	if vm.Platform != "openai" || (vm.Family != "" && vm.Family != "codex") || (actual != "" && actual != runtime) {
		return &APIError{Operation: "ensure_slot", StatusCode: 409, Code: "slot_identity_mismatch"}
	}
	return nil
}

// ImportAccessToken also updates a slot. vm2api replaces the credentials on
// this endpoint, clearing any prior RT rather than retaining it. Do not use the
// Anthropic /oauth/credential endpoint for a Codex slot.
func (c *Client) ImportAccessToken(ctx context.Context, id string, credential AccessTokenCredential) (*Slot, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(credential.AccessToken) == "" || strings.ContainsAny(credential.AccessToken, "\r\n") {
		return nil, errors.New("vm2api: access token required")
	}
	body := struct {
		VMID        string `json:"vm_id"`
		Mode        string `json:"mode"`
		AccessToken string `json:"access_token"`
		AccountID   string `json:"chatgpt_account_id,omitempty"`
		Email       string `json:"email,omitempty"`
		ExpiresAt   int64  `json:"expires_at,omitempty"`
	}{VMID: id, Mode: "access_token", AccessToken: credential.AccessToken, AccountID: credential.ChatGPTAccountID, Email: credential.Email}
	if !credential.ExpiresAt.IsZero() {
		body.ExpiresAt = credential.ExpiresAt.Unix()
	}
	var wire slotWire
	if err := c.control(ctx, http.MethodPost, "/api/panel/vms/import", "import_access_token", body, &wire); err != nil {
		return nil, err
	}
	if wire.VM.ID != id || wire.VM.Platform != "openai" || !wire.VM.HasToken {
		return nil, &APIError{Operation: "import_access_token", Code: "slot_identity_mismatch"}
	}
	return &wire.VM, nil
}

func (c *Client) StartSlot(ctx context.Context, id string) (*SlotResult, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	var wire slotWire
	if err := c.control(ctx, http.MethodPost, "/api/panel/vms/"+id+"/start", "start_slot", struct{}{}, &wire); err != nil {
		return nil, err
	}
	r, err := slotOutcome(wire, "start_slot", true)
	if err == nil && r.VM.ID != id {
		return r, &APIError{Operation: "start_slot", Code: "slot_identity_mismatch"}
	}
	return r, err
}
func (c *Client) SetSchedulable(ctx context.Context, id string, enabled bool) error {
	if err := validID(id); err != nil {
		return err
	}
	var result struct {
		ID          string `json:"id"`
		Schedulable *bool  `json:"schedulable"`
	}
	err := c.control(ctx, http.MethodPost, "/api/panel/vms/"+id+"/schedulable", "set_schedulable", struct {
		Schedulable bool `json:"schedulable"`
	}{enabled}, &result)
	if err != nil {
		return err
	}
	if result.ID != id || result.Schedulable == nil || *result.Schedulable != enabled {
		return &APIError{Operation: "set_schedulable", Code: "scheduling_state_mismatch"}
	}
	return nil
}
func (c *Client) StopSlot(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	var wire slotWire
	err := c.control(ctx, http.MethodPost, "/api/panel/vms/"+id+"/stop", "stop_slot", struct{}{}, &wire)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if wire.VM.ID != id || wire.VM.Status != "stopped" {
		return &APIError{Operation: "stop_slot", Code: "slot_not_stopped"}
	}
	return nil
}
func (c *Client) DeleteSlot(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	err := c.control(ctx, http.MethodDelete, "/api/panel/vms/"+id, "delete_slot", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
