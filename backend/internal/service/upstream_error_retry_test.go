package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type upstreamRetrySettingsRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
	reads  int
}

func (r *upstreamRetrySettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if v, ok := r.values[key]; ok {
		return v, nil
	}
	return "", ErrSettingNotFound
}

func (r *upstreamRetrySettingsRepo) GetAll(context.Context) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out, nil
}

func (r *upstreamRetrySettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, v := range values {
		r.values[k] = v
	}
	return nil
}

func upstreamRetryTestContext(t *testing.T, parent context.Context, rules string, retries int) context.Context {
	t.Helper()
	data, err := json.Marshal(UpstreamErrorRetrySettings{Enabled: true, MaxRetries: retries, DelayMS: 100, Errors: rules})
	require.NoError(t, err)
	repo := &upstreamRetrySettingsRepo{values: map[string]string{SettingKeyUpstreamErrorRetry: string(data)}}
	return WithUpstreamErrorRetry(parent, NewSettingService(repo, &config.Config{}))
}

func TestUpstreamErrorRetryMatchesMessageFragments(t *testing.T) {
	policy := compileUpstreamErrorRetryPolicy(UpstreamErrorRetrySettings{Enabled: true, Errors: "503\nservers are currently overloaded\n临时不可用\nbackend_busy"})
	for _, tt := range []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{"status", 503, `{"error":{"message":"try later"}}`, true},
		{"partial case insensitive message", 502, `{"error":{"message":"Our SERVERS ARE CURRENTLY OVERLOADED. Please try again later."}}`, true},
		{"nested stream failure", 502, `{"response":{"error":{"message":"服务临时不可用，请稍后重试"}}}`, true},
		{"error code", 500, `{"error":{"code":"backend_busy"}}`, true},
		{"plain proxy message", 502, "Our servers are currently overloaded. Please try again later.", true},
		{"string error", 500, `{"error":"backend_busy: try later"}`, true},
		{"unmatched", 502, `{"error":{"message":"connection refused"}}`, false},
		{"echoed user input", 500, `{"input":"servers are currently overloaded","error":{"message":"invalid body"}}`, false},
		{"success must not replay", 200, `{"message":"servers are currently overloaded"}`, false},
		{"official 429 preserved", 429, `{"error":{"message":"servers are currently overloaded"}}`, false},
		{"partial usage", 503, `{"response":{"usage":{"input_tokens":9},"error":{"message":"backend_busy"}}}`, false},
		{"partial output", 503, `{"response":{"output":[{"type":"message"}],"error":{"message":"backend_busy"}}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, policy.matches(tt.status, []byte(tt.body))) })
	}
}

func TestUpstreamErrorRetryValidationAndSettingsPersistence(t *testing.T) {
	settings := UpstreamErrorRetrySettings{Enabled: true, MaxRetries: 3, DelayMS: 100, Errors: " 503 \r\n\nServer BUSY\nserver busy\n服务忙 "}
	normalized, err := normalizeUpstreamErrorRetrySettings(settings)
	require.NoError(t, err)
	require.Equal(t, "503\nServer BUSY\n服务忙", normalized.Errors)
	for _, mutate := range []func(*UpstreamErrorRetrySettings){
		func(v *UpstreamErrorRetrySettings) { v.Errors = "\n " },
		func(v *UpstreamErrorRetrySettings) { v.Errors = "429" },
		func(v *UpstreamErrorRetrySettings) { v.Errors = "200" },
		func(v *UpstreamErrorRetrySettings) { v.MaxRetries = 11 },
		func(v *UpstreamErrorRetrySettings) { v.DelayMS = 0 },
		func(v *UpstreamErrorRetrySettings) { v.Errors = strings.Repeat("x", 513) },
	} {
		bad := settings
		mutate(&bad)
		_, err := normalizeUpstreamErrorRetrySettings(bad)
		require.Error(t, err)
	}
	repo := &upstreamRetrySettingsRepo{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	require.False(t, svc.upstreamErrorRetryPolicy(context.Background()).settings.Enabled)
	err = svc.UpdateSettings(context.Background(), &SystemSettings{UpstreamErrorRetry: &settings})
	require.NoError(t, err)
	// Saving refreshes an already-warm disabled cache immediately.
	require.True(t, svc.upstreamErrorRetryPolicy(context.Background()).matches(500, []byte("server busy")))
	stored, err := svc.GetAllSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, normalized, *stored.UpstreamErrorRetry)
	// A new process reads the same policy, without a schema migration.
	require.True(t, NewSettingService(repo, &config.Config{}).upstreamErrorRetryPolicy(context.Background()).settings.Enabled)
}

type upstreamRetryTrackedBody struct {
	io.Reader
	closed bool
}

func (b *upstreamRetryTrackedBody) Close() error { b.closed = true; return nil }

func TestUpstreamErrorRetryHTTPReplaysExactRequestAndClosesFailures(t *testing.T) {
	ctx := upstreamRetryTestContext(t, context.Background(), "currently overloaded", 2)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://upstream.test/v1/responses", strings.NewReader(`{"input":"hello","stream":true}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Session_id", "same-session")
	var bodies []*upstreamRetryTrackedBody
	attempts := 0
	resp, err := DoWithConfiguredUpstreamRetry(req, func(r *http.Request) (*http.Response, error) {
		attempts++
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, `{"input":"hello","stream":true}`, string(body))
		require.Equal(t, req.Header, r.Header)
		require.Equal(t, req.URL, r.URL)
		_ = r.Body.Close()
		status, responseBody := 503, `{"error":{"message":"Our servers are currently overloaded. Please try again later."}}`
		if attempts == 3 {
			status, responseBody = 200, "data: success\n\n"
		}
		tracked := &upstreamRetryTrackedBody{Reader: strings.NewReader(responseBody)}
		bodies = append(bodies, tracked)
		return &http.Response{StatusCode: status, Body: tracked, Header: http.Header{"X-Request-Id": {"final"}}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 3, attempts)
	require.True(t, bodies[0].closed)
	require.True(t, bodies[1].closed)
	require.False(t, bodies[2].closed)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "data: success\n\n", string(data))
	require.Equal(t, "final", resp.Header.Get("X-Request-Id"))
	require.NoError(t, resp.Body.Close())
	require.True(t, bodies[2].closed)
}

func TestUpstreamErrorRetryHTTPKeepsOriginalResponseWhenNotRetried(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		body       string
		replayable bool
	}{
		{"unmatched", 502, `{"error":{"message":"a different error"}}`, true},
		{"too large", 503, strings.Repeat("x", upstreamErrorRetryBodyLimit+17), true},
		{"429", 429, "server busy", true},
		{"unreplayable body", 503, "server busy", false},
		{"success", 200, "server busy", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := upstreamRetryTestContext(t, context.Background(), "503\nserver busy", 2)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://upstream.test", strings.NewReader("body"))
			require.NoError(t, err)
			if !tt.replayable {
				req.GetBody = nil
			}
			attempts := 0
			resp, err := DoWithConfiguredUpstreamRetry(req, func(*http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{StatusCode: tt.status, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})
			require.NoError(t, err)
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, tt.body, string(data))
			require.Equal(t, 1, attempts)
		})
	}
}

