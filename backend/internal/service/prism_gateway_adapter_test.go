//go:build unit

package service

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
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prism"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const prismGatewayConversation = "cdx1_11111111-2222-4333-8444-555555555555"
const prismGatewayCookie = "prism_oai_access_token=synthetic-private-access; prism_session_token=synthetic-private-session"
const prismGatewayAnswer = "Synthetic gateway answer"

// This transport is entirely in memory: even an accidental route change cannot
// reach a real server. Only the protocol shapes come from the captured HAR.
type prismGatewayUpstream struct {
	t                    *testing.T
	account              *Account
	project              string
	calls, starts, polls int
	wantRoles, wantTexts []string
	wantEffort           string
	usage                json.RawMessage
	failurePath          string
	failureStatus        int
	transportFailure     bool
	unknownTerminal      bool
	startBody            map[string]any
	accountAuthToken     string
	rejectAccountAuth    bool
	completedStart       bool
	completedStartError  bool
}

func (u *prismGatewayUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.t.Error("Prism bypassed the configured TLS transport")
	return nil, errors.New("unexpected transport")
}

func (u *prismGatewayUpstream) DoWithTLS(r *http.Request, proxy string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	if r.URL.Scheme != "https" || r.URL.Host != "prism.openai.com" {
		u.t.Error("request escaped fixed origin")
	}
	if proxy != u.account.Proxy.URL() || accountID != u.account.ID || concurrency != u.account.Concurrency {
		u.t.Error("account proxy, identity, or concurrency was lost")
	}
	if profile == nil {
		u.t.Error("account TLS profile was lost")
	}
	if !HTTPUpstreamRedirectsDisabled(r.Context()) || upstreamErrorRetryFromContext(r.Context()) != nil || !AccountProtectionOutcomeExcluded(r.Context()) {
		u.t.Error("private protocol transport policy was lost")
	}
	if r.Header.Get("User-Agent") != prismBrowserUserAgent || r.Header.Get("Authorization") != "" {
		u.t.Error("wrong upstream authentication headers")
	}
	if u.accountAuthToken != "" {
		access, err := r.Cookie("prism_oai_access_token")
		if err != nil || access.Value != u.accountAuthToken {
			u.t.Error("Prism did not use the account token provider")
		}
		if r.URL.Path == "/auth/session" {
			if len(r.Cookies()) != 1 {
				u.t.Error("manual credentials mixed with automatic authentication")
			}
		} else if session, err := r.Cookie("prism_session_token"); err != nil || session.Value != "synthetic-issued-session" {
			u.t.Error("automatically issued session was not retained")
		}
	} else if r.Header.Get("Cookie") != prismGatewayCookie {
		u.t.Error("wrong manual upstream authentication cookie")
	}
	if r.Header.Get("X-Downstream-Private") != "" {
		u.t.Error("downstream headers were forwarded")
	}
	if r.URL.Path == prismUpstreamEndpoint {
		u.starts++
	}
	if r.URL.Path == u.failurePath {
		if u.transportFailure {
			return nil, errors.New("synthetic-private-transport-error")
		}
		return &http.Response{StatusCode: u.failureStatus, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader("<html>synthetic-private-upstream-error</html>")), Request: r}, nil
	}
	header := http.Header{"Content-Type": []string{"application/json"}}
	if strings.HasPrefix(r.URL.Path, "/s/sandboxes/proxy") {
		if r.Header.Get("x-crixet-sandbox-token") != "synthetic-private-sandbox" {
			u.t.Error("sandbox token missing")
		}
		header.Set("x-session-id", "0123456789abcdef0123456789abcdef")
	}
	decode := func() map[string]any {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			u.t.Errorf("invalid fixture request: %v", err)
		}
		return body
	}
	var data any
	var raw string
	switch r.URL.Path {
	case "/auth/session":
		if u.accountAuthToken != "" {
			header.Set("Set-Cookie", "prism_session_token=synthetic-issued-session; Path=/; Secure; HttpOnly")
		}
		data = map[string]any{"user": map[string]any{"is_anonymous": false, "app_metadata": map[string]string{"user_id": "synthetic-user"}}, "policy": map[string]any{"user": map[string]string{"openai_user_id": "synthetic-user", "prism_user_id": "different-prism-user"}}}
		if u.rejectAccountAuth {
			data = map[string]any{"user": nil}
		}
	case "/api/projects":
		body := decode()
		u.project, _ = body["project_uuid"].(string)
		data = map[string]any{"uuid": u.project, "deleted": false}
	case "/api/project-access":
		if r.URL.Query().Get("d") != u.project {
			u.t.Error("incorrect project access binding")
		}
		data = map[string]any{"accessible": true, "project": map[string]any{"uuid": u.project, "deleted": false}}
	case "/api/y":
		if decode()["docId"] != u.project {
			u.t.Error("incorrect document binding")
		}
		data = map[string]string{"url": "wss://prism.openai.com/y/d/" + u.project + "/ws", "baseUrl": prism.Origin + "/y/d/" + u.project, "docId": u.project, "token": "synthetic-private-y", "authorization": "full"}
	case "/api/backend/1/new":
		data = map[string]string{"url": prism.Origin + "/s/sandboxes/proxy", "token": "synthetic-private-sandbox"}
	case "/":
		if r.Header.Get("Next-Action") != prism.DefaultConversationActionID {
			u.t.Error("incorrect conversation action")
		}
		raw = fmt.Sprintf("0:{\"a\":\"$@1\"}\n1:%q\n", prismGatewayConversation)
	case "/s/sandboxes/proxy/heartbeat":
		raw = ""
	case "/api/projects/" + u.project + "/sandbox/resources-token":
		body := decode()
		if body["sandbox_session_id"] != "0123456789abcdef0123456789abcdef" {
			u.t.Error("resource token did not use sandbox session")
		}
		data = map[string]string{"access_token": "synthetic-private-resource", "resources_base_url": prism.Origin + "/s/sandbox-resources"}
	case "/s/sandboxes/proxy/resources-token":
		data = map[string]string{"status": "success"}
	case "/s/sandboxes/proxy/token":
		data = map[string]bool{"success": true}
	case "/s/sandboxes/proxy/wait-for-sync":
		data = map[string]any{"status": "synced", "readinessCapabilities": []string{"current_y_sweet_provider"}, "tokens": map[string]bool{"hasResourceToken": true, "hasResourceBaseUrl": true, "hasResourceProjectId": true, "hasCurrentYSweetToken": true, "hasSyncedYSweetProvider": true}}
	case prismUpstreamEndpoint:
		u.startBody = decode()
		metadata, _ := u.startBody["metadata"].(map[string]any)
		if metadata["model"] != "gpt-5.6-sol" || metadata["reasoning_effort"] != u.wantEffort || metadata["userId"] != "synthetic-user" {
			u.t.Error("upstream model, effort, or user mapping changed")
		}
		var roles, texts []string
		messages, _ := u.startBody["input"].([]any)
		for _, rawMessage := range messages {
			message, _ := rawMessage.(map[string]any)
			role, _ := message["role"].(string)
			roles = append(roles, role)
			var text strings.Builder
			parts, _ := message["content"].([]any)
			if message["type"] != "message" || len(parts) == 0 {
				u.t.Error("input did not use captured message shape")
			}
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if part["type"] != "input_text" {
					u.t.Error("input was not plain text")
				}
				value, _ := part["text"].(string)
				text.WriteString(value)
			}
			texts = append(texts, text.String())
		}
		if !reflect.DeepEqual(roles, u.wantRoles) || !reflect.DeepEqual(texts, u.wantTexts) {
			u.t.Errorf("full conversation changed: roles=%v texts=%v", roles, texts)
		}
		data = map[string]any{"status": "started", "request_id": "synthetic-request", "conversation_id": prismGatewayConversation, "turn_state": map[string]any{"conversation_id": prismGatewayConversation, "workspace_session_id": strings.TrimPrefix(prismGatewayConversation, "cdx1_"), "private": "synthetic-private-turn"}}
		if u.completedStartError {
			data = map[string]any{"status": "completed", "request_id": "synthetic-request", "response": map[string]any{"status": "error", "payload": map[string]any{"reason": "unknown", "httpStatus": 400, "message": "synthetic-private-prompt", "rootCause": "synthetic-private-token"}}}
		} else if u.completedStart {
			data = map[string]any{"status": "completed", "request_id": "synthetic-request", "response": map[string]any{"status": "success", "payload": map[string]any{"id": "resp_immediate", "conversationId": prismGatewayConversation, "output": []any{map[string]any{"id": "msg_immediate", "type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": prismGatewayAnswer}}}}}}}
		}
	case "/api/llm/response_with_tools_status":
		u.polls++
		body := decode()
		if body["request_id"] != "synthetic-request" {
			u.t.Error("poll lost request binding")
		}
		if u.unknownTerminal {
			data = map[string]string{"status": "unknown-terminal", "request_id": "synthetic-request"}
			break
		}
		payload := map[string]any{"id": "resp_upstream", "conversationId": prismGatewayConversation, "output": []any{map[string]any{"id": "msg_synthetic", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": prismGatewayAnswer, "annotations": []any{}, "private": "synthetic-private-output"}}}}, "codexRequestDebug": map[string]string{"token": "synthetic-private-debug"}, "codexListenSnapshot": map[string]string{"token": "synthetic-private-snapshot"}, "codexDeltaFiles": []string{"synthetic-private-file"}}
		if u.usage != nil {
			payload["usage"] = u.usage
		}
		data = map[string]any{"status": "completed", "request_id": "synthetic-request", "response": map[string]any{"status": "success", "payload": payload}}
	default:
		u.t.Errorf("unexpected Prism endpoint: %s", r.URL.Path)
		return nil, errors.New("unexpected endpoint")
	}
	if data != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		raw = string(encoded)
	}
	return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
}

