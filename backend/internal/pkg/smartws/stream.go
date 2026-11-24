package smartws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var eventTypePattern = regexp.MustCompile("^[A-Za-z0-9_.-]+$")

type eventBody struct {
	ctx                          context.Context
	pool                         *Pool
	connection                   *connection
	lease                        *lease
	readMu                       sync.Mutex
	mu                           sync.Mutex
	buffer                       []byte
	terminal, successful, closed bool
	responseID                   string
	previousID                   string
	stop                         chan struct{}
	once                         sync.Once
}

func newEventBody(ctx context.Context, p *Pool, c *connection, l *lease) *eventBody {
	p.mu.Lock()
	previousID := c.lastResponseID
	p.mu.Unlock()
	b := &eventBody{previousID: previousID, ctx: ctx, pool: p, connection: c, lease: l, stop: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = b.Close()
		case <-b.stop:
		}
	}()
	return b
}
func (b *eventBody) Close() error {
	b.once.Do(func() {
		b.mu.Lock()
		b.closed = true
		reuse := b.terminal && b.successful && len(b.buffer) == 0 && b.ctx.Err() == nil
		id := b.responseID
		b.mu.Unlock()
		close(b.stop)
		b.pool.release(b.connection, b.lease, reuse, id)
	})
	return nil
}
func (b *eventBody) failure(reason string) (int, error) {
	b.mu.Lock()
	b.successful = false
	b.mu.Unlock()
	b.pool.count("stream_failed")
	switch size := b.lease.requestBytes; {
	case size <= 64<<10:
		b.pool.count("stream_failure_request_le_64k")
	case size <= 256<<10:
		b.pool.count("stream_failure_request_le_256k")
	case size <= 1<<20:
		b.pool.count("stream_failure_request_le_1m")
	case size <= 4<<20:
		b.pool.count("stream_failure_request_le_4m")
	default:
		b.pool.count("stream_failure_request_le_8m")
	}

	codes := map[string]string{
		"request context cancelled":              "context_cancelled",
		"upstream stream closed":                 "lease_closed",
		"invalid upstream JSON event":            "invalid_json",
		"invalid upstream event type":            "invalid_event_type",
		"conflicting upstream response IDs":      "conflicting_response_id",
		"unexpected upstream response.created":   "unexpected_created",
		"unattributed upstream event":            "unattributed_event",
		"stale upstream response":                "stale_response",
		"upstream event before response.created": "before_created",
		"upstream response ID mismatch":          "response_id_mismatch",
		"invalid completed response":             "invalid_completed",
	}
	code := codes[reason]
	if code == "" {
		code = "connection_interrupted"
	}
	b.pool.count("stream_failure_" + code)

	_ = b.Close()
	return 0, &SentError{Err: errors.New(reason)}
}
func (b *eventBody) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	b.readMu.Lock()
	defer b.readMu.Unlock()
	var staleFrames int
	for {
		b.mu.Lock()
		if len(b.buffer) > 0 && !b.closed {
			n := copy(destination, b.buffer)
			b.buffer = b.buffer[n:]
			b.mu.Unlock()
			return n, nil
		}
		closed, terminal := b.closed, b.terminal
		b.mu.Unlock()
		if closed {
			if err := b.ctx.Err(); err != nil {
				return 0, &SentError{Err: err}
			}
			return 0, io.EOF
		}
		if terminal {
			_ = b.Close()
			return 0, io.EOF
		}
		var next frame
		select {
		case next = <-b.lease.events:
		case <-b.ctx.Done():
			return b.failure("request context cancelled")
		case <-b.lease.done:
			return b.failure("upstream stream closed")
		}
		if next.err != nil {
			var closeErr *websocket.CloseError
			if errors.As(next.err, &closeErr) {
				switch closeErr.Code {
				case 1000:
					b.pool.count("socket_close_normal")
				case 1001:
					b.pool.count("socket_close_going_away")
				case 1002:
					b.pool.count("socket_close_protocol")
				case 1003:
					b.pool.count("socket_close_unsupported")
				case 1006:
					b.pool.count("socket_close_abnormal")
				case 1008:
					b.pool.count("socket_close_policy")
				case 1009:
					b.pool.count("socket_close_too_big")
				case 1011:
					b.pool.count("socket_close_internal")
				default:
					b.pool.count("socket_close_other")
				}
			} else if errors.Is(next.err, websocket.ErrReadLimit) {
				b.pool.count("socket_frame_limit")
			}
			return b.failure("upstream connection interrupted or invalid")
		}
		if validateJSON(next.data) != nil {
			return b.failure("invalid upstream JSON event")
		}
		var event streamEnvelope
		if json.Unmarshal(next.data, &event) != nil || !eventTypePattern.MatchString(event.Type) {
			return b.failure("invalid upstream event type")
		}
		id, valid := event.ownerID()
		if !valid {
			return b.failure("conflicting upstream response IDs")
		}
		b.mu.Lock()
		current := b.responseID
		b.mu.Unlock()
		control := isControlEvent(event.Type)
		if b.previousID != "" && id == b.previousID {
			if control && staleFrames < maxIgnoredFrames {
				staleFrames++
				b.pool.count("stale_frame_ignored")
				continue
			}
			return b.failure("stale upstream response")
		}
		unboundFailure := event.Type == "error" || (event.Type == "response.failed" && id == "")
		if b.previousID != "" && (current == "" || id != current) &&
			(control || unboundFailure) && frameHasTurnEvidence(next.data) {
			return b.failure("unattributed upstream event")
		}
		if event.Type == "response.created" {
			if current != "" || id == "" {
				return b.failure("unexpected upstream response.created")
			}
			b.mu.Lock()
			b.responseID = id
			b.mu.Unlock()
			current = id
		} else if current == "" && event.Type != "error" && event.Type != "response.failed" && !isControlEvent(event.Type) {
			return b.failure("upstream event before response.created")
		}
		if current != "" && id != "" && id != current {
			return b.failure("upstream response ID mismatch")
		}
		terminal, success := false, false
		switch event.Type {
		case "response.completed", "response.done":
			terminal = true
			if event.Response == nil || event.Response.ID == "" || event.Response.ID != current || event.Response.Status != "completed" {
				return b.failure("invalid completed response")
			}
			success = true
		case "response.failed", "response.incomplete", "response.cancelled", "response.canceled", "error":
			terminal = true
		}
		var encoded bytes.Buffer
		var tail []byte
		if event.Type == "error" {
			if id == "" {
				id = current
			}
			// A bare error cannot establish turn ownership on a reused socket.
			if current != "" || b.previousID == "" {
				tail = b.failedTail(id)
			}
			if tail != nil {
				next.data, tail = reconcileFailureUsage(next.data, tail)
			}
		}
		writeStreamEvent(&encoded, event.Type, next.data)
		if tail != nil {
			writeStreamEvent(&encoded, "response.failed", tail)
		}
		b.mu.Lock()
		if b.closed {
			b.mu.Unlock()
			return 0, &SentError{Err: errors.New("stream closed during delivery")}
		}
		b.buffer = encoded.Bytes()
		b.terminal = terminal
		b.successful = success
		b.mu.Unlock()
		if terminal {
			if success {
				b.pool.count("completed")
			} else {
				b.pool.count("terminal_error")
			}
		}
	}
}

