package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func retryCountPointer(value int) *int { return &value }

func retryTestResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Retry-After": []string{"0"}},
		Body:       io.NopCloser(strings.NewReader(http.StatusText(status))),
	}
}

type retryTrackingBody struct {
	io.Reader
	closed bool
}

func (b *retryTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestAccountRateLimit429RetryCountDefaultsAndBounds(t *testing.T) {
	require.Equal(t, DefaultRateLimit429RetryCount, (*Account)(nil).GetRateLimit429RetryCount())
	require.Equal(t, DefaultRateLimit429RetryCount, (&Account{}).GetRateLimit429RetryCount())
	require.Equal(t, 0, (&Account{RateLimit429RetryCount: retryCountPointer(0)}).GetRateLimit429RetryCount())
	require.Equal(t, MaxRateLimit429RetryCount, (&Account{RateLimit429RetryCount: retryCountPointer(99)}).GetRateLimit429RetryCount())
	require.Error(t, ValidateRateLimit429RetryCount(-1))
	require.NoError(t, ValidateRateLimit429RetryCount(0))
	require.NoError(t, ValidateRateLimit429RetryCount(MaxRateLimit429RetryCount))
	require.Error(t, ValidateRateLimit429RetryCount(MaxRateLimit429RetryCount+1))
}

func TestAccount429RetryTotalTimeout(t *testing.T) {
	require.Equal(t, 3*time.Second, account429RetryTotalTimeout(3*time.Second, &Account{RateLimit429RetryCount: retryCountPointer(0)}))
	require.Equal(t, 3*time.Second, account429RetryTotalTimeout(3*time.Second, &Account{RateLimit429RetryCount: retryCountPointer(2)}))
	require.Equal(t, 10*time.Second, account429RetryTotalTimeout(10*time.Second, &Account{}))
	require.Equal(t, time.Minute, account429RetryTotalTimeout(time.Minute, &Account{RateLimit429RetryCount: retryCountPointer(MaxRateLimit429RetryCount)}))
}

func TestAccount429RetryScopeIsIdempotentAndConcurrencySafe(t *testing.T) {
	ctx := WithAccount429RetryScope(context.Background())
	state := account429RetryBudgetFromContext(ctx)
	require.NotNil(t, state)
	require.Same(t, state, account429RetryBudgetFromContext(WithAccount429RetryScope(ctx)))

	const retryLimit = 5
	var wg sync.WaitGroup
	claimed := make(chan bool, 100)
	for i := 0; i < cap(claimed); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok := state.take(99, retryLimit)
			claimed <- ok
		}()
	}
	wg.Wait()
	close(claimed)

	successes := 0
	for ok := range claimed {
		if ok {
			successes++
		}
	}
	require.Equal(t, retryLimit, successes)
	require.Equal(t, retryLimit, state.retriesUsed(99))
}

func TestDoAccount429RetryDoesNotReplaySameRequest(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/v1/responses", bytes.NewBufferString(`{"input":"hello"}`))
	require.NoError(t, err)

	account := &Account{ID: 42, RateLimit429RetryCount: retryCountPointer(2)}
	calls := 0
	bodies := make([]string, 0, 1)
	resp, err := doAccount429Retry(req, account, func(attemptReq *http.Request) (*http.Response, error) {
		calls++
		body, readErr := io.ReadAll(attemptReq.Body)
		require.NoError(t, readErr)
		bodies = append(bodies, string(body))
		return retryTestResponse(http.StatusTooManyRequests), nil
	})

	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Equal(t, 1, calls)
	require.Equal(t, []string{`{"input":"hello"}`}, bodies)
	require.False(t, account429RetriesExhausted(resp))
}

func TestDoAccount429RetryReturns429Immediately(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)
	account := &Account{ID: 7, RateLimit429RetryCount: retryCountPointer(2)}
	calls := 0

	resp, err := doAccount429Retry(req, account, func(*http.Request) (*http.Response, error) {
		calls++
		return retryTestResponse(http.StatusTooManyRequests), nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.False(t, account429RetriesExhausted(resp))
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusText(http.StatusTooManyRequests), string(body))
}

