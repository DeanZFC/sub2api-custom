package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestOpenAISchedulerTopKExcludesActiveModelProtection(t *testing.T) {
	for _, holdKey := range []string{"active_test_model_holds", "active_pass_rate_holds", "active_test_all_models_paused"} {
		t.Run(holdKey, func(t *testing.T) {
			groupID := int64(111)
			accounts := []Account{
				{
					ID: 37101, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1,
					GroupIDs: []int64{groupID}, Priority: 0,
					Extra: map[string]any{holdKey: map[string]any{"public-a": true}},
				},
				{
					ID: 37102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, Schedulable: true, Concurrency: 1,
					GroupIDs: []int64{groupID}, Priority: 5,
				},
			}
			if holdKey == "active_test_all_models_paused" {
				accounts[0].Extra[holdKey] = true
			}
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.LBTopK = 1
			cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
			var acquired []int64
			concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired})
			svc := &OpenAIGatewayService{
				accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
				cache:       &schedulerTestGatewayCache{}, cfg: cfg,
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
				concurrencyService: concurrency,
			}

			selectModel := func(publicModel string, expectedID int64, candidateCount int) {
				t.Helper()
				// The provider alias must not change which public model is held.
				ctx := context.WithValue(context.Background(), ctxkey.RequestedPublicModel, publicModel)
				selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "provider-alias", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.NotNil(t, selection.Account)
				require.Equal(t, expectedID, selection.Account.ID)
				require.Equal(t, candidateCount, decision.CandidateCount)
				require.Equal(t, []int64{expectedID}, acquired, "held accounts must not consume TopK or probe slots")
				selection.ReleaseFunc()
				acquired = nil
			}
			selectModel("public-a", accounts[1].ID, 1)
			if holdKey == "active_test_all_models_paused" {
				selectModel("public-b", accounts[1].ID, 1)
			} else {
				selectModel("public-b", accounts[0].ID, 2)
			}
			delete(accounts[0].Extra, holdKey)
			selectModel("public-a", accounts[0].ID, 2)
		})
	}
}
