package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const codexRetryWSBufferLimit = 4 * 1024 * 1024
const codexRetryWSFrameLimit = 8192

type codexRetryWSFrame struct {
	kind coderws.MessageType
	body []byte
}

type codexRetryWSTurn struct {
	settings     CodexPreOutputRetrySettings
	request      []byte
	started      time.Time
	retries      int
	buffer       []codexRetryWSFrame
	bufferBytes  int
	pendingError []byte
	committed    bool
	exhausted    bool
}

// Only completed failed turns can be replayed on the same connection. Buffer
// error/response.failed pairs together so the first event cannot leak early.
type codexRetryWSFrameConn struct {
	inner     openaiwsv2.FrameConn
	settings  func(context.Context) CodexPreOutputRetrySettings
	onRetry   func([]byte)
	mu        sync.Mutex
	turn      *codexRetryWSTurn
	ready     []codexRetryWSFrame
	readError error
}

func (s *OpenAIGatewayService) codexRetryWSConn(c *gin.Context, account *Account, inner openaiwsv2.FrameConn) openaiwsv2.FrameConn {
	if s.settingService == nil || account == nil || account.Platform != PlatformOpenAI {
		return inner
	}
	return &codexRetryWSFrameConn{
		inner: inner, settings: s.settingService.GetCodexPreOutputRetrySettingsCached,
		onRetry: func(payload []byte) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
				Kind: "retry", Message: sanitizeUpstreamErrorMessage(extractOpenAISSEErrorMessage(payload)),
			})
		},
	}
}

