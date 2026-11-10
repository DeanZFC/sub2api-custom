// Package smartws optionally adapts complete Responses HTTP requests to upstream
// WebSocket messages. It never owns account selection or retries a dispatched request.
package smartws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/gorilla/websocket"
)

// SentError means a generation may have reached upstream. Callers must not replay
// this request merely because no response bytes have reached the client yet.
type SentError struct{ Err error }

func (e *SentError) Error() string     { return "smart WS request possibly dispatched: " + e.Err.Error() }
func (e *SentError) Unwrap() error     { return e.Err }
func (e *SentError) RequestSent() bool { return true }

// Local buffer limits retained from the imported adapter, not upstream guarantees.
// Check both the original body and the encoded wire message.
const maxRequestBytes = 16 << 20
const maxFrameBytes = 8 << 20
const maxNegativeEntries = 256

type poolConfig struct {
	capacity, perKey                            int
	adaptive                                    bool
	perAccount, hardCapacity                    int
	wait                                        time.Duration
	maxWaiters, accountWaiters, maxWaitingBytes int
	idle, lifetime, handshake                   time.Duration
	allowed                                     func(*url.URL) bool
	dialer                                      *websocket.Dialer
	dialConcurrent, dialBurst                   int
	dialRate                                    float64
	dialBackoff, dialBackoffMax                 time.Duration
}

// Pool has bounded, exclusive leases; saturation falls back before dispatch.
type Pool struct {
	mu                    sync.Mutex
	config                poolConfig
	connections           map[*connection]struct{}
	accounts              map[string]*accountState
	changed               chan struct{}
	waiting, waitingBytes int
	opening, active       int
	openingKeys           map[string]int
	negative              map[string]time.Time
	dials                 map[string]*dialState
	handshakes            []handshakeDiagnostic
	counters              map[string]uint64
	closed                bool
	stop                  chan struct{}
	closeOnce             sync.Once
}
type connection struct {
	pool           *Pool
	ws             *websocket.Conn
	key            string
	capabilityKey  string
	accountKey     string
	reusable       bool
	created, used  time.Time
	active         *lease
	lastResponseID string
	closeOnce      sync.Once
}
type lease struct {
	requestBytes int
	events       chan frame
	done         chan struct{}
	once         sync.Once
}
type frame struct {
	data []byte
	err  error
}

// NewPool only permits the existing ChatGPT Codex HTTPS Responses endpoint.
func NewPool() *Pool {
	return newPool(poolConfig{capacity: 128, perKey: 64, adaptive: true, perAccount: 256, hardCapacity: 4096,
		wait: 2500 * time.Millisecond, maxWaiters: 256, accountWaiters: 32, maxWaitingBytes: 64 << 20, idle: 300 * time.Second,
		lifetime: 20 * time.Minute, handshake: 5 * time.Second, allowed: productionTarget,
		dialer: &websocket.Dialer{HandshakeTimeout: 5 * time.Second, EnableCompression: false,
			ReadBufferSize: 16 << 10, WriteBufferSize: 16 << 10,
			NetDialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext}})
}
func newPool(c poolConfig) *Pool {
	configureDialAdmission(&c)
	p := &Pool{config: c, connections: make(map[*connection]struct{}), openingKeys: make(map[string]int),
		accounts: make(map[string]*accountState), changed: make(chan struct{}),
		negative: make(map[string]time.Time), dials: make(map[string]*dialState), counters: make(map[string]uint64), stop: make(chan struct{})}
	go p.sweep()
	return p
}
func productionTarget(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), "chatgpt.com") &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.Fragment == "" &&
		u.Path == "/backend-api/codex/responses"
}
func (p *Pool) count(name string) { p.mu.Lock(); p.counters[name]++; p.mu.Unlock() }
func (p *Pool) Stats() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	capacity, accounts := p.capacityLocked(time.Now())
	idle := 0
	for c := range p.connections {
		if c.active == nil {
			idle++
		}
	}
	result := map[string]any{"connections": len(p.connections), "connecting": p.opening,
		"active": p.active, "capacity": capacity, "closed": p.closed,
		"account_count": accounts, "account_capacity": p.config.perAccount,
		"identity_capacity": p.config.perKey, "capacity_limit": p.config.hardCapacity,
		"idle": idle, "waiting": p.waiting, "waiting_bytes": p.waitingBytes,
		"wait_limit_ms": p.config.wait.Milliseconds(), "wait_limit": p.config.maxWaiters,
		"wait_account_limit": p.config.accountWaiters, "wait_bytes_limit": p.config.maxWaitingBytes,
		"request_limit_bytes": maxRequestBytes, "idle_timeout_seconds": p.config.idle.Seconds(),
		"connection_lifetime_seconds": p.config.lifetime.Seconds()}
	result["dial_concurrency_per_capability"] = p.config.dialConcurrent
	result["dial_rate_per_second"] = p.config.dialRate
	result["dial_burst"] = p.config.dialBurst
	result["dial_capabilities"] = len(p.dials)
	result["handshake_diagnostics"] = append([]handshakeDiagnostic(nil), p.handshakes...)
	for k, v := range p.counters {
		result[k] = v
	}
	return result
}
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.stop)
		all := make([]*connection, 0, len(p.connections))
		for c := range p.connections {
			all = append(all, c)
		}
		p.mu.Unlock()
		for _, c := range all {
			c.discard()
		}
	})
}
func (p *Pool) sweep() {
	timer := time.NewTicker(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-p.stop:
			return
		case now := <-timer.C:
			p.expireIdle(now)
		}
	}
}

