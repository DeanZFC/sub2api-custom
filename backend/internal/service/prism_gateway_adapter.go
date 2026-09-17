package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prism"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const prismUpstreamEndpoint = "/api/llm/response_with_tools_start"
const prismBrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"

// A separate transport adapter retains account routing without applying Codex
// authentication, payload rewrites, plugins, or automatic inference retries.
type prismAccountTransport struct {
	upstream HTTPUpstream
	account  *Account
	profile  *tlsfingerprint.Profile
}

func (t prismAccountTransport) Do(req *http.Request) (*http.Response, error) {
	ctx := WithHTTPUpstreamRedirectsDisabled(req.Context())
	// Mask any retry budget inherited from the gateway. Bootstrap and start are
	// mutations; even an HTTP error is not proof that a task was not accepted.
	ctx = context.WithValue(ctx, upstreamErrorRetryContextKey{}, (*upstreamErrorRetryState)(nil))
	ctx = WithAccountProtectionOutcomeExcluded(ctx)
	proxyURL := ""
	if t.account.Proxy != nil {
		proxyURL = t.account.Proxy.URL()
	}
	return t.upstream.DoWithTLS(req.WithContext(ctx), proxyURL, t.account.ID, t.account.Concurrency, t.profile)
}

func newPrismAccountClient(upstream HTTPUpstream, cfg *config.Config, account *Account, tokenProvider *OpenAITokenProvider) (*prism.Client, error) {
	if upstream == nil {
		return nil, errors.New("Prism transport is unavailable")
	}
	if err := ValidatePrismAccountConfiguration(account); err != nil {
		return nil, err
	}
	prismConfig, err := account.PrismConfig()
	if err != nil {
		return nil, err
	}
	if !prismConfig.Enabled {
		return nil, errors.New("Prism is disabled for this account")
	}
	// Never silently connect directly if a selected proxy was not hydrated.
	if account.ProxyID != nil && account.Proxy == nil {
		return nil, errors.New("Prism account proxy is unavailable")
	}
	profile, err := resolveAccountTLSProfileForOpenAI(account, cfg)
	if err != nil {
		return nil, errors.New("Prism account transport configuration is invalid")
	}
	cookie, _ := account.Credentials[PrismCookieCredentialKey].(string)
	opts := prism.Options{
		UserAgent: prismBrowserUserAgent, ConversationActionID: prismConfig.ConversationActionID,
		Timeout: time.Duration(prismConfig.TimeoutSeconds) * time.Second,
	}
	if account.UsesPrismAccountAuth() {
		// The OpenAI access token is only used to bootstrap Prism's session via
		// GET /auth/session. Prism then sets its own session cookie; the token is
		// never forwarded as Authorization or mixed with a saved Prism cookie.
		opts.AccessTokenProvider = func(ctx context.Context) (string, error) {
			if account.Type == AccountTypeOAuth && tokenProvider != nil {
				return tokenProvider.GetAccessToken(ctx, account)
			}
			if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil && !time.Now().Before(*expiresAt) {
				return "", errors.New("OpenAI access token is expired")
			}
			return account.GetCredential("access_token"), nil
		}
		opts.ExpectedOpenAIUserID = strings.TrimSpace(account.GetCredential("chatgpt_user_id"))
	} else {
		opts.Cookie = cookie
	}
	return prism.NewClient(prismAccountTransport{upstream: upstream, account: account, profile: profile}, opts), nil
}

type prismIncomingRequest struct {
	request      prism.Request
	model        string
	stream       bool
	includeUsage bool
}

type prismRequestError struct{ param, message string }

func (e *prismRequestError) Error() string { return e.message }
func prismUnsupported(param string) error {
	return &prismRequestError{param, "Prism text mode does not support this parameter or content; send the complete text conversation without tools, media, or server-side continuation"}
}