func newPrismGatewayFixture(t *testing.T) (*OpenAIGatewayService, *Account, *prismGatewayUpstream) {
	t.Helper()
	proxyID := int64(37)
	account := &Account{ID: 19, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, ProxyID: &proxyID, Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "synthetic-proxy.invalid", Port: 8080}, Credentials: map[string]any{PrismCookieCredentialKey: prismGatewayCookie, "model_mapping": map[string]any{"client-model": "gpt-5.6-sol"}}, Extra: map[string]any{PrismExtraKey: map[string]any{"enabled": true, "version": 1}}}
	upstream := &prismGatewayUpstream{t: t, account: account, wantEffort: "high", wantRoles: []string{"user"}, wantTexts: []string{"Synthetic question"}}
	return &OpenAIGatewayService{httpUpstream: upstream, cfg: &config.Config{}}, account, upstream
}

func prismGatewayContext(body, path string) (context.Context, *gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), upstreamErrorRetryContextKey{}, &upstreamErrorRetryState{})
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).WithContext(ctx)
	c.Request.Header.Set("Authorization", "Bearer synthetic-private-client-token")
	c.Request.Header.Set("Cookie", "synthetic-private-downstream-cookie")
	c.Request.Header.Set("X-Downstream-Private", "synthetic-private-client-header")
	return ctx, c, recorder
}