// Detach expired idle connections while holding the acquisition lock. Closing
// later must never invalidate a lease that became active after the sweep.
func (p *Pool) expireIdle(now time.Time) {
	p.mu.Lock()
	stale := p.expiredIdleLocked(now)
	p.pruneAccountsLocked(now)
	for k, expiry := range p.negative {
		if !expiry.After(now) {
			delete(p.negative, k)
		}
	}
	p.mu.Unlock()
	for _, c := range stale {
		c.discard()
	}
}

func (p *Pool) expired(c *connection, now time.Time) bool {
	return now.Sub(c.used) >= p.config.idle || now.Sub(c.created) >= p.config.lifetime
}
func (c *connection) discard() {
	c.closeOnce.Do(func() {
		c.pool.mu.Lock()
		c.pool.detachLocked(c)
		c.pool.signalLocked()
		c.pool.mu.Unlock()
		_ = c.ws.Close()
	})
}
func newLease() *lease   { return &lease{events: make(chan frame, 1), done: make(chan struct{})} }
func (l *lease) finish() { l.once.Do(func() { close(l.done) }) }

// The sole socket reader is retained while idle. Unsolicited idle frames poison
// the connection instead of becoming the next user's response.
func (c *connection) readLoop() {
	for {
		kind, data, err := c.ws.ReadMessage()
		if err == nil && kind != websocket.TextMessage {
			err = errors.New("non_text_frame")
		}
		if err != nil {
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) && (closeErr.Code == 1008 || closeErr.Code == 1009) {
				c.pool.negativeCache(c.capabilityKey)
			}
		}
		c.pool.mu.Lock()
		current := c.active
		// Once an error or unsolicited idle frame is known, make the socket
		// unavailable under the same lock used by acquisition. Closing later
		// must never race a fresh lease onto a known-dead connection.
		if err != nil || current == nil {
			c.pool.detachLocked(c)
			c.pool.signalLocked()
		}
		c.pool.mu.Unlock()
		if current == nil {
			c.discard()
			return
		}
		if err != nil {
			select {
			case current.events <- frame{err: err}:
			case <-current.done:
			}
			c.discard()
			return
		}
		select {
		case current.events <- frame{data: data}:
		case <-current.done:
			// A frame received for a completed lease may not be rerouted to a new one.
			c.discard()
			return
		}
	}
}
func (p *Pool) release(c *connection, l *lease, reuse bool, responseID string) {
	p.mu.Lock()
	if c.active != l {
		p.mu.Unlock()
		return
	}
	c.active = nil
	p.active--
	if responseID != "" {
		c.lastResponseID = responseID
	}
	c.used = time.Now()
	_, present := p.connections[c]
	keep := reuse && c.reusable && present && !p.closed && !p.expired(c, c.used)
	if !keep {
		p.detachLocked(c)
	}
	p.signalLocked()
	p.mu.Unlock()
	l.finish()
	if !keep {
		c.discard()
	}
}
func (p *Pool) negativeCache(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for k, t := range p.negative {
		if !t.After(now) {
			delete(p.negative, k)
		}
	}
	if len(p.negative) >= maxNegativeEntries {
		var oldestKey string
		var oldest time.Time
		for k, t := range p.negative {
			if oldestKey == "" || t.Before(oldest) {
				oldestKey = k
				oldest = t
			}
		}
		delete(p.negative, oldestKey)
	}
	p.negative[key] = now.Add(60 * time.Second)
}
func digest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(h, "%d:", len(part))
		_, _ = io.WriteString(h, part)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func handshakeHeaders(in http.Header) http.Header {
	out := make(http.Header)
	blocked := map[string]bool{"connection": true, "upgrade": true, "host": true, "content-length": true,
		"transfer-encoding": true, "content-type": true, "accept": true, "accept-encoding": true,
		"proxy-authorization": true, "proxy-connection": true, "keep-alive": true, "trailer": true, "te": true}
	for _, line := range in.Values("Connection") {
		for _, name := range strings.Split(line, ",") {
			blocked[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for name, values := range in {
		lower := strings.ToLower(name)
		if blocked[lower] || strings.HasPrefix(lower, "sec-websocket-") {
			continue
		}
		for _, v := range values {
			out.Add(name, v)
		}
	}
	beta := out.Get("OpenAI-Beta")
	if !strings.Contains(beta, "responses_websockets=") {
		if beta != "" {
			beta += ","
		}
		out.Set("OpenAI-Beta", beta+"responses_websockets=2026-02-06")
	}
	return out
}
func headersKey(h http.Header, capability bool) string {
	normalized := make(map[string][]string)
	for k, v := range h {
		lower := strings.ToLower(k)
		if capability && lower != "authorization" && lower != "chatgpt-account-id" && lower != "openai-beta" {
			continue
		}
		normalized[lower] = append(normalized[lower], v...)
	}
	keys := make([]string, 0, len(normalized))
	for k := range normalized {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%d:%s", len(k), k)
		for _, v := range normalized[k] {
			fmt.Fprintf(&b, "%d:%s", len(v), v)
		}
	}
	return b.String()
}
func reliableSession(h http.Header) bool {
	for _, k := range []string{"Session_id", "Session-Id", "Conversation_id", "Conversation-Id"} {
		if strings.TrimSpace(h.Get(k)) != "" {
			return true
		}
	}
	return false
}

// Try leaves req.Body untouched. handled=false guarantees no generation was sent,
// so the caller can invoke its original HTTP/plugin implementation unchanged.
func (p *Pool) Try(req *http.Request, proxyURL, scope string) (*http.Response, bool, error) {
	if p == nil || req == nil || req.Method != http.MethodPost || req.URL == nil ||
		!p.config.allowed(req.URL) || req.GetBody == nil {
		return nil, false, nil
	}
	if err := req.Context().Err(); err != nil {
		return nil, true, err
	}
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
		return nil, false, nil
	}
	if req.ContentLength > maxRequestBytes {
		p.count("request_too_large")
		return nil, false, nil
	}
	if req.ContentLength <= 0 {
		return nil, false, nil
	}
	budgetCtx, releaseBudget := httputil.WithRequestBodyMemoryScope(req.Context())
	defer releaseBudget()
	if err := httputil.ReserveRequestBodyMemory(budgetCtx, 4*(req.ContentLength+1)); err != nil {
		return nil, true, err
	}
	copyBody, err := req.GetBody()
	if err != nil {
		return nil, false, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(copyBody, req.ContentLength+1))
	_ = copyBody.Close()
	if int64(len(data)) != req.ContentLength || len(data) > maxRequestBytes {
		p.count("request_too_large")
		return nil, false, nil
	}
	if readErr != nil {
		p.count("ineligible")
		return nil, false, nil
	}
	message, ok := prepareMessage(data)
	if !ok {
		p.count("ineligible")
		return nil, false, nil
	}
	if len(message) > maxRequestBytes {
		p.count("request_too_large")
		return nil, false, nil
	}
	dialer := *p.config.dialer
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		proxy, parseErr := url.Parse(proxyURL)
		if parseErr != nil || proxy.Hostname() == "" || (proxy.Scheme != "http" && proxy.Scheme != "socks5" && proxy.Scheme != "socks5h") ||
			proxy.Fragment != "" || proxy.RawQuery != "" || (proxy.Path != "" && proxy.Path != "/") {
			p.count("unsupported_proxy")
			return nil, false, nil
		}
		if proxy.Scheme == "socks5h" {
			proxy.Scheme = "socks5"
		}
		dialer.Proxy = http.ProxyURL(proxy)
	} else {
		dialer.Proxy = nil
	}
	headers := handshakeHeaders(req.Header)
	u := *req.URL
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	key := digest(u.String(), proxyURL, scope, headersKey(headers, false))
	capabilityKey := digest(u.String(), proxyURL, headersKey(headers, true))
	accountKey := accountAdmissionKey(&u, headers)
	reusable := strings.TrimSpace(scope) != "" && reliableSession(headers)
	var c *connection
	var permit *dialPermit
	responseHeaders := make(http.Header)
	l := newLease()
	l.requestBytes = len(message)
	c, permit, err = p.acquire(req.Context(), key, capabilityKey, accountKey, reusable, l)
	if err != nil {
		return nil, true, err
	}
	if c == nil && permit == nil {
		return nil, false, nil
	}
	if c == nil {
		dialCtx, cancel := context.WithTimeout(req.Context(), p.config.handshake)
		ws, resp, dialErr := dialer.DialContext(dialCtx, u.String(), headers)
		cancel()
		if dialErr != nil {
			sample, complete := sampleHandshakeBody(resp)
			outcome := "connect_error"
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				outcome = "http_error"
				switch resp.StatusCode {
				case 400, 404, 405, 426, 501:
					outcome = "unsupported"
				}
				if resp.StatusCode == 403 && complete && len(sample) == 0 {
					outcome = "blank_403"
				}
			}
			p.recordHandshake(key, capabilityKey, headers, resp, sample, complete, outcome)
			p.mu.Lock()
			p.finishDialLocked(permit, outcome, time.Now())
			p.finishOpeningLocked(key, accountKey)
			p.mu.Unlock()
			if ws != nil {
				_ = ws.Close()
			}
			if req.Context().Err() != nil {
				if resp != nil && resp.Body != nil {
					_ = resp.Body.Close()
				}
				return nil, true, req.Context().Err()
			}
			if outcome == "blank_403" {
				if resp.Body != nil {
					_ = resp.Body.Close()
				}
				p.count("blank_403_fallback")
				return nil, false, nil
			}
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				switch resp.StatusCode {
				case 400, 404, 405, 426, 501:
					if resp.Body != nil {
						_ = resp.Body.Close()
					}
					p.negativeCache(capabilityKey)
					p.count("unsupported_handshake")
					return nil, false, nil
				default:
					p.count("handshake_http_error")
					resp.Request = req
					if resp.Body == nil {
						resp.Body = http.NoBody
					}
					return resp, true, nil
				}
			}
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			p.count("connect_fallback")
			return nil, false, nil
		}
		responseHeaders = handshakeResponseHeaders(resp)
		ws.SetReadLimit(maxFrameBytes)
		c = &connection{pool: p, ws: ws, key: key, capabilityKey: capabilityKey, accountKey: accountKey, reusable: reusable, created: time.Now(), used: time.Now(), active: l}
		p.mu.Lock()
		p.finishDialLocked(permit, "success", time.Now())
		p.finishOpeningLocked(key, accountKey)
		if p.closed {
			p.mu.Unlock()
			_ = ws.Close()
			return nil, false, nil
		}
		p.connections[c] = struct{}{}
		p.accounts[accountKey].sockets++
		p.active++
		p.counters["opened"]++
		p.mu.Unlock()
		go c.readLoop()
	}
	if err := req.Context().Err(); err != nil {
		p.release(c, l, false, "")
		return nil, true, err
	}
	deadline := time.Now().Add(p.config.handshake)
	if requested, exists := req.Context().Deadline(); exists && requested.Before(deadline) {
		deadline = requested
	}
	_ = c.ws.SetWriteDeadline(deadline)
	// Dispatch begins here. Even a partial/failed write is not safely replayable.
	err = c.ws.WriteMessage(websocket.TextMessage, message)
	_ = c.ws.SetWriteDeadline(time.Time{})
	if err != nil {
		p.count("dispatch_uncertain")
		p.release(c, l, false, "")
		return nil, true, &SentError{Err: errors.New("upstream write interrupted")}
	}
	p.count("dispatched")
	body := newEventBody(req.Context(), p, c, l)
	responseHeaders.Set("Content-Type", "text/event-stream")
	responseHeaders.Set("X-Sub2-Upstream-Transport", "websocket")
	return &http.Response{StatusCode: 200, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: responseHeaders,
		Body:   body, ContentLength: -1, Request: req}, true, nil
}