// Only understood semantics are accepted. In particular, silently discarding
// client tools would make a coding client appear to work while breaking it.
func parsePrismIncoming(body []byte, account *Account, chat bool, defaultModel string) (*prismIncomingRequest, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return nil, &prismRequestError{"", "Invalid JSON request"}
	}
	out := &prismIncomingRequest{}
	if json.Unmarshal(fields["model"], &out.model) != nil || strings.TrimSpace(out.model) == "" {
		return nil, &prismRequestError{"model", "A model is required"}
	}
	if !account.IsModelSupported(out.model) {
		return nil, &prismRequestError{"model", "Model is not configured for this Prism account"}
	}
	out.request.Model = resolveOpenAIForwardModel(account, out.model, defaultModel)
	out.request.ReasoningEffort = "medium"
	var textVerbosity string
	for key, raw := range fields {
		if string(raw) == "null" {
			continue
		}
		switch key {
		case "model", "input", "messages", "instructions":
		case "stream":
			if json.Unmarshal(raw, &out.stream) != nil {
				return nil, prismUnsupported("stream")
			}
		case "reasoning":
			if chat {
				return nil, prismUnsupported("reasoning")
			}
			var v map[string]json.RawMessage
			if json.Unmarshal(raw, &v) != nil {
				return nil, prismUnsupported("reasoning")
			}
			for k := range v {
				if k != "effort" {
					return nil, prismUnsupported("reasoning")
				}
			}
			if val, ok := v["effort"]; ok && json.Unmarshal(val, &out.request.ReasoningEffort) != nil {
				return nil, prismUnsupported("reasoning.effort")
			}
		case "reasoning_effort":
			if !chat || json.Unmarshal(raw, &out.request.ReasoningEffort) != nil {
				return nil, prismUnsupported("reasoning_effort")
			}
		case "tools":
			var v []json.RawMessage
			if json.Unmarshal(raw, &v) != nil || len(v) != 0 {
				return nil, prismUnsupported("tools")
			}
		case "tool_choice":
			if string(raw) != `"none"` {
				return nil, prismUnsupported("tool_choice")
			}
		case "store", "background", "parallel_tool_calls":
			if string(raw) != "false" {
				return nil, prismUnsupported(key)
			}
		case "include":
			var v []string
			if json.Unmarshal(raw, &v) != nil || len(v) != 0 {
				return nil, prismUnsupported(key)
			}
		case "text", "response_format":
			verbosity, err := parsePrismTextOptions(raw, key)
			if err != nil {
				return nil, err
			}
			if key == "text" {
				textVerbosity = verbosity
			}
		case "stream_options":
			var v map[string]json.RawMessage
			if json.Unmarshal(raw, &v) != nil {
				return nil, prismUnsupported(key)
			}
			for k, val := range v {
				if k != "include_usage" || json.Unmarshal(val, &out.includeUsage) != nil {
					return nil, prismUnsupported(key)
				}
			}
		default:
			return nil, &prismRequestError{"", "Prism text mode received an unsupported parameter"}
		}
	}
	switch out.request.ReasoningEffort {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return nil, prismUnsupported("reasoning.effort")
	}
	var messages []map[string]any
	if raw := fields["instructions"]; len(raw) > 0 && string(raw) != "null" {
		if chat {
			return nil, prismUnsupported("instructions")
		}
		var instructions string
		if json.Unmarshal(raw, &instructions) != nil {
			return nil, prismUnsupported("instructions")
		}
		if instructions != "" {
			messages = append(messages, map[string]any{"role": "system", "content": instructions})
		}
	}
	if preference := prismVerbosityInstruction(textVerbosity); preference != "" {
		messages = append(messages, map[string]any{"role": "developer", "content": preference})
	}
	inputKey := "input"
	if chat {
		inputKey = "messages"
		if _, exists := fields["input"]; exists {
			return nil, prismUnsupported("input")
		}
	} else if _, exists := fields["messages"]; exists {
		return nil, prismUnsupported("messages")
	}
	raw := fields[inputKey]
	var inputText string
	if !chat && json.Unmarshal(raw, &inputText) == nil && string(raw) != "null" {
		messages = append(messages, map[string]any{"role": "user", "content": inputText})
	} else {
		var input []map[string]json.RawMessage
		if json.Unmarshal(raw, &input) != nil || len(input) == 0 {
			return nil, prismUnsupported(inputKey)
		}
		for _, m := range input {
			for key := range m {
				switch key {
				case "role", "content", "type":
				case "id", "status":
					var value string
					if json.Unmarshal(m[key], &value) != nil {
						return nil, prismUnsupported(inputKey)
					}
				default:
					return nil, prismUnsupported(inputKey)
				}
			}
			var role, typ string
			if json.Unmarshal(m["role"], &role) != nil {
				return nil, prismUnsupported(inputKey)
			}
			if v, ok := m["type"]; ok && (json.Unmarshal(v, &typ) != nil || typ != "message") {
				return nil, prismUnsupported(inputKey)
			}
			switch role {
			case "system", "developer", "user", "assistant":
			default:
				return nil, prismUnsupported(inputKey)
			}
			var content string
			if json.Unmarshal(m["content"], &content) != nil || string(m["content"]) == "null" {
				var parts []map[string]json.RawMessage
				if json.Unmarshal(m["content"], &parts) != nil || len(parts) == 0 {
					return nil, prismUnsupported(inputKey)
				}
				var b strings.Builder
				for _, part := range parts {
					for key := range part {
						if key == "annotations" {
							var annotations []json.RawMessage
							if json.Unmarshal(part[key], &annotations) != nil || len(annotations) != 0 {
								return nil, prismUnsupported(inputKey)
							}
							continue
						}
						if key != "type" && key != "text" {
							return nil, prismUnsupported(inputKey)
						}
					}
					var kind, text string
					if json.Unmarshal(part["type"], &kind) != nil || json.Unmarshal(part["text"], &text) != nil || string(part["text"]) == "null" {
						return nil, prismUnsupported(inputKey)
					}
					if chat && kind != "text" || !chat && kind != "input_text" && kind != "output_text" {
						return nil, prismUnsupported(inputKey)
					}
					b.WriteString(text)
				}
				content = b.String()
			}
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
	}
	out.request.Input, _ = json.Marshal(messages)
	return out, nil
}

