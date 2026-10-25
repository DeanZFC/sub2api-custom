package vmbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func authorizedRequest(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testSecret)
	return r
}

func TestManagementAuthenticationAndRefreshTokenRejection(t *testing.T) {
	m := newTestManager(t, &testProvider{})
	h := m.Handler()
	for _, tc := range []struct {
		path, auth string
		status     int
	}{
		{"/health", "", 200}, {"/v1/health", "", 401}, {"/v1/health", "Bearer wrong", 401}, {"/v1/health", "Bearer " + testSecret, 200}, {"/v1/accounts/1", "", 401},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		r.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s returned %d", tc.path, w.Code)
		}
		if tc.status == 200 && strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
			t.Fatal("health exposed additional state")
		}
	}
	for _, body := range []string{
		`{"name":"account","access_token":"AT","chatgpt_account_id":"chatgpt-1","refresh_token":"RT-never-accept"}`,
		`{"name":"account","access_token":"AT","chatgpt_account_id":"chatgpt-1"} {}`,
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/accounts/1", body))
		if w.Code != 400 {
			t.Fatal("accepted unknown token field or trailing JSON")
		}
		if strings.Contains(w.Body.String(), "RT-never-accept") {
			t.Fatal("echoed secret")
		}
	}
	_, err := m.Status(1)
	requireCode(t, err, "account_not_found")
	body, _ := json.Marshal(testInput(1))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/accounts/1", string(body)))
	if w.Code != 201 {
		t.Fatalf("create=%d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authorizedRequest(http.MethodPost, "/v1/accounts/1", string(body)))
	if w.Code != 200 {
		t.Fatal("idempotent create did not reuse")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, authorizedRequest(http.MethodDelete, "/v1/accounts/1", ""))
	if w.Code != 204 {
		t.Fatal("delete failed")
	}
}

func TestResponsesPreservePayloadStatusAndUsageAndStripBridgeCredentials(t *testing.T) {
	payload := `{ "model": "gpt-test", "input": [{"role":"user","content":"keep spacing"}], "stream":false }`
	upstreamBody := `{"error":{"message":"quota"},"usage":{"input_tokens":23,"output_tokens":9}}`
	var updates atomic.Int32
	p := &testProvider{identity: func(_ context.Context, r Resources, i Identity) error {
		if r.VMID != "vm-1" || i.AccessToken != "latest-access-token" || i.ChatGPTAccountID != "chatgpt-1" {
			t.Error("wrong account identity update")
		}
		updates.Add(1)
		return nil
	}, forward: func(_ context.Context, r Resources, req *http.Request) (*http.Response, error) {
		if updates.Load() != 1 || r.APIKey != "private-key-1" || req.URL.Path != "/v1/responses" {
			t.Error("identity was not synchronized before account-scoped request")
		}
		for _, name := range []string{"Authorization", AccessTokenHeader, AccountIdentityHeader, "Proxy-Authorization", "Connection", "X-Hop"} {
			if req.Header.Get(name) != "" {
				t.Errorf("leaked header %s", name)
			}
		}
		actual, _ := io.ReadAll(req.Body)
		if string(actual) != payload {
			t.Error("inference body changed")
		}
		return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}, "X-Upstream": {"keep"}, "Set-Cookie": {"private=secret"}, "Connection": {"X-Private"}, "X-Private": {"drop"}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}, nil
	}}
	m := newTestManager(t, p)
	provisionAccount(t, m, 1)
	r := authorizedRequest(http.MethodPost, "/v1/accounts/1/responses", payload)
	r.Header.Set(AccessTokenHeader, "latest-access-token")
	r.Header.Set(AccountIdentityHeader, "chatgpt-1")
	r.Header.Set("Proxy-Authorization", "secret")
	r.Header.Set("Connection", "X-Hop")
	r.Header.Set("X-Hop", "private")
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, r)
	if w.Code != 429 || w.Body.String() != upstreamBody || w.Header().Get("X-Upstream") != "keep" {
		t.Fatalf("response changed: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Private") != "" {
		t.Fatal("unsafe response headers forwarded")
	}
	status, _ := m.Status(1)
	if status.ActiveLeases != 0 {
		t.Fatal("response did not release lease")
	}
}

func TestStreamingFlushAndClientCancellationReleaseLease(t *testing.T) {
	upstreamCancelled := make(chan struct{})
	firstChunk := "data: {\"type\":\"response.created\"}\n\n"
	p := &testProvider{forward: func(ctx context.Context, _ Resources, _ *http.Request) (*http.Response, error) {
		r, w := io.Pipe()
		go func() {
			defer func() { _ = w.Close() }()
			_, _ = io.WriteString(w, firstChunk)
			<-ctx.Done()
			close(upstreamCancelled)
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}, nil
	}}
	m := newTestManager(t, p)
	provisionAccount(t, m, 1)
	server := httptest.NewServer(m.Handler())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/accounts/1/responses", strings.NewReader(`{"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testSecret)
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "data: ") {
		t.Fatalf("stream was buffered: %q %v", line, err)
	}
	status, _ := m.Status(1)
	if status.ActiveLeases != 1 {
		t.Fatal("stream lease missing")
	}
	cancel()
	_ = response.Body.Close()
	waitSignal(t, upstreamCancelled)
	deadline := time.Now().Add(time.Second)
	for {
		status, _ = m.Status(1)
		if status.ActiveLeases == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled stream leaked lease")
		}
		time.Sleep(time.Millisecond)
	}
}
