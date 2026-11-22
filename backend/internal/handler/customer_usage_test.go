package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type billingFixture struct {
	service.CustomerUsageRepository
	filters   usagestats.UsageLogFilters
	position  service.UsageSyncPosition
	exportErr error
}

func (f *billingFixture) Key(_ context.Context, uid, id int64) (*service.CustomerUsageKey, error) {
	if uid == 7 && id == 9 {
		return &service.CustomerUsageKey{ID: 9, Name: "old"}, nil
	}
	return nil, nil
}
func (f *billingFixture) Sync(_ context.Context, filters usagestats.UsageLogFilters, pos service.UsageSyncPosition, _ int) ([]service.CustomerUsageRow, service.UsageSyncPosition, bool, error) {
	f.filters = filters
	f.position = pos
	return []service.CustomerUsageRow{}, service.UsageSyncPosition{XID: 75, ID: 90, Fence: 100}, true, nil
}
func (f *billingFixture) Export(_ context.Context, filters usagestats.UsageLogFilters, _ pagination.PaginationParams, visit func(service.CustomerUsageRow) error) error {
	f.filters = filters
	code := 206
	if err := visit(service.CustomerUsageRow{ID: 1, RequestID: "req", APIKeyID: 9, KeyName: " =1+1", Model: "model,with\nquotes\"", ActualCost: "0.0000000001", HTTPStatus: &code}); err != nil {
		return err
	}
	return f.exportErr
}

func billingRequest(t *testing.T, h *UsageHandler, id int64, path string) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if id > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
		}
		c.Next()
	})
	engine.GET("/sync", h.Sync)
	engine.GET("/export", h.ExportCSV)
	engine.GET("/keys", h.BillingKeys)
	engine.GET("/retention", h.Retention)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestCustomerUsageCursorBoundToSubjectAndFilters(t *testing.T) {
	f := &billingFixture{}
	h := &UsageHandler{billing: f, cursorKey: []byte("test-signing-key")}
	for _, path := range []string{"/keys", "/sync", "/export", "/retention"} {
		require.Equal(t, 401, billingRequest(t, h, 0, path).Code)
	}
	const query = "/sync?model=m&start_date=2026-01-01&timezone=UTC"
	w := billingRequest(t, h, 7, query+"&user_id=8")
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 7, f.filters.UserID)
	var result struct {
		Data struct {
			Cursor string `json:"next_cursor"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	token := url.QueryEscape(result.Data.Cursor)
	w = billingRequest(t, h, 7, query+"&cursor="+token)
	require.Equal(t, 200, w.Code)
	require.Equal(t, service.UsageSyncPosition{XID: 75, ID: 90, Fence: 100}, f.position)
	require.Equal(t, 400, billingRequest(t, h, 8, query+"&cursor="+token).Code)
	require.Equal(t, 400, billingRequest(t, h, 7, "/sync?model=z&cursor="+token).Code)
	require.Equal(t, 400, billingRequest(t, h, 7, query+"&cursor=x"+token).Code)
	require.Equal(t, 404, billingRequest(t, h, 8, "/sync?api_key_id=9&user_id=7").Code)
	require.Equal(t, 404, billingRequest(t, h, 8, "/export?api_key_id=9").Code)
	require.Equal(t, 200, billingRequest(t, h, 7, "/sync?api_key_id=9").Code)
}
func TestCustomerUsageCSVIsAtomicAndScoped(t *testing.T) {
	f := &billingFixture{}
	h := &UsageHandler{billing: f, exportSlots: make(chan struct{}, 1)}
	w := billingRequest(t, h, 7, "/export?user_id=100&model=m")
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 7, f.filters.UserID)
	require.Equal(t, "m", f.filters.Model)
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\uFEFF"))).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	values := map[string]string{}
	for i, col := range records[0] {
		values[col] = records[1][i]
	}
	require.Equal(t, "' =1+1", values["API Key Name"])
	require.Equal(t, "model,with\nquotes\"", values["Model"])
	require.Equal(t, "0.0000000001", values["Billed Cost"])
	require.Equal(t, "206", values["HTTP Status"])
	require.Equal(t, "req", values["Request ID"])
	require.NotContains(t, values, "account_id")
	require.NotContains(t, values, "IP Address")
	for _, tc := range []struct {
		err  error
		code int
	}{{service.ErrUsageExportBusy, 429}, {errCSVSize, 413}, {context.DeadlineExceeded, 504}, {errors.New("db read failed"), 500}} {
		f.exportErr = tc.err
		w = billingRequest(t, h, 7, "/export")
		require.Equal(t, tc.code, w.Code)
		require.NotContains(t, w.Body.String(), "Billed Cost")
		require.Empty(t, w.Header().Get("Content-Disposition"))
	}
}
