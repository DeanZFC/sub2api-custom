package service

import (
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestOutputTPSInvalidDurationUnknown(t *testing.T) {
	for _, ms := range []float64{0, -1, math.Inf(1), math.NaN()} {
		require.Nil(t, OutputTokensPerSecond(100, ms))
	}
	require.Equal(t, 100.0, *OutputTokensPerSecond(100, 1000))
}
