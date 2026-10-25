package vm2api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/vmbridge"
)

func TestProviderLifecycleIsIdempotentAndSingleAccount(t *testing.T) {
	slotID := DeterministicSlotID("installation-a", 17)
	var mu sync.Mutex
	var slot *Slot
	var key *keyWire
	var creates, keyCreates int
	var tokens, events []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		path := r.URL.Path
		events = append(events, r.Method+" "+path)
		if r.Header.Get("Authorization") != "Bearer master-secret" {
			t.Error("wrong management authentication")
		}
		switch {
		case r.Method == http.MethodGet && path == "/api/panel/vms/"+slotID:
			if slot == nil {
				http.NotFound(w, r)
				return
			}
			replyData(w, map[string]any{"vm": slot})
		case r.Method == http.MethodPost && path == "/api/panel/vms/create":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["id"] != slotID || body["start"] != false || body["runtime_type"] != "docker" || body["platform"] != "openai" {
				t.Error("wrong isolated slot creation")
			}
			creates++
			slot = &Slot{ID: slotID, Name: "account 17", Platform: "openai", Family: "codex", RuntimeType: RuntimeDocker, Status: "stopped"}
			replyData(w, map[string]any{"vm": slot})
		case r.Method == http.MethodPost && path == "/api/panel/vms/"+slotID+"/schedulable":
			var body struct {
				Schedulable bool `json:"schedulable"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			slot.Schedulable = body.Schedulable
			replyData(w, map[string]any{"id": slotID, "schedulable": slot.Schedulable})
		case r.Method == http.MethodPost && path == "/api/panel/proxies/local":
			replyData(w, map[string]any{"ok": true, "proxy": Proxy{ID: "px-local", Enabled: true}})
		case r.Method == http.MethodPost && path == "/api/panel/proxies/px-local/bind":
			var body struct {
				VMID string `json:"vm_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.VMID != slotID {
				t.Error("proxy bound to a different account")
			}
			replyData(w, map[string]any{"proxy": Proxy{ID: "px-local", Enabled: true, BoundVMIDs: []string{slotID}}})
		case r.Method == http.MethodPost && path == "/api/panel/vms/import":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 4 || body["vm_id"] != slotID || body["mode"] != "access_token" || body["chatgpt_account_id"] != "chatgpt-17" {
				t.Error("identity import scope or access-token-only fields invalid")
			}
			token, _ := body["access_token"].(string)
			tokens = append(tokens, token)
			slot.HasToken = true
			replyData(w, map[string]any{"vm": slot})
		case r.Method == http.MethodPost && path == "/api/panel/vms/"+slotID+"/start":
			slot.Status = "running"
			replyData(w, map[string]any{"vm": slot, "boot": map[string]any{"ok": true}})
		case r.Method == http.MethodGet && path == "/api/panel/api-keys":
			keys := []keyWire{}
			if key != nil {
				copy := *key
				copy.Key = "masked"
				keys = append(keys, copy)
			}
			replyJSON(w, map[string]any{"ok": true, "keys": keys})
		case r.Method == http.MethodPost && path == "/api/panel/api-keys":
			var body keyWire
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Name != keyName(slotID) || !singleSlotKey(body.managed(), slotID) {
				t.Error("key not scoped to this account")
			}
			keyCreates++
			body.ID = "key-17"
			body.Key = "slot-secret"
			body.Status = "active"
			key = &body
			replyJSON(w, map[string]any{"ok": true, "item": key})
		case r.Method == http.MethodPost && path == "/api/panel/api-keys/key-17/reveal":
			replyJSON(w, map[string]any{"ok": true, "id": key.ID, "key": key.Key})
		case r.Method == http.MethodDelete && path == "/api/panel/api-keys/key-17":
			if key == nil {
				http.NotFound(w, r)
				return
			}
			key = nil
			replyJSON(w, map[string]any{"ok": true})
		case r.Method == http.MethodPost && path == "/api/panel/vms/"+slotID+"/stop":
			if slot == nil {
				http.NotFound(w, r)
				return
			}
			slot.Status = "stopped"
			slot.Schedulable = false
			replyData(w, map[string]any{"vm": slot})
		case r.Method == http.MethodDelete && path == "/api/panel/vms/"+slotID:
			if slot == nil {
				http.NotFound(w, r)
				return
			}
			if key != nil || slot.Status != "stopped" {
				t.Error("slot destroyed before key revocation and stop")
			}
			slot = nil
			replyJSON(w, map[string]any{"ok": true})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	})
	p, _ := NewProvider(c, "installation-a")
	in := vmbridge.ProvisionRequest{AccountID: 17, Name: "account 17", AccessToken: "at-first", ChatGPTAccountID: "chatgpt-17"}
	first, err := p.Provision(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Provision(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.VMID != slotID || first.APIKey != "slot-secret" {
		t.Fatal("retry failed to reuse isolated resources")
	}
	if err = p.UpdateIdentity(context.Background(), first, vmbridge.Identity{AccessToken: "at-next", ChatGPTAccountID: "chatgpt-17"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if creates != 1 || keyCreates != 1 || !reflect.DeepEqual(tokens, []string{"at-first", "at-first", "at-next"}) {
		t.Error("retry duplicated resources or token update failed")
	}
	mu.Unlock()
	if err = p.Delete(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err = p.Delete(context.Background(), first); err != nil {
		t.Fatalf("idempotent deletion failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if slot != nil || key != nil {
		t.Fatal("resources remain after deletion")
	}
	for _, event := range events {
		if strings.HasPrefix(event, "DELETE /api/panel/proxies/") {
			t.Fatal("shared proxy deleted")
		}
	}
}

func TestProviderReturnsPartialResourcesOnStartFailure(t *testing.T) {
	slotID := DeterministicSlotID("installation", 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/api/panel/vms/create" {
			t.Error("provisioning continued after a failed create")
		}
		replyData(w, map[string]any{"vm": Slot{ID: slotID}, "start_error": "sensitive detail"})
	})
	p, _ := NewProvider(c, "installation")
	resources, err := p.Provision(context.Background(), vmbridge.ProvisionRequest{AccountID: 1, Name: "one", AccessToken: "at", ChatGPTAccountID: "one"})
	if err == nil || resources.VMID != slotID || resources.APIKey != "" {
		t.Fatal("failed create lost recoverable resources")
	}
}

func TestDeleteKeepsMetadataWhenStopFails(t *testing.T) {
	var deleted bool
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/panel/api-keys":
			replyJSON(w, map[string]any{"ok": true, "keys": []any{}})
		case strings.HasSuffix(r.URL.Path, "/stop"):
			http.Error(w, "runtime did not stop", 500)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/panel/vms/"):
			deleted = true
			replyJSON(w, map[string]any{"ok": true})
		default:
			replyJSON(w, map[string]any{"ok": true})
		}
	})
	p, _ := NewProvider(c, "installation")
	if err := p.Delete(context.Background(), vmbridge.Resources{VMID: "slot-a", KeyID: "key-a"}); err == nil {
		t.Fatal("failed stop was accepted")
	}
	if deleted {
		t.Fatal("runtime recovery metadata destroyed despite failed stop")
	}
}

