package repository

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestUsageCacheEligibilityMatchesChannelMonitor(t *testing.T) {
	require.Equal(t, usageLogCacheEligible, strings.ReplaceAll(channelMonitorV2CacheEligibleUL, "ul.", ""))
}
