package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func strictTraversalContext(t *testing.T) context.Context {
	t.Helper()
	group := &service.Group{ID: 75, StrictFailover: service.StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{1}, {2}}}}
	ctx := service.WithAPIKeyGroupFallbackRouting(context.WithValue(context.Background(), ctxkey.Group, group), nil)
	// Preparation marks the actual scheduled group even when its repository is
	// unavailable. Use that public preparation entry point without a scheduler,
	// network calls, or a test-only context setter.
	gateway := &service.OpenAIGatewayService{}
	err := gateway.ValidateStrictFailoverBoundAccount(ctx, &group.ID, &service.Account{ID: 1}, "gpt-5.1", service.OpenAIEndpointCapabilityResponses, service.OpenAIUpstreamTransportAny)
	require.ErrorIs(t, err, service.ErrStrictFailoverUnconfirmed)
	require.True(t, service.StrictFailoverRequestActive(ctx))
	return ctx
}

func TestStrictFailoverTraversesBeyondOrdinarySwitchBudget(t *testing.T) {
	ctx := strictTraversalContext(t)
	state := NewFailoverState(3, false)
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable, ObservedUpstreamStatus: http.StatusServiceUnavailable}
	unscheduler := &mockTempUnscheduler{}
	for id := int64(1); id <= 12; id++ {
		require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, unscheduler, id, service.PlatformOpenAI, 0, failure))
		require.Contains(t, state.FailedAccountIDs, id)
	}
	require.Equal(t, 12, state.SwitchCount)
	require.Len(t, state.FailedAccountIDs, 12)

	// A fully visited pool terminates without clearing its exclusions or
	// sleeping and resurrecting the first tier after the last upstream 503.
	started := time.Now()
	require.Equal(t, FailoverExhausted, state.HandleSelectionExhausted(ctx))
	require.Less(t, time.Since(started), time.Second)
	require.Len(t, state.FailedAccountIDs, 12)

	ordinary := NewFailoverState(3, false)
	for id := int64(1); id <= 3; id++ {
		require.Equal(t, FailoverContinue, ordinary.HandleFailoverError(context.Background(), unscheduler, id, service.PlatformOpenAI, 0, failure))
	}
	require.Equal(t, FailoverExhausted, ordinary.HandleFailoverError(context.Background(), unscheduler, 4, service.PlatformOpenAI, 0, failure))
}

func TestStrictFailoverProfitVetoTraversesWholePool(t *testing.T) {
	ctx := strictTraversalContext(t)
	state := NewFailoverState(3, false)
	failed := make(map[int64]struct{})
	count := 0
	for id := int64(1); id <= 20; id++ {
		require.Equal(t, FailoverContinue, state.RecordProfitVeto(id, ctx))
		require.True(t, recordOpenAIProfitVeto(failed, id, &count, ctx))
	}
	require.Len(t, state.FailedAccountIDs, 20)
	require.Len(t, failed, 20)
	require.Equal(t, 20, state.ProfitVetoCount())
	require.Equal(t, FailoverExhausted, state.HandleSelectionExhausted(ctx))
	// A selector accidentally returning an excluded candidate must stop instead
	// of turning the relaxed numeric budget into an empty retry loop.
	require.Equal(t, FailoverExhausted, state.RecordProfitVeto(1, ctx))
	require.False(t, recordOpenAIProfitVeto(failed, 1, &count, ctx))
	require.Equal(t, 20, count)
	require.Equal(t, 20, state.ProfitVetoCount())
}

func TestStrictFailoverFirstOutputTraversesPeers(t *testing.T) {
	ctx := strictTraversalContext(t)
	failure := &service.UpstreamFailoverError{SafeToFailoverAfterWrite: true}
	count := 0
	for i := 0; i < 12; i++ {
		require.False(t, openAIFirstOutputFailoverExhausted(failure, &count, ctx))
	}
	require.Equal(t, 12, count)
	// This only relaxes the traversal budget. The caller still checks semantic
	// output safety, and selection still requires explicit tier failure evidence.
	ordinaryCount := 0
	require.False(t, openAIFirstOutputFailoverExhausted(failure, &ordinaryCount, context.Background()))
	require.True(t, openAIFirstOutputFailoverExhausted(failure, &ordinaryCount, context.Background()))
}

