package qqbot

import (
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestCheckinValidationAndRandomPrecision(t *testing.T) {
	for _, c := range []CheckinConfig{
		{MinAmount: -1, MaxAmount: 1}, {MinAmount: 2, MaxAmount: 1}, {MaxAmount: 1001},
		{MaxAmount: math.NaN()}, {MaxAmount: math.Inf(1)}, {MaxAmount: .000000001},
	} {
		require.Error(t, validateCheckin(c))
	}
	for _, c := range []CheckinConfig{
		{MinAmount: 0, MaxAmount: 0}, {MinAmount: .12345678, MaxAmount: .12345678},
		{MinAmount: .00000001, MaxAmount: .00000002}, {MinAmount: 999, MaxAmount: 1000},
	} {
		for range 100 {
			amount, err := randomCheckinAmount(c)
			require.NoError(t, err)
			require.GreaterOrEqual(t, amount, c.MinAmount)
			require.LessOrEqual(t, amount, c.MaxAmount)
			require.InDelta(t, math.Round(amount*1e8), amount*1e8, .001)
		}
	}
}
func TestLotteryProbabilityDistributionAndValidation(t *testing.T) {
	c := defaultLotteryConfig()
	require.NoError(t, validateLottery(c))
	counts := map[string]int{}
	for i := range int64(10000) {
		counts[lotteryPrizeAt(c, i).Name]++
	}
	require.Equal(t, 1000, counts[c.Prizes[0].Name])
	require.Equal(t, 9000, counts[c.Prizes[1].Name])
	for _, mutate := range []func(*LotteryConfig){
		func(c *LotteryConfig) { c.RechargeStep = 0 }, func(c *LotteryConfig) { c.RechargeStep = .001 },
		func(c *LotteryConfig) { c.RechargeStep = math.NaN() }, func(c *LotteryConfig) { c.ChancesPerStep = 0 },
		func(c *LotteryConfig) { c.ChancesPerStep = 1001 }, func(c *LotteryConfig) { c.Title = " " },
		func(c *LotteryConfig) { c.Prizes = nil }, func(c *LotteryConfig) { c.Prizes[0].Amount = -1 },
		func(c *LotteryConfig) { c.Prizes[0].ProbabilityBPS = 0 }, func(c *LotteryConfig) { c.Prizes[0].ProbabilityBPS = 1001 },
	} {
		c := defaultLotteryConfig()
		mutate(&c)
		require.Error(t, validateLottery(c))
	}
}