// Terminals are cumulative. Keep both failure envelopes consistent for
// consumers that stop at the first error or only read the failed terminal.
func reconcileFailureUsage(first, tail []byte) ([]byte, []byte) {
	usage := gjson.GetBytes(tail, "response.usage")
	if hasUsageTokens(usage) {
		if enriched, err := sjson.SetRawBytes(first, "usage", []byte(usage.Raw)); err == nil {
			first = enriched
		}
	} else {
		usage = gjson.GetBytes(first, "usage")
		if !hasUsageTokens(usage) {
			usage = gjson.GetBytes(first, "response.usage")
		}
		if hasUsageTokens(usage) {
			if enriched, err := sjson.SetRawBytes(tail, "response.usage", []byte(usage.Raw)); err == nil {
				tail = enriched
			}
		}
	}
	return first, tail
}

func hasUsageTokens(usage gjson.Result) bool {
	for _, k := range []string{"input_tokens", "output_tokens", "total_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
		if n := usage.Get(k); n.Type == gjson.Number && n.Int() > 0 {
			return true
		}
	}
	return false
}

func writeStreamEvent(encoded *bytes.Buffer, kind string, data []byte) {
	encoded.WriteString("event: ")
	encoded.WriteString(kind)
	encoded.WriteByte('\n')
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		encoded.WriteString("data: ")
		encoded.Write(line)
		encoded.WriteByte('\n')
	}
	encoded.WriteByte('\n')
}

