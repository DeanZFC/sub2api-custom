package qqbot

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"math/big"
)

const (
	checkinAmountScale      = 1e8
	checkinProbabilityTotal = 10000
)

// CheckinRewardDistribution belongs to the administrator configuration only.
// Never include it in user DTOs, command replies, or public activity rules.
type CheckinRewardDistribution struct {
	Bands []CheckinRewardBand `json:"bands"`
}

type CheckinRewardBand struct {
	EndBPS         int `json:"end_bps"` // Upper boundary as a fraction of the min-to-max span.
	ProbabilityBPS int `json:"probability_bps"`
}

func legacyCheckinDistribution(lower, middle, upper int) *CheckinRewardDistribution {
	return &CheckinRewardDistribution{Bands: []CheckinRewardBand{{5000, lower}, {7500, middle}, {10000, upper}}}
}

// Read settings and requests saved with the original fixed boundaries.
func (d *CheckinRewardDistribution) UnmarshalJSON(data []byte) error {
	var wire struct {
		Bands           []CheckinRewardBand `json:"bands"`
		LowerHalfBPS    int                 `json:"lower_half_bps"`
		NextQuarterBPS  int                 `json:"next_quarter_bps"`
		UpperQuarterBPS int                 `json:"upper_quarter_bps"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Bands != nil {
		// A cached three-field editor may echo bands alongside its edited
		// legacy weights. Do not acknowledge an edit that would be ignored.
		if wire.LowerHalfBPS != 0 || wire.NextQuarterBPS != 0 || wire.UpperQuarterBPS != 0 {
			return errors.New("请刷新签到配置页面后重新设置区间概率")
		}
		d.Bands = wire.Bands
	} else {
		*d = *legacyCheckinDistribution(wire.LowerHalfBPS, wire.NextQuarterBPS, wire.UpperQuarterBPS)
	}
	return nil
}

func (c CheckinConfig) rewardDistribution() *CheckinRewardDistribution {
	if c.RewardDistribution != nil {
		return c.RewardDistribution
	}
	return legacyCheckinDistribution(7000, 2000, 1000)
}

func validateCheckinDistribution(d *CheckinRewardDistribution) error {
	end, total := 0, 0
	for _, band := range d.Bands {
		if band.EndBPS <= end || band.EndBPS > checkinProbabilityTotal {
			return errors.New("签到区间终点必须递增，且不超过 100%")
		}
		end = band.EndBPS
		if band.ProbabilityBPS < 0 || band.ProbabilityBPS > checkinProbabilityTotal {
			return errors.New("签到各区间概率需在 0% 到 100% 之间，最多两位小数")
		}
		total += band.ProbabilityBPS
	}
	if end != checkinProbabilityTotal {
		return errors.New("签到区间必须完整覆盖金额跨度的 0% 到 100%")
	}
	if total != checkinProbabilityTotal {
		return errors.New("签到各区间的概率合计必须为 100%")
	}
	return nil
}

type checkinAmountBand struct {
	lo, hi int64
	weight int
}

func checkinAmountBands(c CheckinConfig) []checkinAmountBand {
	lo, hi := checkinAmountBounds(c)
	span := hi - lo
	rules := c.rewardDistribution().Bands
	bands := make([]checkinAmountBand, 0, len(rules))
	start := lo
	for _, rule := range rules {
		// Round up to the first representable amount in the next band.
		next := lo + (span*int64(rule.EndBPS)+checkinProbabilityTotal-1)/checkinProbabilityTotal
		end := next - 1
		if rule.EndBPS == checkinProbabilityTotal {
			end = hi
		}
		// A band narrower than one amount unit can have no distinct value.
		bands = append(bands, checkinAmountBand{start, max(start, end), rule.ProbabilityBPS})
		start = next
	}
	return bands
}

func randomCheckinAmount(c CheckinConfig) (float64, error) {
	return randomCheckinAmountWith(c, func(limit int64) (int64, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(limit))
		if err != nil {
			return 0, err
		}
		return n.Int64(), nil
	})
}

// Select a band using the configured probabilities, then sample uniformly
// within that band. draw must return an independent uniform integer in [0, limit).
func randomCheckinAmountWith(c CheckinConfig, draw func(limit int64) (int64, error)) (float64, error) {
	if err := validateCheckin(c); err != nil {
		return 0, err
	}
	lo, hi := checkinAmountBounds(c)
	if lo == hi {
		return float64(lo) / checkinAmountScale, nil
	}
	roll, err := draw(checkinProbabilityTotal)
	if err != nil {
		return 0, err
	}
	for _, band := range checkinAmountBands(c) {
		roll -= int64(band.weight)
		if roll < 0 {
			offset, err := draw(band.hi - band.lo + 1)
			if err != nil {
				return 0, err
			}
			return float64(band.lo+offset) / checkinAmountScale, nil
		}
	}
	return 0, errors.New("checkin roll outside validated distribution")
}

func checkinAmountBounds(c CheckinConfig) (int64, int64) {
	return int64(math.Round(c.MinAmount * checkinAmountScale)), int64(math.Round(c.MaxAmount * checkinAmountScale))
}

func checkinExpectedAmount(c CheckinConfig) float64 {
	lo, hi := checkinAmountBounds(c)
	if lo == hi {
		return float64(lo) / checkinAmountScale
	}
	var mean float64
	for _, band := range checkinAmountBands(c) {
		mean += (float64(band.lo) + float64(band.hi)) / 2 * float64(band.weight) / checkinProbabilityTotal
	}
	return mean / checkinAmountScale
}
