package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBatchImageErrorMessageOverridePreservesLocalErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"submit provider failure", service.ErrBatchImageProviderSubmitFailed, "统一提示"},
		{"provider item failure", service.ErrBatchImageItemFailed, "统一提示"},
		{"local balance", service.ErrBatchImageInsufficientBalance, "insufficient balance for batch image hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/images/batches/task/items/item/content", nil).
				WithContext(service.WithUpstreamErrorMessage(context.Background(), "统一提示"))
			batchImageError(c, tc.err)
			require.Contains(t, writer.Body.String(), tc.want)
		})
	}
}