// validateJSON rejects duplicate keys at every nesting level and excessive
// nesting. The ordinary decoder alone silently accepts duplicate object keys.
func validateJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 128 {
			return errors.New("JSON nesting limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				seen := make(map[string]bool)
				for decoder.More() {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					s, ok := key.(string)
					if !ok || seen[s] {
						return errors.New("duplicate JSON field")
					}
					seen[s] = true
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := decoder.Token()
				if err != nil || end != json.Delim('}') {
					return errors.New("invalid object")
				}
			case '[':
				for decoder.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := decoder.Token()
				if err != nil || end != json.Delim(']') {
					return errors.New("invalid array")
				}
			default:
				return errors.New("unexpected delimiter")
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	_, err := decoder.Token()
	if err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func prepareMessage(data []byte) ([]byte, bool) {
	if validateJSON(data) != nil {
		return nil, false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return nil, false
	}
	if string(bytes.TrimSpace(object["stream"])) != "true" {
		return nil, false
	}
	if value, exists := object["background"]; exists {
		v := string(bytes.TrimSpace(value))
		if v != "false" && v != "null" {
			return nil, false
		}
	}
	for _, key := range []string{"previous_response_id", "conversation"} {
		if value, exists := object[key]; exists {
			if string(bytes.TrimSpace(value)) != "null" {
				return nil, false
			}
			// Explicit null carries no server-side continuation; full input is intact.
			delete(object, key)
		}
	}
	for _, key := range []string{"type", "stream_id"} {
		if _, exists := object[key]; exists {
			return nil, false
		}
	}
	var metadata map[string]json.RawMessage
	if value, exists := object["metadata"]; exists && json.Unmarshal(value, &metadata) == nil {
		for _, key := range []string{"previous_response_id", "conversation"} {
			if _, exists := metadata[key]; exists {
				return nil, false
			}
		}
	}
	delete(object, "stream")
	delete(object, "background")
	object["type"] = json.RawMessage("\"response.create\"")
	// A WebSocket frame is JSON, never embedded HTML. Escaping <, >, &
	// would expand ordinary tool/document content up to sixfold for no benefit.
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(object)
	return bytes.TrimSuffix(encoded.Bytes(), []byte{"\n"[0]}), err == nil
}
