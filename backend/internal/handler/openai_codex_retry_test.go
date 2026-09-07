//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAICodexRetryExhaustedPreservesUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := `{"error":{"type":"upstream_unavailable","code":"auth_unavailable","message":"no auth available; server_is_overloaded","extra":{"retry_after":30}}}`
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "committed_stream"}[streaming], func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			if streaming {
				_, err := c.Writer.WriteString(": ping\n\n")
				require.NoError(t, err)
				c.Writer.Flush()
			}
			(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
				Reason: service.CodexPreOutputRetryReason, StatusCode: 503, ClientStatusCode: 503,
				ResponseBody: []byte(payload), ResponseHeaders: http.Header{"Content-Type": {"application/json"}},
				ClientMessage: "must not replace the original payload",
			}, streaming)
			if !streaming {
				require.Equal(t, 503, recorder.Code)
				require.Equal(t, payload, recorder.Body.String())
			} else {
				require.Equal(t, 200, recorder.Code)
				require.Contains(t, recorder.Body.String(), strings.TrimSuffix(payload, "}"))
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "data:"))
			}
			require.NotContains(t, recorder.Body.String(), "must not replace")
			require.NotContains(t, recorder.Body.String(), `"server_error"`)
		})
	}
}
