package qqbot

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRankingConfigValidation(t *testing.T) {
	cfg := defaultRankingConfig()
	require.True(t, cfg.Enabled)
	require.False(t, cfg.RewardsEnabled)
	require.NoError(t, validateRankingConfig(cfg))
	cfg.RewardsEnabled = true
	require.Error(t, validateRankingConfig(cfg))
	cfg.TokenRewards = []RankingPrize{{Rank: 10, Reward: GrowthReward{Type: "balance", Amount: 0.00000001}}}
	require.NoError(t, validateRankingConfig(cfg))
	for _, rank := range []int{0, -1, 11} {
		cfg.TokenRewards[0].Rank = rank
		require.Error(t, validateRankingConfig(cfg))
	}
	cfg.TokenRewards[0].Rank = 1
	cfg.TokenRewards = append(cfg.TokenRewards, cfg.TokenRewards[0])
	require.Error(t, validateRankingConfig(cfg))
	cfg.TokenRewards = cfg.TokenRewards[:1]
	cfg.TokenRewards[0].Reward = GrowthReward{Type: "physical", Quantity: 1}
	require.Error(t, validateRankingConfig(cfg))
	cfg.TokenRewards[0].Reward.PrizeName = "Prize"
	require.NoError(t, validateRankingConfig(cfg))
	cfg.TokenRewards[0].Reward = GrowthReward{Type: "balance", Amount: 1001}
	require.Error(t, validateRankingConfig(cfg))
	cfg.RewardsEnabled = false
	cfg.TokenRewards = nil
	cfg.AmountRewards = nil
	require.NoError(t, validateRankingConfig(cfg))
	normalizeRankingPrizes(&cfg)
	require.NotNil(t, cfg.TokenRewards)
	require.NotNil(t, cfg.AmountRewards)
}

func TestRankingAliasIsPrivateAndChangesEachDay(t *testing.T) {
	c := Config{AppID: "test-app", AppSecret: "test-only-secret"}
	alias := rankingAlias(c, "2026-10-21", 123456789)
	require.Regexp(t, `^用户 \*{4}[A-F0-9]{8}$`, alias)
	require.NotContains(t, alias, "123456789")
	require.Equal(t, alias, rankingAlias(c, "2026-10-21", 123456789))
	require.NotEqual(t, alias, rankingAlias(c, "2026-10-22", 123456789))
	require.NotEqual(t, alias, rankingAlias(c, "2026-10-21", 123456790))
	c.AppSecret = "different-test-secret"
	require.NotEqual(t, alias, rankingAlias(c, "2026-10-21", 123456789))
	at, err := time.Parse(time.RFC3339, "2026-10-21T16:00:00Z")
	require.NoError(t, err)
	require.Equal(t, "2026-10-22T00:00:00+08:00", beijingDayStart(at).Format(time.RFC3339))
	require.True(t, isBuiltinCommand("今日排行榜"))
	require.True(t, isBuiltinCommand("今日排行版"))
	require.False(t, isBuiltinCommand("今日Token排行"))
	require.Error(t, validateCustomCommands([]CustomCommand{{Command: "/今日排行榜", Reply: "override", Enabled: true}}))
}
