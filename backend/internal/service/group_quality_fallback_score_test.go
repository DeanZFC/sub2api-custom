package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type groupQualityFallbackScoreTestGate struct {
	groupQualityFallbackTestGate
	scores       map[int64]*ChannelQualityScore
	scoreErr     error
	scoreIDs     []int64
	scoreModel   string
	scoreLookups int
	scoreLookup  func(context.Context) error
}

func (g *groupQualityFallbackScoreTestGate) ChannelQualityScores(ctx context.Context, ids []int64, model string) (map[int64]*ChannelQualityScore, error) {
	g.scoreIDs = append([]int64(nil), ids...)
	g.scoreModel = model
	g.scoreLookups++
	if g.scoreLookup != nil {
		if err := g.scoreLookup(ctx); err != nil {
			return nil, err
		}
	}
	return g.scores, g.scoreErr
}

func groupQualityFallbackTestScore(status string, value float64) *ChannelQualityScore {
	return &ChannelQualityScore{Status: status, Value: &value}
}

func TestGroupQualityFallbackUsesIndependentScoreForExactPublicModel(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			t.Run(mode+"/"+string(transport), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(43)
				accounts := []Account{
					groupQualityFallbackTestAccount(1, groupID, 0, true),
					groupQualityFallbackTestAccount(2, groupID, 100, true),
					groupQualityFallbackTestAccount(3, groupID, -100, true),
					groupQualityFallbackTestAccount(4, groupID, -200, true),
				}
				accounts[2].Schedulable = false
				accounts[3].Extra["active_pass_rate_holds"] = map[string]any{"public-model": true}
				gate := &groupQualityFallbackScoreTestGate{
					groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true},
					scores: map[int64]*ChannelQualityScore{
						1: groupQualityFallbackTestScore("ready", 10),
						2: groupQualityFallbackTestScore("ready", 90),
						3: groupQualityFallbackTestScore("ready", 100),
						4: groupQualityFallbackTestScore("ready", 100),
					},
				}
				svc := groupQualityFallbackService(mode, accounts, &gate.groupQualityFallbackTestGate)
				svc.concurrencyService.passiveQualityGate = gate
				ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
				ctx = context.WithValue(ctx, ctxkey.Model, "provider-model")
				ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, "public-model")
				req.RequestedModel = "provider-model"
				req.RequiredTransport = transport
				selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
				require.NoError(t, err)
				require.Equal(t, int64(2), selection.Account.ID, "independent quality evidence must override the old priority order")
				require.Equal(t, "public-model", gate.scoreModel)
				require.ElementsMatch(t, []int64{1, 2}, gate.scoreIDs, "manual and active intelligence holds remain hard exclusions")
				require.Len(t, gate.calls, 1)
				require.Equal(t, "public-model", gate.calls[0].Model)
				selection.ReleaseFunc()
			})
		}
	}
}