func TestUpstreamErrorRetrySharesBudgetAcrossTransportAndFailover(t *testing.T) {
	ctx := upstreamRetryTestContext(t, context.Background(), "busy", 2)
	failure := &UpstreamFailoverError{StatusCode: 503, ResponseBody: []byte(`{"error":{"message":"busy"}}`)}
	claimed, err := TryConfiguredUpstreamErrorRetry(ctx, failure)
	require.True(t, claimed)
	require.NoError(t, err)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://upstream.test", strings.NewReader("body"))
	attempts := 0
	resp, err := DoWithConfiguredUpstreamRetry(req, func(*http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(string(failure.ResponseBody)))}, nil
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 2, attempts)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, failure.ResponseBody, data)
	claimed, err = TryConfiguredUpstreamErrorRetry(ctx, failure)
	require.False(t, claimed)
	require.NoError(t, err)
}

func TestUpstreamErrorRetryCancellationStopsDetachedRequests(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	ctx := upstreamRetryTestContext(t, parent, "503", 3)
	req, _ := http.NewRequestWithContext(context.WithoutCancel(ctx), http.MethodPost, "http://upstream.test", strings.NewReader("body"))
	attempts := 0
	body := &upstreamRetryTrackedBody{Reader: strings.NewReader("busy")}
	start := time.Now()
	resp, err := DoWithConfiguredUpstreamRetry(req, func(*http.Request) (*http.Response, error) {
		attempts++
		time.AfterFunc(10*time.Millisecond, cancel)
		return &http.Response{StatusCode: 503, Body: body}, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
	require.Equal(t, 1, attempts)
	require.True(t, body.closed)
	require.Less(t, time.Since(start), time.Second)
}

func TestUpstreamErrorRetryNetworkErrorsAreNotReplayed(t *testing.T) {
	ctx := upstreamRetryTestContext(t, context.Background(), "timeout", 3)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://upstream.test", strings.NewReader("body"))
	attempts := 0
	expected := errors.New("timeout")
	_, err := DoWithConfiguredUpstreamRetry(req, func(*http.Request) (*http.Response, error) { attempts++; return nil, expected })
	require.ErrorIs(t, err, expected)
	require.Equal(t, 1, attempts)
}

func TestUpstreamErrorRetryPreservesPartialUsageDecision(t *testing.T) {
	ctx := upstreamRetryTestContext(t, context.Background(), "busy", 3)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}
	payload := []byte(`{"type":"response.failed","response":{"usage":{"input_tokens":9},"error":{"message":"busy"}}}`)
	failure := svc.newOpenAIStreamFailoverError(c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false, "request", payload, "busy")
	// Built-in error-envelope rewriting must not erase the no-replay decision.
	require.True(t, failure.ConfiguredRetryUnsafe)
	claimed, err := TryConfiguredUpstreamErrorRetry(ctx, failure)
	require.False(t, claimed)
	require.NoError(t, err)
}

func TestUpstreamErrorRetryStreamMessageBeforeAndAfterOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, output := range []bool{false, true} {
			name := "native"
			if passthrough {
				name = "passthrough"
			}
			if output {
				name += "_after_output"
			}
			t.Run(name, func(t *testing.T) {
				ctx := upstreamRetryTestContext(t, context.Background(), "capacity temporarily busy", 2)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				stream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\n"
				if output {
					stream += "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
				}
				stream += "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"custom_error\",\"message\":\"The CAPACITY TEMPORARILY BUSY. Try later.\"}}\n\n"
				resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(stream))}
				svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
				account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
				var err error
				if passthrough {
					_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5", "gpt-5")
				} else {
					_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5", "gpt-5")
				}
				var failure *UpstreamFailoverError
				if output {
					require.False(t, errors.As(err, &failure))
					require.Contains(t, rec.Body.String(), "partial")
				} else {
					require.ErrorAs(t, err, &failure)
					require.Empty(t, rec.Body.String(), "failed initial events must stay buffered")
					claimed, waitErr := TryConfiguredUpstreamErrorRetry(ctx, failure)
					require.True(t, claimed)
					require.NoError(t, waitErr)
				}
			})
		}
	}
}

