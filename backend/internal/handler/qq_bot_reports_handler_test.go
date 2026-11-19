package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQQRewardReportAdminAndInvalidQueries(t *testing.T) {
	for _, role := range []string{"", "user", "admin"} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), role); c.Next() })
		r.GET("/reports", h.RewardReport)
		queries := []string{"?page=0", "?page=-1", "?page=bad", "?page_size=0", "?page_size=101", "?kind=unknown", "?reward_type=money", "?start_date=2026-02-29", "?start_date=2026-11-19&end_date=2026-11-18", "?start_date=2024-01-01&end_date=2025-01-01"}
		if role != "admin" {
			queries = append(queries, "")
		}
		for _, query := range queries {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/reports"+query, nil))
			want := 403
			if role == "admin" {
				want = 400
			}
			require.Equal(t, want, w.Code, query)
		}
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
}

func TestQQRewardReportNoStoreAndEmptyResponse(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	cfg := qqbot.DefaultConfig()
	cfg.AppID = "app"
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT value FROM settings").WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
	mock.ExpectQuery("WITH draw_records").WillReturnRows(sqlmock.NewRows([]string{"summary", "daily", "kinds", "users", "items"}).AddRow(`{"reward_count":0}`, `[]`, `[]`, `[]`, `[]`))
	mock.ExpectCommit()
	h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), "admin"); c.Next() })
	r.GET("/reports", h.RewardReport)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/reports?start_date=2026-11-18&end_date=2026-11-18", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Contains(t, w.Body.String(), `"items":[]`)
	require.Contains(t, w.Body.String(), `"date":"2026-11-18"`)
	require.NoError(t, mock.ExpectationsWereMet())
}