// The only concurrent work is upstream I/O. All writes to gin and its response
// writer stay on the caller goroutine, including keepalives and cancellation.
func generatePrismWithHeartbeat(ctx context.Context, client *prism.Client, request prism.Request, heartbeat func() error) (*prism.Result, error) {
	if heartbeat == nil {
		return client.Generate(ctx, request)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type completion struct {
		result *prism.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() { r, e := client.Generate(ctx, request); done <- completion{r, e} }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case c := <-done:
			return c.result, c.err
		case <-ticker.C:
			if err := heartbeat(); err != nil {
				return nil, err
			}
		}
	}
}

func (s *OpenAIGatewayService) forwardPrism(ctx context.Context, c *gin.Context, account *Account, body []byte, chat bool, defaultModel string) (*OpenAIForwardResult, error) {
	start := time.Now()
	if isOpenAIResponsesCompactPath(c) {
		return nil, writePrismError(c, prismUnsupported("compact"))
	}
	incoming, err := parsePrismIncoming(body, account, chat, defaultModel)
	if err != nil {
		return nil, writePrismError(c, err)
	}
	client, err := newPrismAccountClient(s.httpUpstream, s.cfg, account, s.openAITokenProvider)
	if err != nil {
		return nil, writePrismError(c, errors.New("Prism account configuration is invalid or incomplete"))
	}
	beginUpstreamResponseModelObservation(c)
	SetActualOpenAIUpstreamEndpoint(c, prismUpstreamEndpoint)
	c.Header("X-Sub2api-Upstream", "prism")
	// Captured Prism responses contain no token usage. Do not present zero as
	// upstream-reported usage or use a local estimate for charging.
	c.Header("X-Sub2api-Usage-Source", "unavailable")
	responseID := "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if chat {
		responseID = "chatcmpl-" + strings.TrimPrefix(responseID, "resp_")
	}
	created := time.Now().Unix()
	wire := &prismSSEWriter{c: c}
	var heartbeat func() error
	if incoming.stream {
		c.Header("X-Sub2api-Usage-Source", "pending")
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
		heartbeat = func() error { _, e := fmt.Fprint(c.Writer, ": waiting for Prism\n\n"); c.Writer.Flush(); return e }
		if !chat {
			if err := wire.start(gin.H{"id": responseID, "object": "response", "created_at": created, "model": incoming.model, "error": nil, "incomplete_details": nil}); err != nil {
				return nil, err
			}
		} else if err := heartbeat(); err != nil {
			return nil, err
		}
	}
	result, err := generatePrismWithHeartbeat(ctx, client, incoming.request, heartbeat)
	if !AccountProtectionOutcomeExcluded(ctx) {
		status := http.StatusOK
		if err != nil {
			status = http.StatusBadGateway
			var p *prism.Error
			if errors.As(err, &p) && p.StatusCode != 0 {
				status = p.StatusCode
			}
		}
		if !errors.Is(err, context.Canceled) {
			ObserveAccountProtectionOutcome(account.ID, status, err != nil && status == http.StatusBadGateway)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if incoming.stream {
			problem := prismClientError(err)
			status := http.StatusBadGateway
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			setOpsUpstreamError(c, status, problem["message"].(string), "")
			MarkOpsStreamFailure(c, problem["type"].(string), problem["code"].(string), problem["message"].(string), status)
			if chat {
				_ = wire.data(gin.H{"error": problem})
				_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
				c.Writer.Flush()
			} else {
				_ = wire.event("response.failed", gin.H{"response": gin.H{"id": responseID, "object": "response", "created_at": created, "status": "failed", "model": incoming.model, "output": []any{}, "usage": nil, "error": problem}})
			}
			MarkResponseCommitted(c)
			return nil, err
		}
		return nil, writePrismError(c, err)
	}
	firstToken := int(time.Since(start).Milliseconds()) // First complete upstream text, before downstream writes.
	var usage any
	forwardUsage := OpenAIUsage{}
	usageUnavailable := len(result.Usage) == 0
	if !usageUnavailable {
		_ = json.Unmarshal(result.Usage, &usage)
		_ = json.Unmarshal(result.Usage, &forwardUsage)
		var details struct {
			Input struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		}
		_ = json.Unmarshal(result.Usage, &details)
		forwardUsage.CacheReadInputTokens = details.Input.Cached
		if !incoming.stream {
			c.Header("X-Sub2api-Usage-Source", "upstream")
		}
	}
	var writeErr error
	if chat {
		var chatUsage any
		if !usageUnavailable {
			counts := gin.H{"prompt_tokens": forwardUsage.InputTokens, "completion_tokens": forwardUsage.OutputTokens, "total_tokens": forwardUsage.InputTokens + forwardUsage.OutputTokens, "prompt_tokens_details": gin.H{"cached_tokens": forwardUsage.CacheReadInputTokens}}
			if values, ok := usage.(map[string]any); ok {
				if details, exists := values["output_tokens_details"]; exists {
					counts["completion_tokens_details"] = details
				}
			}
			chatUsage = counts
		}
		if incoming.stream {
			chunk := func(delta gin.H, finish any, choices bool, withUsage bool) error {
				items := []any{}
				if choices {
					items = append(items, gin.H{"index": 0, "delta": delta, "finish_reason": finish})
				}
				event := gin.H{"id": responseID, "object": "chat.completion.chunk", "created": created, "model": incoming.model, "choices": items}
				if withUsage {
					event["usage"] = chatUsage
				}
				return wire.data(event)
			}
			if writeErr = chunk(gin.H{"role": "assistant", "content": ""}, nil, true, false); writeErr == nil {
				writeErr = chunk(gin.H{"content": result.Text}, nil, true, false)
			}
			if writeErr == nil {
				writeErr = chunk(gin.H{}, "stop", true, false)
			}
			if writeErr == nil && incoming.includeUsage {
				writeErr = chunk(nil, nil, false, true)
			}
			if writeErr == nil {
				_, writeErr = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
				c.Writer.Flush()
			}
		} else {
			c.JSON(http.StatusOK, gin.H{"id": responseID, "object": "chat.completion", "created": created, "model": incoming.model, "choices": []any{gin.H{"index": 0, "message": gin.H{"role": "assistant", "content": result.Text}, "finish_reason": "stop"}}, "usage": chatUsage})
		}
	} else {
		output := []any{}
		_ = json.Unmarshal(result.Output, &output)
		response := gin.H{"id": responseID, "object": "response", "created_at": created, "status": "completed", "model": incoming.model, "output": output, "usage": usage, "error": nil, "incomplete_details": nil}
		if incoming.stream {
			writeErr = wire.response(response)
		} else {
			c.JSON(http.StatusOK, response)
		}
	}
	duration := time.Since(start)
	effort := incoming.request.ReasoningEffort
	return &OpenAIForwardResult{RequestID: responseID, ResponseID: responseID, Model: incoming.model, UpstreamModel: incoming.request.Model, UpstreamEndpoint: prismUpstreamEndpoint, Usage: forwardUsage, UsageUnavailable: usageUnavailable, ReasoningEffort: &effort, Stream: incoming.stream, Duration: duration, FirstTokenMs: &firstToken, ClientDisconnect: writeErr != nil}, writeErr
}

