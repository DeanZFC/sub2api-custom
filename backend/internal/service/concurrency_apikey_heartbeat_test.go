//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"testing/synctest"
	"time"
)

type heartbeatAPIKeyCache struct {
	stubConcurrencyCacheForTest
	refreshCalls int
	owned        bool
}

func (c *heartbeatAPIKeyCache) APIKeySlotRefreshInterval() time.Duration { return time.Second }
func (c *heartbeatAPIKeyCache) RefreshAPIKeySlot(context.Context, int64, string) (bool, error) {
	c.refreshCalls++
	return c.owned, nil
}
func TestTrackAPIKeySlot_HeartbeatLivesUntilRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := &heartbeatAPIKeyCache{owned: true}
		svc := &ConcurrencyService{cache: cache}
		ctx, cancel := context.WithCancel(context.Background())
		release := svc.TrackAPIKeySlot(ctx, 88)
		synctest.Wait()
		cancel() // disconnected clients may still drain billable upstream output.
		time.Sleep(4 * time.Second)
		synctest.Wait()
		require.GreaterOrEqual(t, cache.refreshCalls, 3)
		release()
		release()
		synctest.Wait()
		n := cache.refreshCalls
		time.Sleep(3 * time.Second)
		synctest.Wait()
		require.Equal(t, n, cache.refreshCalls)
		require.Equal(t, []int64{88}, cache.releasedAPIKeyIDs)
	})
}
func TestTrackAPIKeySlot_HeartbeatStopsAfterLostOwnership(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := &heartbeatAPIKeyCache{}
		release := (&ConcurrencyService{cache: cache}).TrackAPIKeySlot(context.Background(), 88)
		defer release()
		synctest.Wait()
		time.Sleep(4 * time.Second)
		synctest.Wait()
		require.Equal(t, 1, cache.refreshCalls)
	})
}
