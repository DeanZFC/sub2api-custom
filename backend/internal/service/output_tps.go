package service

import "math"

// OutputTokensPerSecond uses token and generation-time sums from the same
// eligible stream population. Missing or invalid timing is unknown, never zero.
func OutputTokensPerSecond(outputTokens int64, generationMS float64) *float64 {
	if outputTokens <= 0 || generationMS <= 0 || math.IsNaN(generationMS) || math.IsInf(generationMS, 0) {
		return nil
	}
	v := float64(outputTokens) * 1000 / generationMS
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}
