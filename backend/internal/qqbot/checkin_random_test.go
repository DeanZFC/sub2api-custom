package qqbot

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckinWeightedDistribution(t *testing.T) {
	// Enumerate all equally likely draw combinations instead of using a flaky
	// statistical sample. The unused second draw still has n equally likely values.
	for _, n := range []int64{1, 2, 4, 20} {
		t.Run(fmt.Sprintf("%d monetary units", n), func(t *testing.T) {
			const lo int64 = 12345678
			c := CheckinConfig{MinAmount: float64(lo) / 1e8, MaxAmount: float64(lo+n-1) / 1e8}
			counts := make([]int64, n)
			for branch := range int64(5) {
				for first := range n {
					for second := range n {
						draws := []int64{branch, first, second}
						amount, err := randomCheckinAmountWith(c, func(limit int64) (int64, error) {
							value := draws[0]
							draws = draws[1:]
							require.Less(t, value, limit)
							return value, nil
						})
						require.NoError(t, err)
						offset := int64(math.Round(amount*1e8)) - lo
						require.GreaterOrEqual(t, offset, int64(0))
						require.Less(t, offset, n)
						counts[offset]++
					}
				}
			}
			var weightedOffset, lowerHalf, upperQuarter int64
			for k, count := range counts {
				// One uniform branch plus four minimum-of-two branches.
				require.Equal(t, n+4*(2*n-2*int64(k)-1), count)
				weightedOffset += int64(k) * count
				if int64(k) < n/2 {
					lowerHalf += count
				}
				if int64(k) >= 3*n/4 {
					upperQuarter += count
				}
			}
			total := float64(5 * n * n)
			mean := (float64(lo) + float64(weightedOffset)/total) / 1e8
			require.InDelta(t, mean, checkinExpectedAmount(c), 1e-15)
			if n%2 == 0 {
				require.Equal(t, .7, float64(lowerHalf)/total)
			}
			if n%4 == 0 {
				require.Equal(t, .1, float64(upperQuarter)/total)
			}
		})
	}
}

func TestCheckinWeightedEndpointsAndDraws(t *testing.T) {
	const span int64 = 100000000000
	c := CheckinConfig{MinAmount: 0, MaxAmount: 1000}
	for _, tc := range []struct {
		name  string
		draws []int64
		want  float64
	}{
		{"uniform lower endpoint", []int64{0, 0}, 0},
		{"uniform upper endpoint", []int64{0, span}, 1000},
		{"weighted first lower", []int64{1, 0, span}, 0},
		{"weighted second lower", []int64{4, span, 1}, .00000001},
		{"weighted upper endpoint", []int64{4, span, span}, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := 0
			amount, err := randomCheckinAmountWith(c, func(limit int64) (int64, error) {
				require.Less(t, index, len(tc.draws))
				if index == 0 {
					require.Equal(t, int64(5), limit)
				} else {
					require.Equal(t, span+1, limit)
				}
				value := tc.draws[index]
				index++
				return value, nil
			})
			require.NoError(t, err)
			require.Equal(t, tc.want, amount)
			require.Equal(t, len(tc.draws), index)
		})
	}
	// The full supported range would overflow an int64 product in the mean.
	require.InDelta(t, 366.6666666653333, checkinExpectedAmount(c), 1e-10)
}

func TestCheckinWeightedFixedAndInvalidRanges(t *testing.T) {
	neverDraw := func(int64) (int64, error) {
		t.Fatal("fixed or invalid configuration must not consume randomness")
		return 0, nil
	}
	for _, value := range []float64{0, .12345678, 1000} {
		c := CheckinConfig{MinAmount: value, MaxAmount: value}
		amount, err := randomCheckinAmountWith(c, neverDraw)
		require.NoError(t, err)
		require.Equal(t, value, amount)
		require.Equal(t, value, checkinExpectedAmount(c))
	}
	for _, c := range []CheckinConfig{
		{MinAmount: -1}, {MinAmount: 2, MaxAmount: 1}, {MaxAmount: 1001},
		{MaxAmount: .000000001}, {MaxAmount: math.NaN()}, {MaxAmount: math.Inf(1)},
	} {
		amount, err := randomCheckinAmountWith(c, neverDraw)
		require.Error(t, err)
		require.Zero(t, amount)
	}
}

func TestCheckinWeightedRandomErrors(t *testing.T) {
	wantErr := errors.New("random source unavailable")
	for failAt := range 3 {
		t.Run(fmt.Sprintf("draw %d", failAt), func(t *testing.T) {
			index := 0
			amount, err := randomCheckinAmountWith(CheckinConfig{MinAmount: 1, MaxAmount: 3}, func(int64) (int64, error) {
				index++
				if index-1 == failAt {
					return 0, wantErr
				}
				return 1, nil
			})
			require.ErrorIs(t, err, wantErr)
			require.Zero(t, amount)
			require.Equal(t, failAt+1, index)
		})
	}
}