func TestDoAccount429RetryTreatsResponse429AsAuthoritativeWithAdapterError(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)
	account := &Account{RateLimit429RetryCount: retryCountPointer(1)}
	calls := 0
	adapterErr := errors.New("adapter returned response and error")

	resp, err := doAccount429Retry(req, account, func(*http.Request) (*http.Response, error) {
		calls++
		return retryTestResponse(http.StatusTooManyRequests), adapterErr
	})

	require.ErrorIs(t, err, adapterErr)
	require.Equal(t, 1, calls)
	require.False(t, account429RetriesExhausted(resp))
}

func TestDoAccount429RetryDoesNotShareOrConsumeBudget(t *testing.T) {
	ctx := WithAccount429RetryScope(context.Background())
	account := &Account{ID: 71, RateLimit429RetryCount: retryCountPointer(2)}

	firstReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/first", nil)
	require.NoError(t, err)
	firstCalls := 0
	firstResp, err := doAccount429Retry(firstReq, account, func(*http.Request) (*http.Response, error) {
		firstCalls++
		return retryTestResponse(http.StatusTooManyRequests), nil
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, firstResp.StatusCode)
	require.Equal(t, 1, firstCalls)

	// A later official attempt is independent because this compatibility helper
	// no longer consumes a transparent retry budget.
	secondReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/second", nil)
	require.NoError(t, err)
	secondCalls := 0
	secondResp, err := doAccount429Retry(secondReq, account, func(*http.Request) (*http.Response, error) {
		secondCalls++
		return retryTestResponse(http.StatusTooManyRequests), nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, secondCalls)
	require.False(t, account429RetriesExhausted(secondResp))

	thirdReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/third", nil)
	require.NoError(t, err)
	thirdCalls := 0
	thirdResp, err := doAccount429Retry(thirdReq, account, func(*http.Request) (*http.Response, error) {
		thirdCalls++
		return retryTestResponse(http.StatusTooManyRequests), nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, thirdCalls)
	require.False(t, account429RetriesExhausted(thirdResp))
}

func TestDoAccount429RetryKeepsSeparateBudgetPerAccount(t *testing.T) {
	ctx := WithAccount429RetryScope(context.Background())
	for _, accountID := range []int64{101, 202} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/v1/models", nil)
		require.NoError(t, err)
		calls := 0
		resp, err := doAccount429Retry(req, &Account{ID: accountID, RateLimit429RetryCount: retryCountPointer(1)}, func(*http.Request) (*http.Response, error) {
			calls++
			return retryTestResponse(http.StatusTooManyRequests), nil
		})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.False(t, account429RetriesExhausted(resp))
	}
}

func TestDoAccount429RetryClosesResponseOnContextTransportError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)
	body := &retryTrackingBody{Reader: strings.NewReader("limited")}

	resp, err := doAccount429Retry(req, &Account{ID: 72, RateLimit429RetryCount: retryCountPointer(1)}, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: body}, context.Canceled
	})

	require.Same(t, body, resp.Body)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, body.closed)
}

func TestDoAccountHTTPUpstreamRejectsNilAccount(t *testing.T) {
	_, err := doAccountHTTPUpstream(&nilAccountHTTPUpstreamStub{}, nil, "", nil)
	require.EqualError(t, err, "upstream 429 retry: account is nil")
}

func TestDoAccount429RetryRejectsNilAccount(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)

	_, err = doAccount429Retry(req, nil, func(*http.Request) (*http.Response, error) {
		t.Fatal("nil account must be rejected before transport execution")
		return nil, nil
	})
	require.EqualError(t, err, "upstream 429 retry: account is nil")
}

type nilAccountHTTPUpstreamStub struct{}

func (*nilAccountHTTPUpstreamStub) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, nil
}

func (*nilAccountHTTPUpstreamStub) DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
	return nil, nil
}

