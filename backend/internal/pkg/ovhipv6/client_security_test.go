package ovhipv6

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientTokenFilePrecedenceAndInvalidConfiguration(t *testing.T) {
	t.Setenv("OVH_IPV6_CONTROL_URL", "http://127.0.0.1:11881")
	t.Setenv("OVH_IPV6_CONTROL_TOKEN", strings.Repeat("e", 40))
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(strings.Repeat("f", 40)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVH_IPV6_CONTROL_TOKEN_FILE", path)
	c, err := LoadFromEnv()
	if err != nil || c.Token != strings.Repeat("f", 40) {
		t.Fatalf("file precedence: %v", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if c, err = LoadFromEnv(); err == nil || c != nil {
		t.Fatal("missing configured file fell back to environment token")
	}
	t.Setenv("OVH_IPV6_CONTROL_TOKEN_FILE", "")
	for _, raw := range []string{"ftp://127.0.0.1", "http://user:secret@127.0.0.1", "http://127.0.0.1/path", "http://127.0.0.1?key=x", "http://127.0.0.1?", "http://127.0.0.1#x", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:", "http://[fe80::1%25eth0]"} {
		t.Setenv("OVH_IPV6_CONTROL_URL", raw)
		if c, err = LoadFromEnv(); err == nil || c != nil {
			t.Errorf("accepted invalid origin %q", raw)
		}
	}
}

func TestClientDoesNotRedirectOrAcceptEscapingPaths(t *testing.T) {
	var redirected bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true; w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c := &Client{BaseURL: source.URL, Token: strings.Repeat("x", 40), HTTP: source.Client()}
	var result Status
	if err := c.Call(context.Background(), http.MethodGet, "/v1/status", nil, &result); err == nil {
		t.Fatal("redirect reported success")
	}
	if redirected {
		t.Fatal("redirect followed with control request")
	}
	for _, path := range []string{"//evil.example/v1/status", "/v1/leases/../status", "/v1/leases/1?token=x", "/v1/leases/1%2factivate", "/v1/leases/0", "/v1/leases/01"} {
		if err := c.Call(context.Background(), http.MethodDelete, path, nil, &result); err == nil {
			t.Errorf("accepted invalid path %q", path)
		}
	}
}

func TestClientAcceptsValidControlReplyAndBoundsPayload(t *testing.T) {
	token := strings.Repeat("x", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing authorization")
		}
		if r.URL.Path == "/v1/status" {
			_, _ = w.Write([]byte(`{"available":true,"issued":"12"}`))
			return
		}
		_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	}))
	defer server.Close()
	c := &Client{BaseURL: server.URL, Token: token, HTTP: server.Client()}
	var status Status
	if err := c.Call(context.Background(), http.MethodGet, "/v1/status", nil, &status); err != nil || !status.Available || status.Issued != "12" {
		t.Fatalf("valid reply: %#v %v", status, err)
	}
	if err := c.Call(context.Background(), http.MethodPost, "/v1/leases", nil, &Lease{}); err == nil {
		t.Fatal("accepted oversized reply")
	}
}
