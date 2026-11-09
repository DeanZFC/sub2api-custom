package handler

import (
	"fmt"
	"math"
	"mime"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *PaymentHandler) GetInvoiceConfig(c *gin.Context) {
	if _, ok := requireAuth(c); !ok {
		return
	}
	cfg, err := h.paymentService.GetInvoiceConfig(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, cfg)
}

func (h *PaymentHandler) ListInvoiceOrders(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, availableAmount, err := h.paymentService.ListInvoiceOrders(c.Request.Context(), subject.UserID, page, pageSize)
	if response.ErrorFrom(c, err) {
		return
	}
	pages := max(1, int(math.Ceil(float64(total)/float64(pageSize))))
	response.Success(c, gin.H{
		"items": items, "total": total, "page": page, "page_size": pageSize,
		"pages": pages, "available_amount": availableAmount,
	})
}

func (h *PaymentHandler) ListInvoiceRequests(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.paymentService.ListInvoiceRequests(c.Request.Context(), subject.UserID, page, pageSize, "")
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Paginated(c, items, int64(total), page, pageSize)
}

func (h *PaymentHandler) CreateInvoiceRequest(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	// Bound a request before decoding the potentially large order-ID array.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10)
	var req service.CreateInvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid invoice application")
		return
	}
	item, err := h.paymentService.CreateInvoiceRequest(c.Request.Context(), subject.UserID, req)
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Created(c, item)
}

func (h *PaymentHandler) DownloadInvoicePDF(c *gin.Context) {
	subject, ok := requireAuth(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid invoice ID")
		return
	}
	data, fileName, err := h.paymentService.GetInvoicePDF(c.Request.Context(), id, subject.UserID)
	if response.ErrorFrom(c, err) {
		return
	}
	if fileName == "" {
		fileName = fmt.Sprintf("invoice-%d.pdf", id)
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	c.Data(http.StatusOK, "application/pdf", data)
}
