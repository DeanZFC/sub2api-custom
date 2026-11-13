package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *ProxyHandler) GetOVHIPv6Storage(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if adminaccess.RestrictedFromContext(c.Request.Context()) != nil {
		response.Error(c, 403, "OVH IPv6 存储统计仅限完整管理员访问")
		return
	}
	svc, ok := h.adminService.(service.OVHIPv6StorageAdmin)
	if !ok {
		response.Error(c, 503, "OVH IPv6 存储统计不可用")
		return
	}
	stats, err := svc.GetOVHIPv6Storage(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}
