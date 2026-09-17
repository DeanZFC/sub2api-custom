// Package prism implements the private Prism protocol observed in the supplied
// HAR. It is deliberately limited to isolated, text-only generations.
package prism

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	Origin                      = "https://prism.openai.com"
	DefaultConversationActionID = "60da882267cc0bc7eb416a223823671858b83d4b0d"
	proxyPath                   = "/s/sandboxes/proxy"
	maxBodyBytes                = 8 << 20
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var actionPattern = regexp.MustCompile(`^[0-9a-f]{42}$`)
var sessionPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Doer implementations must honor request cancellation and must not follow
// redirects. NewClient enforces that policy for a standard *http.Client.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	Cookie string
	// AccessTokenProvider enables account authentication. The token is sent only
	// as Prism's OpenAI cookie; /auth/session issues the separate Prism session.
	// A supplied Cookie is ignored in this mode, so identities cannot be mixed.
	AccessTokenProvider  func(context.Context) (string, error)
	ExpectedOpenAIUserID string
	UserAgent            string
	ConversationActionID string
	Timeout              time.Duration
	PollInterval         time.Duration
	// OnProgress reports only progress; private upstream state never leaves this package.
	OnProgress func()
}

type Request struct {
	Input           json.RawMessage
	Model           string
	ReasoningEffort string
}

type Result struct {
	ID             string
	Output         json.RawMessage
	Text           string
	Usage          json.RawMessage
	ConversationID string
}

// Error is safe to log. It never includes response bodies, URLs, or credentials.
// Submitted means the start request was sent and must not be retried.
// Terminal means a matching completed envelope was received, so its outcome is
// no longer pending even when the result cannot be consumed.
type Error struct {
	Stage      string
	StatusCode int
	Submitted  bool
	Terminal   bool
	Code       string
	cause      error
}

func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("prism %s: %s (HTTP %d)", e.Stage, e.Code, e.StatusCode)
	}
	return "prism " + e.Stage + ": " + e.Code
}
func (e *Error) Unwrap() error { return e.cause }

type Client struct {
	doer Doer
	opts Options
	jar  http.CookieJar
}