func TestAccount429RetryMarkerSurvivesResponseWithoutRequest(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/v1/responses", bytes.NewBufferString(`{"input":"hello"}`))
	require.NoError(t, err)
	resp := retryTestResponse(http.StatusTooManyRequests)
	// Most real http.Client responses carry Request, but lightweight upstream
	// adapters and test doubles do not. The request passed to the retry helper
	// must be enough to carry the marker through later body reconstruction.
	resp.Request = nil
	markAccount429RetriesExhausted(resp, req, 5)

	require.True(t, account429RetriesExhausted(resp))
	require.NotNil(t, resp.Request)
	require.NotNil(t, resp.Request.Context().Value(account429RetryMarkerContextKey{}))
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Equal(t, http.StatusText(http.StatusTooManyRequests), string(body))

	// A caller may consume the body and replace it with a replayable copy
	// before constructing the failover error. Preserve the marker explicitly.
	resp.Body = preserveAccount429RetryMarker(resp, io.NopCloser(bytes.NewReader(body)))
	failoverErr := finalizeAccount429Failover(resp, &UpstreamFailoverError{StatusCode: resp.StatusCode})
	require.True(t, failoverErr.Account429RetryExhausted)
	require.True(t, account429RetriesExhausted(resp))
}

func TestAccount429RetryMarkerDoesNotApplyToNon429Response(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)
	resp := retryTestResponse(http.StatusOK)
	markAccount429RetriesExhausted(resp, req, 5)
	require.False(t, account429RetriesExhausted(resp))
	require.False(t, finalizeAccount429Failover(resp, &UpstreamFailoverError{StatusCode: resp.StatusCode}).Account429RetryExhausted)
}

func TestDoAccount429RetryZeroDisablesRetry(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)
	account := &Account{RateLimit429RetryCount: retryCountPointer(0)}
	calls := 0

	resp, err := doAccount429Retry(req, account, func(*http.Request) (*http.Response, error) {
		calls++
		return retryTestResponse(http.StatusTooManyRequests), nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.False(t, account429RetriesExhausted(resp), "关闭新功能时必须保留原有 429 处理语义")
}

func TestDoAccount429RetryStopsWhenRequestCannotReplay(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.com/upload", io.NopCloser(strings.NewReader("payload")))
	require.NoError(t, err)
	require.Nil(t, req.GetBody)
	account := &Account{RateLimit429RetryCount: retryCountPointer(5)}
	calls := 0

	resp, err := doAccount429Retry(req, account, func(*http.Request) (*http.Response, error) {
		calls++
		return retryTestResponse(http.StatusTooManyRequests), nil
	})

	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.False(t, account429RetriesExhausted(resp), "未实际用尽重试预算时必须保留原有 429 处理语义")
}

func TestDoAccount429RetryHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/v1/models", nil)
	require.NoError(t, err)
	account := &Account{RateLimit429RetryCount: retryCountPointer(5)}
	calls := 0

	resp, err := doAccount429Retry(req, account, func(*http.Request) (*http.Response, error) {
		calls++
		cancel()
		return retryTestResponse(http.StatusTooManyRequests), nil
	})

	require.NotNil(t, resp)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestDialAccount429RetryDoesNotReplayHandshake(t *testing.T) {
	account := &Account{ID: 81, RateLimit429RetryCount: retryCountPointer(2)}
	calls := 0
	wantErr := errors.New("handshake rejected")
	conn, status, _, exhausted, err := dialAccount429Retry(context.Background(), account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
		calls++
		return nil, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"0"}}, wantErr
	})

	require.ErrorIs(t, err, wantErr)
	require.Nil(t, conn)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.False(t, exhausted)
	require.Equal(t, 1, calls)
}

