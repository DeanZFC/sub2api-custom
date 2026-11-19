package handler

import (
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/qqbot"
	"github.com/gin-gonic/gin"
)

func (h *QQBotHandler) RewardReport(c *gin.Context) {
	if !requireQQBotAdmin(c) {
		return
	}
	var filter qqbot.ReportFilter
	if c.ShouldBindQuery(&filter) != nil || (c.Query("page") != "" && filter.Page == 0) || (c.Query("page_size") != "" && filter.PageSize == 0) {
		response.BadRequest(c, qqbot.ErrReportFilter.Error())
		return
	}
	filter, err := filter.Normalize(time.Now())
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	out, err := h.bot.RewardReport(c.Request.Context(), filter)
	if err != nil {
		if errors.Is(err, qqbot.ErrReportFilter) {
			response.BadRequest(c, err.Error())
		} else {
			response.InternalError(c, "读取奖励报表失败")
		}
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, out)
}
