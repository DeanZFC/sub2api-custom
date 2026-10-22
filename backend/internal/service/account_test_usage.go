package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

type accountTestUsageKey struct{}
type accountTestUsageCollector struct {
	mu                 sync.Mutex
	attempts           []*ScheduledTestRequestAttempt
	deferred           *QualityDeferredError
	qualitySettlements []qualityProbeSettlement
}

func (s *AccountTestService) doAccountTestHTTP(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	return executeQualityTestHTTP(req, id, func(r *http.Request) (*http.Response, error) { return s.httpUpstream.Do(r, proxy, id, concurrency) })
}
func (s *AccountTestService) doAccountTestHTTPWithTLS(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return executeQualityTestHTTP(req, id, func(r *http.Request) (*http.Response, error) {
		return s.httpUpstream.DoWithTLS(r, proxy, id, concurrency, profile)
	})
}

func observeAccountTestResponse(ctx context.Context, started time.Time, resp *http.Response, err error) (*http.Response, error) {
	collector, _ := ctx.Value(accountTestUsageKey{}).(*accountTestUsageCollector)
	if collector == nil {
		return resp, err
	}
	attempt := &ScheduledTestRequestAttempt{StartedAt: started}
	settle, _ := ctx.Value(qualitySettlementKey{}).(func(*ScheduledTestRequestAttempt))
	if err != nil {
		var netErr net.Error
		switch {
		case errors.Is(err, context.Canceled):
			attempt.FailureKind = "canceled"
		case errors.As(err, &netErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			attempt.FailureKind = "network"
		default:
			attempt.FailureKind = "unknown"
		}
	}
	if resp != nil {
		attempt.HTTPStatus = resp.StatusCode
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, e := strconv.ParseInt(value, 10, 32); e == nil && seconds >= 0 {
				t := time.Now().UTC().Add(time.Duration(seconds) * time.Second)
				attempt.RetryAfter = &t
			} else if t, e := http.ParseTime(value); e == nil {
				attempt.RetryAfter = &t
			}
		}
		if resp.Body != nil {
			resp.Body = &accountTestUsageBody{ReadCloser: resp.Body, attempt: attempt, settle: settle}
		}
	}
	if (resp == nil || resp.Body == nil) && settle != nil {
		settle(attempt)
	}
	collector.mu.Lock()
	collector.attempts = append(collector.attempts, attempt)
	collector.mu.Unlock()
	return resp, err
}
func (c *accountTestUsageCollector) snapshot() *ScheduledTestExecutionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := newScheduledTestExecutionInfo()
	for _, a := range c.attempts {
		attempt := *a
		if attempt.HTTPStatus >= 400 {
			attempt.FailureKind = scheduledTestFailureKind(&ScheduledTestResult{Status: "failed", ExecutionInfo: &ScheduledTestExecutionInfo{
				Attempts: []ScheduledTestRequestAttempt{attempt}, FailureKind: attempt.FailureKind,
			}})
		}
		out.Attempts = append(out.Attempts, attempt)
	}
	if len(out.Attempts) > 0 {
		last := out.Attempts[len(out.Attempts)-1]
		out.FailureKind = last.FailureKind
		out.RetryAfter = last.RetryAfter
	}
	if len(out.Attempts) > 0 {
		out.summarize()
	}
	return out
}

// Inspect only protocol metadata, never model text, URLs, headers, or credentials.
// Parsing is bounded and does not drain a response or alter the caller's reads.
type accountTestUsageBody struct {
	io.ReadCloser
	attempt    *ScheduledTestRequestAttempt
	buffer     []byte
	sse        bool
	overflow   bool
	settle     func(*ScheduledTestRequestAttempt)
	settleOnce sync.Once
	eventKind  string
}

