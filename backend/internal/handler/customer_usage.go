package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) checkBillingKey(c *gin.Context, userID, keyID int64) bool {
	key, err := h.billing.Key(c.Request.Context(), userID, keyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return false
	}
	if key == nil {
		response.NotFound(c, "Billing key not found")
		return false
	}
	return true
}

func billingSubject(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "private, no-store")
	s, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || s.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return 0, false
	}
	return s.UserID, true
}

func (h *UsageHandler) BillingKeys(c *gin.Context) {
	uid, ok := billingSubject(c)
	if !ok {
		return
	}
	page, size := response.ParsePagination(c)
	keys, total, err := h.billing.Keys(c.Request.Context(), uid, pagination.PaginationParams{Page: page, PageSize: size})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, keys, total, page, size)
}

func (h *UsageHandler) Retention(c *gin.Context) {
	if _, ok := billingSubject(c); !ok {
		return
	}
	days, err := h.opsService.RequestRetentionDays(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"request_retention_days": days})
}

func (h *UsageHandler) cursorMAC(filters usagestats.UsageLogFilters, payload []byte) []byte {
	mac := hmac.New(sha256.New, h.cursorKey)
	mac.Write([]byte("customer-usage-v1\x00"))
	_ = json.NewEncoder(mac).Encode(filters)
	mac.Write(payload)
	return mac.Sum(nil)
}

func (h *UsageHandler) encodePosition(filters usagestats.UsageLogFilters, pos service.UsageSyncPosition) string {
	payload, _ := json.Marshal(pos)
	return base64.RawURLEncoding.EncodeToString(append(payload, h.cursorMAC(filters, payload)...))
}

func (h *UsageHandler) decodePosition(filters usagestats.UsageLogFilters, token string) (pos service.UsageSyncPosition, ok bool) {
	if token == "" {
		return pos, true
	}
	if len(token) > 512 {
		return pos, false
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) <= sha256.Size {
		return pos, false
	}
	payload, sig := data[:len(data)-sha256.Size], data[len(data)-sha256.Size:]
	if !hmac.Equal(h.cursorMAC(filters, payload), sig) {
		return pos, false
	}
	err = json.Unmarshal(payload, &pos)
	return pos, err == nil
}

func (h *UsageHandler) Sync(c *gin.Context) {
	if _, ok := billingSubject(c); !ok {
		return
	}
	parsed, ok := h.parseUserUsageFilters(c, false)
	if !ok {
		return
	}
	if len(h.cursorKey) == 0 {
		response.InternalError(c, "Cursor signing unavailable")
		return
	}
	pos, ok := h.decodePosition(parsed.Filters, c.Query("cursor"))
	if !ok {
		response.BadRequest(c, "Invalid cursor for this account or filter")
		return
	}
	_, limit := response.ParsePagination(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	rows, next, more, err := h.billing.Sync(ctx, parsed.Filters, pos, limit)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"items": rows, "next_cursor": h.encodePosition(parsed.Filters, next), "has_more": more})
}

func csvText(s string) string {
	trimmed := strings.TrimLeftFunc(s, unicode.IsSpace)
	if strings.HasPrefix(s, "\t") || strings.HasPrefix(s, "\r") || strings.HasPrefix(s, "\n") || (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + s
	}
	return s
}

func csvInt(n *int) string {
	if n == nil {
		return ""
	}
	return strconv.Itoa(*n)
}

var customerCSVHeader = []string{
	"ID", "Time", "Request ID", "API Key ID", "API Key Name", "Model",
	"Reasoning Effort", "Inbound Endpoint", "Type", "Billing Mode",
	"Input Tokens", "Output Tokens", "Cache Read Tokens", "Cache Creation Tokens",
	"Rate Multiplier", "Billed Cost", "Original Cost", "First Token (ms)", "Duration (ms)", "HTTP Status",
}

func customerCSVRow(r service.CustomerUsageRow) []string {
	return []string{
		strconv.FormatInt(r.ID, 10), r.CreatedAt.UTC().Format(time.RFC3339Nano), csvText(r.RequestID),
		strconv.FormatInt(r.APIKeyID, 10), csvText(r.KeyName), csvText(r.Model),
		csvText(r.ReasoningEffort), csvText(r.Endpoint), csvText(r.RequestType), csvText(r.BillingMode),
		strconv.Itoa(r.InputTokens), strconv.Itoa(r.OutputTokens), strconv.Itoa(r.CacheReadTokens), strconv.Itoa(r.CacheCreationTokens),
		r.RateMultiplier, r.ActualCost, r.TotalCost, csvInt(r.FirstTokenMS), csvInt(r.DurationMS), csvInt(r.HTTPStatus),
	}
}

const maxUsageCSVSize = 128 << 20

var errCSVSize = errors.New("CSV exceeds 128 MiB; narrow the date range")

type cappedCSVFile struct {
	dest io.Writer
	size int64
}

func (w *cappedCSVFile) Write(p []byte) (int, error) {
	if w.size+int64(len(p)) > maxUsageCSVSize {
		return 0, errCSVSize
	}
	n, err := w.dest.Write(p)
	w.size += int64(n)
	return n, err
}

func (h *UsageHandler) ExportCSV(c *gin.Context) {
	if _, ok := billingSubject(c); !ok {
		return
	}
	parsed, ok := h.parseUserUsageFilters(c, false)
	if !ok {
		return
	}
	select {
	case h.exportSlots <- struct{}{}:
		defer func() { <-h.exportSlots }()
	default:
		c.Header("Retry-After", "5")
		response.Error(c, 429, "Usage export busy")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	// Finish and validate the snapshot before responding. A timed-out database
	// query must never become a successful download of an incomplete file.
	file, err := os.CreateTemp("", "sub2api-usage-*.csv")
	if err != nil {
		response.InternalError(c, "Cannot prepare export")
		return
	}
	// On Unix an open file can be unlinked immediately, including on process exit.
	if err := os.Remove(file.Name()); err != nil {
		defer os.Remove(file.Name())
	}
	defer file.Close()
	output := &cappedCSVFile{dest: file}
	if _, err = io.WriteString(output, "\uFEFF"); err != nil {
		response.InternalError(c, "Cannot write export")
		return
	}
	writer := csv.NewWriter(output)
	err = writer.Write(customerCSVHeader)
	sort := pagination.PaginationParams{SortBy: c.DefaultQuery("sort_by", "created_at"), SortOrder: c.DefaultQuery("sort_order", "desc")}
	if err == nil {
		err = h.billing.Export(ctx, parsed.Filters, sort, func(row service.CustomerUsageRow) error { return writer.Write(customerCSVRow(row)) })
	}
	writer.Flush()
	if err == nil {
		err = writer.Error()
	}
	switch {
	case errors.Is(err, service.ErrUsageExportBusy):
		c.Header("Retry-After", "5")
		response.Error(c, 429, "Usage export busy")
		return
	case errors.Is(err, errCSVSize):
		response.Error(c, 413, errCSVSize.Error())
		return
	case errors.Is(err, context.DeadlineExceeded):
		response.Error(c, 504, "Usage export timed out; narrow the date range")
		return
	case err != nil:
		response.ErrorFrom(c, err)
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		response.InternalError(c, "Cannot read export")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="usage.csv"`)
	c.DataFromReader(http.StatusOK, output.size, "text/csv; charset=utf-8", file, nil)
}