func prismClientError(err error) gin.H {
	problem := gin.H{"type": "upstream_error", "code": "prism_upstream_error", "message": "Prism request failed"}
	var invalid *prismRequestError
	var upstream *prism.Error
	switch {
	case errors.As(err, &invalid):
		problem["type"] = "invalid_request_error"
		problem["code"] = "prism_unsupported_request"
		problem["message"] = invalid.message
		if invalid.param != "" {
			problem["param"] = invalid.param
		}
	case errors.As(err, &upstream):
		problem["message"] = upstream.Error()
		if upstream.Stage == "auth" && (upstream.Code == "account_auth_rejected" || upstream.StatusCode == http.StatusUnauthorized) {
			problem["code"] = "prism_authentication_failed"
			problem["message"] = "Prism did not accept this account authentication; sign in to Prism with the account or configure manual Prism Cookie authentication"
		}
		if upstream.Stage == "auth" && upstream.Code == "account_token_unavailable" {
			problem["code"] = "prism_account_token_unavailable"
			problem["message"] = "Unable to obtain an OpenAI access token for Prism; check or renew the account authorization"
		}
		if upstream.Terminal {
			problem["code"] = "prism_" + upstream.Code
		}
		if upstream.Submitted && !upstream.Terminal {
			problem["message"] = upstream.Error() + "; generation may have started, and was not resubmitted"
		}
	case errors.Is(err, context.DeadlineExceeded):
		problem["message"] = "Prism request timed out"
	}
	return problem
}

