package vm2api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Keep this list aligned with vm2api handle-codex's public kernel headers.
// Administrative routing overrides and credentials never enter this list.
var inferenceHeaders = []string{
	"Accept", "Content-Type", "OpenAI-Beta", "User-Agent", "Originator", "Version",
	"Session_id", "Conversation_id", "Session-Id", "Thread-Id", "X-Session-Id", "X-Conversation-Id",
	"X-Client-Request-Id", "X-Request-Id", "X-Codex-Beta-Features", "X-Codex-Installation-Id",
	"X-Codex-Parent-Thread-Id", "X-Codex-Turn-Metadata", "X-Codex-Turn-State", "X-Codex-Window-Id",
	"X-OpenAI-Internal-Codex-Responses-Lite", "X-OpenAI-Memgen-Request", "X-OpenAI-Subagent",
	"X-Codex-Image-Turn-Id",
}

// forward accepts only paths chosen by this package. It never takes a user URL
// and never uses the control-plane master key. The caller owns the response
// body and request lifetime, including long-lived SSE streams.
func (c *Client) forward(ctx context.Context, method, path, operation, key string, body io.Reader, headers http.Header) (*http.Response, error) {
	req, err := c.request(ctx, method, path, key, body)
	if err != nil {
		return nil, err
	}
	// In particular, do not forward Authorization, Cookie, X-API-Key, Host,
	// proxy headers or vm2api's operator routing/debug headers.
	for _, name := range inferenceHeaders {
		if values := headers.Values(name); len(values) > 0 {
			req.Header[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
		}
	}
	// Sub2API's direct Codex transport historically uses underscore names;
	// vm2api's session parser uses hyphens. Preserve explicit public headers.
	if req.Header.Get("Session-Id") == "" {
		if value := headers.Get("Session_id"); value != "" {
			req.Header.Set("Session-Id", value)
		}
	}
	if req.Header.Get("X-Conversation-Id") == "" {
		if value := headers.Get("Conversation_id"); value != "" {
			req.Header.Set("X-Conversation-Id", value)
		}
	}
	if method == http.MethodGet && path == "/v1/responses" && strings.EqualFold(headers.Get("Upgrade"), "websocket") {
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		for _, name := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
			if values := headers.Values(name); len(values) > 0 {
				req.Header[name] = append([]string(nil), values...)
			}
		}
	}
	return c.send(req, operation)
}

func (c *Client) ForwardResponses(ctx context.Context, key string, body io.Reader, headers http.Header) (*http.Response, error) {
	return c.forward(ctx, http.MethodPost, "/v1/responses", "responses", key, body, headers)
}
func (c *Client) ForwardModels(ctx context.Context, key string) (*http.Response, error) {
	return c.forward(ctx, http.MethodGet, "/v1/models", "models", key, nil, nil)
}
func (c *Client) ForwardUsage(ctx context.Context, key string) (*http.Response, error) {
	return c.forward(ctx, http.MethodGet, "/v1/usage?platform=openai", "usage", key, nil, nil)
}

func readPublicJSON(resp *http.Response, operation string) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxControlBody+1))
	if err != nil {
		return nil, &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "read_failed"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "http_error"}
	}
	if len(data) > maxControlBody || !json.Valid(data) {
		return nil, &APIError{Operation: operation, StatusCode: resp.StatusCode, Code: "invalid_response"}
	}
	return data, nil
}

func (c *Client) Models(ctx context.Context, key string) (*ModelList, error) {
	resp, err := c.ForwardModels(ctx, key)
	if err != nil {
		return nil, err
	}
	data, err := readPublicJSON(resp, "models")
	if err != nil {
		return nil, err
	}
	var models ModelList
	if json.Unmarshal(data, &models) != nil || models.Object != "list" {
		return nil, &APIError{Operation: "models", Code: "invalid_response"}
	}
	return &models, nil
}

func (c *Client) Usage(ctx context.Context, key string) (json.RawMessage, error) {
	resp, err := c.ForwardUsage(ctx, key)
	if err != nil {
		return nil, err
	}
	return readPublicJSON(resp, "usage")
}
