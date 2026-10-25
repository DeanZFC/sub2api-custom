package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type openAIVMWebSocketRoute struct {
	url     string
	headers http.Header
	client  *http.Client
}

// Only an internal VM route sets this client. Its transport has no environment
// proxy and rejects redirects, keeping bridge credentials at the configured host.
type openAIVMWSHTTPClientContextKey struct{}

func withOpenAIVMWSHTTPClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, openAIVMWSHTTPClientContextKey{}, client)
}

func openAIVMWSHTTPClientFromContext(ctx context.Context) *http.Client {
	if ctx == nil {
		return nil
	}
	client, _ := ctx.Value(openAIVMWSHTTPClientContextKey{}).(*http.Client)
	return client
}

type openAIVMQueuedFrame struct {
	kind    coderws.MessageType
	payload []byte
}

// vm2api queues at most 32 pending requests and handles interrupt outside that
// queue. Keep policy checks and concurrency leases at actual turn start rather
// than acquiring them while a previous turn is still using the account.
type openAIVMQueuedClientConn struct {
	openaiwsv2.FrameConn
	forwardControl func(context.Context, coderws.MessageType, []byte) error
	maxBytes       int64
	queuedBytes    atomic.Int64
	frames         chan openAIVMQueuedFrame
	ready          chan struct{}
	readErr        chan error
	start          sync.Once
}

func newOpenAIVMQueuedClientConn(inner openaiwsv2.FrameConn, maxBytes int64, forward func(context.Context, coderws.MessageType, []byte) error) *openAIVMQueuedClientConn {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024
	}
	return &openAIVMQueuedClientConn{FrameConn: inner, forwardControl: forward, maxBytes: maxBytes, frames: make(chan openAIVMQueuedFrame, 32), ready: make(chan struct{}, 1), readErr: make(chan error, 1)}
}

func (c *openAIVMQueuedClientConn) turnCompleted() {
	select {
	case c.ready <- struct{}{}:
	default:
	}
}

func (c *openAIVMQueuedClientConn) read(ctx context.Context) {
	for {
		kind, payload, err := c.FrameConn.ReadFrame(ctx)
		if err != nil {
			c.readErr <- err
			return
		}
		if kind == coderws.MessageBinary {
			c.readErr <- NewOpenAIWSClientCloseError(coderws.StatusUnsupportedData, "Responses WebSocket accepts text frames only", nil)
			return
		}
		if gjson.ValidBytes(payload) && gjson.ParseBytes(payload).IsObject() && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.interrupt" {
			if err := c.forwardControl(ctx, kind, payload); err != nil {
				c.readErr <- err
				return
			}
			continue
		}
		if c.queuedBytes.Add(int64(len(payload))) > c.maxBytes {
			c.readErr <- NewOpenAIWSClientCloseError(coderws.StatusMessageTooBig, "Responses WebSocket receive buffer exceeds request body limit", nil)
			return
		}
		select {
		case c.frames <- openAIVMQueuedFrame{kind: kind, payload: payload}:
		default:
			c.readErr <- NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "Responses WebSocket receive queue is full", nil)
			return
		}
	}
}

func (c *openAIVMQueuedClientConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	c.start.Do(func() { go c.read(ctx) })
	select {
	case <-ctx.Done():
		return coderws.MessageText, nil, ctx.Err()
	case err := <-c.readErr:
		return coderws.MessageText, nil, err
	case <-c.ready:
	}
	for {
		select {
		case <-ctx.Done():
			return coderws.MessageText, nil, ctx.Err()
		case err := <-c.readErr:
			return coderws.MessageText, nil, err
		case frame := <-c.frames:
			c.queuedBytes.Add(-int64(len(frame.payload)))
			// Match vm2api's receive validation without consuming a real turn or
			// acquiring a lease. Rejections leave this queue permit available.
			if invalid := openAIVMWSInvalidClientFrame(frame.payload); invalid != nil {
				writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := c.WriteFrame(writeCtx, coderws.MessageText, invalid)
				cancel()
				if err != nil {
					return coderws.MessageText, nil, err
				}
				continue
			}
			return frame.kind, frame.payload, nil
		}
	}
}

func openAIVMWSInvalidClientFrame(payload []byte) []byte {
	code, message, param := "", "", ""
	if !gjson.ValidBytes(payload) {
		code, message = "invalid_json", "response.create frame must be valid JSON"
	} else if !gjson.ParseBytes(payload).IsObject() {
		code, message, param = "invalid_value", "response.create frame must be a JSON object", "type"
	} else if gjson.GetBytes(payload, "type").String() != "response.create" {
		code, message, param = "invalid_value", "unsupported Responses WebSocket message type", "type"
	} else if stream := gjson.GetBytes(payload, "stream"); stream.Exists() && stream.Type != gjson.True {
		code, message, param = "invalid_value", "Responses WebSocket requests require stream=true", "stream"
	}
	if code == "" {
		return nil
	}
	err := map[string]any{"type": "invalid_request_error", "code": code, "message": message}
	if param != "" {
		err["param"] = param
	}
	result, _ := json.Marshal(map[string]any{"type": "error", "status": 400, "error": err})
	return result
}

