package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Admission errors are terminal, including after earlier account failures.
func handleAPIKeyFallbackAdmissionError(c *gin.Context, err error, streamStarted bool) bool {
	var admission *service.APIKeyFallbackAdmissionError
	if !errors.As(err, &admission) {
		return false
	}
	cls := classifySelectionFailureError(err, noAccountErrorClassification{})
	_, _, _, retryAfter := billingErrorDetails(admission.Cause)
	if retryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(retryAfter))
	}
	if strings.Contains(c.Request.URL.Path, "/v1beta/") {
		googleErrorWithType(c, cls.Status, cls.ErrType, "", cls.Message)
		return true
	}
	payload := gin.H{"type": "error", "error": gin.H{"type": cls.ErrType, "message": cls.Message}}
	if streamStarted {
		service.MarkOpsStreamError(c, cls.ErrType, cls.Message, cls.Status)
		raw, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", raw)
		c.Writer.Flush()
	} else {
		c.JSON(cls.Status, payload)
	}
	return true
}
