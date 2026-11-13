package ipv6egress

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDaemonRequiresExplicitClientAndHostAllowLists(t *testing.T) {
	cfg := testConfig(t)
	cfg.AllowedClients = nil
	if cfg.Validate() == nil {
		t.Fatal("missing client restrictions accepted")
	}
	cfg = testConfig(t)
	cfg.AllowedClients = []string{"0.0.0.0/0"}
	if cfg.Validate() == nil {
		t.Fatal("default-route client restriction accepted")
	}
	cfg = testConfig(t)
	cfg.AllowedHosts = nil
	if cfg.Validate() == nil {
		t.Fatal("missing host restrictions accepted")
	}
	cfg = testConfig(t)
	cfg.AllowedHosts = []string{"*.example.com"}
	if cfg.Validate() == nil {
		t.Fatal("wildcard destination accepted")
	}
	cfg = testConfig(t)
	cfg.AllowedHosts = []string{"other.example"}
	if cfg.Validate() == nil {
		t.Fatal("probe outside destination list accepted")
	}
}

func TestDaemonRejectsClientAndDestinationWithoutForwarding(t *testing.T) {
	a, _ := newTestAgent(t)
	// Forwarding headers never authorize an untrusted socket peer.
	r := httptest.NewRequest(http.MethodPost, "/v1/leases", nil)
	r.RemoteAddr = "198.51.100.1:1234"
	r.Header.Set("Authorization", "Bearer "+a.config.ControlToken)
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	w := httptest.NewRecorder()
	a.ControlHandler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || a.Status().Issued != "0" {
		t.Fatalf("untrusted client allocated: %d", w.Code)
	}
	l, err := a.Create(r.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://unlisted.example/a", "http://upstream.example.evil/a"} {
		r = httptest.NewRequest(http.MethodGet, target, nil)
		r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(l.Username+":"+l.Password)))
		w = httptest.NewRecorder()
		a.ProxyHandler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("destination %q accepted: %d", target, w.Code)
		}
	}
	r = httptest.NewRequest(http.MethodConnect, "http://upstream.example:443", strings.NewReader(""))
	r.Host = "unlisted.example:443"
	r.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(l.Username+":"+l.Password)))
	w = httptest.NewRecorder()
	a.ProxyHandler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("CONNECT destination accepted: %d", w.Code)
	}
}