func TestDialAccount429RetryReportsExhaustionOnlyAfterConfiguredRetries(t *testing.T) {
	wantErr := errors.New("handshake rejected")

	t.Run("configured retries exhausted", func(t *testing.T) {
		account := &Account{ID: 82, RateLimit429RetryCount: retryCountPointer(2)}
		calls := 0
		_, status, _, exhausted, err := dialAccount429Retry(context.Background(), account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
			calls++
			return nil, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"0"}}, wantErr
		})

		require.ErrorIs(t, err, wantErr)
		require.Equal(t, http.StatusTooManyRequests, status)
		require.False(t, exhausted)
		require.Equal(t, 1, calls)
	})

	t.Run("zero retries is not exhaustion", func(t *testing.T) {
		account := &Account{ID: 83, RateLimit429RetryCount: retryCountPointer(0)}
		calls := 0
		_, status, _, exhausted, err := dialAccount429Retry(context.Background(), account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
			calls++
			return nil, http.StatusTooManyRequests, nil, wantErr
		})

		require.ErrorIs(t, err, wantErr)
		require.Equal(t, http.StatusTooManyRequests, status)
		require.False(t, exhausted)
		require.Equal(t, 1, calls)
	})

	t.Run("context cancellation is not exhaustion", func(t *testing.T) {
		account := &Account{ID: 84, RateLimit429RetryCount: retryCountPointer(2)}
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, status, _, exhausted, err := dialAccount429Retry(ctx, account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
			calls++
			cancel()
			return nil, http.StatusTooManyRequests, nil, wantErr
		})

		require.ErrorIs(t, err, wantErr)
		require.Equal(t, http.StatusTooManyRequests, status)
		require.False(t, exhausted)
		require.Equal(t, 1, calls)
	})

	t.Run("nil dial error is still a failed handshake", func(t *testing.T) {
		account := &Account{ID: 85, RateLimit429RetryCount: retryCountPointer(0)}
		_, status, _, exhausted, err := dialAccount429Retry(context.Background(), account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
			return nil, http.StatusTooManyRequests, nil, nil
		})

		require.ErrorIs(t, err, errUpstreamWS429Handshake)
		require.Equal(t, http.StatusTooManyRequests, status)
		require.False(t, exhausted)
	})
}

func TestDialAccount429RetryDoesNotConsumeBudgetAcrossDials(t *testing.T) {
	ctx := WithAccount429RetryScope(context.Background())
	account := &Account{ID: 86, RateLimit429RetryCount: retryCountPointer(1)}
	wantErr := errors.New("handshake rejected")

	firstCalls := 0
	_, _, _, exhausted, err := dialAccount429Retry(ctx, account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
		firstCalls++
		return nil, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"0"}}, wantErr
	})
	require.ErrorIs(t, err, wantErr)
	require.False(t, exhausted)
	require.Equal(t, 1, firstCalls)

	secondCalls := 0
	_, _, _, exhausted, err = dialAccount429Retry(ctx, account, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
		secondCalls++
		return nil, http.StatusTooManyRequests, nil, wantErr
	})
	require.ErrorIs(t, err, wantErr)
	require.False(t, exhausted)
	require.Equal(t, 1, secondCalls)
}

func TestDialAccount429RetryRejectsNilAccount(t *testing.T) {
	_, _, _, exhausted, err := dialAccount429Retry(context.Background(), nil, func(context.Context) (openAIWSClientConn, int, http.Header, error) {
		t.Fatal("nil account must be rejected before dialing")
		return nil, 0, nil, nil
	})
	require.EqualError(t, err, "upstream 429 retry: account is nil")
	require.False(t, exhausted)
}

func TestUpstream429RetryDelayCapsRetryAfter(t *testing.T) {
	header := http.Header{"Retry-After": []string{"30"}}
	require.Equal(t, 30*time.Second, upstream429RetryDelay(header, 0))
	header.Set("Retry-After", "120")
	require.Equal(t, maxUpstream429RetryAfter, upstream429RetryDelay(header, 0))
	header.Set("Retry-After", "999999999999999999999999999999999999999999")
	require.Equal(t, maxUpstream429RetryAfter, upstream429RetryDelay(header, 0))
	require.Equal(t, 500*time.Millisecond, upstream429RetryDelay(nil, 0))
	require.Equal(t, 8*time.Second, upstream429RetryDelay(nil, 8))
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)

	delay, ok := parseRetryAfter(now.Add(3*time.Second).Format(http.TimeFormat), now)
	require.True(t, ok)
	require.Equal(t, 3*time.Second, delay)

	delay, ok = parseRetryAfter(now.Add(-time.Second).Format(http.TimeFormat), now)
	require.True(t, ok)
	require.Zero(t, delay)
}