func writePrismError(c *gin.Context, err error) error {
	status := http.StatusBadGateway
	var invalid *prismRequestError
	var upstream *prism.Error
	if errors.As(err, &invalid) {
		status = http.StatusBadRequest
	} else if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	} else if errors.As(err, &upstream) && upstream.StatusCode == http.StatusTooManyRequests {
		status = http.StatusTooManyRequests
	}
	problem := prismClientError(err)
	setOpsUpstreamError(c, status, problem["message"].(string), "")
	c.JSON(status, gin.H{"error": problem})
	MarkResponseCommitted(c)
	return err // Never an UpstreamFailoverError: do not resubmit on another account.
}

type prismSSEWriter struct {
	c        *gin.Context
	sequence int
	started  bool
}

func (w *prismSSEWriter) data(v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintf(w.c.Writer, "data: %s\n\n", b)
	w.c.Writer.Flush()
	return e
}
func (w *prismSSEWriter) event(kind string, v gin.H) error {
	v["type"] = kind
	v["sequence_number"] = w.sequence
	w.sequence++
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintf(w.c.Writer, "event: %s\ndata: %s\n\n", kind, b)
	w.c.Writer.Flush()
	return e
}
func (w *prismSSEWriter) start(response gin.H) error {
	if w.started {
		return nil
	}
	initial := gin.H{}
	for k, v := range response {
		initial[k] = v
	}
	initial["status"] = "in_progress"
	initial["output"] = []any{}
	initial["usage"] = nil
	if e := w.event("response.created", gin.H{"response": initial}); e != nil {
		return e
	}
	if e := w.event("response.in_progress", gin.H{"response": initial}); e != nil {
		return e
	}
	w.started = true
	return nil
}
func (w *prismSSEWriter) response(final gin.H) error {
	if err := w.start(final); err != nil {
		return err
	}
	for index, raw := range final["output"].([]any) {
		item := raw.(map[string]any)
		added := gin.H{"id": item["id"], "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
		if e := w.event("response.output_item.added", gin.H{"output_index": index, "item": added}); e != nil {
			return e
		}
		for partIndex, rawPart := range item["content"].([]any) {
			part := rawPart.(map[string]any)
			base := func() gin.H { return gin.H{"item_id": item["id"], "output_index": index, "content_index": partIndex} }
			v := base()
			v["part"] = gin.H{"type": "output_text", "text": "", "annotations": []any{}}
			if e := w.event("response.content_part.added", v); e != nil {
				return e
			}
			v = base()
			v["delta"] = part["text"]
			if e := w.event("response.output_text.delta", v); e != nil {
				return e
			}
			v = base()
			v["text"] = part["text"]
			if e := w.event("response.output_text.done", v); e != nil {
				return e
			}
			v = base()
			v["part"] = part
			if e := w.event("response.content_part.done", v); e != nil {
				return e
			}
		}
		if e := w.event("response.output_item.done", gin.H{"output_index": index, "item": item}); e != nil {
			return e
		}
	}
	return w.event("response.completed", gin.H{"response": final})
}
