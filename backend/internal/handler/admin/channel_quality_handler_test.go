package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type qualityOverviewRepo struct {
	filter service.ChannelQualityFilter
	calls  int
}

func (r *qualityOverviewRepo) ListQualityAccounts(_ context.Context, f service.ChannelQualityFilter) (*service.ChannelQualityPage, error) {
	r.calls++
	r.filter = f
	return &service.ChannelQualityPage{Items: []service.ChannelQualityAccount{}}, nil
}
func TestChannelQualityOverviewFilters(t *testing.T) {
	r := &qualityOverviewRepo{}
	h := NewChannelQualityHandler(r, nil)
	router := gin.New()
	router.GET("/quality", h.Overview)
	for _, path := range []string{"/quality?account_id=-1", "/quality?after=abc", "/quality?group_id=999999999999999999999"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 400, w.Code)
	}
	require.Zero(t, r.calls)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/quality?account_id=62&model=A&problems_only=true&after=1", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, service.ChannelQualityFilter{AccountID: 62, Model: "A", ProblemsOnly: true, After: 1, Limit: 50}, r.filter)
}
