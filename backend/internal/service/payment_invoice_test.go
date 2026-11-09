//go:build unit

package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/invoicerequest"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func invoiceTestService(client *dbent.Client, enabled bool, minimum string) *PaymentService {
	return &PaymentService{entClient: client, configService: &PaymentConfigService{
		settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
			SettingInvoiceEnabled: fmt.Sprint(enabled), SettingInvoiceMinAmount: minimum,
		}},
	}}
}

func invoiceTestUser(t *testing.T, client *dbent.Client) *dbent.User {
	t.Helper()
	nonce := fmt.Sprintf("invoice-%d", time.Now().UnixNano())
	user, err := client.User.Create().SetEmail(nonce + "@example.com").SetUsername(nonce).SetPasswordHash("hash").Save(context.Background())
	require.NoError(t, err)
	return user
}

func invoiceTestOrder(t *testing.T, client *dbent.Client, user *dbent.User, payAmount float64) *dbent.PaymentOrder {
	t.Helper()
	order, err := client.PaymentOrder.Create().SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
		SetAmount(payAmount * 2).SetPayAmount(payAmount).SetRechargeCode("").
		SetOutTradeNo(fmt.Sprintf("sub2_invoice_%d", time.Now().UnixNano())).SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("").SetOrderType(payment.OrderTypeBalance).SetStatus(OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).SetCompletedAt(time.Now()).SetClientIP("127.0.0.1").SetSrcHost("localhost").Save(context.Background())
	require.NoError(t, err)
	return order
}

func invoiceTestInput(ids ...int64) CreateInvoiceRequest {
	return CreateInvoiceRequest{OrderIDs: ids, BuyerType: "personal", Title: "张三", Emails: []string{"invoice@example.com"}}
}

func TestInvoiceApplicationLifecycleAndScopedPDF(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, other := invoiceTestUser(t, client), invoiceTestUser(t, client)
	first, second := invoiceTestOrder(t, client, user, 100.01), invoiceTestOrder(t, client, user, 199.99)
	svc := invoiceTestService(client, true, "300")

	application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(first.ID, second.ID))
	require.NoError(t, err)
	require.Equal(t, 300.0, application.Amount, "invoice uses actual payment, not credited balance")
	require.Equal(t, "pending", application.Status)
	_, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(first.ID, second.ID))
	require.Equal(t, "INVOICE_ORDER_UNAVAILABLE", infraerrors.Reason(err))

	_, err = svc.RejectInvoiceRequest(ctx, application.ID, "请修正抬头")
	require.NoError(t, err)
	first, err = client.PaymentOrder.Get(ctx, first.ID)
	require.NoError(t, err)
	require.Nil(t, first.InvoiceRequestID)
	application, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(first.ID, second.ID))
	require.NoError(t, err)
	_, err = svc.IssueInvoiceRequest(ctx, application.ID, "INV001", "已开具", "invoice.pdf", []byte("wrong"))
	require.Equal(t, "INVALID_INVOICE_PDF", infraerrors.Reason(err))
	pdf := []byte("%PDF-1.7\nfixture\n%%EOF")
	issued, err := svc.IssueInvoiceRequest(ctx, application.ID, "INV001", "已开具", "invoice.pdf", pdf)
	require.NoError(t, err)
	require.Equal(t, "issued", issued.Status)
	_, _, err = svc.GetInvoicePDF(ctx, application.ID, other.ID)
	require.Equal(t, "INVOICE_NOT_FOUND", infraerrors.Reason(err))
	actual, filename, err := svc.GetInvoicePDF(ctx, application.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, pdf, actual)
	require.Equal(t, "invoice.pdf", filename)
	_, err = svc.RejectInvoiceRequest(ctx, application.ID, "重复处理")
	require.Equal(t, "INVOICE_ALREADY_PROCESSED", infraerrors.Reason(err))
	items, total, err := svc.ListInvoiceRequests(ctx, other.ID, 1, 20, "")
	require.NoError(t, err)
	require.Zero(t, total)
	require.Empty(t, items)
	items, total, err = svc.ListInvoiceRequests(ctx, user.ID, 1, 20, "")
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, items, 2)
	require.Equal(t, []int64{first.ID, second.ID}, items[1].OrderIDs, "rejection preserves the original application snapshot")
}

