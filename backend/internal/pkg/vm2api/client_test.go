package vm2api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/vmbridge"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	c, err := NewClient(Config{BaseURL: s.URL, MasterKey: "master-secret", HTTPClient: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func replyJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func replyData(w http.ResponseWriter, v any) { replyJSON(w, map[string]any{"ok": true, "data": v}) }

func TestCreateKeyRequiresExactSingleSlotScope(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/panel/api-keys" || r.Header.Get("Authorization") != "Bearer master-secret" {
			t.Errorf("unexpected management request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		want := map[string]any{"name": "slot-a-oauth", "category": "oauth", "group_type": "openai", "allowed_vms": []any{"slot-a"}}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("unexpected single-slot key configuration: %#v", body)
		}
		w.WriteHeader(http.StatusCreated)
		replyJSON(w, map[string]any{"ok": true, "item": map[string]any{"id": "key-a", "key": "slot-secret", "name": "slot-a-oauth", "category": "oauth", "group_type": "openai", "allowed_vms": []string{"slot-a"}, "status": "active"}})
	})
	k, err := c.CreateSingleSlotKey(context.Background(), "slot-a-oauth", "slot-a")
	if err != nil || k.Key != "slot-secret" {
		t.Fatalf("key creation failed: %v", err)
	}
	b, _ := json.Marshal(k)
	if strings.Contains(string(b), "slot-secret") {
		t.Fatal("public key serialization leaked credential")
	}
}

func TestImportAccessTokenOnly(t *testing.T) {
	var calls int
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/panel/vms/import" {
			t.Errorf("wrong import path: %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		want := map[string]any{"vm_id": "slot-a", "mode": "access_token", "access_token": fmt.Sprintf("at-%d", calls), "chatgpt_account_id": "chatgpt-a", "expires_at": float64(1800000000)}
		if !reflect.DeepEqual(body, want) {
			t.Error("import did not contain the exact access-token-only payload")
		}
		replyData(w, map[string]any{"vm": Slot{ID: "slot-a", Platform: "openai", HasToken: true}})
	})
	for i := 1; i <= 2; i++ {
		_, err := c.ImportAccessToken(context.Background(), "slot-a", AccessTokenCredential{AccessToken: fmt.Sprintf("at-%d", i), ChatGPTAccountID: "chatgpt-a", ExpiresAt: time.Unix(1800000000, 0)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatal("token update did not run")
	}
}

func TestCreateSlotRejectsSuccessfulHTTPWithStartError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["runtime_type"] != "docker" || body["platform"] != "openai" || body["family"] != "codex" {
			t.Errorf("wrong slot configuration: %#v", body)
		}
		replyData(w, map[string]any{"vm": Slot{ID: "slot-a", Status: "stopped"}, "start_error": "failed with at-secret and password-secret"})
	})
	result, err := c.CreateSlot(context.Background(), CreateSlotRequest{ID: "slot-a", Start: true})
	if err == nil || result == nil || result.VM.ID != "slot-a" || !result.StartFailed {
		t.Fatalf("ambiguous create was not preserved as failure: result=%+v err=%v", result, err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatal("start error leaked upstream text")
	}
}

func TestEnsureSlotRejectsExistingRuntimeMismatch(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("incompatible slot was mutated")
		}
		replyData(w, map[string]any{"vm": Slot{ID: "slot-a", Platform: "openai", RuntimeType: RuntimeKVM}})
	})
	_, attempted, err := c.EnsureSlot(context.Background(), CreateSlotRequest{ID: "slot-a", RuntimeType: RuntimeDocker})
	if err == nil || attempted {
		t.Fatal("incompatible runtime was adopted")
	}
}

func TestManagementTimeoutAndSafeErrors(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
			replyData(w, map[string]any{})
		})
		c.managementTimeout = 5 * time.Millisecond
		_, err := c.GetSlot(context.Background(), "slot-a")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline error, got %v", err)
		}
	})
	t.Run("redaction", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, "master-secret at-secret password-secret")
		})
		_, err := c.GetSlot(context.Background(), "slot-a")
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe error: %v", err)
		}
	})
}

