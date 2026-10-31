//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func unavailableFallbackFixture() (*APIKey, *groupRepoStubForAdmin) {
	primaryID, poolID, keyID := int64(801), int64(802), int64(803)
	primary := &Group{ID: primaryID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.7, FallbackGroupIDOnUnavailable: &poolID, SubscriptionType: SubscriptionTypeSubscription}
	pool := &Group{ID: poolID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 9, SubscriptionType: SubscriptionTypeStandard}
	keyGroup := &Group{ID: keyID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 2.5, SubscriptionType: SubscriptionTypeStandard}
	return &APIKey{ID: 811, UserID: 812, User: &User{ID: 812, Status: StatusActive}, GroupID: &primaryID, Group: primary, FallbackGroupID: &keyID, FallbackGroup: keyGroup, Status: StatusActive},
		&groupRepoStubForAdmin{getByIDByID: map[int64]*Group{primaryID: primary, poolID: pool, keyID: keyGroup}}
}

func TestGroupUnavailableFallback_OrderAndBillingIdentity(t *testing.T) {
	for _, healthy := range []int64{801, 802, 803} {
		t.Run(string(rune('A'+healthy-801)), func(t *testing.T) {
			key, groups := unavailableFallbackFixture()
			ctx := WithAPIKeyGroupFallbackRouting(context.WithValue(context.Background(), ctxkey.Group, cloneUsageGroup(key.Group)), key)
			attempts := []int64{}
			selection, err := selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
				func(selectedCtx context.Context, id *int64) (*AccountSelectionResult, error) {
					attempts = append(attempts, *id)
					if *id != healthy {
						return nil, ErrNoAvailableAccounts
					}
					if *id == 802 {
						require.Equal(t, int64(801), accountFallbackBillingGroup(selectedCtx).ID)
					}
					return &AccountSelectionResult{Account: &Account{ID: *id + 100}}, nil
				}, markGroupFallbackSelection)
			require.NoError(t, err)
			require.Equal(t, []int64{801, 802, 803}[:healthy-800], attempts)
			wantBilling := int64(801)
			if healthy == 803 {
				wantBilling = 803
			}
			require.Equal(t, wantBilling, *key.GroupID)
			require.Equal(t, wantBilling, key.Group.ID)
			require.Equal(t, wantBilling, ctx.Value(ctxkey.Group).(*Group).ID)
			require.Equal(t, healthy == 802, selection.UsedGroupFallback)
			require.Equal(t, healthy == 803, selection.UsedAPIKeyFallback)
		})
	}
}

func TestGroupUnavailableFallback_DoesNotSkipUnknownOrOwnerBoundStates(t *testing.T) {
	for _, strict := range []bool{false, true} {
		for _, cause := range []error{errors.New("database unavailable"), ErrStrictFailoverUnconfirmed, ErrStrictFailoverOwnerBound, context.Canceled, &ownerBoundQualityError{ErrAccountQualityHeld}} {
			key, groups := unavailableFallbackFixture()
			key.Group.StrictFailover.Enabled = strict
			calls := 0
			ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
			_, err := selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
				func(context.Context, *int64) (*Account, error) { calls++; return nil, cause }, nil)
			require.Same(t, cause, err)
			require.Equal(t, 1, calls)
		}
	}
	for _, failure := range []error{ErrNoAvailableAccounts, errStrictFailoverAllUnavailable} {
		key, groups := unavailableFallbackFixture()
		key.Group.StrictFailover.Enabled = true
		ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
		calls := 0
		_, err := selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
			func(_ context.Context, id *int64) (*Account, error) {
				calls++
				if *id == 801 {
					return nil, failure
				}
				return &Account{ID: 1}, nil
			}, nil)
		if failure == ErrNoAvailableAccounts {
			require.Error(t, err)
			require.Equal(t, 1, calls)
		} else {
			require.NoError(t, err)
			require.Equal(t, 2, calls)
		}
	}
}

func TestGroupUnavailableFallback_TargetStrictFailureCannotReachAPIKeyFallback(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	groups.getByIDByID[802].StrictFailover.Enabled = true
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
	attempts := []int64{}
	_, err := selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
		func(_ context.Context, id *int64) (*Account, error) {
			attempts = append(attempts, *id)
			return nil, ErrNoAvailableAccounts
		}, nil)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{801, 802}, attempts)
}

func TestGroupUnavailableFallback_APIKeyRetryDoesNotExpandAnotherChain(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	key.FallbackGroup.FallbackGroupIDOnUnavailable = key.Group.FallbackGroupIDOnUnavailable
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
	_, err := selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
		func(_ context.Context, id *int64) (*Account, error) {
			if *id == 803 {
				return &Account{ID: 901}, nil
			}
			return nil, ErrNoAvailableAccounts
		}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(803), *key.GroupID)
	attempts := []int64{}
	_, err = selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
		func(_ context.Context, id *int64) (*Account, error) {
			attempts = append(attempts, *id)
			return nil, ErrNoAvailableAccounts
		}, nil)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Equal(t, []int64{803}, attempts)
}

