package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPassiveQualityCacheImpactUsesSameWindowAndSuccessfulRequests(t *testing.T) {
	now := time.Now()
	c := isolatedPassiveTestConfig()
	ptr := func(v float64) *float64 { return &v }
	samples := []PassiveQualitySample{
		{Key: "cold", At: now, CacheReason: "account_switched", InputCost: ptr(.2), TTFT: ptr(50)},
		{Key: "unknown", At: now.Add(-time.Second), InputCost: ptr(0), TTFT: ptr(100)},
		{Key: "missing", At: now.Add(-2 * time.Second)},
		{Key: "invalid", At: now.Add(-3 * time.Second), InputCost: ptr(math.NaN()), TTFT: ptr(-1)},
		{Key: "failed", At: now.Add(-4 * time.Second), Failed: true, CacheReason: "account_switched", InputCost: ptr(100), TTFT: ptr(9000)},
		{Key: "outside", At: now.Add(-301 * time.Second), InputCost: ptr(100), TTFT: ptr(9000)},
		{Key: "future", At: now.Add(time.Second), InputCost: ptr(100), TTFT: ptr(9000)},
	}
	samples = append(samples, samples[0])
	e := PassiveQualityCacheWindowEvidence(c, c.CacheProtection.Quick, samples, time.Time{}, time.Time{}, now, true)
	require.Equal(t, 5, e.TotalSamples)
	require.Equal(t, 1, e.FailedSamples)
	require.Equal(t, 1, e.AccountSwitches)
	require.Equal(t, 2, e.InputCostSamples)
	require.NotNil(t, e.InputCost)
	require.InDelta(t, .2, *e.InputCost, 1e-12)
	require.Equal(t, 2, e.TTFTSamples)
	require.Equal(t, 100.0, *e.TTFTP90)
	missing := PassiveQualityCacheWindowEvidence(c, c.CacheProtection.Quick, nil, time.Time{}, time.Time{}, now, true)
	require.Nil(t, missing.InputCost)
	require.Nil(t, missing.TTFTP90)
	zero := PassiveQualityCacheWindowEvidence(c, c.CacheProtection.Quick, samples[1:2], time.Time{}, time.Time{}, now, true)
	require.NotNil(t, zero.InputCost)
	require.Zero(t, *zero.InputCost)
}
