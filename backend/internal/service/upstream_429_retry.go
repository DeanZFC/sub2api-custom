package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
)

const (
	defaultUpstream429RetryDelay = 500 * time.Millisecond
	maxUpstream429RetryDelay     = 8 * time.Second
	maxUpstream429RetryAfter     = time.Minute
	maxAccount429RetryTotalTime  = 5 * time.Minute
	maxRetryResponseDrainBytes   = 64 << 10
)

// account429RetryResponseBody marks the final 429 returned after the account's
// request-local retry budget has been exhausted. The marker stays internal to
// the process and cannot leak into downstream response headers.
type account429RetryResponseBody struct {
	io.ReadCloser
	retries int
}

type account429RetryScopeContextKey struct{}

type account429RetryMarkerContextKey struct{}

type account429RetryMarker struct {
	retries int
}

// account429RetryBudget is shared by every upstream attempt spawned from one
// downstream request. Keeping usage per account means failover accounts receive
// their own configured allowance, while official same-account retry loops do
// not accidentally obtain a fresh allowance on every iteration.
type account429RetryBudget struct {
	mu   sync.Mutex
	used map[int64]int
}

type account429RetryDo func(*http.Request) (*http.Response, error)

type account429RetryWSDial func(context.Context) (openAIWSClientConn, int, http.Header, error)

var errUpstreamWS429Handshake = errors.New("upstream WebSocket handshake rejected with status 429")

// WithAccount429RetryScope attaches one request-local, per-account retry budget
// to ctx. Calling it repeatedly is idempotent, so middleware and direct service
// entry points can safely share the same scope.
func WithAccount429RetryScope(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if account429RetryBudgetFromContext(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, account429RetryScopeContextKey{}, &account429RetryBudget{})
}

func account429RetryBudgetFromContext(ctx context.Context) *account429RetryBudget {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(account429RetryScopeContextKey{}).(*account429RetryBudget)
	return state
}

func ensureAccount429RetryBudget(ctx context.Context) (context.Context, *account429RetryBudget) {
	ctx = WithAccount429RetryScope(ctx)
	return ctx, account429RetryBudgetFromContext(ctx)
}

// take reserves exactly one extra retry. The returned retry number is
// one-based and is used for logging/backoff. It is safe when multiple upstream
// branches derived from the same request race on the same account.
func (b *account429RetryBudget) take(accountID int64, limit int) (int, bool) {
	if b == nil || limit <= 0 {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used == nil {
		b.used = make(map[int64]int)
	}
	if b.used[accountID] >= limit {
		return b.used[accountID], false
	}
	b.used[accountID]++
	return b.used[accountID], true
}

func (b *account429RetryBudget) retriesUsed(accountID int64) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used[accountID]
}

// account429RetryTotalTimeout returns a bounded outer timeout that can cover
// every configured attempt plus the maximum Retry-After delay between them.
func account429RetryTotalTimeout(perAttempt time.Duration, account *Account) time.Duration {
	if perAttempt <= 0 {
		return 0
	}
	retryLimit := account.GetRateLimit429RetryCount()
	attempts := time.Duration(retryLimit + 1)
	waits := time.Duration(retryLimit) * maxUpstream429RetryAfter
	if attempts > 0 && perAttempt > (maxAccount429RetryTotalTime-waits)/attempts {
		return maxAccount429RetryTotalTime
	}
	total := attempts*perAttempt + waits
	if total > maxAccount429RetryTotalTime {
		return maxAccount429RetryTotalTime
	}
	return total
}

