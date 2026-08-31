package service

import (
	"context"
	"sync"
	"testing"
)

type proxyPoolConcurrencyTestCache struct {
	ConcurrencyCache
	mu     sync.Mutex
	counts map[int64]int
}

func (c *proxyPoolConcurrencyTestCache) AcquireAccountProxySlot(_ context.Context, accountID, proxyID int64, maxConcurrency int, requestID string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = make(map[int64]int)
	}
	if c.counts[accountID<<32|proxyID] >= maxConcurrency {
		return false, nil
	}
	c.counts[accountID<<32|proxyID]++
	return true, nil
}

func (c *proxyPoolConcurrencyTestCache) AcquireAccountProxySlotBalanced(_ context.Context, accountID int64, proxyIDs []int64, maxConcurrency int, requestID string) (bool, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(proxyIDs) == 0 {
		return false, 0, nil
	}
	if c.counts == nil {
		c.counts = make(map[int64]int)
	}
	start := 0
	for i := 0; i < len(requestID); i++ {
		start = (start*31 + int(requestID[i])) % len(proxyIDs)
	}
	selected := int64(0)
	selectedCount := maxConcurrency
	for step := 0; step < len(proxyIDs); step++ {
		proxyID := proxyIDs[(start+step)%len(proxyIDs)]
		count := c.counts[accountID<<32|proxyID]
		if count < selectedCount {
			selected = proxyID
			selectedCount = count
		}
	}
	if selected == 0 {
		return false, 0, nil
	}
	c.counts[accountID<<32|selected]++
	return true, selected, nil
}

func (c *proxyPoolConcurrencyTestCache) ReleaseAccountProxySlot(_ context.Context, accountID, proxyID int64, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := accountID<<32 | proxyID
	if c.counts[key] > 0 {
		c.counts[key]--
	}
	return nil
}

func (c *proxyPoolConcurrencyTestCache) GetAccountProxyConcurrency(_ context.Context, accountID, proxyID int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[accountID<<32|proxyID], nil
}

func TestAcquireAccountProxySlotDistributesAcrossLeastLoadedProxies(t *testing.T) {
	cache := &proxyPoolConcurrencyTestCache{counts: make(map[int64]int)}
	service := NewConcurrencyService(cache)
	proxyIDs := []int64{101, 102, 103}

	selected := make(map[int64]int)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 21; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, proxyID, err := service.AcquireAccountProxySlot(context.Background(), 7, proxyIDs, 10)
			if err != nil || result == nil || !result.Acquired {
				t.Errorf("unexpected acquire result: acquired=%v proxy=%d err=%v", result != nil && result.Acquired, proxyID, err)
				return
			}
			mu.Lock()
			selected[proxyID]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, proxyID := range proxyIDs {
		if selected[proxyID] != 7 {
			t.Fatalf("proxy %d selected %d times, want 7 (all selections: %#v)", proxyID, selected[proxyID], selected)
		}
	}

	for i := 0; i < 9; i++ {
		result, proxyID, err := service.AcquireAccountProxySlot(context.Background(), 7, proxyIDs, 10)
		if err != nil || result == nil || !result.Acquired || proxyID <= 0 {
			t.Fatalf("fill acquire #%d failed: acquired=%v proxy=%d err=%v", i+1, result != nil && result.Acquired, proxyID, err)
		}
	}
	result, proxyID, err := service.AcquireAccountProxySlot(context.Background(), 7, proxyIDs, 10)
	if err != nil {
		t.Fatalf("acquire after capacity: %v", err)
	}
	if result == nil || result.Acquired || proxyID != 0 {
		t.Fatalf("expected pool to be full, got acquired=%v proxy=%d", result != nil && result.Acquired, proxyID)
	}
}

func TestAcquireAccountProxySlotFallsBackToAccountBucketWithoutExtension(t *testing.T) {
	cache := &proxyPoolLegacyConcurrencyTestCache{}
	service := NewConcurrencyService(cache)

	result, proxyID, err := service.AcquireAccountProxySlot(context.Background(), 9, []int64{0, 201, 201}, 4)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if result == nil || !result.Acquired || proxyID != 201 {
		t.Fatalf("unexpected result: acquired=%v proxy=%d", result != nil && result.Acquired, proxyID)
	}
}

type proxyPoolLegacyConcurrencyTestCache struct{ ConcurrencyCache }

func (proxyPoolLegacyConcurrencyTestCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
