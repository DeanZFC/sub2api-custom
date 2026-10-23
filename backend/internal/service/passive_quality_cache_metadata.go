package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// PassiveQualityCacheMetadata is captured before the upstream response exists.
// It contains digests only, never prompt text or credentials. Reuse eligibility
// compares earlier completed prompts with current prefixes, independently of hits.
type PassiveQualityCacheMetadata struct {
	Version           int       `json:"version"`
	AccountID         int64     `json:"account_id"`
	SessionHash       string    `json:"session_hash"`
	Model             string    `json:"model"`
	Scope             string    `json:"scope"`
	StartedAt         time.Time `json:"started_at"`
	PrefixHashes      []string  `json:"prefix_hashes"`
	PromptHash        string    `json:"prompt_hash"`
	MinimumTokens     int64     `json:"minimum_tokens"`
	RetentionSeconds  int       `json:"retention_seconds"`
	WireVerified      bool      `json:"wire_verified"`
	UnsupportedReason string    `json:"unsupported_reason,omitempty"`
}
type passiveQualityCacheMetadataKey struct{}
type passiveQualityCacheCaptureEnabledKey struct{}

func passiveQualityCacheCaptureEnabled(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	enabled, _ := c.Request.Context().Value(passiveQualityCacheCaptureEnabledKey{}).(func() bool)
	return enabled != nil && enabled()
}

func passiveQualityDigest(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// CapturePassiveQualityCacheRequest freezes request-side evidence before
// forwarding, including once per WS turn. Both request and usage contexts share
// the immutable value; nested adapters preserve the first logical request.
func CapturePassiveQualityCacheRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, sessionHash, model string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || account == nil {
		return ctx
	}
	if !passiveQualityCacheCaptureEnabled(c) {
		return ctx
	}
	// c.Request owns the active turn. A long-lived caller may still hold an
	// older context after BeginTurn clears this slot; never revive that value.
	if c.Request != nil {
		if old, ok := c.Request.Context().Value(passiveQualityCacheMetadataKey{}).(*PassiveQualityCacheMetadata); ok && old != nil && old.AccountID == account.ID {
			return context.WithValue(ctx, passiveQualityCacheMetadataKey{}, old)
		}
	}
	m := buildPassiveQualityCacheMetadata(account, body, sessionHash, model, getAPIKeyIDFromContext(c), time.Now())
	ctx = context.WithValue(ctx, passiveQualityCacheMetadataKey{}, m)
	if c.Request != nil {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheMetadataKey{}, m))
	}
	return ctx
}

// capturePassiveQualityCacheHTTPRequest runs at the final request builder, after
// account mapping, protocol conversion, prompt-cache identity and header rewrites.
// GetBody reads a private copy, leaving the outgoing stream untouched.
func capturePassiveQualityCacheHTTPRequest(c *gin.Context, account *Account, req *http.Request) {
	if c == nil || c.Request == nil || req == nil {
		return
	}
	if !passiveQualityCacheCaptureEnabled(c) {
		return
	}
	// A retry may have a different body. Until this request is inspected, an
	// earlier verified send cannot be evidence for it. Clone rather than
	// mutating snapshots already handed to asynchronous usage workers.
	if previous, _ := c.Request.Context().Value(passiveQualityCacheMetadataKey{}).(*PassiveQualityCacheMetadata); previous != nil {
		unknown := *previous
		unknown.WireVerified = false
		unknown.PrefixHashes, unknown.PromptHash = nil, ""
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheMetadataKey{}, &unknown))
	}
	if req.GetBody == nil {
		return
	}
	reader, err := req.GetBody()
	if err != nil {
		return
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
	if err != nil {
		return
	}
	capturePassiveQualityCacheWire(c, account, body, req.URL.String(), req.Header)
}

