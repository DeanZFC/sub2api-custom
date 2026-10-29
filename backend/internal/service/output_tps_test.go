package service

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOutputTPSWeightedEvidence(t *testing.T) {
	now := time.Now()
	samples := []PassiveQualitySample{
		{At: now, OutputTokens: 100, GenerationMS: 1000},
		{At: now.Add(-time.Second), OutputTokens: 100, GenerationMS: 9000},
		{At: now, OutputTokens: 1000, GenerationMS: 0},
		{At: now, Failed: true, OutputTokens: 1000, GenerationMS: 1000},
		{At: now, OutputTokens: 1000, GenerationMS: math.NaN()},
	}
	e := PassiveQualitySummarize(samples, now.Add(-time.Second), true)
	require.Equal(t, 20.0, *e.OutputTPS, "sum(tokens)/sum(seconds), not mean(request TPS)")
	require.Equal(t, 2, e.TPSSamples)
	require.Equal(t, 1, e.TPSFresh)
	require.Equal(t, 100.0, *e.FreshOutputTPS)
	require.Nil(t, PassiveQualitySummarize(nil, now, true).OutputTPS)
	for _, ms := range []float64{0, -1, math.Inf(1), math.NaN()} {
		require.Nil(t, OutputTokensPerSecond(100, ms))
	}
}

func TestPassiveQualityTPSIndependentProtectionAndRecovery(t *testing.T) {
	c := passiveTestConfig()
	c.SchemaVersion = 2
	c.ErrorRate.Enabled = false
	c.TPS = PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 15}
	require.NoError(t, c.Validate())
	s := PassiveQualityMetricState{}
	now := time.Now()
	evidence := func(at time.Time, rate float64) PassiveQualityEvidence {
		var samples []PassiveQualitySample
		for i := 0; i < 5; i++ {
			samples = append(samples, PassiveQualitySample{At: at.Add(-time.Duration(i) * time.Millisecond), OutputTokens: int64(rate), GenerationMS: 1000})
		}
		return PassiveQualitySummarize(samples, s.LastEvidenceAt, true)
	}
	e := evidence(now, 5)
	require.Empty(t, AdvancePassiveQualityTPS(c, &s, e, now))
	e = evidence(now, 5)
	require.Empty(t, AdvancePassiveQualityTPS(c, &s, e, now), "same evidence cannot reconfirm")
	e = evidence(now.Add(time.Second), 5)
	require.Equal(t, "tps_limited", AdvancePassiveQualityTPS(c, &s, e, now.Add(time.Second)))
	e = PassiveQualityEvidence{Samples: 5, Fresh: 5, Complete: true, TTFT: new(float64(1)), TTFTSamples: 5, TTFTFresh: 5, Latest: now.Add(time.Minute)}
	require.Empty(t, AdvancePassiveQualityTPS(c, &s, e, now.Add(time.Minute)))
	require.Equal(t, "limited", s.Phase, "fast TTFT cannot recover missing TPS evidence")
	for i := 0; i < 2; i++ {
		at := now.Add(time.Minute + time.Duration(i)*time.Second)
		e = evidence(at, 20)
		AdvancePassiveQualityTPS(c, &s, e, at)
	}
	require.Equal(t, "healthy", s.Phase)
	require.False(t, PassiveQualityMetricConfig(c, "availability").TPS.Enabled)
	c.TPS.Recover = 5
	require.Error(t, c.Validate())
}
