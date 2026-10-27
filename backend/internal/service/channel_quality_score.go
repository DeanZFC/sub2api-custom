package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var ErrChannelQualityScoreConflict = errors.New("channel quality score configuration changed")

// Scoring has its own reference values. Protection thresholds, active test
// verdicts and the presence of a protection policy never change this scale.
type ChannelQualityScoreMetric struct {
	Weight float64 `json:"weight"`
	Good   float64 `json:"good"`
	Bad    float64 `json:"bad"`
}

type ChannelQualityScoreConfig struct {
	Enabled       bool                      `json:"enabled"`
	WindowSeconds int                       `json:"window_seconds"`
	MaxRequests   int                       `json:"max_requests"`
	MinSamples    int                       `json:"min_samples"`
	ErrorRate     ChannelQualityScoreMetric `json:"error_rate"`
	CacheRate     ChannelQualityScoreMetric `json:"cache_rate"`
	TTFT          ChannelQualityScoreMetric `json:"ttft"`
	Duration      ChannelQualityScoreMetric `json:"duration"`
	EvidenceSince time.Time                 `json:"-"`
	Version       int64                     `json:"version"`
	UpdatedAt     time.Time                 `json:"updated_at"`
}

func DefaultChannelQualityScoreConfig() ChannelQualityScoreConfig {
	return ChannelQualityScoreConfig{
		Enabled: true, WindowSeconds: 900, MaxRequests: 100, MinSamples: 20,
		ErrorRate: ChannelQualityScoreMetric{Weight: 40, Good: 0, Bad: 20},
		CacheRate: ChannelQualityScoreMetric{Weight: 25, Good: 80, Bad: 0},
		TTFT:      ChannelQualityScoreMetric{Weight: 25, Good: 2000, Bad: 15000},
		Duration:  ChannelQualityScoreMetric{Weight: 10, Good: 10000, Bad: 120000},
	}
}

func (c *ChannelQualityScoreConfig) Validate() error {
	if c == nil {
		return fmt.Errorf("score configuration is required")
	}
	if c.WindowSeconds < 60 || c.WindowSeconds > 86400 {
		return fmt.Errorf("window_seconds must be between 60 and 86400")
	}
	if c.MaxRequests < 5 || c.MaxRequests > 1000 || c.MinSamples < 5 || c.MinSamples > c.MaxRequests {
		return fmt.Errorf("max_requests must be between 5 and 1000; min_samples must be between 5 and max_requests")
	}
	var total float64
	for _, m := range []struct {
		key  string
		v    ChannelQualityScoreMetric
		max  float64
		high bool
	}{{"error_rate", c.ErrorRate, 100, false}, {"cache_rate", c.CacheRate, 100, true}, {"ttft", c.TTFT, 3600000, false}, {"duration", c.Duration, 3600000, false}} {
		if !qualityScoreFinite(m.v.Weight) || !qualityScoreFinite(m.v.Good) || !qualityScoreFinite(m.v.Bad) || m.v.Weight < 0 || m.v.Weight > 100 || m.v.Good < 0 || m.v.Good > m.max || m.v.Bad < 0 || m.v.Bad > m.max {
			return fmt.Errorf("invalid %s score weight or reference values", m.key)
		}
		if (m.high && m.v.Good <= m.v.Bad) || (!m.high && m.v.Good >= m.v.Bad) {
			return fmt.Errorf("%s good/bad reference values have an invalid direction", m.key)
		}
		total += m.v.Weight
	}
	if total <= 0 {
		return fmt.Errorf("at least one score dimension must have positive weight")
	}
	return nil
}

type ChannelQualityScoreDimension struct {
	Key     string   `json:"key"`
	Value   *float64 `json:"value"`
	Score   *float64 `json:"score"`
	Weight  float64  `json:"weight"`
	Samples int      `json:"samples"`
	Status  string   `json:"status"`
	Unit    string   `json:"unit"`
	Reason  string   `json:"reason"`
}

type ChannelQualityScore struct {
	Value           *float64                       `json:"value"`
	Status          string                         `json:"status"`
	Samples         int                            `json:"samples"`
	Models          int                            `json:"models"`
	LatestAt        time.Time                      `json:"latest_at"`
	Model           string                         `json:"model"`
	WindowSeconds   int                            `json:"window_seconds"`
	MaxRequests     int                            `json:"max_requests"`
	MinSamples      int                            `json:"min_samples"`
	Confidence      float64                        `json:"confidence"`
	CoveragePercent float64                        `json:"coverage_percent"`
	Dimensions      []ChannelQualityScoreDimension `json:"dimensions"`
}

func (s *ChannelQualityScore) IsRankable() bool {
	return s != nil && s.Value != nil && qualityScoreFinite(*s.Value) && (s.Status == "ready" || s.Status == "partial")
}

type ChannelQualityScoreProvider interface {
	ChannelQualityScores(context.Context, []int64, string) (map[int64]*ChannelQualityScore, error)
}

type ChannelQualityScoreSettingsRepository interface {
	GetQualityScoreConfig(context.Context) (ChannelQualityScoreConfig, error)
	SaveQualityScoreConfig(context.Context, *ChannelQualityScoreConfig) error
}

// Observations describe completed, real upstream attempts. CacheReusable is
// established from earlier completed, matching request digests, never from the
// hit counter itself. Cold requests and missing metadata have no cache score.
type ChannelQualityScoreObservation struct {
	Key                 string
	At                  time.Time
	Model               string
	Failed              bool
	TTFT                *float64
	Duration            *float64
	CacheReported       bool
	CacheReusable       bool
	CacheInput          int64
	CacheRead           int64
	CacheExpectedTokens int64
}

