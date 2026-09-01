package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

func TestGetAccountsLoadBatchAggregatesProxyPoolCapacity(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	ctx := context.Background()
	proxyIDs := []int64{201, 202, 203}

	for i := 0; i < 21; i++ {
		acquired, _, err := cache.AcquireAccountProxySlotBalanced(ctx, 8, proxyIDs, 10, fmt.Sprintf("load-test-%d", i))
		require.NoError(t, err)
		require.True(t, acquired)
	}
	loadMap, err := cache.GetAccountsLoadBatch(ctx, []service.AccountWithConcurrency{{
		ID:                           8,
		MaxConcurrency:               10,
		ProxyConcurrencyLimitEnabled: true,
		ProxyPoolIDs:                 proxyIDs,
	}})
	require.NoError(t, err)
	require.Equal(t, 21, loadMap[8].CurrentConcurrency)
	require.Equal(t, 70, loadMap[8].LoadRate, "3 个代理各 10 并发时，总容量应为 30")
}
