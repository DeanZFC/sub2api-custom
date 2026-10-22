//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestChannelQualitySchedulingDoesNotMutateSharedAccount(t *testing.T) {
	a := Account{Concurrency: 8, Priority: 3}
	require.NoError(t, json.Unmarshal([]byte(`{"passive_quality":[{"scope":"model","model":"A","phase":"limited","concurrency_limit":2,"priority_penalty":5},{"scope":"account","phase":"limited","concurrency_limit":4,"priority_penalty":2}]}`), &a.Extra))
	b := qualitySchedulingAccount(a, "A")
	require.Equal(t, 2, b.Concurrency)
	require.Equal(t, 8, b.Priority)
	require.Equal(t, b, qualitySchedulingAccount(b, "A"))
	other := qualitySchedulingAccount(a, "B")
	require.Equal(t, 4, other.Concurrency)
	require.Equal(t, 5, other.Priority)
	b.ProxyIDs = []int64{1, 2, 3}
	require.Equal(t, 2, b.TotalConcurrency())
	require.Equal(t, 2, b.TotalLoadFactor())
	require.Equal(t, 8, a.Concurrency)
	require.Equal(t, 3, a.Priority)
	require.False(t, a.PassiveQualityBlocked("A"))
}

type qualityLimitRepo struct {
	passiveAdmissionTestRepo
	cap int
	err error
}

func (r *qualityLimitRepo) PassiveQualityConcurrencyLimit(_ context.Context, _ int64, model string) (int, error) {
	r.model = model
	return r.cap, r.err
}

func TestChannelQualityAdmissionUsesLiveCapAndModel(t *testing.T) {
	cache := &qualityLimitCache{}
	svc := NewConcurrencyService(cache)
	gate := &qualityLimitRepo{passiveAdmissionTestRepo: passiveAdmissionTestRepo{allow: true}, cap: 2}
	svc.passiveQualityGate = gate
	ctx := context.WithValue(context.Background(), ctxkey.Model, "A")
	result, err := svc.AcquireAccountSlot(ctx, 62, 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, "A", gate.model)
	result.ReleaseFunc()
	require.Equal(t, 2, cache.limit)
}

type qualityLimitCache struct {
	stubConcurrencyCacheForTest
	limit int
}

func (c *qualityLimitCache) AcquireAccountSlot(_ context.Context, _ int64, limit int, _ string) (bool, error) {
	c.limit = limit
	return true, nil
}