func (c *codexRetryWSFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	settings := c.settings(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if kind == coderws.MessageText && gjson.GetBytes(payload, "type").String() == "response.create" && settings.Enabled {
		c.turn = &codexRetryWSTurn{settings: settings, request: append([]byte(nil), payload...), started: time.Now()}
	} else if c.turn != nil {
		// Cancellation or other client-side state changes invalidate replay.
		c.turn.committed = true
	}
	return c.inner.WriteFrame(ctx, kind, payload)
}

func (c *codexRetryWSFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	for {
		c.mu.Lock()
		if len(c.ready) > 0 {
			frame := c.ready[0]
			c.ready[0] = codexRetryWSFrame{}
			c.ready = c.ready[1:]
			c.mu.Unlock()
			return frame.kind, frame.body, nil
		}
		if c.readError != nil {
			err := c.readError
			c.mu.Unlock()
			return coderws.MessageText, nil, err
		}
		readCtx := ctx
		cancel := func() {}
		if c.turn != nil && !c.turn.committed && len(c.turn.pendingError) > 0 {
			readCtx, cancel = context.WithDeadline(ctx, c.turn.started.Add(time.Duration(c.turn.settings.MaxRetryWindowSeconds)*time.Second))
		}
		c.mu.Unlock()
		kind, payload, err := c.inner.ReadFrame(readCtx)
		cancel()
		c.mu.Lock()
		turn := c.turn
		if err != nil {
			if turn != nil && len(turn.buffer) > 0 && ctx.Err() == nil {
				if turn.settings.BufferUntilComplete && !turn.committed && len(turn.pendingError) > 0 {
					c.ready = codexRetryWSErrorFrames(turn.buffer)
					turn.exhausted = true
				} else {
					c.ready = turn.buffer
				}
				turn.buffer, turn.pendingError, turn.bufferBytes = nil, nil, 0
				c.readError = err
				turn.committed = true
				c.mu.Unlock()
				continue
			}
			c.mu.Unlock()
			return kind, payload, err
		}
		if turn == nil || (turn.committed && len(turn.buffer) == 0) {
			c.mu.Unlock()
			return kind, payload, nil
		}
		eventType := gjson.GetBytes(payload, "type").String()
		matched := kind == coderws.MessageText && (eventType == "error" || eventType == "response.failed") && turn.settings.matches(payload, "")
		terminalErrorMissing := !gjson.GetBytes(payload, "response.error").IsObject() && !gjson.GetBytes(payload, "error").IsObject()
		delay := turn.settings.retryDelay(turn.retries, nil, time.Now())
		if matched && !turn.committed && !turn.settings.canRetryAfter(turn.retries, turn.started, delay) {
			turn.exhausted = true
		}
		if kind == coderws.MessageText && !turn.committed && eventType == "response.failed" && (matched || (terminalErrorMissing && len(turn.pendingError) > 0)) && turn.settings.canRetryAfter(turn.retries, turn.started, delay) {
			c.mu.Unlock()
			if err := waitCodexRetry(ctx, delay); err != nil {
				return kind, nil, err
			}
			c.mu.Lock()
			if c.turn != turn || turn.committed || !turn.settings.canRetryAfter(turn.retries, turn.started, 0) {
				c.ready = codexRetryWSFailureFrames(turn, kind, payload)
				turn.exhausted = true
				turn.committed = true
				turn.buffer = nil
				c.mu.Unlock()
				continue
			}
			turn.retries++
			// The terminal event has been consumed; keep previous_response_id and
			// connection-local conversation state intact when resending the turn.
			err := c.inner.WriteFrame(ctx, coderws.MessageText, turn.request)
			if err != nil {
				// Deliver the original failure before reporting a broken transport.
				c.ready = codexRetryWSFailureFrames(turn, kind, payload)
				turn.buffer, turn.pendingError, turn.bufferBytes = nil, nil, 0
				turn.committed, turn.exhausted = true, true
				c.readError = err
				c.mu.Unlock()
				continue
			}
			turn.buffer, turn.pendingError, turn.bufferBytes = nil, nil, 0
			c.mu.Unlock()
			if c.onRetry != nil {
				c.onRetry(payload)
			}
			continue
		}
		if kind == coderws.MessageText && eventType == "response.failed" && turn.settings.BufferUntilComplete && !turn.committed {
			c.ready = codexRetryWSFailureFrames(turn, kind, payload)
			turn.exhausted = matched || (terminalErrorMissing && len(turn.pendingError) > 0)
			turn.buffer, turn.pendingError, turn.bufferBytes = nil, nil, 0
			turn.committed = true
			c.mu.Unlock()
			continue
		}
		turn.buffer = append(turn.buffer, codexRetryWSFrame{kind: kind, body: append([]byte(nil), payload...)})
		turn.bufferBytes += len(payload)
		if matched && eventType == "error" && !turn.committed && turn.bufferBytes < codexRetryWSBufferLimit && len(turn.buffer) < codexRetryWSFrameLimit && turn.settings.canRetryAfter(turn.retries, turn.started, delay) {
			turn.pendingError = append([]byte(nil), payload...)
		} else if turn.committed || kind != coderws.MessageText || (!turn.settings.BufferUntilComplete && openAIStreamDataStartsClientOutput(string(payload), eventType)) || openAIStreamEventTypeIsTerminal(eventType) || eventType == "error" || turn.bufferBytes >= codexRetryWSBufferLimit || len(turn.buffer) >= codexRetryWSFrameLimit || time.Since(turn.started) >= time.Duration(turn.settings.MaxRetryWindowSeconds)*time.Second {
			if turn.settings.BufferUntilComplete && !turn.committed && eventType == "error" {
				c.ready = codexRetryWSErrorFrames(turn.buffer)
			} else {
				c.ready = turn.buffer
			}
			turn.buffer, turn.pendingError, turn.bufferBytes = nil, nil, 0
			turn.committed = true
		}
		c.mu.Unlock()
	}
}

func codexRetryWSFailureFrames(turn *codexRetryWSTurn, kind coderws.MessageType, payload []byte) []codexRetryWSFrame {
	frames := turn.buffer
	if turn.settings.BufferUntilComplete && !turn.committed {
		frames = codexRetryWSErrorFrames(turn.buffer)
	}
	return append(frames, codexRetryWSFrame{kind: kind, body: append([]byte(nil), payload...)})
}

func codexRetryWSErrorFrames(buffer []codexRetryWSFrame) []codexRetryWSFrame {
	var frames []codexRetryWSFrame
	for _, frame := range buffer {
		eventType := gjson.GetBytes(frame.body, "type").String()
		if eventType == "error" || eventType == "response.failed" {
			frames = append(frames, frame)
		}
	}
	return frames
}

func (c *codexRetryWSFrameConn) Close() error { return c.inner.Close() }

func codexRetryWSExhausted(conn openaiwsv2.FrameConn) bool {
	retry, ok := conn.(*codexRetryWSFrameConn)
	if !ok {
		return false
	}
	retry.mu.Lock()
	defer retry.mu.Unlock()
	return retry.turn != nil && retry.turn.exhausted
}

type codexRetryLeaseFrameConn struct {
	lease        *openAIWSConnLease
	readTimeout  time.Duration
	writeTimeout time.Duration
}

func (c *codexRetryLeaseFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	payload, err := c.lease.ReadMessageWithContextTimeout(ctx, c.readTimeout)
	return coderws.MessageText, payload, err
}

func (c *codexRetryLeaseFrameConn) WriteFrame(ctx context.Context, _ coderws.MessageType, payload []byte) error {
	return c.lease.WriteJSONWithContextTimeout(ctx, json.RawMessage(payload), c.writeTimeout)
}

func (c *codexRetryLeaseFrameConn) Close() error { return nil }
