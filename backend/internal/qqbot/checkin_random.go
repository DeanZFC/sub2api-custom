package qqbot

import (
	"crypto/rand"
	"math"
	"math/big"
)

const (
	checkinAmountScale        = 1e8
	checkinUniformWeight      = 1
	checkinDistributionWeight = 5
)

func randomCheckinAmount(c CheckinConfig) (float64, error) {
	return randomCheckinAmountWith(c, func(limit int64) (int64, error) {
		n, err := rand.Int(rand.Reader, big.NewInt(limit))
		if err != nil {
			return 0, err
		}
		return n.Int64(), nil
	})
}

// Use a 20% uniform draw and an 80% minimum of two independent uniform draws.
// This smoothly favors lower amounts while keeping a meaningful upper tail:
// approximately 70% in the lower half and 10% in the upper quarter. Sampling
// integer monetary units preserves eight decimals and includes both endpoints.
// draw must return an independent uniform integer in [0, limit).
func randomCheckinAmountWith(c CheckinConfig, draw func(limit int64) (int64, error)) (float64, error) {
	if err := validateCheckin(c); err != nil {
		return 0, err
	}
	lo, hi := checkinAmountBounds(c)
	if lo == hi {
		return float64(lo) / checkinAmountScale, nil
	}
	branch, err := draw(checkinDistributionWeight)
	if err != nil {
		return 0, err
	}
	offset, err := draw(hi - lo + 1)
	if err != nil {
		return 0, err
	}
	if branch >= checkinUniformWeight {
		other, err := draw(hi - lo + 1)
		if err != nil {
			return 0, err
		}
		offset = min(offset, other)
	}
	return float64(lo+offset) / checkinAmountScale, nil
}

func checkinAmountBounds(c CheckinConfig) (int64, int64) {
	return int64(math.Round(c.MinAmount * checkinAmountScale)), int64(math.Round(c.MaxAmount * checkinAmountScale))
}

func checkinExpectedAmount(c CheckinConfig) float64 {
	lo, hi := checkinAmountBounds(c)
	n := float64(hi - lo + 1)
	uniformWeight := float64(checkinUniformWeight) / checkinDistributionWeight
	// E[min(X,Y)] for independent uniform integers in [0,n) is
	// (n-1)*(2*n-1)/(6*n). Convert before multiplying to avoid int64 overflow.
	meanOffset := uniformWeight*(n-1)/2 + (1-uniformWeight)*(n-1)*(2*n-1)/(6*n)
	return (float64(lo) + meanOffset) / checkinAmountScale
}