func TestInvoiceApplicationValidatesEligibilityAndThreshold(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, other := invoiceTestUser(t, client), invoiceTestUser(t, client)
	order := invoiceTestOrder(t, client, user, 299.99)
	svc := invoiceTestService(client, false, "300")
	_, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.Equal(t, "INVOICE_DISABLED", infraerrors.Reason(err))
	svc = invoiceTestService(client, true, "300")
	_, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.Equal(t, "INVOICE_MIN_AMOUNT", infraerrors.Reason(err))
	_, err = svc.CreateInvoiceRequest(ctx, other.ID, invoiceTestInput(order.ID))
	require.Equal(t, "INVOICE_ORDER_UNAVAILABLE", infraerrors.Reason(err))
	_, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID, order.ID))
	require.Equal(t, "INVALID_INVOICE_APPLICATION", infraerrors.Reason(err))
	require.Zero(t, client.InvoiceRequest.Query().CountX(ctx))
	require.Nil(t, client.PaymentOrder.GetX(ctx, order.ID).InvoiceRequestID)

	invalid := invoiceTestInput(order.ID)
	invalid.BuyerType = "company"
	_, err = svc.CreateInvoiceRequest(ctx, user.ID, invalid)
	require.Equal(t, "INVALID_INVOICE_APPLICATION", infraerrors.Reason(err))
	invalid.TaxID, invalid.Emails = "91310000EXAMPLE", []string{"Name <a@example.com>"}
	_, err = svc.CreateInvoiceRequest(ctx, user.ID, invalid)
	require.Equal(t, "INVALID_INVOICE_APPLICATION", infraerrors.Reason(err))
}

func TestInvoiceOrdersUseFullEligibleTotalAcrossPages(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user := invoiceTestUser(t, client)
	invoiceTestOrder(t, client, user, 100.10)
	invoiceTestOrder(t, client, user, 200.20)
	foreign := invoiceTestOrder(t, client, user, 999)
	client.PaymentOrder.UpdateOneID(foreign.ID).SetProviderSnapshot(map[string]any{"currency": "USD"}).ExecX(ctx)
	unknown := invoiceTestOrder(t, client, user, 999)
	client.PaymentOrder.UpdateOneID(unknown.ID).SetPaymentType(payment.TypeStripe).SetProviderKey(payment.TypeStripe).ExecX(ctx)
	refunded := invoiceTestOrder(t, client, user, 999)
	client.PaymentOrder.UpdateOneID(refunded.ID).SetRefundAmount(1).ExecX(ctx)
	subscription := invoiceTestOrder(t, client, user, 999)
	client.PaymentOrder.UpdateOneID(subscription.ID).SetOrderType(payment.OrderTypeSubscription).ExecX(ctx)
	svc := invoiceTestService(client, true, "300")
	items, count, amount, err := svc.ListInvoiceOrders(ctx, user.ID, 1, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, 2, count)
	require.Equal(t, 300.30, amount)
	_, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(foreign.ID))
	require.Equal(t, "INVOICE_ORDER_UNAVAILABLE", infraerrors.Reason(err))
}

func TestInvoicePaginationMatchesHTTPPageSize(t *testing.T) {
	page, size := invoicePagination(1, 200)
	require.Equal(t, 1, page)
	require.Equal(t, 200, size)
	_, size = invoicePagination(1, 1001)
	require.Equal(t, 1000, size)
}

