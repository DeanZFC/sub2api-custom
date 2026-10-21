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
		r.GET("/rankings", h.GetRankingConfig)
		r.PUT("/rankings", h.SaveRankingConfig)
		r.GET("/invite", h.GetInviteConfig)
		r.PUT("/invite", h.SaveInviteConfig)
		r.GET("/lottery", h.GetLotteryConfig)
		r.PUT("/lottery", h.SaveLotteryConfig)
		r.GET("/lottery/draws", h.ListLotteryDraws)
		r.GET("/lottery/users/:user_id", h.LotteryState)
		for _, tc := range []struct{ method, path, body string }{
			{"PUT", "/rankings", `{"rewards_enabled":true,"token_rewards":[],"amount_rewards":[]}`},
			{"PUT", "/rankings", `{"token_rewards":[{"rank":11,"reward":{"type":"chances","quantity":1}}]}`},
			{"PUT", "/rankings", `{"token_rewards":[{"rank":1.5,"reward":{"type":"chances","quantity":1}}]}`},
			{"PUT", "/rankings", `{"token_rewards":[{"rank":1,"reward":{"type":"chances","quantity":1}},{"rank":1,"reward":{"type":"balance","amount":1}}]}`},
			{"PUT", "/rankings", `{"amount_rewards":[{"rank":1,"reward":{"type":"physical","quantity":1}}]}`},
			{"PUT", "/rankings", `{"amount_rewards":[{"rank":1,"reward":{"type":"balance","amount":1001}}]}`},
			{"PUT", "/checkin", `{"min_amount":2,"max_amount":1}`},
			{"PUT", "/checkin", `{"streak_days":-1}`},
			{"PUT", "/checkin", `{"growth":{"tasks":[{"id":"bad","name":"Invalid task","kind":"usage_count","target":1.5,"chances":1,"enabled":true}]}}`},
			{"PUT", "/checkin", `{"growth":{"tasks":[{"id":"bad","name":"Invalid task","kind":"recharge_total","target":1.001,"chances":1,"enabled":true}]}}`},
			{"PUT", "/checkin", `{"min_amount":0.1,"max_amount":0.5,"min_recharge_30d":0.001}`},
			{"PUT", "/lottery", `{"enabled":true,"recharge_step":0}`},
			{"PUT", "/lottery", `{"title":"test","recharge_step":10,"chances_per_step":1,"prizes":[{"name":"normal","amount":0,"probability_bps":10000}],"guarantee_enabled":true,"guarantee_every":0}`},
			{"PUT", "/lottery", `{"title":"test","recharge_step":10,"chances_per_step":1,"prizes":[{"name":"normal","amount":0,"probability_bps":10000}],"guarantee_enabled":true,"guarantee_every":1.5}`},
			{"PUT", "/lottery", `{"title":"test","recharge_step":10,"chances_per_step":1,"prizes":[{"name":"normal","amount":0,"probability_bps":10000}],"guarantee_enabled":true,"guarantee_every":10,"guarantee_prizes":[{"name":"invalid","amount":0,"probability_bps":10000}]}`},
			{"PUT", "/invite", `{"invite_reward":-1,"invite_daily_limit":10}`},
			{"PUT", "/invite", `{"invite_reward":1001,"invite_daily_limit":10}`},
			{"PUT", "/invite", `{"invite_reward":1,"invite_daily_limit":100001}`},
			{"PUT", "/invite", `{"invite_reward":1,"invite_daily_limit":-1}`},
			{"PUT", "/invite", `{"invite_reward":1,"invite_daily_limit":1.5}`},
			{"PUT", "/invite", `{"invite_reward":1,"invite_daily_limit":10,"min_recharge_total":-1}`},
			{"PUT", "/checkin", `{"recharge_window_days":0}`},
			{"PUT", "/checkin", `{"recharge_window_days":-1}`},
			{"PUT", "/checkin", `{"recharge_window_days":1.5}`},
			{"PUT", "/checkin", `{"recharge_window_days":3651}`},
			{"PUT", "/checkin", `{"recharge_window_days":"abc"}`},
			{"PUT", "/invite", `{"recharge_window_days":0}`},
			{"PUT", "/invite", `{"recharge_window_days":-1}`},
			{"PUT", "/invite", `{"recharge_window_days":1.5}`},
			{"PUT", "/invite", `{"recharge_window_days":3651}`},
			{"PUT", "/invite", `{"recharge_window_days":"abc"}`},
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
			for _, path := range []string{"/checkin", "/lottery", "/invite", "/rankings"} {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				require.Equal(t, 403, w.Code)
			}
		}
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
}
