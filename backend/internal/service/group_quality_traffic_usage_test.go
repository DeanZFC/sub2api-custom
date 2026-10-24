package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityUsageContextFreezesBudgetAcrossWSRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request.Header.Set("Upgrade", "websocket")
	BeginOpsStreamTurn(c, 1)
	SetGroupQualityTrafficGroup(c, 9)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte("{\"model\":\"public-first\"}"))
	connectionCtx := c.Request.Context()
	gate := &groupTrafficTestGate{allow: true}
	bindGroupQualityTrafficLease(connectionCtx, gate, 62, "public-first", "lease-first")
	firstUsageCtx := PassiveQualityUsageContext(connectionCtx, context.Background())
	BeginOpsStreamTurn(c, 2)
	SetGroupQualityTrafficModel(c.Request.Context(), "public-second")
	bindGroupQualityTrafficLease(connectionCtx, gate, 63, "public-second", "lease-second")
	beforeRetry, _ := GroupQualityTrafficRequestFromContext(connectionCtx)
	ResumeOpsStreamTurn(c, 1)
	afterRetry, _ := GroupQualityTrafficRequestFromContext(connectionCtx)
	require.Equal(t, beforeRetry, afterRetry, "local WS turn numbering reset cannot discard an admitted retry lease")
	frozen, _ := GroupQualityTrafficRequestFromContext(firstUsageCtx)
	require.Equal(t, "lease-first", frozen.LeaseID)
	require.Equal(t, "public-first", frozen.Model)
	require.EqualValues(t, 9, frozen.GroupID)
	require.NotEqual(t, beforeRetry.Key, frozen.Key)
	BeginOpsStreamTurn(c, 2)
	next, _ := GroupQualityTrafficRequestFromContext(connectionCtx)
	require.NotEqual(t, beforeRetry.Key, next.Key, "new logical turn after retry starts its own budget")
	require.Empty(t, next.LeaseID)
}

func TestRecordOpenAIGroupQualityTrafficUsageKnownFailureOnly(t *testing.T) {
	for _, tt := range []struct {
		name    string
		result  *OpenAIForwardResult
		settles bool
	}{
		{"nil unknown", nil, false},
		{"empty unknown", &OpenAIForwardResult{}, false},
		{"explicit zero", &OpenAIForwardResult{Usage: OpenAIUsage{CacheUsageReported: true}}, false},
		{"known failed text", &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 1000, CacheReadInputTokens: 800, CacheUsageReported: true}}, true},
		{"known input unknown cache", &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 1000}}, true},
		{"media", &OpenAIForwardResult{ImageCount: 1, Usage: OpenAIUsage{InputTokens: 1000}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gate := &groupTrafficTestGate{allow: true}
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn", GroupID: 9, Model: "public", EstimatedInput: 8192})
			bindGroupQualityTrafficLease(ctx, gate, 62, "public", "lease-one")
			frozen := PassiveQualityUsageContext(ctx, context.Background())
			bindGroupQualityTrafficLease(ctx, gate, 63, "next", "lease-two")
			RecordOpenAIGroupQualityTrafficUsage(frozen, 62, tt.result)
			if tt.settles {
				require.Len(t, gate.settled, 1)
				require.Equal(t, "lease-one", gate.settled[0].LeaseID)
				require.Equal(t, int64(1000), gate.settleInputs[0])
			} else {
				require.Empty(t, gate.settled)
			}
			require.Empty(t, gate.trafficReleases, "unknown usage is never refunded as zero")
		})
	}
}
