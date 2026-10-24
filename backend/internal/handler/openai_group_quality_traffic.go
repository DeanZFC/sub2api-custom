package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

const groupQualityBudgetErrorCode = "group_quality_budget_exhausted"
const groupQualityBudgetErrorMessage = "Group quality validation capacity is temporarily exhausted; please retry after 5 seconds"

// A connection that began on an emergency lease must try ordinary admission
// on every later turn. Recovery can become healthy while the WS stays open.
func (h *OpenAIGatewayHandler) tryAcquireOpenAIWSTurnAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, account **service.Account) (func(), bool, error) {
	fallback, authorized := service.GroupQualityFallbackFromContext(ctx)
	normalCtx := service.WithoutGroupQualityFallback(ctx)
	release, acquired, err := h.concurrencyHelper.TryAcquireAccountSlot(normalCtx, accountID, maxConcurrency, account)
	if err == nil || (!errors.Is(err, service.ErrAccountAdmissionDenied) && !errors.Is(err, service.ErrGroupQualityTrafficBudget)) {
		return release, acquired, err
	}
	traffic, valid := service.GroupQualityTrafficRequestFromContext(ctx)
	if !authorized || !valid || ctx.Err() != nil || fallback.AccountID != accountID || fallback.GroupID != traffic.GroupID || strings.TrimSpace(traffic.Model) == "" {
		return release, acquired, err
	}
	// Preserve the established credential and group; only the current public
	// model is refreshed. Live admission rechecks hard gates and lease capacity.
	fallback.Model = traffic.Model
	return h.concurrencyHelper.TryAcquireAccountSlot(service.WithGroupQualityFallback(normalCtx, fallback), accountID, maxConcurrency, account)
}

func (h *OpenAIGatewayHandler) handleGroupQualityBudgetError(c *gin.Context, err error, streamStarted bool) bool {
	if !errors.Is(err, service.ErrGroupQualityTrafficBudget) {
		return false
	}
	markOpsRoutingCapacityLimited(c)
	c.Header("Retry-After", "5")
	if c.Request != nil && strings.HasSuffix(c.Request.URL.Path, "/messages") {
		payload := gin.H{"type": "error", "error": gin.H{"type": "rate_limit_error", "code": groupQualityBudgetErrorCode, "message": groupQualityBudgetErrorMessage}}
		if streamStarted {
			service.MarkOpsStreamError(c, "rate_limit_error", groupQualityBudgetErrorMessage, http.StatusServiceUnavailable)
			encoded, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", encoded)
			c.Writer.Flush()
		} else {
			c.JSON(http.StatusServiceUnavailable, payload)
		}
		return true
	}
	h.handleStreamingAwareErrorWithCode(c, http.StatusServiceUnavailable, "rate_limit_error", groupQualityBudgetErrorCode, groupQualityBudgetErrorMessage, streamStarted, false)
	return true
}

func handleGroupQualityBudgetWSError(c *gin.Context, conn *coderws.Conn, err error) bool {
	if !errors.Is(err, service.ErrGroupQualityTrafficBudget) {
		return false
	}
	markOpsRoutingCapacityLimited(c)
	if conn != nil {
		payload, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": "rate_limit_error", "code": groupQualityBudgetErrorCode, "message": groupQualityBudgetErrorMessage, "retry_after": 5}})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = conn.Write(ctx, coderws.MessageText, payload)
		cancel()
		closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, groupQualityBudgetErrorCode)
	}
	return true
}