func TestUpstreamErrorRetryConcurrentBudgetIsBounded(t *testing.T) {
	ctx := upstreamRetryTestContext(t, context.Background(), "busy", 3)
	var claimed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := TryConfiguredUpstreamErrorRetry(ctx, &UpstreamFailoverError{StatusCode: 503, ResponseBody: []byte("busy")}); ok {
				claimed.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(3), claimed.Load())
}

func TestUpstreamErrorRetryStreamKeepsUsageAndOfficialLimits(t *testing.T) {
	ctx := upstreamRetryTestContext(t, context.Background(), "502\nbusy\nlimit", 3)
	for _, payload := range []string{
		`{"error":{"code":"insufficient_quota","message":"busy"}}`,
		`{"response":{"error":{"type":"usage_limit_reached","message":"busy"}}}`,
		`{"error":{"message":"Rate limit exceeded"}}`,
	} {
		require.Nil(t, configuredOpenAIStreamRetryFailure(ctx, []byte(payload), extractOpenAISSEErrorMessage([]byte(payload)), nil))
	}
	require.Nil(t, configuredOpenAIStreamRetryFailure(ctx, []byte(`{"error":{"message":"busy"}}`), "busy", &OpenAIUsage{InputTokens: 8}))

	for _, passthrough := range []bool{false, true} {
		for _, streaming := range []bool{false, true} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"usage\":{\"input_tokens\":8}}}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"busy\"}}}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			if streaming && passthrough {
				_, err = svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-5", "gpt-5")
			} else if streaming {
				_, err = svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-5", "gpt-5")
			} else if passthrough {
				_, err = svc.handlePassthroughSSEToJSON(resp, c, account, []byte(body), "gpt-5", "gpt-5")
			} else {
				_, err = svc.handleSSEToJSON(resp, c, account, []byte(body), "gpt-5", "gpt-5")
			}
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.ConfiguredRetryUnsafe, "earlier usage must survive error envelope rewriting")
			claimed, err := TryConfiguredUpstreamErrorRetry(ctx, failure)
			require.False(t, claimed)
			require.NoError(t, err)
		}
	}
}