func NewClient(doer Doer, opts Options) *Client {
	if doer == nil {
		doer = http.DefaultClient
	}
	if standard, ok := doer.(*http.Client); ok {
		clone := *standard
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		clone.Jar = nil // All cookies are scoped to the fixed Prism origin below.
		doer = &clone
	}
	if opts.ConversationActionID == "" {
		opts.ConversationActionID = DefaultConversationActionID
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Minute
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(Origin)
	cookieRequest := &http.Request{Header: http.Header{"Cookie": []string{opts.Cookie}}}
	jar.SetCookies(origin, cookieRequest.Cookies())
	return &Client{doer: doer, opts: opts, jar: jar}
}

func failure(stage, code string, submitted bool) *Error {
	return &Error{Stage: stage, Code: code, Submitted: submitted}
}

// Generate creates a fresh project, conversation, and sandbox for every call.
// Input must contain the complete caller-provided text conversation history.
func (c *Client) Generate(ctx context.Context, input Request) (*Result, error) {
	normalizedInput, valid := normalizeInput(input.Input)
	if !valid || strings.TrimSpace(input.Model) == "" || !actionPattern.MatchString(c.opts.ConversationActionID) || (strings.TrimSpace(c.opts.Cookie) == "" && c.opts.AccessTokenProvider == nil) {
		return nil, failure("input", "invalid_request", false)
	}
	input.Input = normalizedInput
	if input.ReasoningEffort == "" {
		input.ReasoningEffort = "medium"
	}
	ctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	s := &generation{client: c, jar: c.jar}
	if c.opts.AccessTokenProvider != nil {
		token, err := c.opts.AccessTokenProvider(ctx)
		if ctx.Err() != nil {
			return nil, &Error{Stage: "auth", Code: "canceled", cause: ctx.Err()}
		}
		if err != nil {
			// Refresh errors may contain provider response bodies. Never wrap them.
			return nil, failure("auth", "account_token_unavailable", false)
		}
		cookie := &http.Cookie{Name: "prism_oai_access_token", Value: token, Path: "/", Secure: true, HttpOnly: true}
		if token == "" || len(token) > 32*1024 || cookie.Valid() != nil {
			return nil, failure("auth", "invalid_account_token", false)
		}
		// Keep each generated session local to this request, including when a
		// caller reuses a Client after refreshing or replacing its account token.
		s.jar, _ = cookiejar.New(nil)
		origin, _ := url.Parse(Origin)
		s.jar.SetCookies(origin, []*http.Cookie{cookie})
	}
	var session struct {
		User struct {
			Anonymous   bool `json:"is_anonymous"`
			AppMetadata struct {
				UserID string `json:"user_id"`
			} `json:"app_metadata"`
		} `json:"user"`
		Policy struct {
			User struct {
				OpenAIUserID string `json:"openai_user_id"`
			} `json:"user"`
		} `json:"policy"`
	}
	if err := s.json(ctx, "auth", http.MethodGet, "/auth/session", nil, false, &session); err != nil {
		return nil, err
	}
	userID := session.User.AppMetadata.UserID
	if userID == "" || session.User.Anonymous || (session.Policy.User.OpenAIUserID != "" && session.Policy.User.OpenAIUserID != userID) {
		if c.opts.AccessTokenProvider != nil {
			return nil, failure("auth", "account_auth_rejected", false)
		}
		return nil, failure("auth", "invalid_identity", false)
	}
	if c.opts.AccessTokenProvider != nil {
		if c.opts.ExpectedOpenAIUserID != "" && c.opts.ExpectedOpenAIUserID != userID {
			return nil, failure("auth", "account_identity_mismatch", false)
		}
		origin, _ := url.Parse(Origin)
		hasSession := false
		for _, cookie := range s.jar.Cookies(origin) {
			if cookie.Name == "prism_session_token" && cookie.Value != "" {
				hasSession = true
			}
		}
		if !hasSession {
			return nil, failure("auth", "session_cookie_missing", false)
		}
	}
	projectID, err := newUUID()
	if err != nil {
		return nil, failure("project", "id_generation_failed", false)
	}
	var project struct {
		UUID    string `json:"uuid"`
		Deleted bool   `json:"deleted"`
	}
	if err := s.json(ctx, "project", http.MethodPost, "/api/projects", map[string]any{"project_uuid": projectID, "title": "API conversation", "file_uuids": []string{}}, false, &project); err != nil {
		return nil, err
	}
	if project.UUID != projectID || project.Deleted {
		return nil, failure("project", "invalid_response", false)
	}
	var access struct {
		Accessible bool `json:"accessible"`
		Project    struct {
			UUID    string `json:"uuid"`
			Deleted bool   `json:"deleted"`
		} `json:"project"`
	}
	if err := s.json(ctx, "project_access", http.MethodGet, "/api/project-access?d="+projectID, nil, false, &access); err != nil {
		return nil, err
	}
	if !access.Accessible || access.Project.UUID != projectID || access.Project.Deleted {
		return nil, failure("project_access", "access_denied", false)
	}
	var y struct {
		URL           string `json:"url"`
		BaseURL       string `json:"baseUrl"`
		DocID         string `json:"docId"`
		Token         string `json:"token"`
		Authorization string `json:"authorization"`
	}
	if err := s.json(ctx, "document_token", http.MethodPost, "/api/y", map[string]string{"docId": projectID}, false, &y); err != nil {
		return nil, err
	}
	if y.DocID != projectID || y.Token == "" || y.Authorization != "full" || y.URL != "wss://prism.openai.com/y/d/"+projectID+"/ws" || y.BaseURL != Origin+"/y/d/"+projectID {
		return nil, failure("document_token", "invalid_response", false)
	}
	var sandbox struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := s.json(ctx, "sandbox", http.MethodPost, "/api/backend/1/new", nil, false, &sandbox); err != nil {
		return nil, err
	}
	if sandbox.URL != Origin+proxyPath || sandbox.Token == "" {
		return nil, failure("sandbox", "invalid_response", false)
	}
	s.sandboxToken = sandbox.Token
	conversationBody, _ := json.Marshal([]string{projectID})
	conversationRaw, _, err := s.send(ctx, "conversation", http.MethodPost, "/?u="+projectID+"&pg=1&m=main.tex", conversationBody, false, true)
	if err != nil {
		return nil, err
	}
	conversationID := parseConversation(conversationRaw)
	if conversationID == "" {
		return nil, failure("conversation", "invalid_response", false)
	}
	if _, _, err := s.send(ctx, "heartbeat", http.MethodGet, proxyPath+"/heartbeat", nil, true, false); err != nil {
		return nil, err
	}
	if !sessionPattern.MatchString(s.sandboxSessionID) {
		return nil, failure("heartbeat", "missing_sandbox_session", false)
	}
	var resources struct {
		Token   string `json:"access_token"`
		BaseURL string `json:"resources_base_url"`
	}
	if err := s.json(ctx, "resource_token", http.MethodPost, "/api/projects/"+projectID+"/sandbox/resources-token", map[string]string{"sandbox_session_id": s.sandboxSessionID, "sandbox_token": s.sandboxToken}, false, &resources); err != nil {
		return nil, err
	}
	if resources.Token == "" || resources.BaseURL != Origin+"/s/sandbox-resources" {
		return nil, failure("resource_token", "invalid_response", false)
	}
	var resourceInstall struct {
		Status string `json:"status"`
	}
	if err := s.json(ctx, "resource_install", http.MethodPost, proxyPath+"/resources-token", map[string]string{"token": resources.Token, "resourceBaseUrl": resources.BaseURL + "/", "projectId": projectID}, true, &resourceInstall); err != nil {
		return nil, err
	}
	if resourceInstall.Status != "success" {
		return nil, failure("resource_install", "invalid_response", false)
	}
	var documentInstall struct {
		Success bool `json:"success"`
	}
	if err := s.json(ctx, "document_install", http.MethodPost, proxyPath+"/token", y, true, &documentInstall); err != nil {
		return nil, err
	}
	if !documentInstall.Success {
		return nil, failure("document_install", "invalid_response", false)
	}
	var sync struct {
		Status       string   `json:"status"`
		Capabilities []string `json:"readinessCapabilities"`
		Tokens       struct {
			Resource bool `json:"hasResourceToken"`
			BaseURL  bool `json:"hasResourceBaseUrl"`
			Project  bool `json:"hasResourceProjectId"`
			Document bool `json:"hasCurrentYSweetToken"`
			Synced   bool `json:"hasSyncedYSweetProvider"`
		} `json:"tokens"`
	}
	if err := s.json(ctx, "sync", http.MethodGet, proxyPath+"/wait-for-sync?wait_ms=10000", nil, true, &sync); err != nil {
		return nil, err
	}
	ready := false
	for _, capability := range sync.Capabilities {
		if capability == "current_y_sweet_provider" {
			ready = true
		}
	}
	if sync.Status != "synced" || !ready || !sync.Tokens.Resource || !sync.Tokens.BaseURL || !sync.Tokens.Project || !sync.Tokens.Document || !sync.Tokens.Synced {
		return nil, failure("sync", "not_ready", false)
	}
	metadata := map[string]string{"projectId": projectID, "userId": userID, "model": input.Model, "reasoning_effort": input.ReasoningEffort, "frontend_origin": Origin, "sandbox_url": Origin + proxyPath + "/", "sandbox_token": s.sandboxToken}
	var turn turnResponse
	// Even a transport error at this point can mean the server accepted the turn.
	s.submitted = true
	if err := s.json(ctx, "start", http.MethodPost, "/api/llm/response_with_tools_start", map[string]any{"input": input.Input, "metadata": metadata, "conversationId": conversationID}, false, &turn); err != nil {
		return nil, err
	}
	if strings.TrimSpace(turn.RequestID) == "" || (turn.ConversationID != "" && turn.ConversationID != conversationID) {
		return nil, failure("start", "invalid_response", true)
	}
	// Prism can finish synchronously, including rejecting a model before a
	// polling state exists. This is the same result envelope returned by polling.
	if turn.Status == "completed" {
		return parseCompletedResult(turn.Response, conversationID)
	}
	workspaceSessionID := turnWorkspaceSession(turn.State, conversationID)
	if turn.Status != "started" || turn.ConversationID != conversationID || workspaceSessionID == "" {
		return nil, failure("start", "invalid_response", true)
	}
	requestID := turn.RequestID
	for {
		if c.opts.OnProgress != nil {
			c.opts.OnProgress()
		}
		timer := time.NewTimer(c.opts.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, &Error{Stage: "poll", Code: "canceled", Submitted: true, cause: ctx.Err()}
		case <-timer.C:
		}
		var next turnResponse
		if err := s.json(ctx, "poll", http.MethodPost, "/api/llm/response_with_tools_status", map[string]any{"request_id": requestID, "turn_state": turn.State}, false, &next); err != nil {
			return nil, err
		}
		if next.RequestID != requestID || (next.ConversationID != "" && next.ConversationID != conversationID) {
			return nil, failure("poll", "invalid_response", true)
		}
		switch next.Status {
		case "pending":
			if turnWorkspaceSession(next.State, conversationID) != workspaceSessionID {
				return nil, failure("poll", "invalid_turn_state", true)
			}
			turn = next // Each poll must use the latest opaque state, including all unknown fields.
		case "completed":
			return parseCompletedResult(next.Response, conversationID)
		default:
			return nil, failure("poll", "unknown_status", true)
		}
	}
}

type generation struct {
	client                         *Client
	jar                            http.CookieJar
	sandboxToken, sandboxSessionID string
	submitted                      bool
}
type turnResponse struct {
	Status         string          `json:"status"`
	RequestID      string          `json:"request_id"`
	ConversationID string          `json:"conversation_id"`
	State          json.RawMessage `json:"turn_state"`
	Response       json.RawMessage `json:"response"`
}

func (s *generation) json(ctx context.Context, stage, method, path string, payload any, sandbox bool, target any) error {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return failure(stage, "invalid_request", s.submitted)
		}
	}
	raw, _, err := s.send(ctx, stage, method, path, body, sandbox, false)
	if err != nil {
		return err
	}
	if json.Unmarshal(raw, target) != nil {
		return failure(stage, "invalid_response", s.submitted)
	}
	return nil
}