// doAccount429Retry retries a replayable request on the same selected account.
// Intermediate 429 responses never reach provider-specific error handlers, so
// account cooldown/rate-limit side effects only run after the final attempt.
func doAccount429Retry(req *http.Request, account *Account, do account429RetryDo) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("upstream 429 retry: nil request")
	}
	if account == nil {
		return nil, errors.New("upstream 429 retry: account is nil")
	}
	if do == nil {
		return nil, errors.New("upstream 429 retry: nil transport")
	}

	retryLimit := account.GetRateLimit429RetryCount()
	if retryLimit <= 0 {
		return do(req)
	}
	retryCtx, retryBudget := ensureAccount429RetryBudget(req.Context())
	baseReq := req.WithContext(retryCtx)
	attemptReq := baseReq
	for {
		resp, err := do(attemptReq)
		if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
			return resp, err
		}
		if ctxErr := attemptReq.Context().Err(); ctxErr != nil {
			drainAndCloseRetryResponse(resp)
			return nil, ctxErr
		}
		// HTTP status is authoritative for a response-bearing transport result.
		// A plugin/adapter may return both a 429 response and a non-context error;
		// treat that as the same retryable 429 while preserving cancellation.
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				drainAndCloseRetryResponse(resp)
				return nil, err
			}
			err = nil
		}

		if !requestCanReplay(attemptReq) {
			slog.Warn("upstream_429_same_account_retry_unreplayable",
				"account_id", accountIDForRetryLog(account),
				"retry_count", retryBudget.retriesUsed(account.ID),
				"retry_limit", retryLimit,
			)
			return resp, nil
		}

		nextReq, cloneErr := cloneRequestForRetry(baseReq)
		if cloneErr != nil {
			slog.Warn("upstream_429_same_account_retry_unreplayable",
				"account_id", accountIDForRetryLog(account),
				"retry_count", retryBudget.retriesUsed(account.ID),
				"retry_limit", retryLimit,
				"error", cloneErr,
			)
			return resp, nil
		}
		retryNumber, ok := retryBudget.take(account.ID, retryLimit)
		if !ok {
			if nextReq.Body != nil && nextReq.Body != http.NoBody {
				_ = nextReq.Body.Close()
			}
			markAccount429RetriesExhausted(resp, attemptReq, retryNumber)
			return resp, nil
		}
		delay := upstream429RetryDelay(resp.Header, retryNumber-1)
		drainAndCloseRetryResponse(resp)
		slog.Warn("upstream_429_same_account_retry",
			"account_id", accountIDForRetryLog(account),
			"retry_count", retryNumber,
			"retry_limit", retryLimit,
			"retry_delay", delay,
		)
		if err := sleepUpstream429Retry(attemptReq.Context(), delay); err != nil {
			if nextReq.Body != nil && nextReq.Body != http.NoBody {
				_ = nextReq.Body.Close()
			}
			return nil, err
		}

		attemptReq = nextReq
	}
}

func doAccountHTTPUpstream(
	upstream HTTPUpstream,
	req *http.Request,
	proxyURL string,
	account *Account,
) (*http.Response, error) {
	if upstream == nil {
		return nil, errors.New("upstream 429 retry: HTTP upstream is nil")
	}
	if account == nil {
		return nil, errors.New("upstream 429 retry: account is nil")
	}
	return doAccount429Retry(req, account, func(attemptReq *http.Request) (*http.Response, error) {
		return upstream.Do(attemptReq, proxyURL, account.ID, account.Concurrency)
	})
}

func doAccountHTTPUpstreamWithTLS(
	upstream HTTPUpstream,
	req *http.Request,
	proxyURL string,
	account *Account,
	profile *tlsfingerprint.Profile,
) (*http.Response, error) {
	if upstream == nil {
		return nil, errors.New("upstream 429 retry: HTTP upstream is nil")
	}
	if account == nil {
		return nil, errors.New("upstream 429 retry: account is nil")
	}
	return doAccount429Retry(req, account, func(attemptReq *http.Request) (*http.Response, error) {
		return upstream.DoWithTLS(attemptReq, proxyURL, account.ID, account.Concurrency, profile)
	})
}