func prismGatewayAssertPrivateAbsent(t *testing.T, body string) {
	t.Helper()
	for _, value := range []string{"synthetic-private", prismGatewayConversation, "codexRequestDebug", "codexListenSnapshot", "codexDeltaFiles", "turn_state", "sandbox_token"} {
		require.NotContains(t, body, value)
	}
}

func TestPrismGatewayBufferedAdapters(t *testing.T) {
	for _, chat := range []bool{false, true} {
		t.Run(fmt.Sprintf("chat_%t", chat), func(t *testing.T) {
			svc, account, upstream := newPrismGatewayFixture(t)
			upstream.wantRoles = []string{"system", "user", "assistant", "user"}
			upstream.wantTexts = []string{"Synthetic instructions", "Earlier question", "Earlier answer", "Synthetic question"}
			body := `{"model":"client-model","instructions":"Synthetic instructions","reasoning":{"effort":"high"},"input":[{"role":"user","content":"Earlier question"},{"id":"msg_prior","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Earlier answer","annotations":[]}]},{"role":"user","content":"Synthetic question"}]}`
			path := "/v1/responses"
			if chat {
				path = "/v1/chat/completions"
				body = `{"model":"client-model","reasoning_effort":"high","messages":[{"role":"system","content":"Synthetic instructions"},{"role":"user","content":"Earlier question"},{"role":"assistant","content":"Earlier answer"},{"role":"user","content":[{"type":"text","text":"Synthetic question"}]}]}`
			}
			ctx, c, rec := prismGatewayContext(body, path)
			var result *OpenAIForwardResult
			var err error
			if chat {
				result, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "wrong-fallback-model")
			} else {
				result, err = svc.Forward(ctx, c, account, []byte(body))
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 200, rec.Code)
			require.Equal(t, 1, upstream.starts)
			require.Equal(t, 1, upstream.polls)
			require.Equal(t, "client-model", result.Model)
			require.Equal(t, "gpt-5.6-sol", result.UpstreamModel)
			require.Equal(t, "high", *result.ReasoningEffort)
			require.True(t, result.UsageUnavailable)
			require.Zero(t, result.Usage.InputTokens)
			require.Zero(t, result.Usage.OutputTokens)
			require.Equal(t, prismUpstreamEndpoint, result.UpstreamEndpoint)
			require.Equal(t, "unavailable", rec.Header().Get("X-Sub2api-Usage-Source"))
			var payload map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
			usage, present := payload["usage"]
			require.True(t, present)
			require.Nil(t, usage)
			require.Equal(t, "client-model", payload["model"])
			require.Contains(t, rec.Body.String(), prismGatewayAnswer)
			prismGatewayAssertPrivateAbsent(t, rec.Body.String())
		})
	}
}

