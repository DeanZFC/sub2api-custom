package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestOpsAccountRequestStatsBatchCompleteHourAndMissingAccounts(t *testing.T) {
	before := time.Now().UTC()
	zero := 0.0
	repo := &opsRepoMock{GetAccountRequestStatsFn: func(_ context.Context, ids []int64, start, end time.Time) (map[string]*OpsAccountRequestStats, error) {
		require.Equal(t, []int64{2, 1}, ids, "deduplicate IDs while retaining order")
		require.Equal(t, time.Hour, end.Sub(start))
		require.False(t, end.Before(before))
		require.False(t, end.After(time.Now().UTC()))
		return map[string]*OpsAccountRequestStats{
			"2":  {CacheRate: &zero, AvgFirstTokenMs: &zero, AvgDurationMs: &zero, CacheSamples: 25, FirstTokenSamples: 25, DurationSamples: 25},
			"99": {},
		}, nil
	}}
	svc := &OpsService{opsRepo: repo}
	result, err := svc.GetAccountRequestStatsBatch(context.Background(), []int64{2, 1, 2})
	require.NoError(t, err)
	require.Len(t, result.Stats, 2)
	require.EqualValues(t, 25, result.Stats["2"].CacheSamples)
	require.NotNil(t, result.Stats["2"].CacheRate)
	require.Zero(t, *result.Stats["2"].CacheRate)
	require.Nil(t, result.Stats["1"].CacheRate)
	require.Nil(t, result.Stats["1"].AvgFirstTokenMs)
	require.Nil(t, result.Stats["1"].AvgDurationMs)
	require.Equal(t, result.Stats["2"].WindowStart, result.Stats["1"].WindowStart)
	require.Equal(t, result.Stats["2"].WindowEnd, result.Stats["1"].WindowEnd)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"cache_rate":null`)
	require.Contains(t, string(encoded), `"cache_rate":0`)
}

func TestOpsAccountRequestStatsBatchValidation(t *testing.T) {
	for _, ids := range [][]int64{nil, {}, {0}, {-1}, make([]int64, 101)} {
		svc := &OpsService{opsRepo: &opsRepoMock{GetAccountRequestStatsFn: func(context.Context, []int64, time.Time, time.Time) (map[string]*OpsAccountRequestStats, error) {
			t.Fatal("invalid IDs must not query the repository")
			return nil, nil
		}}}
		_, err := svc.GetAccountRequestStatsBatch(context.Background(), ids)
		require.Error(t, err)
		require.Equal(t, 400, infraerrors.Code(err))
	}
	_, err := (&OpsService{}).GetAccountRequestStatsBatch(context.Background(), []int64{1})
	require.Equal(t, 503, infraerrors.Code(err))
}

func TestOpsAccountRequestStatsBatchPropagatesDatabaseFailure(t *testing.T) {
	want := errors.New("query failed")
	svc := &OpsService{opsRepo: &opsRepoMock{GetAccountRequestStatsFn: func(context.Context, []int64, time.Time, time.Time) (map[string]*OpsAccountRequestStats, error) {
		return nil, want
	}}}
	_, err := svc.GetAccountRequestStatsBatch(context.Background(), []int64{1})
	require.ErrorIs(t, err, want)
}