func TestGroupQualityFallbackUnknownScoresAndProviderFailuresRemainAvailable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scores map[int64]*ChannelQualityScore
		err    error
		full   map[int64]bool
		want   int64
	}{
		{name: "equal scores keep scheduler ties", scores: map[int64]*ChannelQualityScore{1: groupQualityFallbackTestScore("ready", 80), 2: groupQualityFallbackTestScore("ready", 80)}, want: 1},
		{name: "partial evidence is rankable", scores: map[int64]*ChannelQualityScore{1: groupQualityFallbackTestScore("ready", 60), 2: groupQualityFallbackTestScore("partial", 80)}, want: 2},
		{name: "known zero outranks unknown", scores: map[int64]*ChannelQualityScore{2: groupQualityFallbackTestScore("ready", 0)}, want: 2},
		{name: "unknown still used when scored account full", scores: map[int64]*ChannelQualityScore{1: {Status: "insufficient"}, 2: groupQualityFallbackTestScore("ready", 90)}, full: map[int64]bool{2: false}, want: 1},
		{name: "all missing keep scheduler order", want: 1},
		{name: "nil summary keeps scheduler order", scores: map[int64]*ChannelQualityScore{1: nil, 2: nil}, want: 1},
		{name: "incomplete is not zero", scores: map[int64]*ChannelQualityScore{1: groupQualityFallbackTestScore("incomplete", 100), 2: groupQualityFallbackTestScore("partial", 20)}, want: 2},
		{name: "missing score value is unknown", scores: map[int64]*ChannelQualityScore{1: {Status: "ready"}, 2: groupQualityFallbackTestScore("ready", 20)}, want: 2},
		{name: "disabled keeps scheduler order", scores: map[int64]*ChannelQualityScore{1: {Status: "disabled"}, 2: groupQualityFallbackTestScore("ready", 100)}, want: 1},
		{name: "provider failure keeps scheduler order", scores: map[int64]*ChannelQualityScore{2: groupQualityFallbackTestScore("ready", 100)}, err: errors.New("score storage unavailable"), want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groupID := int64(43)
			gate := &groupQualityFallbackScoreTestGate{groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true}, scores: tc.scores, scoreErr: tc.err}
			svc := groupQualityFallbackService("legacy", []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityFallbackTestAccount(2, groupID, 100, true)}, &gate.groupQualityFallbackTestGate)
			svc.concurrencyService.passiveQualityGate = gate
			svc.concurrencyService.cache = schedulerTestConcurrencyCache{acquireResults: tc.full}
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.NoError(t, err)
			require.Equal(t, tc.want, selection.Account.ID)
			require.Equal(t, 1, gate.scoreLookups)
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityFallbackIndependentScorePreservesAffinityAndPreviousOwner(t *testing.T) {
	groupID := int64(43)
	accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityFallbackTestAccount(2, groupID, 100, true)}
	gate := &groupQualityFallbackScoreTestGate{
		groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true},
		scores:                       map[int64]*ChannelQualityScore{1: groupQualityFallbackTestScore("ready", 10), 2: groupQualityFallbackTestScore("ready", 90)},
	}
	svc := groupQualityFallbackService("legacy", accounts, &gate.groupQualityFallbackTestGate)
	svc.concurrencyService.passiveQualityGate = gate
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:conversation": 1}}
	svc.cache = cache
	ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
	selectAccount := func(want int64) {
		t.Helper()
		selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
		require.NoError(t, err)
		require.Equal(t, want, selection.Account.ID)
		require.True(t, selection.PreserveStickyBinding)
		selection.ReleaseFunc()
	}
	selectAccount(2)
	gate.scores[1], gate.scores[2] = groupQualityFallbackTestScore("ready", 100), groupQualityFallbackTestScore("ready", 0)
	selectAccount(2)
	require.Equal(t, int64(1), cache.sessionBindings["openai:conversation"], "fallback must preserve ordinary sticky ownership")
	req.PreviousResponseID = "resp_owner"
	req.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, req.PreviousResponseID, 1, time.Hour))
	selectAccount(1)
	require.Equal(t, []int64{1}, gate.scoreIDs, "previous response owner is enforced before scoring")
}

func TestGroupQualityFallbackScoreTimeoutAndRequestCancellation(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "score timeout retains serving", true: "request cancellation prevents admission"}[cancelRequest], func(t *testing.T) {
			groupID := int64(43)
			gate := &groupQualityFallbackScoreTestGate{groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true}}
			svc := groupQualityFallbackService("legacy", []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityFallbackTestAccount(2, groupID, 100, true)}, &gate.groupQualityFallbackTestGate)
			svc.concurrencyService.passiveQualityGate = gate
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			var observedErr error
			gate.scoreLookup = func(scoreCtx context.Context) error {
				deadline, ok := scoreCtx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), groupQualityFallbackScoreTimeout)
				if cancelRequest {
					cancel()
				}
				<-scoreCtx.Done()
				observedErr = scoreCtx.Err()
				return observedErr
			}
			var acquired []int64
			svc.concurrencyService.cache = schedulerTestConcurrencyCache{acquiredIDs: &acquired}
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			if cancelRequest {
				require.ErrorIs(t, err, context.Canceled)
				require.ErrorIs(t, observedErr, context.Canceled)
				require.Nil(t, selection)
				require.Empty(t, acquired)
				require.Empty(t, gate.calls)
				return
			}
			require.NoError(t, err)
			require.ErrorIs(t, observedErr, context.DeadlineExceeded)
			require.Equal(t, int64(1), selection.Account.ID)
			selection.ReleaseFunc()
		})
	}
}
