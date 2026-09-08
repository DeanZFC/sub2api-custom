package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const codexRetryHTTPBufferLimit = 4 * 1024 * 1024

type codexRetryReadChunk struct {
	data []byte
	err  error
}

// Retain one in-flight read when the buffering deadline expires, then hand it
// to the ordinary stream reader. Closing cancels both the producer and body.
type codexRetryBufferedBody struct {
	original           io.ReadCloser
	ctx                context.Context
	cancel             context.CancelFunc
	chunks             chan codexRetryReadChunk
	prefix             *bytes.Reader
	pending            []byte
	readErr            error
	close              sync.Once
	semanticOutputSeen bool
}

func newCodexRetryBufferedBody(ctx context.Context, original io.ReadCloser) *codexRetryBufferedBody {
	ctx, cancel := context.WithCancel(ctx)
	body := &codexRetryBufferedBody{original: original, ctx: ctx, cancel: cancel, chunks: make(chan codexRetryReadChunk, 1)}
	go func() {
		defer close(body.chunks)
		for {
			data := make([]byte, 32*1024)
			n, err := original.Read(data)
			select {
			case body.chunks <- codexRetryReadChunk{data: data[:n], err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return body
}

func (b *codexRetryBufferedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.prefix != nil && b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	for len(b.pending) == 0 {
		if b.readErr != nil {
			return 0, b.readErr
		}
		select {
		case <-b.ctx.Done():
			return 0, b.ctx.Err()
		case chunk, ok := <-b.chunks:
			if !ok {
				return 0, io.EOF
			}
			b.pending, b.readErr = chunk.data, chunk.err
		}
	}
	n := copy(p, b.pending)
	b.pending = b.pending[n:]
	return n, nil
}

func (b *codexRetryBufferedBody) Close() error {
	var err error
	b.close.Do(func() {
		b.cancel()
		err = b.original.Close()
	})
	return err
}

// Buffer wire bytes before the normal SSE parser sees any output. A matched
// failure can be discarded without exposing text or tool calls from that try.
// On the size/time limit, replay the prefix and continue streaming on the same
// body; never restart a request after handing its output to the normal parser.
func bufferCodexRetrySSE(ctx context.Context, original io.ReadCloser, settings CodexPreOutputRetrySettings, limit int, timeout time.Duration, firstOutputRemaining ...time.Duration) (*codexRetryBufferedBody, *openAICompatSSEFrame, error) {
	body := newCodexRetryBufferedBody(ctx, original)
	var prefix bytes.Buffer
	var line []byte
	parser := openAICompatSSEFrameParser{}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	var firstOutputTimer *time.Timer
	var firstOutputCh <-chan time.Time
	if len(firstOutputRemaining) > 0 && firstOutputRemaining[0] > 0 {
		firstOutputTimer = time.NewTimer(firstOutputRemaining[0])
		firstOutputCh = firstOutputTimer.C
		defer firstOutputTimer.Stop()
	}
	release := func() (*codexRetryBufferedBody, *openAICompatSSEFrame, error) {
		body.prefix = bytes.NewReader(prefix.Bytes())
		return body, nil, nil
	}
	inspect := func(frame openAICompatSSEFrame) (*openAICompatSSEFrame, bool) {
		frame.EventType = effectiveOpenAISSEEventType([]byte(frame.Data), frame.EventType)
		if (frame.EventType == "error" || frame.EventType == "response.failed") && settings.matches([]byte(frame.Data), "") {
			return &frame, true
		}
		if openAIStreamDataStartsClientOutput(frame.Data, frame.EventType) {
			body.semanticOutputSeen = true
			if firstOutputTimer != nil {
				firstOutputTimer.Stop()
				firstOutputCh = nil
			}
		}
		return nil, openAIStreamEventIsTerminalWithType(frame.Data, frame.EventType)
	}
	for {
		select {
		case <-ctx.Done():
			_ = body.Close()
			return nil, nil, ctx.Err()
		case <-timer.C:
			return release()
		case <-firstOutputCh:
			return release()
		case chunk, ok := <-body.chunks:
			if !ok {
				body.readErr = io.EOF
				return release()
			}
			if prefix.Len()+len(chunk.data) > limit {
				body.pending, body.readErr = chunk.data, chunk.err
				return release()
			}
			_, _ = prefix.Write(chunk.data)
			for _, ch := range chunk.data {
				if ch != '\n' {
					line = append(line, ch)
					continue
				}
				frame, complete := parser.AddLine(strings.TrimSuffix(string(line), "\r"))
				line = line[:0]
				if complete {
					failure, terminal := inspect(frame)
					if failure != nil {
						_ = body.Close()
						return nil, failure, nil
					}
					if terminal {
						body.readErr = chunk.err
						return release()
					}
				}
			}
			if chunk.err != nil {
				if len(line) > 0 {
					_, _ = parser.AddLine(strings.TrimSuffix(string(line), "\r"))
				}
				if frame, complete := parser.AddLine(""); complete {
					if failure, _ := inspect(frame); failure != nil {
						_ = body.Close()
						return nil, failure, nil
					}
				}
				body.readErr = chunk.err
				return release()
			}
		}
	}
}

func (s *OpenAIGatewayService) prepareCodexBufferedResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, firstOutputTimeout time.Duration) error {
	raw, _ := c.Get(codexRetrySettingsContextKey)
	settings, ok := raw.(CodexPreOutputRetrySettings)
	if !ok || !settings.Enabled || !settings.BufferUntilComplete || account == nil || account.Platform != PlatformOpenAI {
		return nil
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	interval := 10 * time.Second
	if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
		interval = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
	}
	retryStarted := startTime
	if rawStart, exists := c.Get(codexRetryStartedContextKey); exists {
		if started, valid := rawStart.(time.Time); valid {
			retryStarted = started
		}
	}
	timeout := time.Until(retryStarted.Add(time.Duration(settings.MaxRetryWindowSeconds) * time.Second))
	if timeout <= 0 {
		return nil
	}
	// Stop the official first-output deadline only after semantic progress.
	firstOutputRemaining := time.Duration(0)
	if firstOutputTimeout > 0 {
		firstOutputRemaining = time.Until(startTime.Add(firstOutputTimeout))
		if firstOutputRemaining <= 0 {
			return nil
		}
	}
	stopKeepalive := startOpenAISSEKeepalive(c, interval)
	body, failure, err := bufferCodexRetrySSE(ctx, resp.Body, settings, codexRetryHTTPBufferLimit, timeout, firstOutputRemaining)
	stopKeepalive()
	if err != nil {
		return err
	}
	if failure != nil {
		return s.newCodexPreOutputRetrySSEError(c, account, resp, []byte(failure.Data), failure.EventType, extractOpenAISSEErrorMessage([]byte(failure.Data)))
	}
	resp.Body = body
	return nil
}
