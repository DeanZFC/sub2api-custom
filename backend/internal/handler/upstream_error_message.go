package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// UpstreamErrorMessageMiddleware must be installed BEFORE OpsErrorLogger: the
// inner capture writer sees the original error, then this outer writer changes
// only the client copy. Hijacked WebSockets use the same request policy directly.
func UpstreamErrorMessageMiddleware(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		message := settings.GetUpstreamErrorMessage(c.Request.Context())
		c.Request = c.Request.WithContext(service.WithUpstreamErrorMessage(c.Request.Context(), message))
		if message == "" {
			c.Next()
			return
		}
		original := c.Writer
		w := &upstreamErrorMessageWriter{ResponseWriter: original, c: c}
		c.Writer = w
		defer func() {
			w.finish()
			c.Writer = original
		}()
		c.Next()
	}
}

type upstreamErrorMessageWriter struct {
	gin.ResponseWriter
	c       *gin.Context
	mu      sync.Mutex
	pending []byte
	sse     bool
	started bool
	size    int
	scanPos int
	lineLen int
	skipLF  bool
}

func (w *upstreamErrorMessageWriter) WriteHeaderNow() {
	// The original body's byte length may change. Never retain a stale length.
	contentType := w.Header().Get("Content-Type")
	if w.Status() >= 400 || strings.Contains(contentType, "json") || strings.Contains(contentType, "text/event-stream") {
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeaderNow()
}

func (w *upstreamErrorMessageWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		return w.ResponseWriter.Size()
	}
	return w.size
}

