package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityCaptureKeepsUpstreamHTTPFailureAfterClientCancellation(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-6-astra")
			ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "canceled-upstream-failure")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			groupID := int64(8)
			key := &APIKey{GroupID: &groupID}
			c.Set("api_key", key)
			s.Capture()(c)
			SetPassiveQualityAttemptGroup(c, 9)

			// The detached upstream returns an actual HTTP failure after the
			// downstream has gone away. Its eventual 499 must not erase that evidence.
			cancel()
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 2298, UpstreamStatusCode: status, Kind: "failover", Message: "Upstream request failed", upstreamHTTPResponseObserved: true})
			c.Status(499)
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{AccountID: 2298, UpstreamStatusCode: status, Kind: "retry_exhausted", upstreamHTTPResponseObserved: true})

			require.Len(t, s.queue, 2, "confirmed upstream failures must survive downstream cancellation")
			first, repeated := <-s.queue, <-s.queue
			require.Equal(t, int64(2298), first.AccountID)
			require.Equal(t, int64(9), first.GroupID)
			require.Equal(t, "gpt-6-astra", first.Model)
			require.Equal(t, c.GetString(passiveQualityTurnKey), first.RequestKey)
			require.Equal(t, first.RequestKey, repeated.RequestKey, "the repository must still be able to deduplicate the attempt")
			require.Equal(t, first.StartedAt, repeated.StartedAt)
			require.False(t, first.NonText)
			require.Equal(t, int64(8), *key.GroupID)
		})
	}
}

func TestPassiveQualityCaptureDoesNotTreatClientCancellationAsUpstreamFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		canceled bool
		event    OpsUpstreamErrorEvent
	}{
		{"client_closed_status", true, OpsUpstreamErrorEvent{UpstreamStatusCode: 499, Kind: "http_error"}},
		{"canceled_transport", true, OpsUpstreamErrorEvent{Kind: "request_error", Message: "context canceled"}},
		{"ambiguous_transport_after_cancel", true, OpsUpstreamErrorEvent{Kind: "request_error", Message: "unexpected EOF"}},
		{"synthetic_protocol_failure_after_cancel", true, OpsUpstreamErrorEvent{UpstreamStatusCode: http.StatusBadGateway, Kind: "failover", Message: "OpenAI stream ended before a terminal event"}},
		{"canceled_error_before_context_observation", false, OpsUpstreamErrorEvent{Kind: "request_error", Message: "context canceled"}},
		{"local_admission", false, OpsUpstreamErrorEvent{Kind: "request_error", Stage: "admission", Message: "no concurrency slot"}},
		{"canceled_local_admission", true, OpsUpstreamErrorEvent{Kind: "request_error", Stage: "admission", Message: "no concurrency slot"}},
		{"request_validation", true, OpsUpstreamErrorEvent{UpstreamStatusCode: http.StatusBadRequest, Kind: "http_error", upstreamHTTPResponseObserved: true}},
		{"policy_refusal", true, OpsUpstreamErrorEvent{UpstreamStatusCode: http.StatusForbidden, Kind: "http_error", upstreamHTTPResponseObserved: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-6-astra")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			s.Capture()(c)
			if tc.canceled {
				cancel()
			}
			tc.event.AccountID = 2298
			appendOpsUpstreamError(c, tc.event)
			require.Empty(t, s.queue)
		})
	}
}

type passiveQualityCancelOnReadBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b *passiveQualityCancelOnReadBody) Read(p []byte) (int, error) {
	b.cancel()
	return b.Reader.Read(p)
}

func (b *passiveQualityCancelOnReadBody) Close() error { return nil }

func TestPassiveQualityCaptureOpenAIHTTPFailoverAfterClientCancellation(t *testing.T) {
	for _, path := range []string{"responses_native", "responses_passthrough", "chat_completions"} {
		t.Run(path, func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			endpoint := "/v1/responses"
			body := []byte(`{"model":"gpt-6-astra","stream":true,"input":"hello"}`)
			if path == "chat_completions" {
				endpoint = "/v1/chat/completions"
				body = []byte(`{"model":"gpt-6-astra","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			}
			c.Request = httptest.NewRequest(http.MethodPost, endpoint, nil)
			ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-6-astra")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			groupID := int64(8)
			c.Set("api_key", &APIKey{GroupID: &groupID})
			s.Capture()(c)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"provider-502"}},
				Body: &passiveQualityCancelOnReadBody{
					Reader: strings.NewReader(`{"error":{"message":"Upstream request failed"}}`),
					cancel: cancel,
				},
			}}
			gateway := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{
				ID: 2298, Name: "GPT-Pro", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.example.test"},
				Extra:       map[string]any{"openai_passthrough": path == "responses_passthrough"},
			}
			var err error
			if path == "chat_completions" {
				_, err = gateway.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			} else {
				_, err = gateway.Forward(ctx, c, account, body)
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Equal(t, http.StatusBadGateway, failover.StatusCode)
			require.ErrorIs(t, c.Request.Context().Err(), context.Canceled)
			require.Len(t, s.queue, 1, "the real forwarding path must retain its received HTTP 502")
			failure := <-s.queue
			require.Equal(t, account.ID, failure.AccountID)
			require.Equal(t, groupID, failure.GroupID)
			require.Equal(t, "gpt-6-astra", failure.Model)
		})
	}
}

func TestPassiveQualityCaptureDoesNotTreatCanceledStreamMarkerAsUpstreamHTTPFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(canceled), func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-6-astra")
			ctx = context.WithValue(ctx, ctxkey.AccountID, int64(2298))
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			s.Capture()(c)
			if canceled {
				cancel()
			}
			MarkOpsStreamFailure(c, "upstream_error", "upstream_connection_error", "Upstream request failed", http.StatusBadGateway)
			if canceled {
				require.Empty(t, s.queue, "a logical stream status alone does not prove an upstream HTTP response after cancellation")
			} else {
				require.Len(t, s.queue, 1, "connected stream failure capture must remain unchanged")
			}
		})
	}
}

func TestPassiveQualityCaptureRejectsCanceledSyntheticHTTPErrorHandlers(t *testing.T) {
	for _, path := range []string{"native", "passthrough_failover", "passthrough_error"} {
		t.Run(path, func(t *testing.T) {
			s := NewPassiveQualityService(nil, nil)
			defer s.cancel()
			s.enabled.Store(true)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-6-astra")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			s.Capture()(c)
			cancel()
			body := []byte(`{"error":{"message":"synthetic protocol error"}}`)
			resp := &http.Response{StatusCode: http.StatusBadGateway, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}
			account := &Account{ID: 2298, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			gateway := &OpenAIGatewayService{cfg: &config.Config{}}
			var err error
			switch path {
			case "native":
				_, err = gateway.handleErrorResponseWithHTTPObservation(ctx, resp, c, account, nil, false)
			case "passthrough_failover":
				err = gateway.handleFailoverErrorResponsePassthroughWithHTTPObservation(ctx, resp, c, account, nil, body, false)
			case "passthrough_error":
				err = gateway.handleErrorResponsePassthroughWithHTTPObservation(ctx, resp, c, account, nil, body, false)
			}
			require.Error(t, err)
			require.Empty(t, s.queue, "shared HTTP error handlers must preserve synthetic response provenance")
		})
	}
}
