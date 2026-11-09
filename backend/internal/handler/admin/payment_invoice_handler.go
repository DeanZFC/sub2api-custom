package admin

import (
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const invoicePDFMaxBytes = 10 << 20

func (h *PaymentHandler) GetInvoiceConfig(c *gin.Context) {
	cfg, err := h.paymentService.GetInvoiceConfig(c.Request.Context())
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, cfg)
}

func (h *PaymentHandler) UpdateInvoiceConfig(c *gin.Context) {
	var req struct {
		Enabled      *bool    `json:"enabled"`
		MinAmount    *float64 `json:"min_amount"`
		Instructions *string  `json:"instructions"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid invoice configuration")
		return
	}
	if err := h.configService.UpdatePaymentConfig(c.Request.Context(), service.UpdatePaymentConfigRequest{
		InvoiceEnabled: req.Enabled, InvoiceMinAmount: req.MinAmount, InvoiceInstructions: req.Instructions,
	}); response.ErrorFrom(c, err) {
		return
	}
	h.GetInvoiceConfig(c)
}

func (h *PaymentHandler) ListInvoiceRequests(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	items, total, err := h.paymentService.ListInvoiceRequests(c.Request.Context(), 0, page, pageSize, c.Query("status"))
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Paginated(c, items, int64(total), page, pageSize)
}

func (h *PaymentHandler) RejectInvoiceRequest(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req struct {
		AdminRemark string `json:"admin_remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid rejection reason")
		return
	}
	item, err := h.paymentService.RejectInvoiceRequest(c.Request.Context(), id, req.AdminRemark)
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, item)
}

func (h *PaymentHandler) IssueInvoiceRequest(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	// Allow bounded multipart metadata in addition to the PDF itself.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, invoicePDFMaxBytes+(64<<10))
	if err := c.Request.ParseMultipartForm(invoicePDFMaxBytes); err != nil {
		response.BadRequest(c, "Invalid upload; invoice PDF must not exceed 10 MiB")
		return
	}
	defer func() { _ = c.Request.MultipartForm.RemoveAll() }()
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.BadRequest(c, "Invoice PDF is required")
		return
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, invoicePDFMaxBytes+1))
	if err != nil || len(data) > invoicePDFMaxBytes {
		response.BadRequest(c, "Unable to read PDF or file exceeds 10 MiB")
		return
	}
	item, err := h.paymentService.IssueInvoiceRequest(c.Request.Context(), id,
		c.Request.FormValue("invoice_number"), c.Request.FormValue("admin_remark"), header.Filename, data)
	if response.ErrorFrom(c, err) {
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, item)
}

func (h *PaymentHandler) DownloadInvoicePDF(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	data, fileName, err := h.paymentService.GetInvoicePDF(c.Request.Context(), id, 0)
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