func prismGatewaySSEData(t *testing.T, body string) ([]map[string]any, int) {
	t.Helper()
	var events []map[string]any
	done := 0
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		if raw == "[DONE]" {
			done++
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		events = append(events, event)
	}
	return events, done
}

func TestPrismGatewayStreamingAdapters(t *testing.T) {
	for _, chat := range []bool{false, true} {
		t.Run(fmt.Sprintf("chat_%t", chat), func(t *testing.T) {
			svc, account, upstream := newPrismGatewayFixture(t)
			body := `{"model":"client-model","reasoning":{"effort":"high"},"input":"Synthetic question","stream":true}`
			path := "/v1/responses"
			if chat {
				path = "/v1/chat/completions"
				body = `{"model":"client-model","reasoning_effort":"high","messages":[{"role":"user","content":"Synthetic question"}],"stream":true,"stream_options":{"include_usage":true}}`
			}
			ctx, c, rec := prismGatewayContext(body, path)
			var result *OpenAIForwardResult
			var err error
			if chat {
				result, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
			} else {
				result, err = svc.Forward(ctx, c, account, []byte(body))
			}
			require.NoError(t, err)
			require.True(t, result.Stream)
			require.True(t, result.UsageUnavailable)
			require.Equal(t, 1, upstream.starts)
			require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
			prismGatewayAssertPrivateAbsent(t, rec.Body.String())
			events, done := prismGatewaySSEData(t, rec.Body.String())
			if chat {
				require.Len(t, events, 4)
				require.Equal(t, 1, done)
				for _, event := range events {
					require.Equal(t, "chat.completion.chunk", event["object"])
					require.Equal(t, "client-model", event["model"])
				}
				last := events[len(events)-1]
				usage, present := last["usage"]
				require.True(t, present)
				require.Nil(t, usage)
				require.Empty(t, last["choices"])
				require.Contains(t, rec.Body.String(), `"finish_reason":"stop"`)
			} else {
				want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.output_text.done", "response.content_part.done", "response.output_item.done", "response.completed"}
				require.Len(t, events, len(want))
				require.Zero(t, done)
				for i, event := range events {
					require.Equal(t, want[i], event["type"])
					require.Equal(t, float64(i), event["sequence_number"])
				}
				completed := events[len(events)-1]["response"].(map[string]any)
				require.Nil(t, completed["usage"])
				require.Equal(t, "completed", completed["status"])
			}
		})
	}
}

func TestPrismGatewayFailuresNeverResubmitOrLeak(t *testing.T) {
	for _, tc := range []struct {
		name, path                 string
		status                     int
		transport, unknown, stream bool
		starts                     int
	}{
		{"bootstrap_html", "/auth/session", 403, false, false, false, 0},
		{"start_http", prismUpstreamEndpoint, 503, false, false, false, 1},
		{"start_transport", prismUpstreamEndpoint, 0, true, false, false, 1},
		{"unknown_terminal", "", 0, false, true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account, upstream := newPrismGatewayFixture(t)
			upstream.failurePath = tc.path
			upstream.failureStatus = tc.status
			upstream.transportFailure = tc.transport
			upstream.unknownTerminal = tc.unknown
			body := fmt.Sprintf(`{"model":"client-model","reasoning":{"effort":"high"},"input":"Synthetic question","stream":%t}`, tc.stream)
			ctx, c, rec := prismGatewayContext(body, "/v1/responses")
			result, err := svc.Forward(ctx, c, account, []byte(body))
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, tc.starts, upstream.starts)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			prismGatewayAssertPrivateAbsent(t, rec.Body.String())
			prismGatewayAssertPrivateAbsent(t, err.Error())
			if tc.stream {
				events, _ := prismGatewaySSEData(t, rec.Body.String())
				require.Len(t, events, 3)
				require.Equal(t, "response.created", events[0]["type"])
				require.Equal(t, "response.in_progress", events[1]["type"])
				require.Equal(t, "response.failed", events[2]["type"])
				require.NotContains(t, rec.Body.String(), "response.completed")
			} else {
				require.Equal(t, 502, rec.Code)
			}
		})
	}
}