type streamEnvelope struct {
	Type       string `json:"type"`
	ResponseID string `json:"response_id"`
	Response   *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"response"`
}

// A top-level event id is not a response id.
func (e streamEnvelope) ownerID() (string, bool) {
	if e.Response != nil && e.Response.ID != "" {
		return e.Response.ID, e.ResponseID == "" || e.ResponseID == e.Response.ID
	}
	return e.ResponseID, true
}

// Connection metadata (headers, rate limits, timestamps) is not generation or
// usage. Only inspect control frames and evidence of a possibly unrelated failure.
func frameHasTurnEvidence(data []byte) bool {
	var work bool
	inspect := func(k, v gjson.Result) bool {
		if v.Type == gjson.Null {
			return true
		}
		switch k.Str {
		case "output":
			work = !v.IsArray() || v.Get("#").Int() > 0
		case "usage", "item", "part", "content", "delta", "encrypted_content", "tool_calls", "call_id", "arguments", "output_text", "refusal", "result":
			work = true
		}
		return !work
	}
	frame := gjson.ParseBytes(data)
	frame.ForEach(inspect)
	if !work {
		frame.Get("response").ForEach(inspect)
	}
	return work
}

// Codex sends these connection/request control envelopes before response.created.
// Preserve them as SSE (including metadata headers) without treating them as
// generation ownership or copying their fields into mutable HTTP headers.
func isControlEvent(kind string) bool {
	switch kind {
	case "codex.rate_limits", "codex.response.metadata", "responsesapi.websocket_timing", "keepalive":
		return true
	default:
		return false
	}
}

const maxIgnoredFrames = 8

// Only the matching failed terminal can enrich an error. A different
// lifecycle event ends the search; the failed socket is never reused.
func (b *eventBody) failedTail(id string) []byte {
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	for i := 0; i < maxIgnoredFrames; i++ {
		select {
		case <-b.ctx.Done():
			return nil
		case <-b.stop:
			return nil
		case <-b.lease.done:
			return nil
		case <-timer.C:
			return nil
		case f := <-b.lease.events:
			var e streamEnvelope
			if f.err != nil || validateJSON(f.data) != nil || json.Unmarshal(f.data, &e) != nil {
				return nil
			}
			owner, ok := e.ownerID()
			if !ok {
				return nil
			}
			if b.previousID != "" && owner == b.previousID && isControlEvent(e.Type) {
				b.pool.count("stale_frame_ignored")
				continue
			}
			if owner != "" && (owner == b.previousID || id != "" && id != owner) {
				return nil
			}
			if e.Type == "response.failed" && owner != "" && (id == owner || id == "" && b.previousID == "") {
				return f.data
			}
			if !isControlEvent(e.Type) || frameHasTurnEvidence(f.data) {
				return nil
			}
		}
	}
	return nil
}