func TestGroupUnavailableFallback_SchedulerAndPrimaryRecovery(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	// Group fallback must also work for keys with no personal fallback setting.
	key.FallbackGroupID, key.FallbackGroup = nil, nil
	primaryID := *key.GroupID
	accounts := []Account{{ID: 901, GroupIDs: []int64{802}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1}}
	svc := newAPIKeyFallbackOpenAIService(accounts)
	svc.schedulerSnapshot = NewSchedulerSnapshotService(nil, nil, svc.accountRepo, groups, nil)
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
	selected, _, err := svc.SelectAccountWithScheduler(ctx, key.GroupID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(901), selected.Account.ID)
	require.True(t, selected.UsedGroupFallback)
	require.Equal(t, primaryID, *key.GroupID)
	// A fresh request gets a fresh key and starts at the restored primary.
	accounts = append(accounts, Account{ID: 902, GroupIDs: []int64{801}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1})
	svc = newAPIKeyFallbackOpenAIService(accounts)
	svc.schedulerSnapshot = NewSchedulerSnapshotService(nil, nil, svc.accountRepo, groups, nil)
	key, _ = unavailableFallbackFixture()
	key.FallbackGroupID, key.FallbackGroup = nil, nil
	selected, _, err = svc.SelectAccountWithScheduler(WithAPIKeyGroupFallbackRouting(context.Background(), key), key.GroupID, "", "", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(902), selected.Account.ID)
	require.False(t, selected.UsedGroupFallback)
}

func TestGroupUnavailableFallback_OriginalSubscriptionAndPrice(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
	account, err := selectAccountWithAPIKeyGroupFallback(ctx, key.GroupID, "gpt-5.1", func(_ context.Context, id *int64) (*Account, error) {
		if *id == 801 {
			return nil, ErrNoAvailableAccounts
		}
		return &Account{ID: 901, Type: AccountTypeAPIKey}, nil
	}, groups.GetByIDLite)
	require.NoError(t, err)
	// Detach the same immutable snapshot used by asynchronous usage workers.
	ctx = WithAPIKeyUsageSnapshot(ctx, key)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, subRepo, nil)
	usage := OpenAIUsage{InputTokens: 1000, OutputTokens: 100}
	subscription := &UserSubscription{ID: 821, UserID: key.UserID, GroupID: 801}
	err = svc.RecordUsage(ctx, &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{RequestID: "group-fallback-original-billing", Model: "gpt-5.1", Usage: usage, Duration: time.Second}, APIKey: key, User: key.User, Account: account, Subscription: subscription})
	require.NoError(t, err)
	require.Equal(t, int64(801), *usageRepo.lastLog.GroupID)
	require.Equal(t, 0.7, usageRepo.lastLog.RateMultiplier)
	require.Equal(t, subscription.ID, *billingRepo.lastCmd.SubscriptionID)
	require.Zero(t, subRepo.getActiveCalls, "the provider pool needs no user subscription")
	require.Zero(t, billingRepo.lastCmd.BalanceCost)
	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, 0.7)
	require.InDelta(t, expected.ActualCost, billingRepo.lastCmd.SubscriptionCost, 1e-12)
}

func TestGroupUnavailableFallback_ConfigurationAndAuthRoundtrip(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	admin := &adminServiceImpl{groupRepo: groups}
	require.NoError(t, admin.validateUnavailableFallback(context.Background(), 801, PlatformOpenAI, key.Group.FallbackGroupIDOnUnavailable))
	for _, invalidID := range []int64{801, 999} {
		require.Error(t, admin.validateUnavailableFallback(context.Background(), 801, PlatformOpenAI, &invalidID))
	}
	groups.getByIDByID[802].FallbackGroupIDOnUnavailable = key.GroupID
	require.Error(t, admin.validateUnavailableFallback(context.Background(), 801, PlatformOpenAI, key.Group.FallbackGroupIDOnUnavailable))
	groups.getByIDByID[802].FallbackGroupIDOnUnavailable = nil
	groups.getByIDByID[802].Platform = PlatformAnthropic
	require.Error(t, admin.validateUnavailableFallback(context.Background(), 801, PlatformOpenAI, key.Group.FallbackGroupIDOnUnavailable))
	svc := &APIKeyService{}
	key.FallbackGroupID = nil
	key.FallbackGroup = nil
	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(context.Background(), key)})
	require.NoError(t, err)
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))
	actual, used, err := svc.applyAuthCacheEntry("key", &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.Equal(t, key.Group.FallbackGroupIDOnUnavailable, actual.Group.FallbackGroupIDOnUnavailable)
}

