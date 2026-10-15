package qqbot

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRechargeRequirementValidationAndAlternatives(t *testing.T) {
	for _, amount := range []float64{-1, .001, .00001, 1e9 + .01, math.NaN(), math.Inf(1)} {
		require.Error(t, validateRechargeRequirement(RechargeRequirement{MinRechargeTotal: amount}))
		require.Error(t, validateRechargeRequirement(RechargeRequirement{MinRecharge30d: amount}))
	}
	for _, amount := range []float64{0, .01, .29, 999999999.99, 1e9} {
		require.NoError(t, validateRechargeRequirement(RechargeRequirement{MinRechargeTotal: amount, MinRecharge30d: amount}))
	}
	state := func(total, recent string) RechargeEligibility {
		return RechargeEligibility{Total: decimal.RequireFromString(total), Last30d: decimal.RequireFromString(recent)}
	}
	require.True(t, (RechargeRequirement{}).satisfied(state("0", "0")))
	r := RechargeRequirement{MinRechargeTotal: 100, MinRecharge30d: 10}
	require.False(t, r.satisfied(state("99.99999999", "9.99999999")))
	require.True(t, r.satisfied(state("100", "0")))
	require.True(t, r.satisfied(state("10", "10")))
	require.False(t, (RechargeRequirement{MinRechargeTotal: 100}).satisfied(state("0", "100")))
	require.False(t, (RechargeRequirement{MinRecharge30d: 10}).satisfied(state("100", "0")))
}

func TestRechargeConfigCompatibility(t *testing.T) {
	var checkin CheckinConfig
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"min_amount":0.1,"max_amount":0.5}`), &checkin))
	require.False(t, checkin.enabled(), "existing settings remain unrestricted")
	require.NoError(t, json.Unmarshal([]byte(`{"min_recharge_total":20,"min_recharge_30d":5}`), &checkin))
	require.Equal(t, RechargeRequirement{MinRechargeTotal: 20, MinRecharge30d: 5}, checkin.RechargeRequirement)
	raw, err := json.Marshal(checkin)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"min_recharge_total":20`)
}