func capturePassiveQualityCacheWire(c *gin.Context, account *Account, body []byte, target string, headers http.Header) {
	if c == nil || c.Request == nil || account == nil {
		return
	}
	if !passiveQualityCacheCaptureEnabled(c) {
		return
	}
	previous, _ := c.Request.Context().Value(passiveQualityCacheMetadataKey{}).(*PassiveQualityCacheMetadata)
	session, model := passiveQualityRequestSession(c, body), ""
	if previous != nil && previous.AccountID == account.ID {
		session = previous.SessionHash
		model = previous.Model
	}
	m := buildPassiveQualityCacheMetadata(account, body, session, model, getAPIKeyIDFromContext(c), time.Now())
	if previous != nil && previous.AccountID == account.ID {
		m.SessionHash = previous.SessionHash
		// Stateful history is still unknown if a converter removes its marker.
		if previous.UnsupportedReason == "server_side_history" || previous.UnsupportedReason == "managed_context" {
			m.UnsupportedReason = previous.UnsupportedReason
			m.PrefixHashes = []string{}
			m.PromptHash = ""
		}
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "https" && u.Scheme != "wss") || (u.Hostname() != "api.openai.com" && u.Hostname() != "chatgpt.com" && u.Hostname() != "api.anthropic.com") {
		m.UnsupportedReason = "unknown_cache_scope"
		m.PrefixHashes = []string{}
		m.PromptHash = ""
	}
	// OAuth access-token refresh keeps identity; API-key/header overrides and
	// upstream account/organization changes establish a new cache scope.
	auth := ""
	if !account.IsOAuth() && !account.IsOpenAIOAuthLike() {
		auth = headers.Get("Authorization") + ":" + headers.Get("X-Api-Key")
	}
	m.Scope = passiveQualityDigest(m.Scope + ":" + target + ":" + auth + ":" + headers.Get("Chatgpt-Account-Id") + ":" + headers.Get("OpenAI-Organization") + ":" + headers.Get("OpenAI-Project") + ":" + headers.Get("Session-Id") + ":" + headers.Get("Conversation-Id") + ":" + headers.Get("Anthropic-Beta") + ":" + headers.Get("Anthropic-Version"))
	m.WireVerified = true
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), passiveQualityCacheMetadataKey{}, m))
}