func (b *accountTestUsageBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	qualityUsageReadError(b.attempt, err)
	if !b.overflow {
		b.buffer = append(b.buffer, p[:n]...)
		if len(b.buffer) > 2<<20 {
			b.buffer = nil
			b.overflow = true
		}
	}
	if !b.overflow {
		if !b.sse {
			prefix := bytes.TrimSpace(b.buffer)
			b.sse = bytes.HasPrefix(prefix, []byte("data:")) || bytes.HasPrefix(prefix, []byte("event:")) || bytes.HasPrefix(prefix, []byte(":"))
		}
		if b.sse {
			for {
				end := bytes.IndexByte(b.buffer, '\n')
				if end < 0 {
					break
				}
				b.captureLine(b.buffer[:end])
				b.buffer = b.buffer[end+1:]
			}
		}
		if err != nil {
			if b.sse {
				b.captureLine(b.buffer)
			} else {
				b.captureJSON(b.buffer)
			}
			b.buffer = nil
		}
	}
	return n, err
}
func (b *accountTestUsageBody) Close() error {
	if !b.sse && !b.overflow {
		b.captureJSON(b.buffer)
	}
	b.buffer = nil
	err := b.ReadCloser.Close()
	b.settleOnce.Do(func() {
		if b.settle != nil {
			b.settle(b.attempt)
		}
	})
	return err
}
func (b *accountTestUsageBody) captureLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		b.eventKind = ""
	}
	if bytes.HasPrefix(line, []byte("event:")) {
		b.eventKind = strings.TrimSpace(string(bytes.TrimPrefix(line, []byte("event:"))))
	}
	if bytes.HasPrefix(line, []byte("data:")) {
		b.captureJSON(bytes.TrimSpace(line[5:]))
	}
}
func (b *accountTestUsageBody) captureJSON(raw []byte) {
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return
	}
	captureAccountTestUsageMetadata(b.attempt, data)
	// Anthropic message_start contains an initial output count, not final usage.
	// Responses created/in-progress events are likewise not settlements.
	kind, _ := data["type"].(string)
	if kind == "" {
		kind = b.eventKind
	}
	final := !b.sse || (kind != "message_start" && (!strings.HasPrefix(kind, "response.") || kind == "response.completed" || kind == "response.failed" || kind == "response.incomplete"))
	current := &ScheduledTestRequestAttempt{}
	captureAccountTestUsageMetadata(current, data)
	if final && b.attempt.InputTokens != nil && current.OutputTokens != nil {
		b.attempt.qualityUsageFinal = true
	}
}
func captureAccountTestUsageMetadata(a *ScheduledTestRequestAttempt, data map[string]any) {
	captureAccountTestUsageEnvelope(a, data, 0)
}
func captureAccountTestUsageEnvelope(a *ScheduledTestRequestAttempt, data map[string]any, depth int) {
	if depth > 4 {
		return
	}
	// Fixed protocol envelopes only: do not recurse through content/output text.
	if nested, ok := data["response"].(map[string]any); ok {
		captureAccountTestUsageEnvelope(a, nested, depth+1)
	}
	if nested, ok := data["message"].(map[string]any); ok {
		captureAccountTestUsageEnvelope(a, nested, depth+1)
	}
	set := func(dst **int64, value any) {
		n, ok := value.(float64)
		if !ok || n < 0 || n >= float64(math.MaxInt64) || math.Trunc(n) != n {
			return
		}
		v := int64(n)
		if *dst == nil || v > **dst {
			*dst = &v
		}
	}
	if usage, ok := data["usage"].(map[string]any); ok {
		if _, ok := usage["cache_read_input_tokens"]; ok {
			a.qualityCacheExclusive = true
		}
		if _, ok := usage["cache_creation_input_tokens"]; ok {
			a.qualityCacheExclusive = true
		}
		set(&a.InputTokens, usage["input_tokens"])
		set(&a.InputTokens, usage["prompt_tokens"])
		set(&a.OutputTokens, usage["output_tokens"])
		set(&a.OutputTokens, usage["completion_tokens"])
		set(&a.CacheReadTokens, usage["cache_read_input_tokens"])
		set(&a.CacheCreationTokens, usage["cache_creation_input_tokens"])
		for _, key := range []string{"output_tokens_details", "completion_tokens_details"} {
			if details, ok := usage[key].(map[string]any); ok {
				set(&a.ReasoningTokens, details["reasoning_tokens"])
			}
		}
		for _, key := range []string{"input_tokens_details", "prompt_tokens_details"} {
			if details, ok := usage[key].(map[string]any); ok {
				set(&a.CacheReadTokens, details["cached_tokens"])
			}
		}
	}
	if usage, ok := data["usageMetadata"].(map[string]any); ok {
		set(&a.InputTokens, usage["promptTokenCount"])
		set(&a.ReasoningTokens, usage["thoughtsTokenCount"])
		set(&a.CacheReadTokens, usage["cachedContentTokenCount"])
		if output, ok := usage["candidatesTokenCount"].(float64); ok {
			if reasoning, ok := usage["thoughtsTokenCount"].(float64); ok {
				output += reasoning
			}
			set(&a.OutputTokens, output)
		}
	}
	if e, ok := data["error"].(map[string]any); ok {
		code, _ := e["code"].(string)
		typ, _ := e["type"].(string)
		for _, value := range []string{typ, code} {
			switch strings.ToLower(value) {
			case "invalid_api_key", "authentication_error", "permission_error":
				a.FailureKind = "auth"
			case "insufficient_quota", "billing_hard_limit_reached":
				a.FailureKind = "quota"
			case "rate_limit_exceeded", "rate_limit_error":
				a.FailureKind = "rate_limit"
			case "overloaded_error", "server_error", "api_error":
				a.FailureKind = "server"
			case "invalid_request_error":
				a.FailureKind = "request"
			}
		}
	}
}
