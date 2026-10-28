package service

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func scoreFloat(v float64) *float64 { return &v }
func scoreObservations(now time.Time, n int) []ChannelQualityScoreObservation {
	result := make([]ChannelQualityScoreObservation, n)
	for i := range result {
		result[i] = ChannelQualityScoreObservation{Key: fmt.Sprint(i), At: now.Add(-time.Duration(i) * time.Second), Model: "public", TTFT: scoreFloat(2000), Duration: scoreFloat(10000), CacheReported: true, CacheReusable: true, CacheInput: 2000, CacheRead: 800, CacheExpectedTokens: 1000, OutputTokens: 100, GenerationMS: 2000}
	}
	return result
}

func TestChannelQualityScoreIndependentWeightedScale(t *testing.T) {
	now := time.Now()
	c := DefaultChannelQualityScoreConfig()
	samples := scoreObservations(now, 25)
	for i := range samples {
		samples[i].TTFT = scoreFloat(8500)
		samples[i].Duration = scoreFloat(65000)
		samples[i].CacheRead = 400
		samples[i].GenerationMS = 100000.0 / 27.5
	}
	// 20% failures score 0; other dimensions each score 50. Failure outcomes do
	// not contribute latency/cache, while their 20 successful peers meet the floor.
	for i := 0; i < 5; i++ {
		samples[i].Failed = true
	}
	s := SummarizeChannelQualityScore(c, samples, "public", true, now)
	require.Equal(t, "ready", s.Status)
	require.NotNil(t, s.Value)
	require.InDelta(t, 15, *s.Value, .001)
	require.Equal(t, 25, s.Samples)
	require.Equal(t, 1, s.Models)
	require.Equal(t, 100., s.CoveragePercent)
	require.True(t, s.IsRankable())
	require.Equal(t, 20, s.Dimensions[1].Samples)
	require.Equal(t, 8500., *s.Dimensions[2].Value)
}

func TestChannelQualityScoreMissingCacheIsUnknownAndColdDoesNotPenalize(t *testing.T) {
	now := time.Now()
	c := DefaultChannelQualityScoreConfig()
	samples := scoreObservations(now, 20)
	for i := range samples {
		samples[i].CacheReusable = false
		samples[i].CacheRead = 0
	}
	s := SummarizeChannelQualityScore(c, samples, "public", true, now)
	require.Equal(t, "partial", s.Status)
	require.Equal(t, 100., *s.Value)
	require.Equal(t, 90., s.CoveragePercent)
	require.InDelta(t, .18, s.Confidence, .000001)
	require.Nil(t, s.Dimensions[1].Value)
	require.Nil(t, s.Dimensions[1].Score)
	for i := range samples {
		samples[i].CacheReusable = true
		samples[i].CacheReported = false
	}
	s = SummarizeChannelQualityScore(c, samples, "public", true, now)
	require.Nil(t, s.Dimensions[1].Value)
	for i := range samples {
		samples[i].CacheReported = true
	}
	s = SummarizeChannelQualityScore(c, samples, "public", true, now)
	require.Equal(t, "ready", s.Status)
	require.Equal(t, 90., *s.Value)
	require.Equal(t, 0., *s.Dimensions[1].Value)
}

func TestChannelQualityScoreWindowCapDedupAndPublicModelIsolation(t *testing.T) {
	now := time.Now()
	c := DefaultChannelQualityScoreConfig()
	c.MaxRequests = 20
	samples := scoreObservations(now, 30)
	samples = append(samples, ChannelQualityScoreObservation{Key: "0", At: now.Add(-time.Millisecond), Model: "public", Failed: true})
	samples = append(samples, ChannelQualityScoreObservation{Key: "foreign", At: now, Model: "other", Failed: true})
	samples = append(samples, ChannelQualityScoreObservation{Key: "future", At: now.Add(time.Second), Model: "public", Failed: true})
	samples = append(samples, ChannelQualityScoreObservation{Key: "old", At: now.Add(-time.Hour), Model: "public", Failed: true})
	s := SummarizeChannelQualityScore(c, samples, "public", true, now)
	require.Equal(t, 20, s.Samples)
	require.Equal(t, 5., *s.Dimensions[0].Value)
	require.Equal(t, 75., *s.Value, "only error rate has 20 samples after failure dedup")
	require.Equal(t, 1, s.Models)
	require.Equal(t, "partial", s.Status)
}

