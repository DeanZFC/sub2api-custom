package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQQActivitiesRequireAdminAndValidateBeforeWrites(t *testing.T) {
	for _, role := range []string{"", "user", "admin"} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		h := NewQQBotHandler(qqbot.New(db, qqTestEncryptor{}, true, nil))
		r := gin.New()
		r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUserRole), role); c.Next() })
		r.GET("/checkin", h.GetCheckinConfig)
		r.PUT("/checkin", h.SaveCheckinConfig)
		r.GET("/lottery", h.GetLotteryConfig)
		r.PUT("/lottery", h.SaveLotteryConfig)
		r.GET("/lottery/draws", h.ListLotteryDraws)
		r.GET("/lottery/users/:user_id", h.LotteryState)
		for _, tc := range []struct{ method, path, body string }{
			{"PUT", "/checkin", `{"min_amount":2,"max_amount":1}`},
			{"PUT", "/lottery", `{"enabled":true,"recharge_step":0}`},
			{"GET", "/lottery/draws?page=-1", ""}, {"GET", "/lottery/users/invalid", ""},
		} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			want := 403
			if role == "admin" {
				want = 400
			}
			require.Equal(t, want, w.Code)
		}
		if role != "admin" {
			for _, path := range []string{"/checkin", "/lottery"} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				require.Equal(t, 403, w.Code)
			}
		}
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
}