func buildPassiveQualityCacheMetadata(account *Account, body []byte, sessionHash, model string, apiKeyID int64, now time.Time) *PassiveQualityCacheMetadata {
	m := &PassiveQualityCacheMetadata{Version: 1, StartedAt: now, Model: strings.TrimSpace(model), PrefixHashes: []string{}, RetentionSeconds: 300}
	if account == nil {
		m.UnsupportedReason = "unknown_account"
		return m
	}
	m.AccountID = account.ID
	if len(body) == 0 || len(body) > 8<<20 {
		m.UnsupportedReason = "unsupported_prompt_size"
		return m
	}
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil || root == nil {
		m.UnsupportedReason = "invalid_prompt"
		return m
	}
	if m.Model == "" {
		m.Model, _ = root["model"].(string)
	}
	if strings.TrimSpace(sessionHash) == "" {
		m.UnsupportedReason = "unknown_session"
		return m
	}
	if apiKeyID <= 0 {
		m.UnsupportedReason = "unknown_tenant"
		return m
	}
	m.SessionHash = passiveQualityDigest(fmt.Sprintf("tenant:%d:session:%s", apiKeyID, sessionHash))
	scope, minimum, reason := passiveQualityCacheScope(account, root)
	m.Scope, m.MinimumTokens, m.UnsupportedReason = scope, minimum, reason
	if reason != "" {
		return m
	}
	if passiveQualityHasExternalContent(root) {
		m.UnsupportedReason = "external_or_multimodal_content"
		return m
	}
	if previous, _ := root["previous_response_id"].(string); previous != "" {
		m.UnsupportedReason = "server_side_history"
		return m
	}
	if _, ok := root["context_management"]; ok {
		m.UnsupportedReason = "managed_context"
		return m
	}
	var sequence []any
	field := "messages"
	if v, ok := root["messages"].([]any); ok {
		sequence = v
	} else if v, ok := root["input"].([]any); ok {
		sequence = v
		field = "input"
	} else if v, ok := root["input"].(string); ok {
		sequence = []any{v}
		field = "input"
	}
	if len(sequence) == 0 || len(sequence) > 512 {
		m.UnsupportedReason = "unsupported_prompt_shape"
		return m
	}
	// Unknown request fields remain in the hash, conservatively reducing recall.
	static := make(map[string]any, len(root))
	ignored := map[string]bool{field: true, "stream": true, "stream_options": true, "max_tokens": true, "max_output_tokens": true, "temperature": true, "top_p": true, "metadata": true, "store": true, "user": true, "type": true}
	for k, v := range root {
		if !ignored[k] {
			static[k] = v
		}
	}
	static["_sequence_field"] = field
	// Tool-schema property order can influence provider token serialization.
	// Retain its exact wire representation alongside semantic message hashes.
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() {
		static["_wire_tools"] = passiveQualityDigest(tools.Raw)
	}
	raw, _ := json.Marshal(static)
	h := passiveQualityDigest(string(raw))
	// Anthropic searches only the recent content-block neighborhood of a
	// breakpoint. A matching message boundary outside that neighborhood is
	// insufficient evidence that the earlier prompt can be reused.
	prefixStart := 0
	if account.Platform == PlatformAnthropic {
		blocks := 0
		for i := len(sequence) - 1; i >= 0; i-- {
			if blocks >= 20 {
				prefixStart = i + 1
				break
			}
			message, _ := sequence[i].(map[string]any)
			if content, ok := message["content"].([]any); ok {
				blocks += len(content)
			} else {
				blocks++
			}
		}
	}
	for i, item := range sequence {
		raw, err := json.Marshal(passiveQualityWithoutCacheMarkers(item))
		if err != nil {
			m.UnsupportedReason = "invalid_prompt"
			return m
		}
		h = passiveQualityDigest(h + ":" + string(raw))
		if i >= prefixStart {
			m.PrefixHashes = append(m.PrefixHashes, h)
		}
	}
	if len(m.PrefixHashes) > 64 {
		m.PrefixHashes = append([]string(nil), m.PrefixHashes[len(m.PrefixHashes)-64:]...)
	}
	m.PromptHash = h
	return m
}

func passiveQualityCacheScope(a *Account, root map[string]any) (string, int64, string) {
	if a.IsPoolMode() {
		return "", 0, "unknown_upstream_pool"
	}
	base := strings.TrimSpace(a.GetCredential("base_url"))
	host := ""
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || u.Scheme != "https" {
			return "", 0, "unknown_cache_scope"
		}
		host = strings.ToLower(u.Hostname())
	}
	minimum := int64(1024)
	switch a.Platform {
	case PlatformOpenAI:
		if host != "" && host != "api.openai.com" && (!a.IsOpenAIOAuthLike() || host != "chatgpt.com") {
			return "", 0, "unknown_cache_scope"
		}
		model, _ := root["model"].(string)
		model = strings.ToLower(a.GetMappedModel(model))
		if !strings.HasPrefix(model, "gpt-4o") && !strings.HasPrefix(model, "gpt-4.1") && !strings.HasPrefix(model, "gpt-5") && !strings.HasPrefix(model, "gpt-6") && !strings.HasPrefix(model, "o1") && !strings.HasPrefix(model, "o3") && !strings.HasPrefix(model, "o4") && !strings.Contains(model, "codex") {
			return "", 0, "unsupported_cache_model"
		}
	case PlatformAnthropic:
		if host != "" && host != "api.anthropic.com" {
			return "", 0, "unknown_cache_scope"
		}
		// A partial cache_control breakpoint cannot use the entire input as the
		// expected reusable-token denominator. Only complete-prompt caching qualifies.
		if !passiveQualityAnthropicFullPromptCache(root) {
			return "", 0, "partial_or_unknown_cache_boundary"
		}
		minimum = 4096
	default:
		return "", 0, "unknown_cache_scope"
	}
	identity := a.GetCredential("api_key")
	if a.IsOAuth() || a.IsOpenAIOAuthLike() {
		identity = a.GetCredential("chatgpt_account_id") + ":" + a.GetCredential("organization_id") + ":" + a.GetCredential("account_uuid")
		if identity == "::" {
			identity = a.GetCredential("refresh_token")
		}
		if identity == "" {
			identity = a.GetCredential("access_token")
		}
	}
	if identity == "" {
		return "", 0, "unknown_upstream_identity"
	}
	model, _ := root["model"].(string)
	scope := passiveQualityDigest(a.Platform + ":" + base + ":" + identity + ":" + a.GetMappedModel(model))
	return scope, minimum, ""
}