func (s *generation) send(ctx context.Context, stage, method, path string, body []byte, sandbox, action bool) ([]byte, http.Header, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(body) > maxBodyBytes {
		return nil, nil, failure(stage, "invalid_request", s.submitted)
	}
	req, err := http.NewRequestWithContext(ctx, method, Origin+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, failure(stage, "invalid_request", s.submitted)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", Origin)
	req.Header.Set("Referer", Origin+"/")
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.client.opts.UserAgent != "" {
		req.Header.Set("User-Agent", s.client.opts.UserAgent)
	}
	if sandbox {
		req.Header.Set("x-crixet-sandbox-token", s.sandboxToken)
	}
	if action {
		req.Header.Set("Next-Action", s.client.opts.ConversationActionID)
		req.Header.Set("Next-Router-State-Tree", url.QueryEscape(`["",{"children":["__PAGE__",{},null,null,4096]},null,null,4112]`))
		req.Header.Set("Accept", "text/x-component")
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
	}
	for _, cookie := range s.jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}
	resp, err := s.client.doer.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		e := failure(stage, "transport_error", s.submitted)
		if ctx.Err() != nil {
			e.Code, e.cause = "canceled", ctx.Err()
		}
		return nil, nil, e
	}
	if resp == nil || resp.Body == nil {
		return nil, nil, failure(stage, "invalid_response", s.submitted)
	}
	defer resp.Body.Close()
	if resp.Request != nil && (resp.Request.URL == nil || resp.Request.URL.Scheme != "https" || resp.Request.URL.Host != "prism.openai.com") {
		return nil, nil, failure(stage, "unexpected_origin", s.submitted)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &Error{Stage: stage, StatusCode: resp.StatusCode, Submitted: s.submitted, Code: "http_error"}
	}
	s.jar.SetCookies(req.URL, resp.Cookies())
	if sandbox {
		id := resp.Header.Get("x-session-id")
		if id != "" {
			if !sessionPattern.MatchString(id) || (s.sandboxSessionID != "" && id != s.sandboxSessionID) {
				return nil, nil, failure(stage, "sandbox_session_changed", s.submitted)
			}
			s.sandboxSessionID = id
		}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, &Error{Stage: stage, Code: "canceled", Submitted: s.submitted, cause: ctx.Err()}
		}
		return nil, nil, failure(stage, "response_read_failed", s.submitted)
	}
	if len(raw) > maxBodyBytes {
		return nil, nil, failure(stage, "response_too_large", s.submitted)
	}
	return raw, resp.Header, nil
}

