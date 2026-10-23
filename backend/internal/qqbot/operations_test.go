package qqbot

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestModuleMergeAndIndependentRevision(t *testing.T) {
	a := DefaultConfig()
	a.AppID = "app"
	a.AppSecret = "secret"
	b := a
	b.ChatModel = "changed"
	b.AllowedGroups = []string{"forged"}
	c, err := mergeConfigSection(a, b, "chat")
	require.NoError(t, err)
	require.Equal(t, "changed", c.ChatModel)
	require.Empty(t, c.AllowedGroups)
	require.Equal(t, a.AppSecret, c.AppSecret)
	require.NotEqual(t, sectionVersion(a, "chat"), sectionVersion(c, "chat"))
	require.Equal(t, sectionVersion(a, "connection"), sectionVersion(c, "connection"))
	public := a.Public()
	raw, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"app_secret":"secret"`)
}
func TestLotteryPreviewIncludesGuaranteeReplacement(t *testing.T) {
	c := LotteryConfig{Prizes: []LotteryPrize{{Amount: 1, ProbabilityBPS: 10000}}, GuaranteeEnabled: true, GuaranteeEvery: 10, GuaranteePrizes: []LotteryPrize{{Amount: 5, ProbabilityBPS: 10000}}}
	require.InDelta(t, 1.4, lotteryExpected(c), 1e-8)
	c.GuaranteeEvery = 1
	require.Equal(t, 5.0, lotteryExpected(c))
}
func TestQuietHoursAcrossMidnight(t *testing.T) {
	q := QuietHours{Enabled: true, StartHour: 23, EndHour: 8}
	zone := time.FixedZone("Asia/Shanghai", 28800)
	for _, tc := range []struct {
		hour  int
		quiet bool
	}{{22, false}, {23, true}, {0, true}, {7, true}, {8, false}} {
		require.Equal(t, tc.quiet, q.active(time.Date(2026, 10, 23, tc.hour, 0, 0, 0, zone)))
	}
	q.Enabled = false
	require.False(t, q.active(time.Date(2026, 10, 23, 23, 0, 0, 0, zone)))
}