func (w *upstreamErrorMessageWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *upstreamErrorMessageWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		w.started = true
		w.sse = strings.Contains(w.Header().Get("Content-Type"), "text/event-stream")
	}
	if !w.sse && w.Status() >= 400 && hasOpsUpstreamErrorContext(w.c) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	w.WriteHeaderNow()
	w.size += len(p)
	if w.sse {
		w.pending = append(w.pending, p...)
		if err := w.drainFrames(); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if w.Status() >= 400 || strings.Contains(w.Header().Get("Content-Type"), "json") {
		if len(w.pending) == 0 && gjson.ValidBytes(p) {
			// Most JSON responses arrive in one Write. Inspect that slice directly
			// so successful image/base64 responses do not acquire a second copy.
			_, err := w.ResponseWriter.Write(w.wrapBody(p))
			if err != nil {
				return 0, err
			}
			return len(p), nil
		}
		// Non-streaming protocol responses are buffered until complete, including
		// writers that split one JSON body across multiple calls.
		w.pending = append(w.pending, p...)
		if gjson.ValidBytes(w.pending) {
			_, err := w.ResponseWriter.Write(w.wrapBody(w.pending))
			w.pending = nil
			if err != nil {
				return 0, err
			}
		}
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func (w *upstreamErrorMessageWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.WriteHeaderNow()
	// Incomplete SSE frames/JSON must not escape before their error fields are
	// known. Complete SSE frames have already been forwarded by Write.
	w.ResponseWriter.Flush()
}

func (w *upstreamErrorMessageWriter) drainFrames() error {
	for {
		end := w.frameEnd()
		if end == 0 {
			return nil
		}
		frame := w.pending[:end]
		if _, err := w.ResponseWriter.Write(w.wrapFrame(frame)); err != nil {
			return err
		}
		w.pending = w.pending[end:]
		w.scanPos = 0
	}
}

func (w *upstreamErrorMessageWriter) frameEnd() int {
	// Scan each byte once across fragmented writes. SSE permits LF, CRLF and
	// CR-only streams; skipLF also survives a CRLF split across two writes.
	for w.scanPos < len(w.pending) {
		b := w.pending[w.scanPos]
		w.scanPos++
		if w.skipLF {
			w.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b != '\n' && b != '\r' {
			w.lineLen++
			continue
		}
		w.skipLF = b == '\r'
		if w.lineLen == 0 {
			// Include a CRLF's LF when already available, preserving original
			// successful frames byte-for-byte without waiting for another Write.
			if w.skipLF && w.scanPos < len(w.pending) && w.pending[w.scanPos] == '\n' {
				w.scanPos++
				w.skipLF = false
			}
			return w.scanPos
		}
		w.lineLen = 0
	}
	return 0
}

func (w *upstreamErrorMessageWriter) wrapFrame(frame []byte) []byte {
	event, payload := parseOpsSSEFrameEnvelope(frame)
	eventType := string(event)
	if !service.IsUpstreamErrorPayload(payload, eventType) || w.isLocalError(payload) {
		return frame
	}
	w.recordUntrackedError(payload, frame)
	client := service.WrapUpstreamErrorPayload(w.c.Request.Context(), payload, eventType)
	if bytes.Equal(payload, client) {
		return frame
	}
	var result bytes.Buffer
	if eventType != "" {
		result.WriteString("event: " + eventType + "\n")
	}
	result.WriteString("data: ")
	result.Write(client)
	result.WriteString("\n\n")
	return result.Bytes()
}

func (w *upstreamErrorMessageWriter) isLocalError(payload []byte) bool {
	if service.HasOpsClientBusinessLimited(w.c) || isOpsRoutingCapacityLimited(w.c) {
		return true
	}
	typ := gjson.GetBytes(payload, "error.type").String()
	if typ == "" {
		typ = gjson.GetBytes(payload, "response.error.code").String()
	}
	if typ == "" {
		typ, _ = service.OpsLocalErrorType(w.c)
	}
	// Local quota checks can fail after a recovered upstream attempt. Do not
	// mistake that stale upstream context for the source of the final failure.
	if typ == "billing_error" || typ == "subscription_error" {
		parsed := parseOpsErrorResponse(payload)
		if isOpsLocalBusinessLimitError(parsed.Code, strings.ToLower(parsed.Message)) {
			return true
		}
	}
	for _, e := range service.GetOpsStreamErrors(w.c) {
		if isOpsLocalBusinessLimitError(e.Code, strings.ToLower(e.Message)) {
			return true
		}
	}
	if hasOpsUpstreamErrorContext(w.c) {
		return false
	}
	if w.c.GetInt64(opsAccountIDKey) == 0 {
		return true
	}
	if typ == "authentication_error" || typ == "permission_error" || typ == "rate_limit_error" || typ == "invalid_request_error" || typ == "not_found_error" {
		// A forwarded SSE error may be the only upstream evidence (native
		// passthrough); local admission errors have no selected account.
		if w.sse {
			return w.c.GetInt64(opsAccountIDKey) == 0
		}
		return true
	}
	return false
}

func (w *upstreamErrorMessageWriter) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return
	}
	payload := w.pending
	if w.sse {
		payload = w.wrapFrame(payload)
	} else {
		payload = w.wrapBody(payload)
	}
	_, _ = w.ResponseWriter.Write(payload)
	w.pending = nil
}

func (w *upstreamErrorMessageWriter) wrapBody(payload []byte) []byte {
	// Async polling has already applied the policy using its stored provenance.
	// Keep task identity/expiry/result fields and its local error distinction.
	if gjson.GetBytes(payload, "object").String() == "image.generation.task" {
		return payload
	}
	if !w.isLocalError(payload) && (service.IsUpstreamErrorPayload(payload, "") ||
		(w.Status() >= 400 && hasOpsUpstreamErrorContext(w.c))) {
		w.recordUntrackedError(payload, payload)
		if service.IsUpstreamErrorPayload(payload, "") {
			payload = service.WrapUpstreamErrorPayload(w.c.Request.Context(), payload, "")
		} else {
			// HTML/plain-text provider errors still get the endpoint's JSON shape.
			errorBody := gin.H{"type": "upstream_error", "message": service.ConfiguredUpstreamErrorMessage(w.c.Request.Context())}
			if strings.Contains(w.c.Request.URL.Path, "/v1beta/") {
				errorBody = gin.H{"code": w.Status(), "status": http.StatusText(w.Status()), "message": service.ConfiguredUpstreamErrorMessage(w.c.Request.Context())}
			}
			payload, _ = json.Marshal(gin.H{"error": errorBody})
		}
	}
	return payload
}

func (w *upstreamErrorMessageWriter) recordUntrackedError(payload, envelope []byte) {
	if hasOpsUpstreamErrorContext(w.c) {
		return
	}
	// Some native providers send error JSON with HTTP 200 or data-only SSE.
	// Retain the original before its only client-visible copy is rewritten.
	parsed := parseOpsErrorResponse(envelope)
	status := parsed.StatusCode
	if status < 400 {
		status = w.Status()
	}
	if status < 400 {
		status = http.StatusBadGateway
	}
	service.SetOpsUpstreamError(w.c, status, parsed.Message, truncateString(string(payload), service.OpsErrorLogQueueBodyMaxBytes))
	if w.Status() < 400 && !w.sse {
		service.MarkOpsStreamErrorValue(w.c, service.OpsStreamError{
			ErrType: parsed.ErrorType, Code: parsed.Code, Message: parsed.Message,
			IntendedStatus: status, CountTowardsSLA: true, NonStream: true,
		})
	}
}
