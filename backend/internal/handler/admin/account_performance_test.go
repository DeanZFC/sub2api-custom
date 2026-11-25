package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type performanceBatchReader struct {
	service.AccountPerformanceRepository
	withMetrics bool
}

func (r *performanceBatchReader) Read(_ context.Context, ids []int64, now time.Time, withMetrics bool) (map[int64]service.AccountPerformance, error) {
	r.withMetrics = withMetrics
	state := service.AccountPerformance{AccountID: ids[0], Phase: service.PerformanceMonitoring}
	if withMetrics {
		state.Metrics.WindowStart, state.Metrics.WindowEnd = state.MetricWindow(now)
	}
	return map[int64]service.AccountPerformance{ids[0]: state}, nil
}

func TestAccountPerformanceBatchMetricsOptional(t *testing.T) {
	for _, tc := range []struct {
		request string
		metrics bool
	}{
		{`{"account_ids":[7]}`, true},
		{`{"account_ids":[7],"include_metrics":false}`, false},
	} {
		repo := &performanceBatchReader{}
		h := AccountHandler{performance: service.NewAccountPerformanceService(repo)}
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodPost, "/accounts/performance", strings.NewReader(tc.request))
		c.Request.Header.Set("Content-Type", "application/json")
		h.AccountPerformanceBatch(c)
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, tc.metrics, repo.withMetrics)
		require.Contains(t, response.Body.String(), `"phase":"monitoring"`)
		require.Equal(t, tc.metrics, strings.Contains(response.Body.String(), `"metrics":`))
	}
}