func TestChannelQualityScoreUnknownDisabledAndIncomplete(t *testing.T) {
	now := time.Now()
	c := DefaultChannelQualityScoreConfig()
	for _, test := range []struct {
		name     string
		n        int
		complete bool
		status   string
	}{{"floor", 19, true, "insufficient"}, {"gap", 20, false, "incomplete"}, {"empty", 0, true, "insufficient"}} {
		t.Run(test.name, func(t *testing.T) {
			s := SummarizeChannelQualityScore(c, scoreObservations(now, test.n), "public", test.complete, now)
			require.Equal(t, test.status, s.Status)
			require.Nil(t, s.Value)
			require.False(t, s.IsRankable())
		})
	}
	c.Enabled = false
	s := SummarizeChannelQualityScore(c, scoreObservations(now, 20), "public", true, now)
	require.Equal(t, "disabled", s.Status)
	require.Nil(t, s.Value)
	var absent *ChannelQualityScore
	require.False(t, absent.IsRankable())
}

func TestChannelQualityScoreP90FiniteInputsAndWeightValidation(t *testing.T) {
	now := time.Now()
	c := DefaultChannelQualityScoreConfig()
	c.MinSamples = 5
	samples := scoreObservations(now, 10)
	samples[9].TTFT = scoreFloat(900000) // P90 ignores only the worst of ten.
	samples[0].Duration = scoreFloat(math.NaN())
	s := SummarizeChannelQualityScore(c, samples, "public", true, now)
	require.Equal(t, 2000., *s.Dimensions[2].Value)
	require.Equal(t, 9, s.Dimensions[3].Samples)
	require.NoError(t, c.Validate())
	for _, mutate := range []func(*ChannelQualityScoreConfig){
		func(c *ChannelQualityScoreConfig) { c.ErrorRate.Good = 30 },
		func(c *ChannelQualityScoreConfig) { c.CacheRate.Good = 0 },
		func(c *ChannelQualityScoreConfig) { c.Duration.Bad = math.Inf(1) },
		func(c *ChannelQualityScoreConfig) { c.TTFT.Weight = -1 },
		func(c *ChannelQualityScoreConfig) { c.WindowSeconds = 86401 },
		func(c *ChannelQualityScoreConfig) { c.MaxRequests = 19 },
		func(c *ChannelQualityScoreConfig) {
			c.ErrorRate.Weight = 0
			c.CacheRate.Weight = 0
			c.TTFT.Weight = 0
			c.Duration.Weight = 0
			c.TPS.Weight = 0
		},
	} {
		bad := DefaultChannelQualityScoreConfig()
		mutate(&bad)
		require.Error(t, bad.Validate())
	}
}

func TestChannelQualityScoreAvailabilityOutweighsAllPerformanceDimensions(t *testing.T) {
	now := time.Now()
	stable := scoreObservations(now, 100)
	flaky := scoreObservations(now, 100)
	for i := range stable {
		stable[i].CacheRead = 0
		stable[i].TTFT = scoreFloat(15000)
		stable[i].GenerationMS = 20000
		if i < 10 {
			flaky[i].Failed = true
		}
	}
	c := DefaultChannelQualityScoreConfig()
	good := SummarizeChannelQualityScore(c, stable, "public", true, now)
	bad := SummarizeChannelQualityScore(c, flaky, "public", true, now)
	require.Equal(t, "ready", good.Status)
	require.Equal(t, "ready", bad.Status)
	require.InDelta(t, 70, *good.Value, .001)
	require.InDelta(t, 65, *bad.Value, .001)
	require.Greater(t, *good.Value, *bad.Value, "perfect performance cannot outweigh a ten-point availability loss")
}
