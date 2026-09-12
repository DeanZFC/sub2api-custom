package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type sharedPoolScheduleAccountRepo struct {
	AccountRepository
	byGroup    []Account
	byPlatform []Account
	groupCalls int
	platCalls  int
}

func (r *sharedPoolScheduleAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, _ string) ([]Account, error) {
	r.groupCalls++
	return r.byGroup, nil
}

func (r *sharedPoolScheduleAccountRepo) ListSchedulableByPlatform(_ context.Context, _ string) ([]Account, error) {
	r.platCalls++
	return r.byPlatform, nil
}

func TestSimpleModeSharedKeyUsesSharedGroupAccounts(t *testing.T) {
	repo := &sharedPoolScheduleAccountRepo{
		byGroup:    []Account{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, AccountScope: "shared"}},
		byPlatform: []Account{{ID: 99, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, AccountScope: "system"}},
	}
	svc := NewSchedulerSnapshotService(nil, nil, repo, nil, &config.Config{
		RunMode: config.RunModeSimple,
		Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true}},
	})
	groupID := int64(7)
	ctx := WithSharedKeySchedule(context.Background(), SharedKeySchedule{Priority: SharedKeyPriorityOrder, AccountIDs: []int64{11}})

	accounts, _, err := svc.ListSchedulableAccounts(ctx, &groupID, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Equal(t, 1, repo.groupCalls)
	require.Zero(t, repo.platCalls)
	require.Len(t, accounts, 1)
	require.EqualValues(t, 11, accounts[0].ID)
}

func TestSimpleModeNormalKeyStillUsesSystemAccounts(t *testing.T) {
	repo := &sharedPoolScheduleAccountRepo{
		byGroup:    []Account{{ID: 11, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, AccountScope: "shared"}},
		byPlatform: []Account{{ID: 99, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, AccountScope: "system"}},
	}
	svc := NewSchedulerSnapshotService(nil, nil, repo, nil, &config.Config{
		RunMode: config.RunModeSimple,
		Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{DbFallbackEnabled: true}},
	})
	groupID := int64(7)

	accounts, _, err := svc.ListSchedulableAccounts(context.Background(), &groupID, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Zero(t, repo.groupCalls)
	require.Equal(t, 1, repo.platCalls)
	require.Len(t, accounts, 1)
	require.EqualValues(t, 99, accounts[0].ID)
}
