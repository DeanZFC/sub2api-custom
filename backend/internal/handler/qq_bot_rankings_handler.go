package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *QQBotHandler) GetRankingConfig(c *gin.Context) {
	if !requireQQBotAdmin(c) {
		return
	}
	cfg, err := h.bot.RankingConfig(c.Request.Context())
	if err != nil {
		response.InternalError(c, "读取排行榜配置失败")
		return
	}
	response.Success(c, cfg)
}

func (h *QQBotHandler) SaveRankingConfig(c *gin.Context) {
	if !requireQQBotAdmin(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 65536)
	var cfg qqbot.RankingConfig
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "排行榜配置格式不正确")
		return
	}
	saved, err := h.bot.SaveRankingConfig(c.Request.Context(), cfg)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, saved)
}
