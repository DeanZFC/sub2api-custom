package smartws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sync"

	"github.com/gorilla/websocket"
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
	stop                         chan struct{}
	once                         sync.Once
}

func newEventBody(ctx context.Context, p *Pool, c *connection, l *lease) *eventBody {
	b := &eventBody{ctx: ctx, pool: p, connection: c, lease: l, stop: make(chan struct{})}
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
		var event struct {
			Type       string `json:"type"`
			ResponseID string `json:"response_id"`
			Response   *struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(next.data, &event) != nil || !eventTypePattern.MatchString(event.Type) {
			return b.failure("invalid upstream event type")
		}
		id := event.ResponseID
		if event.Response != nil && event.Response.ID != "" {
			if id != "" && id != event.Response.ID {
				return b.failure("conflicting upstream response IDs")
			}
			id = event.Response.ID
		}
		b.mu.Lock()
		current := b.responseID
		b.mu.Unlock()
		if event.Type == "response.created" {
			if current != "" || id == "" {
				return b.failure("unexpected upstream response.created")
			}
			b.pool.mu.Lock()
			last := b.connection.lastResponseID
			b.pool.mu.Unlock()
			if id == last {
				return b.failure("stale upstream response")
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
		_, _ = encoded.WriteString("event: ")
		_, _ = encoded.WriteString(event.Type)
		_ = encoded.WriteByte('\n')
		for _, line := range bytes.Split(next.data, []byte{'\n'}) {
			_, _ = encoded.WriteString("data: ")
			_, _ = encoded.Write(line)
			_ = encoded.WriteByte('\n')
		}
		_ = encoded.WriteByte('\n')
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

// Codex sends these connection/request control envelopes before response.created.
// Preserve them as SSE (including metadata headers) without treating them as
// generation ownership or copying their fields into mutable HTTP headers.
func isControlEvent(kind string) bool {
	switch kind {
	case "codex.rate_limits", "codex.response.metadata", "responsesapi.websocket_timing":
		return true
	default:
		return false
	}
}
