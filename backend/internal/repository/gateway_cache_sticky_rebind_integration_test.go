//go:build integration

package repository

import (
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func (s *GatewayCacheSuite) TestStickyRebindCASAcrossInstances() {
	first := &gatewayCache{rdb: s.rdb}
	second := &gatewayCache{rdb: s.rdb}
	const groupID int64 = 949002
	swap := service.SessionAccountBindingSwap{
		SessionHash: "openai:cas-real-redis", LegacySessionHash: "openai:cas-real-redis-old",
		ExpectedAccountID: 101, AccountID: 202, TTL: time.Hour, LegacyTTL: 10 * time.Minute,
		ReadLegacy: true, WriteLegacy: true,
	}
	require.NoError(s.T(), first.SetSessionAccountID(s.ctx, groupID, swap.SessionHash, 101, time.Minute))
	require.NoError(s.T(), first.SetSessionAccountID(s.ctx, groupID, swap.LegacySessionHash, 101, time.Minute))
	type outcome struct {
		account int64
		applied bool
		err     error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i, cache := range []*gatewayCache{first, second} {
		candidate := swap
		candidate.AccountID += int64(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			applied, err := cache.CompareAndSwapSessionAccountID(s.ctx, groupID, candidate)
			results <- outcome{candidate.AccountID, applied, err}
		}()
	}
	wg.Wait()
	close(results)
	var winner int64
	wins := 0
	for result := range results {
		require.NoError(s.T(), result.err)
		if result.applied {
			winner = result.account
			wins++
		}
	}
	require.Equal(s.T(), 1, wins)
	for _, key := range []string{swap.SessionHash, swap.LegacySessionHash} {
		account, err := first.GetSessionAccountID(s.ctx, groupID, key)
		require.NoError(s.T(), err)
		require.Equal(s.T(), winner, account)
	}
	stale := swap
	stale.AccountID = 101
	applied, err := second.CompareAndSwapSessionAccountID(s.ctx, groupID, stale)
	require.NoError(s.T(), err)
	require.False(s.T(), applied, "an old waiter cannot restore the previous account")
	cleanup := swap
	cleanup.ExpectedAccountID, cleanup.AccountID = winner, winner
	cleanup.WriteLegacy = false
	applied, err = first.CompareAndSwapSessionAccountID(s.ctx, groupID, cleanup)
	require.NoError(s.T(), err)
	require.True(s.T(), applied)
	_, err = first.GetSessionAccountID(s.ctx, groupID, swap.LegacySessionHash)
	require.ErrorIs(s.T(), err, service.ErrStickySessionNotFound)
}
