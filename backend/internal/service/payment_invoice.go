package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/mail"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/invoicerequest"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const maxInvoiceOrderCount = 500
const maxInvoicePDFBytes = 10 << 20
const maxInvoicePageSize = 1000

type InvoiceConfig struct {
	Enabled      bool    `json:"enabled"`
	MinAmount    float64 `json:"min_amount"`
	Instructions string  `json:"instructions"`
	Currency     string  `json:"currency"`
}

type InvoiceOrder struct {
	ID          int64      `json:"id"`
	PayAmount   float64    `json:"pay_amount"`
	Currency    string     `json:"currency"`
	CompletedAt *time.Time `json:"completed_at"`
}

// InvoiceRequest deliberately excludes the stored PDF, including in admin lists.
type InvoiceRequest struct {
	ID            int64      `json:"id"`
	UserID        int64      `json:"user_id"`
	UserEmail     string     `json:"user_email"`
	OrderIDs      []int64    `json:"order_ids"`
	Amount        float64    `json:"amount"`
	Currency      string     `json:"currency"`
	BuyerType     string     `json:"buyer_type"`
	Title         string     `json:"title"`
	TaxID         string     `json:"tax_id"`
	Emails        []string   `json:"emails"`
	Remark        string     `json:"remark"`
	Status        string     `json:"status"`
	AdminRemark   string     `json:"admin_remark"`
	InvoiceNumber string     `json:"invoice_number"`
	FileName      string     `json:"file_name"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	IssuedAt      *time.Time `json:"issued_at,omitempty"`
}

type CreateInvoiceRequest struct {
	OrderIDs  []int64  `json:"order_ids"`
	BuyerType string   `json:"buyer_type"`
	Title     string   `json:"title"`
	TaxID     string   `json:"tax_id"`
	Emails    []string `json:"emails"`
	Remark    string   `json:"remark"`
}

func (s *PaymentService) GetInvoiceConfig(ctx context.Context) (*InvoiceConfig, error) {
	cfg, err := s.configService.GetPaymentConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &InvoiceConfig{Enabled: cfg.InvoiceEnabled, MinAmount: cfg.InvoiceMinAmount, Instructions: cfg.InvoiceInstructions, Currency: "CNY"}, nil
}

func invoiceEligiblePredicates(userID int64) []predicate.PaymentOrder {
	return []predicate.PaymentOrder{
		paymentorder.UserIDEQ(userID), paymentorder.OrderTypeEQ(payment.OrderTypeBalance),
		paymentorder.StatusEQ(OrderStatusCompleted), paymentorder.RefundAmountEQ(0),
		paymentorder.RefundAtIsNil(), paymentorder.RefundRequestedAtIsNil(), paymentorder.InvoiceRequestIDIsNil(),
	}
}

// Historical foreign-currency orders without a currency snapshot are ambiguous.
// Only the CNY-only methods may safely fall back to CNY for legacy orders.
func invoiceOrderIsCNY(order *dbent.PaymentOrder) bool {
	if order == nil {
		return false
	}
	if raw, exists := order.ProviderSnapshot["currency"]; exists {
		currency, ok := raw.(string)
		return ok && strings.EqualFold(strings.TrimSpace(currency), "CNY")
	}
	providerKey := strings.TrimSpace(psStringValue(order.ProviderKey))
	if snapshot := psOrderProviderSnapshot(order); snapshot != nil && snapshot.ProviderKey != "" {
		providerKey = snapshot.ProviderKey
	}
	if providerKey == payment.TypeStripe || providerKey == payment.TypeAirwallex {
		return false
	}
	switch payment.GetBasePaymentType(order.PaymentType) {
	case payment.TypeAlipay, payment.TypeWxpay, payment.TypeEasyPay:
		return true
	default:
		return false
	}
}

func invoicePayAmount(order *dbent.PaymentOrder) (decimal.Decimal, bool) {
	if order == nil || math.IsNaN(order.PayAmount) || math.IsInf(order.PayAmount, 0) || order.PayAmount <= 0 {
		return decimal.Zero, false
	}
	amount := decimal.NewFromFloat(order.PayAmount)
	return amount, amount.Equal(amount.Round(2))
}

func (s *PaymentService) ListInvoiceOrders(ctx context.Context, userID int64, page, pageSize int) ([]*InvoiceOrder, int, float64, error) {
	orders, err := s.entClient.PaymentOrder.Query().Where(invoiceEligiblePredicates(userID)...).
		Select(paymentorder.FieldID, paymentorder.FieldPayAmount, paymentorder.FieldCompletedAt,
			paymentorder.FieldProviderSnapshot, paymentorder.FieldProviderKey, paymentorder.FieldPaymentType).
		Order(dbent.Desc(paymentorder.FieldCompletedAt), dbent.Desc(paymentorder.FieldID)).All(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	eligible := make([]*InvoiceOrder, 0, len(orders))
	totalAmount := decimal.Zero
	for _, order := range orders {
		amount, valid := invoicePayAmount(order)
		if !valid || !invoiceOrderIsCNY(order) {
			continue
		}
		totalAmount = totalAmount.Add(amount)
		eligible = append(eligible, &InvoiceOrder{ID: order.ID, PayAmount: order.PayAmount, Currency: "CNY", CompletedAt: order.CompletedAt})
	}
	page, pageSize = invoicePagination(page, pageSize)
	total := len(eligible)
	start := min((page-1)*pageSize, total)
	end := min(start+pageSize, total)
	return eligible[start:end], total, totalAmount.InexactFloat64(), nil
}

func invoicePagination(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxInvoicePageSize {
		pageSize = maxInvoicePageSize
	}
	// Bound multiplication even when called outside HTTP pagination parsing.
	if page > 1000000 {
		page = 1000000
	}
	return page, pageSize
}

func invoiceMetadataFields() []string {
	fields := make([]string, 0, len(invoicerequest.Columns)-1)
	for _, field := range invoicerequest.Columns {
		if field != invoicerequest.FieldPdfData {
			fields = append(fields, field)
		}
	}
	return fields
}

func (s *PaymentService) ListInvoiceRequests(ctx context.Context, userID int64, page, pageSize int, status string) ([]*InvoiceRequest, int, error) {
	query := s.entClient.InvoiceRequest.Query()
	if userID > 0 {
		query.Where(invoicerequest.UserIDEQ(userID))
	}
	if status != "" {
		if status != "pending" && status != "issued" && status != "rejected" {
			return nil, 0, infraerrors.BadRequest("INVALID_INVOICE_STATUS", "invalid invoice status")
		}
		query.Where(invoicerequest.StatusEQ(status))
	}
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	page, pageSize = invoicePagination(page, pageSize)
	items, err := query.Select(invoiceMetadataFields()...).Order(dbent.Desc(invoicerequest.FieldID)).Offset((page - 1) * pageSize).Limit(pageSize).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	results := make([]*InvoiceRequest, 0, len(items))
	for _, item := range items {
		results = append(results, invoiceRequestDTO(item))
	}
	return results, total, nil
}

func normalizeInvoiceApplication(req CreateInvoiceRequest) (CreateInvoiceRequest, error) {
	bad := func(message string) (CreateInvoiceRequest, error) {
		return req, infraerrors.BadRequest("INVALID_INVOICE_APPLICATION", message)
	}
	if len(req.OrderIDs) == 0 || len(req.OrderIDs) > maxInvoiceOrderCount {
		return bad("select between 1 and 500 orders")
	}
	ids := append([]int64(nil), req.OrderIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i, id := range ids {
		if id <= 0 || i > 0 && id == ids[i-1] {
			return bad("order IDs must be positive and unique")
		}
	}
	req.OrderIDs = ids
	req.Title, req.TaxID, req.Remark = strings.TrimSpace(req.Title), strings.ToUpper(strings.TrimSpace(req.TaxID)), strings.TrimSpace(req.Remark)
	if req.BuyerType != "personal" && req.BuyerType != "company" {
		return bad("buyer type must be personal or company")
	}
	if req.Title == "" || utf8.RuneCountInString(req.Title) > 200 || utf8.RuneCountInString(req.Remark) > 2000 || len(req.TaxID) > 30 {
		return bad("invalid invoice title, tax ID or remark length")
	}
	if req.BuyerType == "company" && req.TaxID == "" {
		return bad("company invoices require a tax ID")
	}
	for _, char := range req.TaxID {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return bad("tax ID must contain only letters and digits")
		}
	}
	if len(req.Emails) == 0 || len(req.Emails) > 10 {
		return bad("provide between 1 and 10 recipient email addresses")
	}
	emails := make([]string, 0, len(req.Emails))
	seen := make(map[string]bool)
	for _, raw := range req.Emails {
		email := strings.TrimSpace(raw)
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email || len(email) > 254 || !strings.Contains(email, "@") {
			return bad("invalid recipient email address")
		}
		key := strings.ToLower(email)
		if !seen[key] {
			emails = append(emails, email)
			seen[key] = true
		}
	}
	req.Emails = emails
	return req, nil
}

func (s *PaymentService) CreateInvoiceRequest(ctx context.Context, userID int64, input CreateInvoiceRequest) (*InvoiceRequest, error) {
	req, err := normalizeInvoiceApplication(input)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, infraerrors.Forbidden("FORBIDDEN", "invoice applications require an authenticated user")
	}
	cfg, err := s.GetInvoiceConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, infraerrors.Forbidden("INVOICE_DISABLED", "self-service invoicing is disabled")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	query := tx.PaymentOrder.Query().Where(paymentorder.IDIn(req.OrderIDs...), paymentorder.UserIDEQ(userID)).Order(dbent.Asc(paymentorder.FieldID))
	if s.entClient.Driver().Dialect() != dialect.SQLite {
		query.ForUpdate()
	}
	orders, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(orders) != len(req.OrderIDs) {
		return nil, infraerrors.BadRequest("INVOICE_ORDER_UNAVAILABLE", "some selected orders are unavailable")
	}
	total := decimal.Zero
	for _, order := range orders {
		amount, valid := invoicePayAmount(order)
		if !valid || !invoiceOrderIsCNY(order) || order.Status != OrderStatusCompleted || order.OrderType != payment.OrderTypeBalance || order.RefundAmount != 0 || order.RefundAt != nil || order.RefundRequestedAt != nil || order.InvoiceRequestID != nil {
			return nil, infraerrors.Conflict("INVOICE_ORDER_UNAVAILABLE", "some selected orders are unavailable")
		}
		total = total.Add(amount)
	}
	if total.LessThan(decimal.NewFromFloat(cfg.MinAmount)) {
		return nil, infraerrors.BadRequest("INVOICE_MIN_AMOUNT", fmt.Sprintf("invoice amount must be at least CNY %.2f", cfg.MinAmount))
	}
	item, err := tx.InvoiceRequest.Create().SetUserID(userID).SetUserEmail(orders[0].UserEmail).
		SetOrderIds(req.OrderIDs).SetAmount(total.InexactFloat64()).SetCurrency("CNY").
		SetBuyerType(req.BuyerType).SetTitle(req.Title).SetTaxID(req.TaxID).SetEmails(req.Emails).SetRemark(req.Remark).Save(ctx)
	if err != nil {
		return nil, err
	}
	conditions := append(invoiceEligiblePredicates(userID), paymentorder.IDIn(req.OrderIDs...))
	count, err := tx.PaymentOrder.Update().Where(conditions...).SetInvoiceRequestID(item.ID).Save(ctx)
	if err != nil {
		return nil, err
	}
	if count != len(orders) {
		return nil, infraerrors.Conflict("INVOICE_ORDER_UNAVAILABLE", "selected order state changed")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return invoiceRequestDTO(item), nil
}

func (s *PaymentService) lockedInvoiceRequest(ctx context.Context, tx *dbent.Tx, id int64) (*dbent.InvoiceRequest, error) {
	query := tx.InvoiceRequest.Query().Select(invoiceMetadataFields()...).Where(invoicerequest.IDEQ(id))
	if s.entClient.Driver().Dialect() != dialect.SQLite {
		query.ForUpdate()
	}
	item, err := query.Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, infraerrors.NotFound("INVOICE_NOT_FOUND", "invoice application not found")
	}
	if err != nil {
		return nil, err
	}
	if item.Status != "pending" {
		return nil, infraerrors.Conflict("INVOICE_ALREADY_PROCESSED", "invoice application was already processed")
	}
	return item, nil
}

func (s *PaymentService) RejectInvoiceRequest(ctx context.Context, id int64, remark string) (*InvoiceRequest, error) {
	remark = strings.TrimSpace(remark)
	if remark == "" || utf8.RuneCountInString(remark) > 2000 {
		return nil, infraerrors.BadRequest("INVALID_INVOICE_REMARK", "a rejection reason of at most 2000 characters is required")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := s.lockedInvoiceRequest(ctx, tx, id); err != nil {
		return nil, err
	}
	item, err := tx.InvoiceRequest.UpdateOneID(id).SetStatus("rejected").SetAdminRemark(remark).Save(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.PaymentOrder.Update().Where(paymentorder.InvoiceRequestIDEQ(id)).ClearInvoiceRequestID().Save(ctx); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return invoiceRequestDTO(item), nil
}

func (s *PaymentService) IssueInvoiceRequest(ctx context.Context, id int64, invoiceNumber, adminRemark, fileName string, pdf []byte) (*InvoiceRequest, error) {
	invoiceNumber, adminRemark = strings.TrimSpace(invoiceNumber), strings.TrimSpace(adminRemark)
	fileName = filepath.Base(strings.ReplaceAll(fileName, "\\", "/"))
	if invoiceNumber == "" || utf8.RuneCountInString(invoiceNumber) > 100 {
		return nil, infraerrors.BadRequest("INVALID_INVOICE_NUMBER", "invoice number is required and must not exceed 100 characters")
	}
	if len(pdf) == 0 || len(pdf) > maxInvoicePDFBytes || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, infraerrors.BadRequest("INVALID_INVOICE_PDF", "upload a PDF of at most 10 MiB")
	}
	if !strings.EqualFold(filepath.Ext(fileName), ".pdf") || utf8.RuneCountInString(fileName) > 255 || strings.ContainsAny(fileName, "\r\n\x00") || utf8.RuneCountInString(adminRemark) > 2000 {
		return nil, infraerrors.BadRequest("INVALID_INVOICE_FILE", "invalid PDF filename, invoice number or remark")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := s.lockedInvoiceRequest(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	query := tx.PaymentOrder.Query().Where(paymentorder.InvoiceRequestIDEQ(id)).Order(dbent.Asc(paymentorder.FieldID))
	if s.entClient.Driver().Dialect() != dialect.SQLite {
		query.ForUpdate()
	}
	orders, err := query.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(orders) != len(item.OrderIds) {
		return nil, infraerrors.Conflict("INVOICE_ORDER_UNAVAILABLE", "invoice order bindings changed")
	}
	total := decimal.Zero
	for _, order := range orders {
		amount, valid := invoicePayAmount(order)
		if !valid || !invoiceOrderIsCNY(order) || order.UserID != item.UserID || order.Status != OrderStatusCompleted || order.OrderType != payment.OrderTypeBalance || order.RefundAmount != 0 || order.RefundAt != nil || order.RefundRequestedAt != nil {
			return nil, infraerrors.Conflict("INVOICE_ORDER_UNAVAILABLE", "invoice orders are no longer eligible")
		}
		total = total.Add(amount)
	}
	if !total.Equal(decimal.NewFromFloat(item.Amount)) {
		return nil, infraerrors.Conflict("INVOICE_AMOUNT_CHANGED", "invoice order amount changed")
	}
	item, err = tx.InvoiceRequest.UpdateOneID(id).SetStatus("issued").SetInvoiceNumber(invoiceNumber).SetAdminRemark(adminRemark).
		SetFileName(fileName).SetPdfData(pdf).SetIssuedAt(time.Now()).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return invoiceRequestDTO(item), nil
}

func (s *PaymentService) GetInvoicePDF(ctx context.Context, id, userID int64) ([]byte, string, error) {
	query := s.entClient.InvoiceRequest.Query().Where(invoicerequest.IDEQ(id), invoicerequest.StatusEQ("issued"))
	if userID > 0 {
		query.Where(invoicerequest.UserIDEQ(userID))
	}
	item, err := query.Select(invoicerequest.FieldFileName, invoicerequest.FieldPdfData).Only(ctx)
	if dbent.IsNotFound(err) {
		return nil, "", infraerrors.NotFound("INVOICE_NOT_FOUND", "issued invoice not found")
	}
	if err != nil {
		return nil, "", err
	}
	if len(item.PdfData) == 0 {
		return nil, "", infraerrors.NotFound("INVOICE_PDF_NOT_FOUND", "invoice PDF not found")
	}
	return item.PdfData, item.FileName, nil
}

func invoiceRequestDTO(item *dbent.InvoiceRequest) *InvoiceRequest {
	return &InvoiceRequest{
		ID: item.ID, UserID: item.UserID, UserEmail: item.UserEmail, OrderIDs: item.OrderIds,
		Amount: item.Amount, Currency: item.Currency, BuyerType: item.BuyerType,
		Title: item.Title, TaxID: item.TaxID, Emails: item.Emails, Remark: item.Remark,
		Status: item.Status, AdminRemark: item.AdminRemark, InvoiceNumber: item.InvoiceNumber,
		FileName: item.FileName, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, IssuedAt: item.IssuedAt,
	}
}
