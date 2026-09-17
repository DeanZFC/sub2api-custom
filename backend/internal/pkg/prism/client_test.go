package prism

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testConversation = "cdx1_11111111-2222-4333-8444-555555555555"
const testSandboxSession = "0123456789abcdef0123456789abcdef"
const testInput = `[{"type":"message","role":"user","content":[{"type":"input_text","text":"Synthetic test input"}]}]`

type rewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "prism.openai.com" {
		return nil, errors.New("unexpected origin")
	}
	clone := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = r.target.Scheme, r.target.Host
	clone.URL = &u
	resp, err := r.base.RoundTrip(clone)
	if resp != nil {
		resp.Request = req
	}
	return resp, err
}

// Every value here is synthetic. The fixture reproduces the captured protocol
// shapes, without copying credentials, text, project IDs, or user identities.
type fixture struct {
	t          *testing.T
	mu         sync.Mutex
	project    string
	projects   []string
	paths      []string
	polls      int
	startCount int
	workspace  string
	override   func(http.ResponseWriter, *http.Request) bool
}

func (f *fixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.Path)
	if r.URL.Path == "/api/llm/response_with_tools_start" {
		f.startCount++
	}
	if f.override != nil && f.override(w, r) {
		return
	}
	if cookie, err := r.Cookie("session"); err != nil || (cookie.Value != "old" && cookie.Value != "rotated") {
		f.t.Error("missing scoped cookie")
	}
	if r.Header.Get("Origin") != Origin {
		f.t.Error("missing origin")
	}
	if strings.HasPrefix(r.URL.Path, proxyPath) {
		if r.Header.Get("x-crixet-sandbox-token") != "synthetic-sandbox-token" {
			f.t.Error("missing sandbox token")
		}
		w.Header().Set("x-session-id", testSandboxSession)
	} else if r.Header.Get("x-crixet-sandbox-token") != "" {
		f.t.Error("sandbox token leaked to non-proxy endpoint")
	}
	w.Header().Set("Content-Type", "application/json")
	decode := func() map[string]any {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("decode fixture input: %v", err)
		}
		return body
	}
	write := func(v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			f.t.Errorf("encode fixture: %v", err)
		}
	}
	switch r.URL.Path {
	case "/auth/session":
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "rotated", Path: "/", Secure: true, HttpOnly: true})
		write(map[string]any{"user": map[string]any{"id": "synthetic-supabase-id", "is_anonymous": false, "app_metadata": map[string]string{"user_id": "synthetic-openai-user"}}, "policy": map[string]any{"user": map[string]string{"prism_user_id": "wrong-prism-id", "openai_user_id": "synthetic-openai-user"}}})
	case "/api/projects":
		body := decode()
		f.project, _ = body["project_uuid"].(string)
		if !uuidPattern.MatchString(f.project) {
			f.t.Error("invalid generated project UUID")
		}
		if cookie, _ := r.Cookie("session"); cookie == nil || cookie.Value != "rotated" {
			f.t.Error("cookie rotation was not retained")
		}
		f.projects = append(f.projects, f.project)
		f.polls = 0
		write(map[string]any{"uuid": f.project, "deleted": false})
	case "/api/project-access":
		if r.URL.Query().Get("d") != f.project {
			f.t.Error("wrong project access ID")
		}
		write(map[string]any{"accessible": true, "project": map[string]any{"uuid": f.project, "deleted": false}, "userRole": nil})
	case "/api/y":
		if decode()["docId"] != f.project {
			f.t.Error("wrong document ID")
		}
		write(map[string]string{"url": "wss://prism.openai.com/y/d/" + f.project + "/ws", "baseUrl": Origin + "/y/d/" + f.project, "docId": f.project, "token": "synthetic-y-token", "authorization": "full"})
	case "/api/backend/1/new":
		write(map[string]string{"url": Origin + proxyPath, "token": "synthetic-sandbox-token"})
	case "/":
		if r.Header.Get("Next-Action") != DefaultConversationActionID {
			f.t.Error("wrong Next action")
		}
		if r.Header.Get("Accept") != "text/x-component" {
			f.t.Error("wrong action accept")
		}
		if state, err := url.QueryUnescape(r.Header.Get("Next-Router-State-Tree")); err != nil || state != `["",{"children":["__PAGE__",{},null,null,4096]},null,null,4112]` {
			f.t.Error("wrong action router state")
		}
		var args []string
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil || len(args) != 1 || args[0] != f.project {
			f.t.Error("wrong conversation args")
		}
		fmt.Fprintf(w, "0:{\"a\":\"$@1\"}\n1:%q\n", testConversation)
	case proxyPath + "/heartbeat":
		w.WriteHeader(http.StatusOK)
	case "/api/projects/" + f.project + "/sandbox/resources-token":
		body := decode()
		if body["sandbox_session_id"] != testSandboxSession || body["sandbox_token"] != "synthetic-sandbox-token" {
			f.t.Error("resources did not use the heartbeat session")
		}
		write(map[string]string{"access_token": "synthetic-resource-token", "resources_base_url": Origin + "/s/sandbox-resources"})
	case proxyPath + "/resources-token":
		body := decode()
		if body["projectId"] != f.project || body["token"] != "synthetic-resource-token" || body["resourceBaseUrl"] != Origin+"/s/sandbox-resources/" {
			f.t.Error("invalid resource installation")
		}
		write(map[string]string{"status": "success"})
	case proxyPath + "/token":
		body := decode()
		if body["docId"] != f.project || body["token"] != "synthetic-y-token" || body["authorization"] != "full" {
			f.t.Error("document token not forwarded intact")
		}
		write(map[string]any{"success": true, "message": "token received"})
	case proxyPath + "/wait-for-sync":
		if r.URL.Query().Get("wait_ms") != "10000" {
			f.t.Error("missing sync wait")
		}
		write(map[string]any{"status": "synced", "readinessCapabilities": []string{"current_y_sweet_provider"}, "tokens": map[string]bool{"hasResourceToken": true, "hasResourceBaseUrl": true, "hasResourceProjectId": true, "hasCurrentYSweetToken": true, "hasSyncedYSweetProvider": true}})
	case "/api/llm/response_with_tools_start":
		body := decode()
		metadata, _ := body["metadata"].(map[string]any)
		if body["conversationId"] != testConversation || metadata["userId"] != "synthetic-openai-user" || metadata["sandbox_url"] != Origin+proxyPath+"/" || metadata["projectId"] != f.project || metadata["model"] != "test-model" {
			f.t.Error("wrong start metadata")
		}
		if _, ok := body["input"].([]any); !ok {
			f.t.Error("input was not forwarded as JSON")
		}
		state := testState(0)
		if f.workspace != "" {
			state["workspace_session_id"] = f.workspace
		}
		write(map[string]any{"status": "started", "request_id": "synthetic-request-id", "conversation_id": testConversation, "turn_state": state})
	case "/api/llm/response_with_tools_status":
		body := decode()
		state, _ := body["turn_state"].(map[string]any)
		if body["request_id"] != "synthetic-request-id" || state["transcript_cursor"] != float64(f.polls) || state["opaque"] != fmt.Sprintf("state-%d", f.polls) {
			f.t.Error("poll did not preserve latest opaque state")
		}
		f.polls++
		if f.polls == 1 {
			state := testState(1)
			if f.workspace != "" {
				state["workspace_session_id"] = f.workspace
			}
			write(map[string]any{"status": "pending", "request_id": "synthetic-request-id", "turn_state": state, "codex_live_progress": map[string]string{"private": "never return me"}})
			return
		}
		write(map[string]any{"status": "completed", "request_id": "synthetic-request-id", "response": map[string]any{"status": "success", "payload": map[string]any{"id": "resp_synthetic", "conversationId": testConversation, "output": []any{map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Synthetic answer", "annotations": []any{}, "private": "never return me"}}}}, "codexRequestDebug": map[string]string{"token": "never return me"}, "codexListenSnapshot": testState(2), "codexDeltaFiles": []any{"never return me"}}}})
	default:
		f.t.Errorf("unexpected route %s", r.URL.Path)
		w.WriteHeader(404)
	}
}

func testState(cursor int) map[string]any {
	return map[string]any{"version": 1, "conversation_id": testConversation, "workspace_session_id": strings.TrimPrefix(testConversation, "cdx1_"), "sandbox_url": "https://untrusted-returned-state.invalid/path", "sandbox_token": "synthetic-private-turn-token", "transcript_cursor": cursor, "opaque": fmt.Sprintf("state-%d", cursor)}
}

func newFixture(t *testing.T, override func(http.ResponseWriter, *http.Request) bool) (*Client, *fixture) {
	t.Helper()
	f := &fixture{t: t, override: override}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: rewriteTransport{target: u, base: http.DefaultTransport}}
	return NewClient(client, Options{Cookie: "session=old", PollInterval: time.Millisecond, Timeout: time.Second}), f
}

func generate(c *Client, ctx context.Context) (*Result, error) {
	return c.Generate(ctx, Request{Input: json.RawMessage(testInput), Model: "test-model", ReasoningEffort: "medium"})
}

func TestGenerateCapturedFlow(t *testing.T) {
	c, f := newFixture(t, nil)
	progress := 0
	c.opts.OnProgress = func() { progress++ }
	for range 2 {
		result, err := generate(c, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.ID != "resp_synthetic" || result.Text != "Synthetic answer" || result.ConversationID != testConversation || result.Usage != nil {
			t.Fatalf("unexpected public result: %#v", result)
		}
		if strings.Contains(string(result.Output), "private") || strings.Contains(string(result.Output), "never return") || strings.Contains(string(result.Output), "sandbox") {
			t.Fatal("private state escaped public output")
		}
	}
	if f.startCount != 2 || len(f.projects) != 2 || f.projects[0] == f.projects[1] || progress != 4 {
		t.Fatal("generations reused state or progress missing")
	}
}

func TestInitializationFailuresNeverSubmit(t *testing.T) {
	cases := []struct {
		name, path, body, stage string
		status                  int
	}{
		{"forbidden_html", "/auth/session", "<html>synthetic-secret</html>", "auth", 403},
		{"unknown_auth", "/auth/session", `{"user":{"app_metadata":{"user_id":""}}}`, "auth", 200},
		{"access_denied", "/api/project-access", `{"accessible":false}`, "project_access", 200},
		{"sandbox_wrong_origin", "/api/backend/1/new", `{"url":"https://untrusted.invalid/proxy","token":"synthetic-secret"}`, "sandbox", 200},
		{"document_wrong_origin", "/api/y", `{"url":"wss://untrusted.invalid/y/d/x","token":"synthetic-secret"}`, "document_token", 200},
		{"bad_action", "/", `1:"conv_11111111-2222-4333-8444-555555555555"`, "conversation", 200},
		{"not_synced", proxyPath + "/wait-for-sync", `{"status":"synced","tokens":{}}`, "sync", 200},
		{"install_failed", proxyPath + "/resources-token", `{"status":"unknown"}`, "resource_install", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != tc.path {
					return false
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
				return true
			})
			_, err := generate(c, context.Background())
			var pe *Error
			if !errors.As(err, &pe) || pe.Stage != tc.stage || pe.Submitted || f.startCount != 0 {
				t.Fatalf("unexpected failure: %v", err)
			}
			if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "<html>") {
				t.Fatal("unsafe error content")
			}
		})
	}
}

func TestSubmittedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"start_http_error", "/api/llm/response_with_tools_start", `private-token`, 503},
		{"unknown_terminal", "/api/llm/response_with_tools_status", `{"status":"new_terminal_state","request_id":"synthetic-request-id"}`, 200},
		{"invalid_pending_state", "/api/llm/response_with_tools_status", `{"status":"pending","request_id":"synthetic-request-id","turn_state":{"conversation_id":"other"}}`, 200},
		{"unknown_result", "/api/llm/response_with_tools_status", `{"status":"completed","request_id":"synthetic-request-id","response":{"status":"success","payload":{}}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, f := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != tc.path {
					return false
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
				return true
			})
			_, err := generate(c, context.Background())
			var pe *Error
			if !errors.As(err, &pe) || !pe.Submitted || f.startCount != 1 {
				t.Fatalf("unexpected submitted failure: %v", err)
			}
		})
	}
}

func TestCancellation(t *testing.T) {
	c, f := newFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	c.opts.OnProgress = cancel
	_, err := generate(c, ctx)
	var pe *Error
	if !errors.Is(err, context.Canceled) || !errors.As(err, &pe) || !pe.Submitted || f.startCount != 1 || f.polls != 0 {
		t.Fatalf("cancel did not stop polling: %v", err)
	}
}

func TestDeadlineDuringHTTP(t *testing.T) {
	c, _ := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/auth/session" {
			return false
		}
		<-r.Context().Done()
		return true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	_, err := generate(c, ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost deadline: %v", err)
	}
}

func TestRedirectNeverLeaks(t *testing.T) {
	var received atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(200) }))
	defer trap.Close()
	c, _ := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != proxyPath+"/resources-token" {
			return false
		}
		http.Redirect(w, r, trap.URL, http.StatusTemporaryRedirect)
		return true
	})
	_, err := generate(c, context.Background())
	var pe *Error
	if !errors.As(err, &pe) || pe.StatusCode != 307 || received.Load() != 0 {
		t.Fatalf("redirect was followed: %v", err)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("synthetic-private-body-error") }
func (brokenReader) Close() error             { return nil }

func TestTransportAndBodyErrorsSanitized(t *testing.T) {
	for _, tc := range []struct {
		name string
		doer Doer
	}{
		{"transport", doerFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("https://secret.invalid/?token=synthetic-secret")
		})},
		{"read", doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: brokenReader{}}, nil
		})},
		{"oversized", doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxBodyBytes+1)))}, nil
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := NewClient(tc.doer, Options{Cookie: "session=old"})
			_, err := generate(c, context.Background())
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "https") {
				t.Fatalf("unsafe error: %v", err)
			}
		})
	}
}

func TestInvalidInputsDoNotSend(t *testing.T) {
	c := NewClient(doerFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid input was sent"); return nil, nil }), Options{Cookie: "session=old"})
	for _, raw := range []string{`null`, `[]`, `[{"role":"user","content":null}]`, `[{"role":"tool","content":"x"}]`, `[{"role":"user","content":[{"type":"input_image","image_url":"https://image.invalid"}]}]`} {
		if _, err := c.Generate(context.Background(), Request{Input: json.RawMessage(raw), Model: "test-model"}); err == nil {
			t.Errorf("accepted invalid input %s", raw)
		}
	}
}

func TestUsageStrictAndFiltered(t *testing.T) {
	for _, tc := range []struct {
		name, usage string
		valid       bool
	}{
		{"missing", "", true},
		{"null", `null`, true},
		{"full", `{"input_tokens":10,"output_tokens":20,"total_tokens":30,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":5},"private":"never return"}`, true},
		{"unexpected", `{"unexpected":"never return"}`, false},
		{"partial", `{"input_tokens":10,"output_tokens":20}`, false},
		{"negative", `{"input_tokens":-1,"output_tokens":20,"total_tokens":19}`, false},
		{"bad_cache", `{"input_tokens":10,"output_tokens":20,"total_tokens":30,"input_tokens_details":{"cached_tokens":11}}`, false},
		{"bad_reasoning", `{"input_tokens":10,"output_tokens":20,"total_tokens":30,"output_tokens_details":{"reasoning_tokens":-1}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"id": "resp_synthetic", "conversationId": testConversation, "output": []any{map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "answer"}}}}}
			if tc.usage != "" {
				payload["usage"] = json.RawMessage(tc.usage)
			}
			raw, _ := json.Marshal(map[string]any{"status": "success", "payload": payload})
			result, err := parseResult(raw, testConversation)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected usage validation: %v", err)
			}
			if !tc.valid {
				return
			}
			if tc.name == "full" {
				if !strings.Contains(string(result.Usage), `"cached_tokens":4`) || !strings.Contains(string(result.Usage), `"reasoning_tokens":5`) || strings.Contains(string(result.Usage), "private") {
					t.Fatal("usage was not filtered or cache counters lost")
				}
			} else if result.Usage != nil {
				t.Fatal("missing usage was invented")
			}
		})
	}
}

func TestStartTransportFailureIsNeverRetried(t *testing.T) {
	c, _ := newFixture(t, nil)
	base := c.doer
	calls := 0
	c.doer = doerFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/llm/response_with_tools_start" {
			calls++
			return nil, errors.New("synthetic-private-start-error")
		}
		return base.Do(r)
	})
	_, err := generate(c, context.Background())
	var pe *Error
	if !errors.As(err, &pe) || !pe.Submitted || pe.Stage != "start" || calls != 1 || strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected start transport behavior: %v", err)
	}
}

func TestInputNormalizesFullHistoryToCapturedTextShape(t *testing.T) {
	raw := json.RawMessage(`[{"role":"system","content":"system text"},{"role":"user","content":[{"type":"input_text","text":"earlier user"}]},{"role":"assistant","content":[{"type":"output_text","text":"earlier answer"}]},{"role":"user","content":"follow up"}]`)
	normalized, ok := normalizeInput(raw)
	if !ok {
		t.Fatal("valid text history was rejected")
	}
	var messages []struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(normalized, &messages); err != nil {
		t.Fatal(err)
	}
	wantRoles := []string{"system", "user", "assistant", "user"}
	wantText := []string{"system text", "earlier user", "earlier answer", "follow up"}
	if len(messages) != 4 {
		t.Fatal("history was truncated")
	}
	for i, m := range messages {
		if m.Type != "message" || m.Role != wantRoles[i] || len(m.Content) != 1 || m.Content[0].Type != "input_text" || m.Content[0].Text != wantText[i] {
			t.Fatal("history role or text changed")
		}
	}
}

func TestWorkspaceSessionIndependentFromConversation(t *testing.T) {
	c, f := newFixture(t, nil)
	f.workspace = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	result, err := generate(c, context.Background())
	if err != nil || result == nil || result.Text != "Synthetic answer" {
		t.Fatalf("independent workspace UUID was rejected: %v", err)
	}
}

func TestWorkspaceSessionMustRemainStable(t *testing.T) {
	c, _ := newFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/llm/response_with_tools_status" {
			return false
		}
		state := testState(1)
		state["workspace_session_id"] = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		json.NewEncoder(w).Encode(map[string]any{"status": "pending", "request_id": "synthetic-request-id", "turn_state": state})
		return true
	})
	_, err := generate(c, context.Background())
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != "invalid_turn_state" || !pe.Submitted {
		t.Fatalf("workspace switch was accepted: %v", err)
	}
}

type canceledBody struct{ ctx context.Context }

func (b canceledBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, errors.New("synthetic-private-read-cancellation")
}
func (canceledBody) Close() error { return nil }

func TestBodyReadCancellationPreservesContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprintf("deadline_%t", deadline), func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			want := context.Canceled
			if deadline {
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			doer := doerFunc(func(r *http.Request) (*http.Response, error) {
				if !deadline {
					cancel()
				}
				return &http.Response{StatusCode: 200, Body: canceledBody{ctx: r.Context()}, Request: r}, nil
			})
			c := NewClient(doer, Options{Cookie: "session=old"})
			_, err := generate(c, ctx)
			var pe *Error
			if !errors.Is(err, want) || !errors.As(err, &pe) || pe.Code != "canceled" || strings.Contains(err.Error(), "private") {
				t.Fatalf("body cancellation lost its safe context cause: %v", err)
			}
		})
	}
}

func TestResultRejectsIncompleteMessage(t *testing.T) {
	for _, status := range []string{"in_progress", "incomplete", "failed"} {
		raw, _ := json.Marshal(map[string]any{"status": "success", "payload": map[string]any{"id": "resp_synthetic", "conversationId": testConversation, "output": []any{map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "status": status, "content": []any{map[string]string{"type": "output_text", "text": "partial"}}}}}})
		if _, err := parseResult(raw, testConversation); err == nil {
			t.Fatalf("status %s was reported as completed", status)
		}
	}
}
