package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountRequestStatsCaptureRepo struct {
	service.OpsRepository
	ids []int64
}

func (r *accountRequestStatsCaptureRepo) GetAccountRequestStats(_ context.Context, ids []int64, start, end time.Time) (map[string]*service.OpsAccountRequestStats, error) {
	r.ids = ids
	return map[string]*service.OpsAccountRequestStats{}, nil
}

func TestOpsAccountRequestStatsHandlerValidatesAndReturnsNullMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &accountRequestStatsCaptureRepo{}
	handler := NewOpsHandler(service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil))
	router := gin.New()
	router.POST("/stats", handler.GetAccountRequestStatsBatch)
	for _, body := range []string{`{`, `{}`, `{"account_ids":[]}`, `{"account_ids":[0]}`, `{"account_ids":[-2]}`, `{"account_ids":[1.5]}`, `{"account_ids":[` + strings.Repeat("1,", 100) + `1]}`} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/stats", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusBadRequest, recorder.Code, body)
	}
	require.Empty(t, repo.ids)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/stats?time_range=24h", bytes.NewBufferString(`{"account_ids":[2,1,2]}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{2, 1}, repo.ids)
	var envelope struct {
		Data service.OpsAccountRequestStatsBatch `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Stats, 2)
	for _, stats := range envelope.Data.Stats {
		require.Equal(t, time.Hour, stats.WindowEnd.Sub(stats.WindowStart))
		require.Nil(t, stats.CacheRate)
		require.Nil(t, stats.AvgFirstTokenMs)
		require.Nil(t, stats.AvgDurationMs)
	}
	require.Contains(t, recorder.Body.String(), `"cache_rate":null`)
}
