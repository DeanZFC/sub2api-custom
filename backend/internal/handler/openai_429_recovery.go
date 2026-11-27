package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	openAI429RecoveryWindow = 30 * time.Second
	openAI429MaxRetries     = 3
)

// One budget for a downstream request (or one logical WebSocket turn).
// Its deadline applies to retry preparation, never to an in-flight generation.
type openAI429Recovery struct {
	deadline    time.Time
	remaining   int
	lastFailure *service.UpstreamFailoverError
	finish      func(*service.UpstreamFailoverError)
}

func newHTTPOpenAI429Recovery(c *gin.Context, streamStarted *bool, write func(*gin.Context, *service.UpstreamFailoverError, bool)) *openAI429Recovery {
	return &openAI429Recovery{finish: func(failure *service.UpstreamFailoverError) {
		if !failoverClientGone(c) {
			write(c, failure, *streamStarted)
		}
	}}
}

func (b *openAI429Recovery) claim(account *service.Account, failure *service.UpstreamFailoverError, now time.Time) bool {
	if b.deadline.IsZero() {
		if !account.IsOpenAIOAuthLike() || failure.StatusCode != http.StatusTooManyRequests {
			return true
		}
		b.deadline = now.Add(openAI429RecoveryWindow)
		b.remaining = min(openAI429MaxRetries, effectiveSameAccountRetryLimit(failure, account))
	}
	b.lastFailure = failure
	if d := failure.SameAccountRetryDeadline; !d.IsZero() && d.Before(b.deadline) {
		b.deadline = d
	}
	if b.remaining == 0 || !now.Before(b.deadline) {
		b.finish(failure)
		return false
	}
	b.remaining--
	return true
}

func (b *openAI429Recovery) waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if b == nil || b.deadline.IsZero() {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, b.deadline)
}

func (b *openAI429Recovery) expired() bool {
	return b != nil && !b.deadline.IsZero() && !time.Now().Before(b.deadline)
}

// Release a newly acquired slot if the deadline passed during scheduling or admission.
func (b *openAI429Recovery) stopIfExpired(release func()) bool {
	if !b.expired() {
		return false
	}
	if release != nil {
		release()
	}
	b.finish(b.lastFailure)
	return true
}

func (b *openAI429Recovery) stopAfterSelection(s *service.AccountSelectionResult) bool {
	if s != nil {
		return b.stopIfExpired(s.ReleaseFunc)
	}
	return b.stopIfExpired(nil)
}

type openAIRetryAction int

const (
	openAISwitchAccount openAIRetryAction = iota
	openAIRetrySameAccount
	openAIStopRetry
)

// Called after the endpoint's no-replay guard. Configured retries, retries on
// the same account and account switches all consume the same recovery budget.
func (b *openAI429Recovery) afterFailure(ctx context.Context, account *service.Account, failure *service.UpstreamFailoverError, counts map[int64]int, allowSameAccount bool, log *zap.Logger, event string) openAIRetryAction {
	if ctx.Err() != nil || !b.claim(account, failure, time.Now()) {
		return openAIStopRetry
	}
	waitCtx, cancel := b.waitContext(ctx)
	defer cancel()
	if claimed, err := service.TryConfiguredUpstreamErrorRetry(waitCtx, failure); claimed {
		if err != nil {
			b.stopIfExpired(nil)
			return openAIStopRetry
		}
		return openAIRetrySameAccount
	}
	limit := effectiveSameAccountRetryLimit(failure, account)
	count := counts[account.ID]
	if !allowSameAccount || !sameAccountRetryAllowed(failure, count, limit) {
		return openAISwitchAccount
	}
	delay := sameAccountRetryDelayFor(failure, count+1)
	if !b.deadline.IsZero() && !time.Now().Add(delay).Before(b.deadline) {
		b.finish(failure)
		return openAIStopRetry
	}
	counts[account.ID]++
	log.Warn(event, zap.Int64("account_id", account.ID), zap.Int("upstream_status", failure.StatusCode),
		zap.Int("retry_limit", limit), zap.Int("retry_count", count+1), zap.Duration("retry_delay", delay))
	if !sleepWithContext(waitCtx, delay) || ctx.Err() != nil {
		b.stopIfExpired(nil)
		return openAIStopRetry
	}
	return openAIRetrySameAccount
}