func TestProxyImportReusesExactCredentials(t *testing.T) {
	var imports int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/panel/proxies":
			replyData(w, map[string]any{"proxies": []Proxy{{ID: "px-other", Host: "proxy.example", Port: 1080, Enabled: true}, {ID: "px-same", Host: "proxy.example", Port: 1080, Enabled: true}}})
		case "/api/panel/proxies/px-other/reveal":
			replyJSON(w, map[string]any{"ok": true, "id": "px-other", "uri": "socks5://user:different@proxy.example:1080"})
		case "/api/panel/proxies/px-same/reveal":
			replyJSON(w, map[string]any{"ok": true, "id": "px-same", "uri": "socks5://user:correct@proxy.example:1080"})
		case "/api/panel/proxies/import":
			imports++
			http.Error(w, "should reuse existing proxy", 500)
		case "/api/panel/proxies/px-same/bind":
			replyData(w, map[string]any{"proxy": Proxy{ID: "px-same", Enabled: true, BoundVMIDs: []string{"slot-a"}}})
		default:
			t.Errorf("unexpected proxy request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	p, err := c.ImportProxy(context.Background(), ProxyImportRequest{URL: "socks5h://user:correct@proxy.example:1080", BindSlotID: "slot-a"})
	if err != nil || p.ID != "px-same" || imports != 0 {
		t.Fatalf("proxy reuse failed: %v", err)
	}
}

func TestProxyBindingRejectsWorkerFailure(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		replyData(w, map[string]any{"proxy": Proxy{ID: "px-a", Enabled: true, BoundVMIDs: []string{"slot-a"}}, "worker": map[string]any{"ok": false, "error": "password-secret"}})
	})
	_, err := c.BindProxy(context.Background(), "px-a", "slot-a")
	if err == nil || strings.Contains(err.Error(), "password-secret") {
		t.Fatal("worker failure was accepted or leaked")
	}
}

func TestRecoverLostKeyAndRejectBroaderScope(t *testing.T) {
	for _, broad := range []bool{false, true} {
		t.Run(map[bool]string{false: "recover", true: "reject_broad"}[broad], func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/panel/api-keys/key-a/reveal" {
					replyJSON(w, map[string]any{"ok": true, "id": "key-a", "key": "existing-secret"})
					return
				}
				if r.Method != http.MethodGet {
					t.Error("retry created another key")
				}
				k := keyWire{ID: "key-a", Name: "slot-a-oauth", Status: "active", Category: "oauth", GroupType: "openai", AllowedVMs: []string{"slot-a"}}
				if broad {
					k.GroupType = "all"
					k.AllowedVMs = nil
				}
				replyJSON(w, map[string]any{"ok": true, "keys": []keyWire{k}})
			})
			k, err := c.EnsureSingleSlotKey(context.Background(), "slot-a-oauth", "slot-a")
			if broad {
				if err == nil {
					t.Fatal("broad key adopted")
				}
				return
			}
			if err != nil || k.Key != "existing-secret" {
				t.Fatalf("key recovery failed: %v", err)
			}
		})
	}
}
