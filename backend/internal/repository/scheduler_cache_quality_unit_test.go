//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCachePreservesActiveModelProtectionAndRecovery(t *testing.T) {
	for _, holdKey := range []string{"active_test_model_holds", "active_pass_rate_holds", "active_test_all_models_paused"} {
		t.Run(holdKey, func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			account := service.Account{
				ID: 46701, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true,
				Extra: map[string]any{
					holdKey:                    map[string]any{"model-a": map[string]any{"blocked": true}},
					"active_test_model_states": map[string]any{"model-a": map[string]any{"status": "blocked"}},
					"unrelated":                "drop me",
				},
			}
			if holdKey == "active_test_all_models_paused" {
				account.Extra[holdKey] = true
			}
			bucket := service.SchedulerBucket{GroupID: 46701, Platform: account.Platform, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))

			assertSnapshot := func(blocked bool) {
				t.Helper()
				snapshot, hit, err := cache.GetSnapshot(ctx, bucket)
				require.NoError(t, err)
				require.True(t, hit)
				require.Len(t, snapshot, 1)
				require.Equal(t, !blocked, snapshot[0].IsSchedulableForModel("model-a"))
				require.Equal(t, !blocked || holdKey != "active_test_all_models_paused", snapshot[0].IsSchedulableForModel("model-b"))
				require.NotContains(t, snapshot[0].Extra, "active_test_model_states")
				require.NotContains(t, snapshot[0].Extra, "unrelated")
			}
			assertSnapshot(true)

			delete(account.Extra, holdKey)
			require.NoError(t, cache.SetAccount(ctx, &account))
			assertSnapshot(false)
		})
	}
}
