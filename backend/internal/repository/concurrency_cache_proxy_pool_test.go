package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAcquireAccountProxySlotBalancedDistributesConcurrentRequests(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()
	proxyIDs := []int64{101, 102, 103}

	var wg sync.WaitGroup
	selected := make(chan int64, 21)
	for i := 0; i < 21; i++ {
		requestID := fmt.Sprintf("proxy-pool-test-%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			acquired, proxyID, err := cache.AcquireAccountProxySlotBalanced(ctx, 7, proxyIDs, 10, requestID)
			if err != nil || !acquired {
				t.Errorf("acquire failed: acquired=%v proxy=%d err=%v", acquired, proxyID, err)
				return
			}
			selected <- proxyID
		}()
	}
	wg.Wait()
	close(selected)

	counts := map[int64]int{}
	for proxyID := range selected {
		counts[proxyID]++
	}
	for _, proxyID := range proxyIDs {
		current, err := cache.GetAccountProxyConcurrency(ctx, 7, proxyID)
		require.NoError(t, err)
		require.Equal(t, 7, current)
		require.Equal(t, 7, counts[proxyID])
	}
}
