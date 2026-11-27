package repository

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestUsageAndMonitorCacheRatesRequireProbeTelemetry(t *testing.T) {
	require.Contains(t, usageLogCacheEligible, strings.ReplaceAll(channelMonitorV2CacheTelemetryUL, "ul.", ""))
}
