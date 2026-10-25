package vm2api

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// keyWire is private because generic JSON encoding of ManagedKey must never
// expose the plaintext credential. Only an explicit management operation reads it.
type keyWire struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Key        string   `json:"key"`
	Category   string   `json:"category"`
	GroupType  string   `json:"group_type"`
	AllowedVMs []string `json:"allowed_vms"`
	Status     string   `json:"status"`
	VMPoolID   string   `json:"vm_pool_id"`
}

func (w keyWire) managed() *ManagedKey {
	return &ManagedKey{ID: w.ID, Name: w.Name, Key: w.Key, Category: w.Category, GroupType: w.GroupType, AllowedVMs: w.AllowedVMs, Status: w.Status, VMPoolID: w.VMPoolID}
}
func singleSlotKey(k *ManagedKey, slotID string) bool {
	return k != nil && k.Category == "oauth" && k.GroupType == "openai" && k.VMPoolID == "" && len(k.AllowedVMs) == 1 && k.AllowedVMs[0] == slotID
}

// CreateSingleSlotKey always sends both group_type and allowed_vms. Omitting
// group_type in vm2api normalizes the scope to all accounts. A pool is never used.
func (c *Client) CreateSingleSlotKey(ctx context.Context, name, slotID string) (*ManagedKey, error) {
	if err := validID(slotID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("vm2api: key name required")
	}
	body := struct {
		Name       string   `json:"name"`
		Category   string   `json:"category"`
		GroupType  string   `json:"group_type"`
		AllowedVMs []string `json:"allowed_vms"`
	}{name, "oauth", "openai", []string{slotID}}
	var result struct {
		Item keyWire `json:"item"`
	}
	if err := c.control(ctx, http.MethodPost, "/api/panel/api-keys", "create_key", body, &result); err != nil {
		return nil, err
	}
	k := result.Item.managed()
	if validID(k.ID) != nil || !singleSlotKey(k, slotID) || strings.TrimSpace(k.Key) == "" || (k.Status != "" && k.Status != "active") {
		return k, &APIError{Operation: "create_key", Code: "invalid_key_scope"}
	}
	return k, nil
}

func (c *Client) ListKeys(ctx context.Context) ([]ManagedKey, error) {
	var result struct {
		Keys []keyWire `json:"keys"`
	}
	if err := c.control(ctx, http.MethodGet, "/api/panel/api-keys", "list_keys", nil, &result); err != nil {
		return nil, err
	}
	keys := make([]ManagedKey, 0, len(result.Keys))
	for _, w := range result.Keys {
		k := w.managed()
		k.Key = ""
		keys = append(keys, *k)
	}
	return keys, nil
}

func (c *Client) revealKey(ctx context.Context, k *ManagedKey) error {
	if err := validID(k.ID); err != nil {
		return err
	}
	var wire struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := c.control(ctx, http.MethodPost, "/api/panel/api-keys/"+k.ID+"/reveal", "reveal_key", struct{}{}, &wire); err != nil {
		return err
	}
	if wire.ID != k.ID || strings.TrimSpace(wire.Key) == "" {
		return &APIError{Operation: "reveal_key", Code: "key_identity_mismatch"}
	}
	k.Key = wire.Key
	return nil
}

// EnsureSingleSlotKey recovers a create whose response was lost. The stable name
// identifies our key; the complete scope is checked before adopting it. Keys
// are generated randomly by vm2api, so delete/recreate never reuses an old key.
func (c *Client) EnsureSingleSlotKey(ctx context.Context, name, slotID string) (*ManagedKey, error) {
	if err := validID(slotID); err != nil {
		return nil, err
	}
	keys, err := c.ListKeys(ctx)
	if err != nil {
		return nil, err
	}
	var found *ManagedKey
	for i := range keys {
		k := &keys[i]
		if k.Name != name {
			continue
		}
		if !singleSlotKey(k, slotID) || (k.Status != "" && k.Status != "active") {
			return nil, &APIError{Operation: "ensure_key", Code: "existing_key_scope_mismatch"}
		}
		if found != nil {
			return nil, &APIError{Operation: "ensure_key", Code: "duplicate_key_identity"}
		}
		found = k
	}
	if found != nil {
		if err = c.revealKey(ctx, found); err != nil {
			return found, err
		}
		return found, nil
	}
	return c.CreateSingleSlotKey(ctx, name, slotID)
}

func (c *Client) RevokeKey(ctx context.Context, id string) error {
	if err := validID(id); err != nil {
		return err
	}
	err := c.control(ctx, http.MethodDelete, "/api/panel/api-keys/"+id, "revoke_key", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// RevokeSlotKeys also finds keys created just before a request timed out. Only
// the stable name with the exact single-slot scope is eligible for deletion.
func (c *Client) RevokeSlotKeys(ctx context.Context, name, slotID string) error {
	if err := validID(slotID); err != nil {
		return err
	}
	keys, err := c.ListKeys(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for i := range keys {
		k := &keys[i]
		if k.Name != name {
			continue
		}
		if !singleSlotKey(k, slotID) {
			errs = append(errs, &APIError{Operation: "revoke_slot_keys", Code: "existing_key_scope_mismatch"})
			continue
		}
		if err := c.RevokeKey(ctx, k.ID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
