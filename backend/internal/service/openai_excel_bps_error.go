package service

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"strings"
	"time"
)

func writeOpenAICompactSSEFailureMessageParam(c *gin.Context, statusCode int, errType, message, param string) {
	if c == nil {
		return
	}
	MarkOpsStreamError(c, errType, message, statusCode)
	payload, err := json.Marshal(map[string]any{
		"type":            "response.failed",
		"sequence_number": 0,
		"response": map[string]any{
			"id":     "resp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
			"object": "response",
			// 严格客户端把 created_at 当必填字段，缺失会反序列化失败，
			// 终止事件就白发了（退化成盲重连）。与 writeResponsesFailedSSE 对齐。
			"created_at": time.Now().Unix(),
			"status":     "failed",
			"output":     []any{},
			"error": func() map[string]any {
				errorBody := map[string]any{
					"code":    errType,
					"message": message,
				}
				if param != "" {
					errorBody["param"] = param
				}
				return errorBody
			}(),
		},
	})
	if err != nil {
		return
	}
	_, _ = c.Writer.Write([]byte("event: response.failed\ndata: "))
	_, _ = c.Writer.Write(payload)
	_, _ = c.Writer.Write([]byte("\n\n"))
	c.Writer.Flush()
}