func TestStrictFailoverTraversalStillStopsCanceledRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(strictTraversalContext(t))
	cancel()
	state := NewFailoverState(3, false)
	failure := &service.UpstreamFailoverError{StatusCode: http.StatusServiceUnavailable}
	require.Equal(t, FailoverCanceled, state.HandleFailoverError(ctx, &mockTempUnscheduler{}, 1, service.PlatformOpenAI, 0, failure))
	require.Equal(t, FailoverCanceled, state.HandleSelectionExhausted(ctx))
	require.Empty(t, state.FailedAccountIDs)
}

func TestStrictFailoverKeepsSameAccountRetriesBeforeExclusion(t *testing.T) {
	ctx := strictTraversalContext(t)
	state := NewFailoverState(0, false)
	failure := &service.UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true,
		SameAccountRetryDelay: time.Nanosecond,
	}
	unscheduler := &mockTempUnscheduler{}
	require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, unscheduler, 1, service.PlatformOpenAI, 1, failure))
	require.Equal(t, 1, state.SameAccountRetryCount[1])
	require.Empty(t, state.FailedAccountIDs)
	require.Empty(t, unscheduler.calls)
	require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, unscheduler, 1, service.PlatformOpenAI, 1, failure))
	require.Contains(t, state.FailedAccountIDs, int64(1))
	require.Len(t, unscheduler.calls, 1)
}

func TestStrictFailoverTraversalBoundsAntigravityDelay(t *testing.T) {
	ctx := strictTraversalContext(t)
	require.Zero(t, antigravityAccountSwitchDelay(ctx, 1))
	require.Equal(t, time.Second, antigravityAccountSwitchDelay(ctx, 2))
	require.Equal(t, 8*time.Second, antigravityAccountSwitchDelay(ctx, 100))
	require.Equal(t, 99*time.Second, antigravityAccountSwitchDelay(context.Background(), 100))
}

func TestStrictFailoverTraversalEndsWhenScheduledGroupIsOrdinary(t *testing.T) {
	ctx := strictTraversalContext(t)
	group := ctx.Value(ctxkey.Group).(*service.Group)
	group.StrictFailover.Enabled = false
	gateway := &service.OpenAIGatewayService{}
	require.NoError(t, gateway.ValidateStrictFailoverBoundAccount(ctx, &group.ID, &service.Account{ID: 1}, "gpt-5.1", service.OpenAIEndpointCapabilityResponses, service.OpenAIUpstreamTransportAny))
	require.False(t, service.StrictFailoverRequestActive(ctx))
	require.True(t, accountSwitchBudgetExhausted(ctx, 3, 3))
}

func TestStrictFailoverTraversalPreservesPinnedCachedContentOwner(t *testing.T) {
	ctx := strictTraversalContext(t)
	state := NewFailoverState(0, true)
	state.accountSwitchPinned = true
	failure := &service.UpstreamFailoverError{
		StatusCode: http.StatusServiceUnavailable, RetryableOnSameAccount: true,
		SameAccountRetryDelay: time.Nanosecond,
	}
	unscheduler := &mockTempUnscheduler{}
	require.Equal(t, FailoverContinue, state.HandleFailoverError(ctx, unscheduler, 1, service.PlatformGemini, 1, failure))
	require.Equal(t, 1, state.SameAccountRetryCount[1])
	require.Empty(t, state.FailedAccountIDs)
	require.Equal(t, FailoverExhausted, state.HandleFailoverError(ctx, unscheduler, 1, service.PlatformGemini, 1, failure))
	require.Zero(t, state.SwitchCount)
	require.Len(t, state.FailedAccountIDs, 1)
}