func SummarizeChannelQualityScore(c ChannelQualityScoreConfig, observations []ChannelQualityScoreObservation, model string, complete bool, now time.Time) *ChannelQualityScore {
	s := &ChannelQualityScore{Status: "insufficient", Model: strings.TrimSpace(model), WindowSeconds: c.WindowSeconds, MaxRequests: c.MaxRequests, MinSamples: c.MinSamples, Dimensions: []ChannelQualityScoreDimension{}}
	if !c.Enabled {
		s.Status = "disabled"
		return s
	}
	if c.Validate() != nil {
		s.Status = "incomplete"
		return s
	}
	from := now.Add(-time.Duration(c.WindowSeconds) * time.Second)
	if c.EvidenceSince.After(from) {
		from = c.EvidenceSince
	}
	// A duplicate failed attempt wins over its later usage row, independently
	// of input order. The cap applies after filtering and deduplication.
	byKey := map[string]ChannelQualityScoreObservation{}
	for i, v := range observations {
		if v.At.Before(from) || v.At.After(now) || (s.Model != "" && v.Model != s.Model) {
			continue
		}
		key := v.Key
		if key == "" {
			key = fmt.Sprintf("anonymous:%d", i)
		}
		prior, ok := byKey[key]
		if !ok || (v.Failed && !prior.Failed) || (v.Failed == prior.Failed && v.At.After(prior.At)) {
			byKey[key] = v
		}
	}
	items := make([]ChannelQualityScoreObservation, 0, len(byKey))
	for _, v := range byKey {
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].At.Equal(items[j].At) {
			return items[i].Key > items[j].Key
		}
		return items[i].At.After(items[j].At)
	})
	if len(items) > c.MaxRequests {
		items = items[:c.MaxRequests]
	}
	models := map[string]bool{}
	var failed, cacheSamples int
	var input, read int64
	var ttfts, durations []float64
	for _, v := range items {
		s.Samples++
		models[v.Model] = true
		if v.At.After(s.LatestAt) {
			s.LatestAt = v.At
		}
		if v.Failed {
			failed++
			continue
		}
		if v.TTFT != nil && qualityScoreFinite(*v.TTFT) && *v.TTFT >= 0 {
			ttfts = append(ttfts, *v.TTFT)
		}
		if v.Duration != nil && qualityScoreFinite(*v.Duration) && *v.Duration >= 0 {
			durations = append(durations, *v.Duration)
		}
		if v.CacheReported && v.CacheReusable && v.CacheInput > 0 && v.CacheRead >= 0 && v.CacheRead <= v.CacheInput && v.CacheExpectedTokens > 0 && v.CacheExpectedTokens <= v.CacheInput {
			cacheSamples++
			input += v.CacheExpectedTokens
			read += min(v.CacheRead, v.CacheExpectedTokens)
		}
	}
	s.Models = len(models)
	var errorRate, cacheRate *float64
	if s.Samples > 0 {
		v := 100 * float64(failed) / float64(s.Samples)
		errorRate = &v
	}
	if input > 0 {
		v := 100 * float64(read) / float64(input)
		cacheRate = &v
	}
	var available, total, weighted float64
	add := func(key, unit string, metric ChannelQualityScoreMetric, value *float64, samples int) {
		d := ChannelQualityScoreDimension{Key: key, Unit: unit, Weight: metric.Weight, Value: value, Samples: samples, Status: "unknown", Reason: "no_eligible_samples"}
		if metric.Weight == 0 {
			d.Status, d.Reason = "disabled", "zero_weight"
		} else {
			total += metric.Weight
			if !complete {
				d.Reason = "telemetry_incomplete"
			} else if value != nil && samples >= c.MinSamples {
				v := qualityScoreRound(channelQualityIndependentMetricScore(*value, metric))
				d.Score, d.Status, d.Reason = &v, "ready", ""
				available += metric.Weight
				weighted += v * metric.Weight
			} else if samples > 0 {
				d.Status, d.Reason = "insufficient", "minimum_samples"
			}
		}
		s.Dimensions = append(s.Dimensions, d)
	}
	add("error_rate", "percent", c.ErrorRate, errorRate, s.Samples)
	add("cache_rate", "percent", c.CacheRate, cacheRate, cacheSamples)
	add("ttft", "ms", c.TTFT, qualityScoreP90(ttfts), len(ttfts))
	add("duration", "ms", c.Duration, qualityScoreP90(durations), len(durations))
	if !complete {
		s.Status = "incomplete"
		return s
	}
	if s.Samples < c.MinSamples || available == 0 {
		return s
	}
	s.CoveragePercent = qualityScoreRound(100 * available / total)
	s.Confidence = available / total * math.Min(1, float64(s.Samples)/float64(c.MaxRequests))
	v := qualityScoreRound(weighted / available)
	s.Value, s.Status = &v, "ready"
	if available < total {
		s.Status = "partial"
	}
	return s
}

func channelQualityIndependentMetricScore(value float64, metric ChannelQualityScoreMetric) float64 {
	return math.Max(0, math.Min(100, 100*(value-metric.Bad)/(metric.Good-metric.Bad)))
}

func qualityScoreFinite(v float64) bool   { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func qualityScoreRound(v float64) float64 { return math.Round(v*100) / 100 }
func qualityScoreP90(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sort.Float64s(values)
	v := values[int(math.Ceil(.9*float64(len(values))))-1]
	return &v
}