func TestInvoiceIssueRechecksOrderState(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user := invoiceTestUser(t, client)
	order := invoiceTestOrder(t, client, user, 300)
	svc := invoiceTestService(client, true, "300")
	application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.NoError(t, err)
	client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunded).ExecX(ctx)
	_, err = svc.IssueInvoiceRequest(ctx, application.ID, "INV001", "", "invoice.pdf", []byte("%PDF-1.7\nfixture"))
	require.Equal(t, "INVOICE_ORDER_UNAVAILABLE", infraerrors.Reason(err))
	require.Equal(t, "pending", client.InvoiceRequest.GetX(ctx, application.ID).Status)
}

func TestInvoiceIssueRequiresNumberBeforeDatabaseAccess(t *testing.T) {
	svc := &PaymentService{}
	for _, number := range []string{"", " \t ", strings.Repeat("1", 101)} {
		_, err := svc.IssueInvoiceRequest(context.Background(), 1, number, "", "invoice.pdf", []byte("%PDF-1.7\nfixture"))
		require.Equal(t, "INVALID_INVOICE_NUMBER", infraerrors.Reason(err))
	}
}

// Optional real-PostgreSQL check for row locking; point only to an isolated
// development database. Ent initializes its schema, and fixtures are removed.
func TestInvoiceConcurrentPostgresApplication(t *testing.T) {
	dsn := os.Getenv("INVOICE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("INVOICE_TEST_POSTGRES_DSN is not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	user := invoiceTestUser(t, client)
	t.Cleanup(func() {
		_, _ = client.PaymentOrder.Delete().Where(paymentorder.UserIDEQ(user.ID)).Exec(ctx)
		_, _ = client.InvoiceRequest.Delete().Where(invoicerequest.UserIDEQ(user.ID)).Exec(ctx)
		_ = client.User.DeleteOneID(user.ID).Exec(ctx)
	})
	order := invoiceTestOrder(t, client, user, 300)
	svc := invoiceTestService(client, true, "300")
	client.User.UpdateOneID(user.ID).SetBalance(50).ExecX(ctx)
	svc.configService.settingRepo.(*paymentConfigSettingRepoStub).values[SettingInvoiceFeeRate] = "10"
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, "INVOICE_ORDER_UNAVAILABLE", infraerrors.Reason(err))
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, client.InvoiceRequest.Query().Where(invoicerequest.UserIDEQ(user.ID)).CountX(ctx))
	require.Equal(t, 20.0, client.User.GetX(ctx, user.ID).Balance, "duplicate submissions charge only one fee")

	// Different orders must also contend for the same available balance.
	for range 2 {
		invoiceTestOrder(t, client, user, 300)
	}
	available := client.PaymentOrder.Query().Where(paymentorder.UserIDEQ(user.ID), paymentorder.InvoiceRequestIDIsNil()).AllX(ctx)
	require.Len(t, available, 2)
	client.User.UpdateOneID(user.ID).SetBalance(30).ExecX(ctx)
	start = make(chan struct{})
	results = make(chan error, 2)
	for _, nextOrder := range available {
		workers.Add(1)
		go func(orderID int64) {
			defer workers.Done()
			<-start
			_, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(orderID))
			results <- err
		}(nextOrder.ID)
	}
	close(start)
	workers.Wait()
	close(results)
	successes = 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, "INVOICE_INSUFFICIENT_BALANCE", infraerrors.Reason(err))
		}
	}
	require.Equal(t, 1, successes)
	require.Zero(t, client.User.GetX(ctx, user.ID).Balance)
	require.Equal(t, 2, client.InvoiceRequest.Query().Where(invoicerequest.UserIDEQ(user.ID)).CountX(ctx))

	application := client.InvoiceRequest.Query().Where(invoicerequest.UserIDEQ(user.ID)).FirstX(ctx)
	start = make(chan struct{})
	results = make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := svc.RejectInvoiceRequest(ctx, application.ID, "please correct the invoice title")
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes = 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			require.Equal(t, "INVOICE_ALREADY_PROCESSED", infraerrors.Reason(err))
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 30.0, client.User.GetX(ctx, user.ID).Balance, "concurrent rejections refund the fee exactly once")
}