// dialAccount429Retry retries only failed WebSocket handshakes. Once a
// connection has been established and frames have been exchanged, replay must
// remain at the higher-level turn state machine to avoid duplicate output.
func dialAccount429Retry(
	ctx context.Context,
	account *Account,
	dial account429RetryWSDial,
) (openAIWSClientConn, int, http.Header, bool, error) {
	if account == nil {
		return nil, 0, nil, false, errors.New("upstream 429 retry: account is nil")
	}
	if dial == nil {
		return nil, 0, nil, false, errors.New("upstream 429 retry: WebSocket dialer is nil")
	}
	ctx, retryBudget := ensureAccount429RetryBudget(ctx)
	retryLimit := account.GetRateLimit429RetryCount()
	for {
		conn, status, headers, err := dial(ctx)
		if status != http.StatusTooManyRequests {
			return conn, status, headers, false, err
		}
		if conn != nil {
			_ = conn.Close()
			conn = nil
		}
		if err == nil {
			err = errUpstreamWS429Handshake
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return conn, status, headers, false, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return conn, status, headers, false, err
		}
		retryNumber, ok := retryBudget.take(account.ID, retryLimit)
		if !ok {
			return conn, status, headers, retryLimit > 0 && retryNumber >= retryLimit, err
		}
		delay := upstream429RetryDelay(headers, retryNumber-1)
		slog.Warn("upstream_ws_429_same_account_retry",
			"account_id", accountIDForRetryLog(account),
			"retry_count", retryNumber,
			"retry_limit", retryLimit,
			"retry_delay", delay,
		)
		if sleepErr := sleepUpstream429Retry(ctx, delay); sleepErr != nil {
			return nil, status, headers, false, sleepErr
		}
	}
}

func requestCanReplay(req *http.Request) bool {
	if req == nil {
		return false
	}
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func cloneRequestForRetry(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())
	if req.Body == nil || req.Body == http.NoBody {
		clone.Body = req.Body
		return clone, nil
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	clone.Body = body
	clone.GetBody = req.GetBody
	clone.ContentLength = req.ContentLength
	return clone, nil
}

func upstream429RetryDelay(headers http.Header, retryCount int) time.Duration {
	if headers != nil {
		if delay, ok := parseRetryAfter(headers.Get("Retry-After"), time.Now()); ok {
			if delay > maxUpstream429RetryAfter {
				return maxUpstream429RetryAfter
			}
			return delay
		}
	}
	delay := defaultUpstream429RetryDelay
	for i := 0; i < retryCount; i++ {
		if delay >= maxUpstream429RetryDelay/2 {
			return maxUpstream429RetryDelay
		}
		delay *= 2
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		maxSeconds := uint64(maxUpstream429RetryAfter / time.Second)
		if seconds >= maxSeconds {
			return maxUpstream429RetryAfter, true
		}
		return time.Duration(seconds) * time.Second, true
	} else if strings.Trim(value, "0123456789") == "" {
		// ParseUint only fails for an all-digit value when it exceeds uint64.
		// Treat that as a capped delay instead of overflowing time.Duration.
		return maxUpstream429RetryAfter, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if !when.After(now) {
		return 0, true
	}
	return when.Sub(now), true
}

func sleepUpstream429Retry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func drainAndCloseRetryResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRetryResponseDrainBytes))
	_ = resp.Body.Close()
}

func accountIDForRetryLog(account *Account) int64 {
	if account == nil {
		return 0
	}
	return account.ID
}

func account429RetriesExhausted(resp *http.Response) bool {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return false
	}
	if resp.Request != nil {
		if _, ok := resp.Request.Context().Value(account429RetryMarkerContextKey{}).(account429RetryMarker); ok {
			return true
		}
	}
	_, ok := resp.Body.(*account429RetryResponseBody)
	return ok
}

func markAccount429RetriesExhausted(resp *http.Response, req *http.Request, retries int) {
	if resp == nil {
		return
	}
	baseReq := resp.Request
	if baseReq == nil {
		baseReq = req
	}
	if baseReq != nil {
		ctx := context.WithValue(baseReq.Context(), account429RetryMarkerContextKey{}, account429RetryMarker{retries: retries})
		resp.Request = baseReq.WithContext(ctx)
	}
	if resp.Body != nil {
		resp.Body = &account429RetryResponseBody{ReadCloser: resp.Body, retries: retries}
	}
}

func finalizeAccount429Failover(resp *http.Response, failoverErr *UpstreamFailoverError) *UpstreamFailoverError {
	if failoverErr != nil && account429RetriesExhausted(resp) {
		failoverErr.Account429RetryExhausted = true
	}
	return failoverErr
}

func preserveAccount429RetryMarker(source *http.Response, body io.ReadCloser) io.ReadCloser {
	if body == nil || !account429RetriesExhausted(source) {
		return body
	}
	retries := 0
	if marker, ok := source.Body.(*account429RetryResponseBody); ok {
		retries = marker.retries
	}
	return &account429RetryResponseBody{ReadCloser: body, retries: retries}
}