func TestGroupUnavailableFallback_AdminSaveAndClear(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	admin := &adminServiceImpl{groupRepo: groups}
	created, err := admin.CreateGroup(context.Background(), &CreateGroupInput{Name: "primary", Platform: PlatformOpenAI, RateMultiplier: 1, FallbackGroupIDOnUnavailable: key.Group.FallbackGroupIDOnUnavailable})
	require.NoError(t, err)
	require.Equal(t, int64(802), *created.FallbackGroupIDOnUnavailable)
	updated, err := admin.UpdateGroup(context.Background(), 801, &UpdateGroupInput{Description: ptrString("retain fallback")})
	require.NoError(t, err)
	require.Equal(t, int64(802), *updated.FallbackGroupIDOnUnavailable)
	zero := int64(0)
	updated, err = admin.UpdateGroup(context.Background(), 801, &UpdateGroupInput{FallbackGroupIDOnUnavailable: &zero})
	require.NoError(t, err)
	require.Nil(t, updated.FallbackGroupIDOnUnavailable)
}

func TestGroupUnavailableFallback_ProfitUsesActualBillingIdentity(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	groups.getByIDByID[802].ProfitControlEnabled = true
	groups.getByIDByID[802].ProfitMinMargin = 0.1
	groups.getByIDByID[803].ProfitControlEnabled = true
	groups.getByIDByID[803].ProfitMinMargin = 0.1
	svc := &OpenAIGatewayService{schedulerSnapshot: NewSchedulerSnapshotService(nil, nil, nil, groups, nil)}
	ctx := WithAPIKeyGroupFallbackRouting(context.WithValue(context.Background(), ctxkey.Group, key.Group), key)
	_, err := selectWithGroupFallback(ctx, key.GroupID, "gpt-5.1", groups.GetByIDLite,
		func(selectedCtx context.Context, id *int64) (*Account, error) {
			switch *id {
			case 801:
				return nil, ErrNoAvailableAccounts
			case 802:
				require.InDelta(t, 0.7*0.9, svc.resolveOpenAIProfitControlGate(selectedCtx, id).threshold, 1e-12)
				return nil, ErrNoAvailableAccounts
			default:
				require.InDelta(t, 2.5*0.9, svc.resolveOpenAIProfitControlGate(selectedCtx, id).threshold, 1e-12)
				return &Account{ID: 901}, nil
			}
		}, nil)
	require.NoError(t, err)
}

func TestGroupUnavailableFallback_StrictWebSocketRechecksPrimary(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	key.Group.StrictFailover = StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{901}}}
	groups.getByIDByID[802].StrictFailover = StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{902}}}
	repo := &strictAccountRepo{}
	repo.accounts = []Account{
		{ID: 901, GroupIDs: []int64{801}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: false},
		{ID: 902, GroupIDs: []int64{802}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true},
	}
	svc := &OpenAIGatewayService{accountRepo: repo, schedulerSnapshot: NewSchedulerSnapshotService(nil, nil, repo, groups, nil)}
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
	require.NoError(t, svc.ValidateStrictFailoverBoundAccount(ctx, key.GroupID, &repo.accounts[1], "gpt-5.1", "", OpenAIUpstreamTransportAny))
	repo.accounts[0].Schedulable = true
	require.ErrorIs(t, svc.ValidateStrictFailoverBoundAccount(ctx, key.GroupID, &repo.accounts[1], "gpt-5.1", "", OpenAIUpstreamTransportAny), ErrStrictFailoverOwnerBound)
}

func TestGroupUnavailableFallback_BatchImageRetainsSettlementGroup(t *testing.T) {
	key, groups := unavailableFallbackFixture()
	for _, group := range groups.getByIDByID {
		group.Platform = PlatformGemini
		group.AllowBatchImageGeneration = true
	}
	svc, repo, _, _, _ := newTestBatchImagePublicService(true)
	svc.GroupRepo = groups
	account := testBatchImageAccount(901, AccountTypeAPIKey)
	account.GroupIDs = []int64{802}
	svc.AccountRepo = schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
	owner := testBatchImageOwner()
	owner.GroupID = key.GroupID
	owner.FallbackGroupID = key.FallbackGroupID
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
	batch, err := svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")
	require.NoError(t, err)
	job := repo.jobs[batch.ID]
	require.Equal(t, int64(901), *job.AccountID)
	require.Equal(t, int64(801), *job.RoutedGroupID, "deferred settlement must keep the source billing identity")
	require.Equal(t, 0.7, job.GroupRateMultiplier)
	require.Equal(t, int64(801), *key.GroupID)
}
