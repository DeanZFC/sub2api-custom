//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCacheDoesNotReuseLegacyAccountPayloads(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	bucket := service.SchedulerBucket{GroupID: 9, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	legacy := `{"ID":42,"AccountScope":"shared","Status":"active","Schedulable":true}`
	require.NoError(t, cache.rdb.Set(ctx, "sched:acc:42", legacy, 0).Err())
	require.NoError(t, cache.rdb.Set(ctx, "sched:meta:42", legacy, 0).Err())
	require.NoError(t, cache.rdb.Set(ctx, schedulerBucketKey(schedulerReadyPrefix, bucket), "1", 0).Err())
	require.NoError(t, cache.rdb.Set(ctx, schedulerBucketKey(schedulerActivePrefix, bucket), "1", 0).Err())
	require.NoError(t, cache.rdb.Set(ctx, schedulerBucketKey(schedulerVersionPrefix, bucket), "1", 0).Err())
	require.NoError(t, cache.rdb.ZAdd(ctx, schedulerSnapshotKey(bucket, "1"), redis.Z{Score: 0, Member: "42"}).Err())

	account, err := cache.GetAccount(ctx, 42)
	require.NoError(t, err)
	require.Nil(t, account)
	accounts, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.False(t, hit)
	require.Empty(t, accounts)

	current := service.Account{ID: 43, Platform: service.PlatformOpenAI, Status: service.StatusActive, Schedulable: true}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{current}))
	accounts, hit, err = cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, accounts, 1)
	require.Equal(t, current.ID, accounts[0].ID)
}
