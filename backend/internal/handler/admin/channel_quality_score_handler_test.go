package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qualityScoreHandlerRepo struct {
	qualityOverviewRepo
	config service.ChannelQualityScoreConfig
	err    error
	saves  int
	ids    []int64
	model  string
}

func (r *qualityScoreHandlerRepo) GetQualityScoreConfig(context.Context) (service.ChannelQualityScoreConfig, error) {
	return r.config, r.err
}
func (r *qualityScoreHandlerRepo) SaveQualityScoreConfig(_ context.Context, cfg *service.ChannelQualityScoreConfig) error {
	r.saves++
	if r.err != nil {
		return r.err
	}
	if cfg.Version != r.config.Version {
		return service.ErrChannelQualityScoreConflict
	}
	cfg.Version++
	r.config = *cfg
	return nil
}
func (r *qualityScoreHandlerRepo) ChannelQualityScores(_ context.Context, ids []int64, model string) (map[int64]*service.ChannelQualityScore, error) {
	r.ids, r.model = ids, model
	return map[int64]*service.ChannelQualityScore{62: {Status: "insufficient", Model: model}}, r.err
}

func qualityScoreTestRouter(r *qualityScoreHandlerRepo) *gin.Engine {
	h := NewChannelQualityHandler(r, nil)
	router := gin.New()
	router.GET("/config", h.ScoreConfig)
	router.PUT("/config", h.SaveScoreConfig)
	router.GET("/scores", h.Scores)
	return router
}

func TestChannelQualityScoreSettingsHTTP(t *testing.T) {
	r := &qualityScoreHandlerRepo{config: service.DefaultChannelQualityScoreConfig()}
	r.config.Version = 3
	router := qualityScoreTestRouter(r)
	request := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/config", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}
	w := request(http.MethodGet, "")
	require.Equal(t, http.StatusOK, w.Code)
	var got service.ChannelQualityScoreConfig
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, r.config, got)
	for _, bad := range []string{"{", "{}", `{"window_seconds":900,"max_requests":10,"min_samples":11}`} {
		require.Equal(t, http.StatusBadRequest, request(http.MethodPut, bad).Code)
	}
	require.Zero(t, r.saves)
	config := r.config
	config.Duration.Weight = 20
	body, err := json.Marshal(config)
	require.NoError(t, err)
	w = request(http.MethodPut, string(body))
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.EqualValues(t, 4, got.Version)
	require.Equal(t, 20.0, got.Duration.Weight)
	require.Equal(t, http.StatusConflict, request(http.MethodPut, string(body)).Code)
	r.err = errors.New("private database error")
	w = request(http.MethodGet, "")
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "private database error")
}

func TestChannelQualityScoreBatchHTTP(t *testing.T) {
	r := &qualityScoreHandlerRepo{}
	router := qualityScoreTestRouter(r)
	for _, query := range []string{"", "?account_ids=0", "?account_ids=-1", "?account_ids=1,", "?account_ids=9223372036854775808", "?account_ids=" + strings.Repeat("1,", 100) + "1", "?account_ids=62&model=" + strings.Repeat("a", 201)} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/scores"+query, nil))
		require.Equal(t, http.StatusBadRequest, w.Code, query)
	}
	require.Nil(t, r.ids)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/scores?account_ids=62,63,62&model=public-model", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []int64{62, 63}, r.ids)
	require.Equal(t, "public-model", r.model)
	var scores map[int64]*service.ChannelQualityScore
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &scores))
	require.Nil(t, scores[62].Value)
	require.Equal(t, "insufficient", scores[62].Status)
}
