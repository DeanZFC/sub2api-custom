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

const accountQualityHeldErrorCode = "account_quality_paused"
const accountQualityHeldErrorMessage = "Account is temporarily paused by quality protection pending active checks"

func (h *OpenAIGatewayHandler) handleAccountQualityHeldError(c *gin.Context, err error, streamStarted bool) bool {
	if !errors.Is(err, service.ErrAccountQualityHeld) {
		return false
	}
	markOpsRoutingCapacityLimited(c)
	if c.Request != nil && strings.HasSuffix(c.Request.URL.Path, "/messages") {
		payload := gin.H{"type": "error", "error": gin.H{"type": "server_error", "code": accountQualityHeldErrorCode, "message": accountQualityHeldErrorMessage}}
		if streamStarted {
			service.MarkOpsStreamError(c, "server_error", accountQualityHeldErrorMessage, http.StatusServiceUnavailable)
			encoded, _ := json.Marshal(payload)
			_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", encoded)
			c.Writer.Flush()
		} else {
			c.JSON(http.StatusServiceUnavailable, payload)
		}
		return true
	}
	h.handleStreamingAwareErrorWithCode(c, http.StatusServiceUnavailable, "server_error", accountQualityHeldErrorCode, accountQualityHeldErrorMessage, streamStarted, false)
	return true
}

func handleAccountQualityHeldWSError(c *gin.Context, conn *coderws.Conn, err error) bool {
	if !errors.Is(err, service.ErrAccountQualityHeld) {
		return false
	}
	markOpsRoutingCapacityLimited(c)
	if conn != nil {
		payload, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": "server_error", "code": accountQualityHeldErrorCode, "message": accountQualityHeldErrorMessage}})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = conn.Write(ctx, coderws.MessageText, payload)
		cancel()
		closeOpenAIClientWS(conn, coderws.StatusTryAgainLater, accountQualityHeldErrorCode)
	}
	return true
}