func TestPrismGatewayRejectsUnsupportedBeforeNetwork(t *testing.T) {
	for _, tc := range []struct{ name, extra, path string }{
		{"tools", `,"tools":[{"type":"function","name":"test"}]`, "/v1/responses"},
		{"previous_response", `,"previous_response_id":"resp_prior"`, "/v1/responses"},
		{"json_schema", `,"text":{"format":{"type":"json_schema"}}`, "/v1/responses"},
		{"compact", "", "/v1/responses/compact"},
		{"unknown", `,"unexpected_private_parameter":"synthetic-private-value"`, "/v1/responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, account, upstream := newPrismGatewayFixture(t)
			body := `{"model":"client-model","input":"Synthetic question"` + tc.extra + `}`
			ctx, c, rec := prismGatewayContext(body, tc.path)
			_, err := svc.Forward(ctx, c, account, []byte(body))
			require.Error(t, err)
			require.Equal(t, 400, rec.Code)
			require.Zero(t, upstream.calls)
			prismGatewayAssertPrivateAbsent(t, rec.Body.String())
		})
	}
	for _, body := range []string{`{"model":"client-model","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://synthetic.invalid"}]}]}`, `{"model":"client-model","messages":[{"role":"tool","content":"tool result","tool_call_id":"tool_1"}]}`} {
		svc, account, upstream := newPrismGatewayFixture(t)
		ctx, c, rec := prismGatewayContext(body, "/v1/chat/completions")
		var err error
		if strings.Contains(body, `"messages"`) {
			_, err = svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
		} else {
			_, err = svc.Forward(ctx, c, account, []byte(body))
		}
		require.Error(t, err)
		require.Equal(t, 400, rec.Code)
		require.Zero(t, upstream.calls)
	}
}

type prismGatewayAccountRepo struct {
	AccountRepository
	account *Account
}

func (r prismGatewayAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestPrismAccountConnectionUsesGatewayProtocol(t *testing.T) {
	_, account, upstream := newPrismGatewayFixture(t)
	svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}, accountRepo: prismGatewayAccountRepo{account: account}}
	ctx, c, rec := prismGatewayContext("", "/api/admin/accounts/19/test")
	c.Request = c.Request.WithContext(withAccountTestReasoningEffort(ctx, "high"))
	require.NoError(t, svc.TestAccountConnection(c, account.ID, "client-model", "Synthetic question", "text"))
	require.Equal(t, 1, upstream.starts)
	prismGatewayAssertPrivateAbsent(t, rec.Body.String())
	events, done := prismGatewaySSEData(t, rec.Body.String())
	require.Zero(t, done)
	require.Len(t, events, 4)
	require.Equal(t, "test_start", events[0]["type"])
	require.Equal(t, "status", events[1]["type"])
	require.Equal(t, prismGatewayAnswer, events[2]["text"])
	require.Equal(t, "test_complete", events[3]["type"])
	require.Equal(t, true, events[3]["success"])
}

func TestPrismGatewayReportedUsagePreservesCache(t *testing.T) {
	svc, account, upstream := newPrismGatewayFixture(t)
	upstream.usage = json.RawMessage(`{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":4}}`)
	body := `{"model":"client-model","reasoning_effort":"high","messages":[{"role":"user","content":"Synthetic question"}]}`
	ctx, c, rec := prismGatewayContext(body, "/v1/chat/completions")
	result, err := svc.ForwardAsChatCompletions(ctx, c, account, []byte(body), "", "")
	require.NoError(t, err)
	require.False(t, result.UsageUnavailable)
	require.Equal(t, 10, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 4, result.Usage.CacheReadInputTokens)
	require.Equal(t, "upstream", rec.Header().Get("X-Sub2api-Usage-Source"))
	require.Contains(t, rec.Body.String(), `"cached_tokens":4`)
}
