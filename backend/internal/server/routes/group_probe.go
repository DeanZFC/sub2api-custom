package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type groupProbeContextKey struct{}
type groupProbeCall struct {
	group *service.Group
	meter *service.GroupProbeMeter
}

// No listener or public route is registered on this engine. Only a typed local
// caller can supply the group and metering sink. The normal auth chain never
// interprets a header, URL or API key as a monitoring identity.
func NewGroupProbeExecutor(gateway *handler.GatewayHandler, openai *handler.OpenAIGatewayHandler, resolver *service.CompositeRouteResolver,
	settings *service.SettingService, budget service.QualityOperationsRepository, billing *service.BillingService, channels *service.ChannelService) service.GroupProbeExecutor {
	r := gin.New()
	r.Use(middleware.Recovery(), middleware.ClientRequestID(), handler.InboundEndpointMiddleware(), middleware.UpstreamErrorRetry(settings))
	r.Use(func(c *gin.Context) {
		call := c.Request.Context().Value(groupProbeContextKey{}).(groupProbeCall)
		group := call.group
		key := &service.APIKey{GroupID: &group.ID, Group: group, User: &service.User{}}
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{})
		ctx := context.WithValue(c.Request.Context(), ctxkey.Group, group)
		ctx = service.WithAPIKeyGroupFallbackRouting(ctx, key)
		c.Request = c.Request.WithContext(service.WithHeaderOverrideRequestScope(ctx, 0, 0, key.GroupID))
	})
	r.Use(middleware.GroupModelAllowlist(), compositeTargetPlatformMiddleware(resolver))
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		meter := c.Request.Context().Value(groupProbeContextKey{}).(groupProbeCall).meter
		if domain.UsesOpenAIGateway(getGroupPlatform(c)) {
			openai.ProbeChatCompletions(c, meter)
		} else {
			gateway.ProbeChatCompletions(c, meter)
		}
	})
	return func(ctx context.Context, g *service.Group, model string) service.GroupProbeResult {
		meter := service.NewGroupProbeMeter(budget, billing, channels, g.ID, model)
		payload, _ := json.Marshal(gin.H{
			"model":                 model,
			"messages":              []gin.H{{"role": "user", "content": "Reply with OK."}},
			"stream":                true,
			"max_completion_tokens": service.GroupProbeOutputTokens,
			"stream_options":        gin.H{"include_usage": true},
		})
		ctx = context.WithValue(ctx, groupProbeContextKey{}, groupProbeCall{g, meter})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Sub2API-Group-Probe/1")
		w := &groupProbeWriter{headers: make(http.Header), start: time.Now(), status: 200}
		r.ServeHTTP(w, req)
		result := meter.Result
		result.FirstTokenMS = w.first
		if result.Deferred == nil {
			result.Detail = w.failure(ctx, meter.CompletedForward)
			result.Success = result.Detail == ""
		}
		return result
	}
}

// Parse only chat stream metadata. Bound a single unfinished SSE line and discard
// the generated text immediately, including when upstream ignores the token cap.
type groupProbeWriter struct {
	headers                 http.Header
	start                   time.Time
	status                  int
	line                    []byte
	first                   *int
	finished, done, invalid bool
}

func (w *groupProbeWriter) Header() http.Header      { return w.headers }
func (w *groupProbeWriter) WriteHeader(s int)        { w.status = s }
func (w *groupProbeWriter) Flush()                   {}
func (w *groupProbeWriter) CloseNotify() <-chan bool { return nil }
func (w *groupProbeWriter) Write(b []byte) (int, error) {
	n := len(b)
	if w.status >= 400 || w.invalid {
		return n, nil
	}
	for len(b) > 0 {
		line, rest, complete := bytes.Cut(b, []byte{'\n'})
		if len(w.line)+len(line) > 64<<10 {
			w.invalid = true
			w.line = nil
			break
		}
		w.line = append(w.line, line...)
		if !complete {
			break
		}
		w.consume(w.line)
		w.line = w.line[:0]
		b = rest
	}
	return n, nil
}

func (w *groupProbeWriter) consume(line []byte) {
	data, ok := bytes.CutPrefix(line, []byte("data:"))
	if !ok {
		return
	}
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("[DONE]")) {
		w.done = true
		return
	}
	if !gjson.ValidBytes(data) || gjson.GetBytes(data, "error").Exists() {
		w.invalid = true
		return
	}
	content := gjson.GetBytes(data, "choices.0.delta.content")
	if w.first == nil && content.Type == gjson.String && len(bytes.TrimSpace([]byte(content.Str))) > 0 {
		ms := int(time.Since(w.start).Milliseconds())
		w.first = &ms
	}
	if reason := gjson.GetBytes(data, "choices.0.finish_reason").String(); reason != "" {
		w.finished = reason == "stop"
	}
}

func (w *groupProbeWriter) failure(ctx context.Context, forwarded bool) string {
	if ctx.Err() != nil {
		return "timeout"
	}
	if w.status < 200 || w.status >= 300 {
		return fmt.Sprintf("HTTP %d %s", w.status, http.StatusText(w.status))
	}
	if w.invalid || !forwarded || !w.done || !w.finished {
		return "incomplete_stream"
	}
	if w.first == nil {
		return "empty_response"
	}
	return ""
}