func parseConversation(raw []byte) string {
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("1:")) {
			continue
		}
		var id string
		if json.Unmarshal(line[2:], &id) == nil && strings.HasPrefix(id, "cdx1_") && uuidPattern.MatchString(strings.TrimPrefix(id, "cdx1_")) {
			return id
		}
	}
	return ""
}

func turnWorkspaceSession(raw json.RawMessage, conversationID string) string {
	var state struct {
		ConversationID     string `json:"conversation_id"`
		WorkspaceSessionID string `json:"workspace_session_id"`
	}
	if json.Unmarshal(raw, &state) != nil || state.ConversationID != conversationID || !uuidPattern.MatchString(state.WorkspaceSessionID) {
		return ""
	}
	return state.WorkspaceSessionID
}

func newUUID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	s := hex.EncodeToString(data[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func normalizeInput(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 || len(raw) > maxBodyBytes {
		return nil, false
	}
	var messages []struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return nil, false
	}
	normalized := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if message.Type != "" && message.Type != "message" {
			return nil, false
		}
		switch message.Role {
		case "system", "developer", "user", "assistant":
		default:
			return nil, false
		}
		content := make([]map[string]string, 0, 1)
		var text string
		if len(message.Content) > 0 && message.Content[0] == '"' && json.Unmarshal(message.Content, &text) == nil {
			content = append(content, map[string]string{"type": "input_text", "text": text})
		} else {
			var parts []struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if json.Unmarshal(message.Content, &parts) != nil || len(parts) == 0 {
				return nil, false
			}
			for _, part := range parts {
				if (part.Type != "input_text" && part.Type != "output_text") || part.Text == nil {
					return nil, false
				}
				content = append(content, map[string]string{"type": "input_text", "text": *part.Text})
			}
		}
		normalized = append(normalized, map[string]any{"type": "message", "role": message.Role, "content": content})
	}
	encoded, err := json.Marshal(normalized)
	return encoded, err == nil && len(encoded) <= maxBodyBytes
}