// The observer runs before the existing relay parses a terminal event, so its
// AfterTurn callback can retain the same image billing data as HTTP Responses.
type openAIVMObservedFrameConn struct {
	openaiwsv2.FrameConn
	observe    func([]byte)
	interrupts *openAIVMInterruptTracker
}

type openAIVMInterruptTracker struct {
	writeMu    sync.Mutex
	mu         sync.Mutex
	generation uint64
	active     uint64
	pending    []uint64
}

func (c *openAIVMObservedFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	tracker := c.interrupts
	if tracker == nil {
		return c.FrameConn.WriteFrame(ctx, kind, payload)
	}
	tracker.writeMu.Lock()
	defer tracker.writeMu.Unlock()
	tracker.mu.Lock()
	switch gjson.GetBytes(payload, "type").String() {
	case "response.create":
		tracker.generation++
		tracker.active = tracker.generation
	case "response.interrupt":
		if len(tracker.pending) >= 32 {
			tracker.mu.Unlock()
			return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "too many pending Responses interrupts", nil)
		}
		tracker.pending = append(tracker.pending, tracker.active)
	}
	tracker.mu.Unlock()
	return c.FrameConn.WriteFrame(ctx, kind, payload)
}

func (t *openAIVMInterruptTracker) accept(_ []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) == 0 {
		return false
	}
	generation := t.pending[0]
	t.pending = t.pending[1:]
	return generation != 0 && generation == t.active
}

func (t *openAIVMInterruptTracker) complete() {
	t.mu.Lock()
	t.active = 0
	t.mu.Unlock()
}

func (c *openAIVMObservedFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	kind, payload, err := c.FrameConn.ReadFrame(ctx)
	if err == nil && (kind == coderws.MessageText || kind == coderws.MessageBinary) {
		c.observe(payload)
	}
	return kind, payload, err
}

func openAIVMWSImageBillingConfig(c *gin.Context, payload []byte, model string) (*OpenAIResponsesImageBillingConfig, error) {
	if !IsImageGenerationIntent(openAIResponsesEndpoint, model, payload) {
		return nil, nil
	}
	if !GroupAllowsImageGeneration(apiKeyGroup(getAPIKeyFromContext(c))) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalFeatureGate)
		return nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, ImageGenerationPermissionMessage(), nil)
	}
	config, err := resolveOpenAIResponsesImageBillingConfigDetailedFromBody(payload, model)
	if err != nil {
		return nil, NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, err.Error(), err)
	}
	return &config, nil
}

// websocketRoute uses the authoritative binding, including for stale account
// snapshots and shadows. A managed account can never fall back to direct WS.
func (s *OpenAIVMService) websocketRoute(ctx context.Context, account *Account, token string, incoming http.Header) (*openAIVMWebSocketRoute, bool, error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return nil, false, nil
	}
	binding, err := s.repo.GetBinding(ctx, openAIVMOwnerID(account))
	if err != nil {
		return nil, true, err
	}
	if binding == nil {
		if account.IsOpenAIVMBound() {
			return nil, true, errors.New("VM binding is transitioning; direct WebSocket fallback is disabled")
		}
		return nil, false, nil
	}
	if binding.Status != "ready" {
		return nil, true, errors.New("OpenAI VM slot is not ready; direct WebSocket fallback is disabled")
	}
	config, secret, err := s.configured(ctx)
	if err != nil {
		return nil, true, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, true, errors.New("OpenAI VM requires a bearer access token")
	}
	endpoint, err := url.Parse(fmt.Sprintf("%s/v1/accounts/%d/responses", config.BaseURL, binding.AccountID))
	if err != nil {
		return nil, true, err
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	headers := openAIVMRequestHeaders(incoming)
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(ctx, s.accounts, headers, account); err != nil {
		return nil, true, fmt.Errorf("resolve VM WebSocket account identity: %w", err)
	}
	headers.Set("X-VM2API-Access-Token", token)
	headers.Set("X-VM2API-ChatGPT-Account-ID", headers.Get("Chatgpt-Account-Id"))
	headers.Del("Chatgpt-Account-Id")
	headers.Set("Authorization", "Bearer "+secret)
	return &openAIVMWebSocketRoute{url: endpoint.String(), headers: headers, client: s.client}, true, nil
}
