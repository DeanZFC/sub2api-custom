package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAISchedulerSkipsAdmissionDeniedAccounts(t *testing.T) {
	for _, scenario := range []string{"healthy backup", "saturated backup", "all denied"} {
		t.Run(scenario, func(t *testing.T) {
			groupID := int64(51)
			primary := &Account{ID: 2280, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 50, GroupIDs: []int64{groupID}}
			backup := *primary
			backup.ID = 2283
			acquired, released := []int64{}, []int64{}
			cache := schedulerTestConcurrencyCache{acquiredIDs: &acquired, releasedIDs: &released}
			if scenario == "saturated backup" {
				cache.acquireResults = map[int64]bool{backup.ID: false}
			}
			concurrency := NewConcurrencyService(cache)
			concurrency.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(_ context.Context, id int64) (bool, error) {
				return id == backup.ID && scenario != "all denied", nil
			}))
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*primary, backup}},
				cfg:         &config.Config{RunMode: config.RunModeStandard}, concurrencyService: concurrency,
			}
			scheduler := &defaultOpenAIAccountScheduler{service: svc}
			order := []openAIAccountCandidateScore{
				{account: primary, loadInfo: &AccountLoadInfo{AccountID: primary.ID}},
				{account: &backup, loadInfo: &AccountLoadInfo{AccountID: backup.ID}},
			}
			req := OpenAIAccountScheduleRequest{GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "gpt-image-2.5"}
			budget := newOpenAISelectionProbeBudget()
			selection, _, err := scheduler.tryAcquireOpenAISelectionOrderWithBudget(context.Background(), req, order, budget)
			require.NoError(t, err)
			require.Equal(t, []int64{primary.ID, backup.ID}, acquired)
			if scenario == "healthy backup" {
				require.NotNil(t, selection)
				require.Equal(t, backup.ID, selection.Account.ID)
				require.Equal(t, []int64{primary.ID}, released)
				selection.ReleaseFunc()
				return
			}
			require.Nil(t, selection)
			selection, _, _, _, err = scheduler.finishLoadBalanceSelectionFallback(context.Background(), req,
				openAIAccountLoadSelectionAttempt{selectionOrder: order, candidateCount: 2}, budget, openAISelectionFilterStats{})
			if scenario == "all denied" {
				require.ErrorIs(t, err, ErrAccountAdmissionDenied)
				require.Nil(t, selection)
				require.Equal(t, []int64{primary.ID, backup.ID}, released)
			} else {
				require.NoError(t, err)
				require.NotNil(t, selection.WaitPlan)
				require.Equal(t, backup.ID, selection.WaitPlan.AccountID)
			}
		})
	}
}
