package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityUsageContextFreezesEvidenceAcrossWSRetry(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request.Header.Set("Upgrade", "websocket")
	BeginOpsStreamTurn(c, 1)
	SetGroupQualityTrafficGroup(c, 9)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte("{\"model\":\"public-first\"}"))
	connectionCtx := c.Request.Context()
	firstUsageCtx := PassiveQualityUsageContext(connectionCtx, context.Background())
	BeginOpsStreamTurn(c, 2)
	SetGroupQualityTrafficModel(c.Request.Context(), "public-second")
	beforeRetry, _ := GroupQualityTrafficRequestFromContext(connectionCtx)
	ResumeOpsStreamTurn(c, 1)
	afterRetry, _ := GroupQualityTrafficRequestFromContext(connectionCtx)
	require.Equal(t, beforeRetry, afterRetry, "local WS turn numbering reset cannot change the evidence identity")
	frozen, _ := GroupQualityTrafficRequestFromContext(firstUsageCtx)
	require.Equal(t, "public-first", frozen.Model)
	require.EqualValues(t, 9, frozen.GroupID)
	require.NotEqual(t, beforeRetry.Key, frozen.Key)
	BeginOpsStreamTurn(c, 2)
	next, _ := GroupQualityTrafficRequestFromContext(connectionCtx)
	require.NotEqual(t, beforeRetry.Key, next.Key, "new logical turn after retry starts its own evidence identity")
}