func parseCompletedResult(raw json.RawMessage, conversationID string) (*Result, error) {
	result, err := parseResult(raw, conversationID)
	if pe, ok := err.(*Error); ok {
		pe.Terminal = true
	}
	return result, err
}

func parseResult(raw json.RawMessage, conversationID string) (*Result, error) {
	var response struct {
		Status  string `json:"status"`
		Payload struct {
			ID             string `json:"id"`
			ConversationID string `json:"conversationId"`
			Output         []struct {
				ID      string `json:"id"`
				Status  string `json:"status"`
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Type string  `json:"type"`
					Text *string `json:"text"`
				} `json:"content"`
			} `json:"output"`
			Usage      json.RawMessage `json:"usage"`
			Reason     string          `json:"reason"`
			HTTPStatus json.RawMessage `json:"httpStatus"`
		} `json:"payload"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, failure("result", "invalid_response", true)
	}
	if response.Status == "error" {
		// The provider's message/rootCause/debug fields may echo credentials or
		// prompts. Only return fixed categories and a valid numeric HTTP status.
		code := "upstream_rejected"
		switch response.Payload.Reason {
		case "sandbox_reconnecting", "conversation_too_large", "project_edit_access_required":
			code = response.Payload.Reason
		}
		err := failure("result", code, true)
		var status int
		if json.Unmarshal(response.Payload.HTTPStatus, &status) == nil && status >= 100 && status <= 599 {
			err.StatusCode = status
		}
		return nil, err
	}
	if response.Status != "success" || response.Payload.ID == "" || response.Payload.ConversationID != conversationID || len(response.Payload.Output) == 0 {
		return nil, failure("result", "invalid_response", true)
	}
	var text strings.Builder
	output := make([]map[string]any, 0, len(response.Payload.Output))
	for _, item := range response.Payload.Output {
		if item.ID == "" || item.Type != "message" || item.Role != "assistant" || len(item.Content) == 0 || (item.Status != "" && item.Status != "completed") {
			return nil, failure("result", "unsupported_output", true)
		}
		content := make([]map[string]any, 0, len(item.Content))
		for _, part := range item.Content {
			if part.Type != "output_text" || part.Text == nil {
				return nil, failure("result", "unsupported_output", true)
			}
			text.WriteString(*part.Text)
			content = append(content, map[string]any{"type": "output_text", "text": *part.Text, "annotations": []any{}})
		}
		output = append(output, map[string]any{"id": item.ID, "type": "message", "role": "assistant", "status": "completed", "content": content})
	}
	encoded, _ := json.Marshal(output)
	result := &Result{ID: response.Payload.ID, ConversationID: conversationID, Output: encoded, Text: text.String()}
	if len(response.Payload.Usage) > 0 && string(response.Payload.Usage) != "null" {
		// Keep only documented public token counters, never arbitrary upstream fields.
		var usage struct {
			Input        *int64 `json:"input_tokens"`
			Output       *int64 `json:"output_tokens"`
			Total        *int64 `json:"total_tokens"`
			InputDetails *struct {
				Cached *int64 `json:"cached_tokens,omitempty"`
			} `json:"input_tokens_details,omitempty"`
			OutputDetails *struct {
				Reasoning *int64 `json:"reasoning_tokens,omitempty"`
			} `json:"output_tokens_details,omitempty"`
		}
		if json.Unmarshal(response.Payload.Usage, &usage) != nil || usage.Input == nil || usage.Output == nil || usage.Total == nil || *usage.Input < 0 || *usage.Output < 0 || *usage.Total < 0 {
			return nil, failure("result", "invalid_usage", true)
		}
		if usage.InputDetails != nil && (usage.InputDetails.Cached == nil || *usage.InputDetails.Cached < 0 || *usage.InputDetails.Cached > *usage.Input) {
			return nil, failure("result", "invalid_usage", true)
		}
		if usage.OutputDetails != nil && (usage.OutputDetails.Reasoning == nil || *usage.OutputDetails.Reasoning < 0 || *usage.OutputDetails.Reasoning > *usage.Output) {
			return nil, failure("result", "invalid_usage", true)
		}
		result.Usage, _ = json.Marshal(usage)
	}
	return result, nil
}

var _ error = (*Error)(nil)
