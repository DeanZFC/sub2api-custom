package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type groupQualityProfitPolicyTestRepo struct {
	GroupRepository
	group *Group
	err   error
}

func (r groupQualityProfitPolicyTestRepo) GetByIDLite(context.Context, int64) (*Group, error) {
	return r.group, r.err
}

type groupQualityProfitAccountTestRepo struct {
	AccountRepository
	account *Account
	err     error
	calls   int
}

func (r *groupQualityProfitAccountTestRepo) GetByID(context.Context, int64) (*Account, error) {
	r.calls++
	return r.account, r.err
}

func TestGroupQualityFallbackProfitPolicyUnavailableStopsRecovery(t *testing.T) {
	groupID := int64(43)
	for _, tc := range []struct {
		name string
		repo groupQualityProfitPolicyTestRepo
	}{
		{name: "read error", repo: groupQualityProfitPolicyTestRepo{err: errors.New("policy unavailable")}},
		{name: "missing policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService("legacy", []Account{groupQualityFallbackTestAccount(1, groupID, 0, true)}, gate)
			svc.schedulerSnapshot = &SchedulerSnapshotService{groupRepo: tc.repo}
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.ErrorIs(t, err, ErrAccountAdmissionDenied)
			require.ErrorContains(t, err, groupQualityFallbackProfitUnavailable)
			require.Nil(t, selection)
			require.Empty(t, gate.calls, "unverified policy cannot acquire a recovery lease")
			ordinary := svc.withOpenAIProfitControlGate(ctx, &groupID)
			require.Nil(t, ordinary.Value(openAIProfitControlGateCtxKey{}), "ordinary policy failures retain their original semantics")
		})
	}
}

func TestGroupQualityFallbackProfitPolicyUsesOriginalRule(t *testing.T) {
	groupID := int64(43)
	group := profitControlTestGroup(groupID, 0.2, 0)
	group.RateMultiplier = 0.11
	svc := &OpenAIGatewayService{}
	ctx, err := svc.withGroupQualityFallbackProfitControl(profitControlTestCtx(group), &groupID)
	require.NoError(t, err)
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.NotNil(t, gate)
	require.InDelta(t, 0.088, gate.threshold, 1e-12)

	// A gate already frozen for this request remains authoritative.
	group.RateMultiplier = 0.2
	next, err := svc.withGroupQualityFallbackProfitControl(ctx, &groupID)
	require.NoError(t, err)
	require.Same(t, gate, next.Value(openAIProfitControlGateCtxKey{}))

	group.ProfitControlEnabled = false
	ctx, err = svc.withGroupQualityFallbackProfitControl(profitControlTestCtx(group), &groupID)
	require.NoError(t, err)
	require.Nil(t, ctx.Value(openAIProfitControlGateCtxKey{}), "disabled policy retains original behavior")

	group.ProfitControlEnabled = true
	ctx, err = svc.withGroupQualityFallbackProfitControl(WithOpenAIProfitControlSuppressed(profitControlTestCtx(group)), &groupID)
	require.ErrorIs(t, err, ErrAccountAdmissionDenied, "recovery cannot use an enabled policy without its profit gate")
	require.Nil(t, ctx.Value(openAIProfitControlGateCtxKey{}))

	otherGroupID := groupID + 1
	_, err = svc.withGroupQualityFallbackProfitControl(profitControlTestCtx(group), &otherGroupID)
	require.ErrorIs(t, err, ErrAccountAdmissionDenied, "missing target policy cannot authorize recovery")
	_, err = svc.withGroupQualityFallbackProfitControl(context.Background(), &groupID)
	require.ErrorIs(t, err, ErrAccountAdmissionDenied, "missing policy source cannot authorize recovery")
}

func TestGroupQualityFallbackProfitRevalidatesMissingTurnGate(t *testing.T) {
	groupID := int64(43)
	group := profitControlTestGroup(groupID, 0.2, 0)
	group.RateMultiplier = 0.11
	selected := groupQualityFallbackTestAccount(1, groupID, 0, true)
	selected.RateMultiplier = scoreFloat(0.08)
	for _, tc := range []struct {
		name   string
		group  *Group
		err    error
		vetoed bool
	}{
		{name: "configuration failed", err: errors.New("policy unavailable"), vetoed: true},
		{name: "configuration removed", vetoed: true},
		{name: "explicitly disabled", group: &Group{ID: groupID, Platform: PlatformOpenAI}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{schedulerSnapshot: &SchedulerSnapshotService{groupRepo: groupQualityProfitPolicyTestRepo{group: group}}}
			// A composite connection prices with a different group, so each turn
			// must reload the scheduled group's policy.
			billing := &Group{ID: groupID + 1, Platform: PlatformComposite, Status: StatusActive, Hydrated: true, RateMultiplier: 0.11}
			ctx, _ := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(billing), &groupID)
			require.NotNil(t, ctx.Value(openAIProfitControlGateCtxKey{}))
			ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: groupID, AccountID: selected.ID, Model: "public-model"})
			svc.schedulerSnapshot.groupRepo = groupQualityProfitPolicyTestRepo{group: tc.group, err: tc.err}
			ctx, _ = svc.WithOpenAITurnPricingContext(ctx, &groupID)
			require.Nil(t, ctx.Value(openAIProfitControlGateCtxKey{}))
			_, vetoed, reason := svc.ProfitControlVetoLatest(ctx, &selected)
			require.Equal(t, tc.vetoed, vetoed)
			if tc.vetoed {
				require.Equal(t, groupQualityFallbackProfitUnavailable, reason)
			}
		})
	}
}

func TestGroupQualityFallbackProfitTerminalReadIsAuthoritative(t *testing.T) {
	groupID := int64(43)
	group := profitControlTestGroup(groupID, 0.2, 0)
	group.RateMultiplier = 0.11
	for _, tc := range []struct {
		name    string
		rate    float64
		missing bool
		err     error
		vetoed  bool
	}{
		{name: "still affordable", rate: 0.08},
		{name: "raised while acquiring slot", rate: 0.2, vetoed: true},
		{name: "read failed", err: errors.New("account unavailable"), vetoed: true},
		{name: "account deleted", missing: true, vetoed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := groupQualityFallbackTestAccount(1, groupID, 0, true)
			selected.RateMultiplier = scoreFloat(0.08)
			fresh := selected
			fresh.RateMultiplier = &tc.rate
			repo := &groupQualityProfitAccountTestRepo{account: &fresh, err: tc.err}
			if tc.missing {
				repo.account = nil
			}
			svc := &OpenAIGatewayService{accountRepo: repo}
			ordinary, _ := svc.WithOpenAIRequestPricingContext(profitControlTestCtx(group), &groupID)
			_, vetoed, _ := svc.ProfitControlVetoLatest(ordinary, &selected)
			require.False(t, vetoed, "ordinary requests retain original snapshot semantics")
			require.Zero(t, repo.calls)
			recovery := WithGroupQualityFallback(ordinary, GroupQualityFallbackRequest{GroupID: groupID, AccountID: selected.ID, Model: "public-model"})
			latest, vetoed, reason := svc.ProfitControlVetoLatest(recovery, &selected)
			require.Equal(t, tc.vetoed, vetoed)
			require.Equal(t, 1, repo.calls)
			if tc.vetoed {
				require.NotEmpty(t, reason)
			} else {
				require.Same(t, repo.account, latest)
			}
		})
	}
}
