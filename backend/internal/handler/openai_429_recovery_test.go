//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestOpenAI429ResponseRetryLimit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		retryAfter string
		calls      int
	}{
		{"consecutive rate limits", "1", 4},
		{"retry after exceeds request window", "90", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &preContentFailoverUpstream{respond: func(call int) (*http.Response, error) {
				require.LessOrEqual(t, call, tc.calls)
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{"Retry-After": {tc.retryAfter}},
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`)),
				}, nil
			}}
			h := newOpenAIResponsesFailoverTestHandler(t, upstream)
			c, rec := newOpenAIResponsesFailoverTestContext(t, nil)
			h.Responses(c)
			require.Equal(t, tc.calls, upstream.calls())
			require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
			require.Equal(t, tc.retryAfter, rec.Header().Get("Retry-After"))
		})
	}
}

func TestOpenAI429RecoverySharesBudgetAcrossAccountsAndErrors(t *testing.T) {
	var stopped *service.UpstreamFailoverError
	b := openAI429Recovery{finish: func(f *service.UpstreamFailoverError) { stopped = f }}
	now := time.Now()
	oauth := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken}
	apiKey := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey}
	limited := &service.UpstreamFailoverError{StatusCode: 429}
	overloaded := &service.UpstreamFailoverError{StatusCode: 503}
	for i := 0; i < 5; i++ {
		require.True(t, b.claim(apiKey, limited, now))
		require.True(t, b.claim(oauth, overloaded, now))
	}
	require.Zero(t, b.deadline)
	require.True(t, b.claim(oauth, limited, now))
	require.Equal(t, now.Add(30*time.Second), b.deadline)
	require.True(t, b.claim(apiKey, overloaded, now.Add(time.Second)))
	require.True(t, b.claim(oauth, &service.UpstreamFailoverError{StatusCode: 429, SameAccountRetryDeadline: now.Add(time.Minute)}, now.Add(2*time.Second)))
	require.Equal(t, now.Add(30*time.Second), b.deadline)
	require.False(t, b.claim(apiKey, overloaded, now.Add(3*time.Second)))
	require.Same(t, overloaded, stopped)
}

func TestOpenAI429RecoveryDeadlineAndSlotLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		failure := &service.UpstreamFailoverError{StatusCode: 429, SameAccountRetryDeadline: now.Add(2 * time.Second)}
		var stopped *service.UpstreamFailoverError
		b := openAI429Recovery{finish: func(f *service.UpstreamFailoverError) { stopped = f }}
		account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
		require.True(t, b.claim(account, failure, now))
		requestCtx := context.Background()
		waitCtx, cancel := b.waitContext(requestCtx)
		defer cancel()
		<-waitCtx.Done()
		require.Equal(t, now.Add(2*time.Second), time.Now())
		require.ErrorIs(t, waitCtx.Err(), context.DeadlineExceeded)
		releases := 0
		require.True(t, b.stopAfterSelection(&service.AccountSelectionResult{ReleaseFunc: func() { releases++ }}))
		require.Equal(t, 1, releases)
		require.Same(t, failure, stopped)
		require.NoError(t, requestCtx.Err())
	})
}

func TestOpenAI429RecoveryStopsBeforeOverlongBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := &service.UpstreamFailoverError{StatusCode: 429, RetryableOnSameAccount: true, SameAccountRetryDelay: 30 * time.Second}
		account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
		var stopped *service.UpstreamFailoverError
		b := openAI429Recovery{finish: func(f *service.UpstreamFailoverError) { stopped = f }}
		now := time.Now()
		require.Equal(t, openAIStopRetry, b.afterFailure(context.Background(), account, failure, map[int64]int{}, true, zap.NewNop(), "retry"))
		require.Equal(t, now, time.Now())
		require.Same(t, failure, stopped)
	})
}

func TestOpenAI429ResponseCountsLaterAccountSwitches(t *testing.T) {
	accounts := preContentAccounts(5)
	for i := range accounts {
		accounts[i].Type = service.AccountTypeOAuth
		accounts[i].Concurrency = 0
		accounts[i].Credentials = map[string]any{"access_token": "test-token"}
	}
	upstream := &preContentFailoverUpstream{respond: func(call int) (*http.Response, error) {
		require.LessOrEqual(t, call, 4)
		status := 520 // Different failures on replacement accounts still consume this request's budget.
		if call == 1 {
			status = http.StatusTooManyRequests
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}}
	h := newOpenAIResponsesFailoverTestHandler(t, upstream, openAIImagesFailoverAccountRepo{accounts: accounts})
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)
	h.Responses(c)
	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	require.Equal(t, 4, upstream.calls())
	require.NotEqual(t, upstream.hits[1], upstream.hits[2])
	require.NotEqual(t, upstream.hits[2], upstream.hits[3])
}

func TestOpenAI429AdmittedSlotOutlivesRetryWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
		account := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
		b := openAI429Recovery{finish: func(*service.UpstreamFailoverError) { t.Fatal("active generation was terminated") }}
		require.True(t, b.claim(account, &service.UpstreamFailoverError{StatusCode: 429}, time.Now()))
		releases := 0
		sel := &service.AccountSelectionResult{Account: account, Acquired: true, PreserveStickyBinding: true, ReleaseFunc: func() { releases++ }}
		streamStarted := false
		release, status := h.acquireOpenAIAccountSlot(c, nil, "", sel, false, &streamStarted, zap.NewNop(), nil, &b)
		require.Equal(t, openAISlotAcquireOK, status)
		time.Sleep(31 * time.Second)
		synctest.Wait()
		require.Zero(t, releases)
		require.NoError(t, c.Request.Context().Err())
		release()
		require.Equal(t, 1, releases)
	})
}

func TestOpenAI429QueueWaitEndsWithUpstreamRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		h := &OpenAIGatewayHandler{concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(&concurrencyCacheMock{}), SSEPingFormatNone, 0)}
		account := &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
		streamStarted := false
		b := newHTTPOpenAI429Recovery(c, &streamStarted, h.handleFailoverExhausted)
		started := time.Now()
		require.True(t, b.claim(account, &service.UpstreamFailoverError{StatusCode: 429, ResponseHeaders: http.Header{"Retry-After": {"5"}}}, started))
		sel := &service.AccountSelectionResult{Account: account, WaitPlan: &service.AccountWaitPlan{MaxConcurrency: 1, MaxWaiting: 5, Timeout: time.Minute}}
		release, status := h.acquireOpenAIAccountSlot(c, nil, "", sel, false, &streamStarted, zap.NewNop(), nil, b)
		require.Equal(t, openAISlotAcquireFailed, status)
		require.Nil(t, release)
		require.Equal(t, 30*time.Second, time.Since(started))
		require.Equal(t, http.StatusTooManyRequests, rec.Code)
		require.Equal(t, "5", rec.Header().Get("Retry-After"))
		require.NoError(t, c.Request.Context().Err())
	})
}