func TestForwardUsesOnlySlotKeyAndFixedPath(t *testing.T) {
	body := `{"model":"test-model","input":"hello","stream":true}`
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" || r.URL.RawQuery != "" || string(got) != body {
			t.Error("forwarding changed the request or allowed an arbitrary route")
		}
		if r.Header.Get("Authorization") != "Bearer slot-secret" {
			t.Error("forwarding used the wrong credential")
		}
		for _, name := range []string{"Cookie", "X-API-Key", "X-Kin-Vm", "X-Kin-Debug"} {
			if r.Header.Get(name) != "" {
				t.Errorf("unsafe header forwarded: %s", name)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"ok\":true}\n\n")
	})
	p, _ := NewProvider(c, "installation")
	req := httptest.NewRequest(http.MethodPost, "https://untrusted.example/v1/responses?vm_id=other", strings.NewReader(body))
	for _, name := range []string{"Authorization", "Cookie", "X-API-Key", "X-Kin-Vm", "X-Kin-Debug"} {
		req.Header.Set(name, "attacker-value")
	}
	resp, err := p.Forward(context.Background(), vmbridge.Resources{APIKey: "slot-secret"}, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(got), "data: ") {
		t.Fatal("stream body lost")
	}
	req.URL.Path = "/api/panel/api-keys"
	if _, err = p.Forward(context.Background(), vmbridge.Resources{APIKey: "slot-secret"}, req); err == nil {
		t.Fatal("arbitrary management path accepted")
	}
}

func TestForwardStreamsBeyondManagementTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	c.managementTimeout = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := c.ForwardResponses(ctx, "slot-secret", strings.NewReader(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	time.Sleep(10 * time.Millisecond)
	buf := make([]byte, len("data: first\n\n"))
	if _, err = io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("SSE inherited management timeout: %v", err)
	}
}

func TestRedirectCannotMoveCredential(t *testing.T) {
	var redirected atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/trap" {
			redirected.Add(1)
			w.WriteHeader(200)
			return
		}
		http.Redirect(w, r, "/trap", http.StatusTemporaryRedirect)
	})
	if _, err := c.GetSlot(context.Background(), "slot-a"); err == nil {
		t.Fatal("management redirect accepted")
	}
	resp, err := c.ForwardResponses(context.Background(), "slot-secret", strings.NewReader(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect || redirected.Load() != 0 {
		t.Fatal("credential redirect followed")
	}
}

func TestNoSecretsInDiagnostics(t *testing.T) {
	values := []any{Config{BaseURL: "http://secret-host", MasterKey: "secret-master"}, &Client{masterKey: "secret-master"}, AccessTokenCredential{AccessToken: "secret-at"}, ManagedKey{Key: "secret-key"}, ProxyImportRequest{URL: "socks5://user:secret-pass@host:1080"}}
	for _, v := range values {
		if strings.Contains(fmt.Sprintf("%v %+v %#v", v, v, v), "secret-") {
			t.Fatal("diagnostic formatting exposed a credential")
		}
	}
}

func TestModelsAndUsageUseScopedPublicKey(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer slot-secret" || r.Method != http.MethodGet {
			t.Error("public API used management credentials")
		}
		switch r.URL.Path {
		case "/v1/models":
			replyJSON(w, map[string]any{"object": "list", "data": []Model{{ID: "model-a", Object: "model"}}})
		case "/v1/usage":
			if r.URL.RawQuery != "platform=openai" {
				t.Error("usage did not select OpenAI scope")
			}
			replyJSON(w, map[string]any{"platform": "openai", "requests": 3})
		default:
			t.Errorf("unexpected public endpoint: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	models, err := c.Models(context.Background(), "slot-secret")
	if err != nil || len(models.Data) != 1 || models.Data[0].ID != "model-a" {
		t.Fatalf("model list decode failed: %v", err)
	}
	usage, err := c.Usage(context.Background(), "slot-secret")
	if err != nil || !json.Valid(usage) {
		t.Fatalf("usage decode failed: %v", err)
	}
}

func TestControlFailsClosedOnIncompleteSuccess(t *testing.T) {
	t.Run("missing_ok", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			replyJSON(w, map[string]any{"data": map[string]any{"vm": Slot{ID: "slot-a"}}})
		})
		if _, err := c.GetSlot(context.Background(), "slot-a"); err == nil {
			t.Fatal("control response without affirmative success was accepted")
		}
	})
	t.Run("schedule_not_applied", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			replyData(w, map[string]any{"id": "slot-a", "schedulable": false})
		})
		if err := c.SetSchedulable(context.Background(), "slot-a", true); err == nil {
			t.Fatal("failed scheduling change was accepted")
		}
	})
}