func passiveQualityAnthropicFullPromptCache(root map[string]any) bool {
	ephemeral := func(v any) bool { m, ok := v.(map[string]any); return ok && m["type"] == "ephemeral" }
	automatic := ephemeral(root["cache_control"])
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return false
	}
	last, ok := messages[len(messages)-1].(map[string]any)
	if !ok {
		return false
	}
	if content, ok := last["content"].(string); ok {
		return automatic && strings.TrimSpace(content) != ""
	}
	content, ok := last["content"].([]any)
	if !ok || len(content) == 0 {
		return false
	}
	block, ok := content[len(content)-1].(map[string]any)
	if !ok || block["type"] != "text" {
		return false
	}
	text, ok := block["text"].(string)
	// Automatic caching may fall back to an earlier cacheable block. Until
	// that boundary is known, full-input tokens cannot be its denominator.
	return ok && strings.TrimSpace(text) != "" && (automatic || ephemeral(block["cache_control"]))
}

// Breakpoints may move forward as a conversation grows. They control caching,
// not prefix content; removing only this marker preserves exact content hashes.
func passiveQualityWithoutCacheMarkers(value any) any {
	message, ok := value.(map[string]any)
	if !ok {
		return value
	}
	content, ok := message["content"].([]any)
	if !ok {
		return value
	}
	copy := make(map[string]any, len(message))
	for k, v := range message {
		copy[k] = v
	}
	blocks := make([]any, len(content))
	for i, item := range content {
		blocks[i] = item
		block, ok := item.(map[string]any)
		if !ok {
			continue
		}
		clean := make(map[string]any, len(block))
		for k, v := range block {
			if k != "cache_control" {
				clean[k] = v
			}
		}
		// Nested tool inputs/results are user content. A field named
		// cache_control inside them is not a provider caching marker.
		blocks[i] = clean
	}
	copy["content"] = blocks
	return copy
}

func passiveQualityHasExternalContent(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		if kind, ok := v["type"].(string); ok {
			switch kind {
			case "image", "image_url", "input_image", "input_audio", "audio", "video", "input_file", "file", "document":
				return true
			}
		}
		for _, x := range v {
			if passiveQualityHasExternalContent(x) {
				return true
			}
		}
	case []any:
		for _, x := range v {
			if passiveQualityHasExternalContent(x) {
				return true
			}
		}
	}
	return false
}

// This identity is exclusively for reuse evidence; usage_logs.session_id keeps
// its explicit-header-only contract. Content-derived routing hashes must not
// turn a shared prompt into evidence from independent cache sessions.
func passiveQualityRequestSession(c *gin.Context, body []byte) string {
	if c == nil {
		return ""
	}
	if v := ExtractClientSessionID(c); v != "" {
		return v
	}
	if v := explicitOpenAIRequestSessionID(c, body); v != "" {
		return v
	}
	var root struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &root) == nil {
		if parsed := ParseMetadataUserID(root.Metadata.UserID); parsed != nil {
			return parsed.SessionID
		}
	}
	return ""
}
