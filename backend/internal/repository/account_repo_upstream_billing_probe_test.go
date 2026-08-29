package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamBillingProbeExtraIsSchedulerNeutral(t *testing.T) {
	require.True(t, isSchedulerNeutralExtraKey("upstream_billing_probe"))
	require.True(t, isSchedulerNeutralExtraKey("upstream_billing_probe_enabled"))
	require.False(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		"upstream_billing_probe":         map[string]any{"status": "ok"},
		"upstream_billing_probe_enabled": true,
	}))
	require.False(t, isSchedulerNeutralExtraKey(service.CodexQuotaOverdraftEnabledExtraKey))
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		service.CodexQuotaOverdraftEnabledExtraKey: false,
	}))
}
