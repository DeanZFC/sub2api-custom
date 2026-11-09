//go:build unit

package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func setInvoiceTestFees(svc *PaymentService, rate, minimum string) {
	settings := svc.configService.settingRepo.(*paymentConfigSettingRepoStub).values
	settings[SettingInvoiceFeeRate] = rate
	settings[SettingInvoiceFeeMinAmount] = minimum
}

func TestInvoiceFeeChargesGreaterOfPercentageAndMinimum(t *testing.T) {
	for _, tt := range []struct {
		name       string
		amounts    []float64
		rate       string
		minimum    string
		fee        float64
		balance    float64
		newBalance float64
	}{
		{name: "percentage", amounts: []float64{100}, rate: "10", minimum: "0", fee: 10, balance: 50, newBalance: 40},
		{name: "minimum overrides percentage", amounts: []float64{100}, rate: "10", minimum: "20", fee: 20, balance: 50, newBalance: 30},
		{name: "minimum only", amounts: []float64{100}, rate: "0", minimum: "20", fee: 20, balance: 50, newBalance: 30},
		{name: "aggregate rounds half up", amounts: []float64{50.02, 50.03}, rate: "10", minimum: "0", fee: 10.01, balance: 50, newBalance: 39.99},
		{name: "rounds down below half", amounts: []float64{100.04}, rate: "10", minimum: "0", fee: 10, balance: 50, newBalance: 40},
		{name: "exact balance is sufficient", amounts: []float64{100}, rate: "10", minimum: "0", fee: 10, balance: 10, newBalance: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			user := invoiceTestUser(t, client)
			client.User.UpdateOneID(user.ID).SetBalance(tt.balance).SetFrozenBalance(7).SetTotalRecharged(33.3).ExecX(ctx)
			ids := make([]int64, 0, len(tt.amounts))
			for _, amount := range tt.amounts {
				ids = append(ids, invoiceTestOrder(t, client, user, amount).ID)
			}
			svc := invoiceTestService(client, true, "0.01")
			setInvoiceTestFees(svc, tt.rate, tt.minimum)

			application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(ids...))
			require.NoError(t, err)
			require.Equal(t, tt.fee, application.FeeAmount)
			require.False(t, application.FeeRefunded)
			require.Equal(t, tt.fee, client.InvoiceRequest.GetX(ctx, application.ID).FeeAmount)
			updatedUser := client.User.GetX(ctx, user.ID)
			require.InDelta(t, tt.newBalance, updatedUser.Balance, 0.000001)
			require.Equal(t, 7.0, updatedUser.FrozenBalance, "frozen funds are already excluded from the available balance")
			require.Equal(t, 33.3, updatedUser.TotalRecharged)
			for _, id := range ids {
				require.Equal(t, application.ID, *client.PaymentOrder.GetX(ctx, id).InvoiceRequestID)
			}

			_, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(ids...))
			require.Equal(t, "INVOICE_ORDER_UNAVAILABLE", infraerrors.Reason(err))
			require.InDelta(t, tt.newBalance, client.User.GetX(ctx, user.ID).Balance, 0.000001, "duplicate application must not charge again")
			require.Equal(t, 1, client.InvoiceRequest.Query().CountX(ctx))
		})
	}
}

func TestInvoiceFeeInsufficientBalanceLeavesOrdersAvailable(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user := invoiceTestUser(t, client)
	client.User.UpdateOneID(user.ID).SetBalance(19.99).SetFrozenBalance(100).ExecX(ctx)
	order := invoiceTestOrder(t, client, user, 100)
	svc := invoiceTestService(client, true, "100")
	setInvoiceTestFees(svc, "10", "20")

	_, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.Equal(t, "INVOICE_INSUFFICIENT_BALANCE", infraerrors.Reason(err))
	require.Zero(t, client.InvoiceRequest.Query().CountX(ctx))
	require.Nil(t, client.PaymentOrder.GetX(ctx, order.ID).InvoiceRequestID)
	require.Equal(t, 19.99, client.User.GetX(ctx, user.ID).Balance)
	require.Equal(t, 100.0, client.User.GetX(ctx, user.ID).FrozenBalance, "frozen balance must not fund the application")

	client.User.UpdateOneID(user.ID).SetBalance(20).ExecX(ctx)
	application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.NoError(t, err)
	require.Equal(t, 20.0, application.FeeAmount)
	require.Zero(t, client.User.GetX(ctx, user.ID).Balance)
}

func TestInvoiceFeeRollsBackWhenApplicationCannotBeSaved(t *testing.T) {
	for _, tt := range []struct {
		name    string
		trigger string
	}{
		{name: "application insert", trigger: `CREATE TRIGGER fail_invoice BEFORE INSERT ON payment_invoice_requests BEGIN SELECT RAISE(ABORT, 'injected invoice insert failure'); END`},
		{name: "order binding", trigger: `CREATE TRIGGER fail_invoice BEFORE UPDATE OF invoice_request_id ON payment_orders WHEN NEW.invoice_request_id IS NOT NULL BEGIN SELECT RAISE(ABORT, 'injected invoice binding failure'); END`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			user := invoiceTestUser(t, client)
			client.User.UpdateOneID(user.ID).SetBalance(30).ExecX(ctx)
			order := invoiceTestOrder(t, client, user, 100)
			svc := invoiceTestService(client, true, "100")
			setInvoiceTestFees(svc, "10", "20")
			_, err := client.ExecContext(ctx, tt.trigger)
			require.NoError(t, err)

			_, err = svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
			require.ErrorContains(t, err, "injected invoice")
			require.Equal(t, 30.0, client.User.GetX(ctx, user.ID).Balance, "failed application must roll back the prior fee deduction")
			require.Zero(t, client.InvoiceRequest.Query().CountX(ctx))
			require.Nil(t, client.PaymentOrder.GetX(ctx, order.ID).InvoiceRequestID)

			_, err = client.ExecContext(ctx, "DROP TRIGGER fail_invoice")
			require.NoError(t, err)
			application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
			require.NoError(t, err)
			require.Equal(t, 20.0, application.FeeAmount)
			require.Equal(t, 10.0, client.User.GetX(ctx, user.ID).Balance)
		})
	}
}

func TestInvoiceRejectionRefundsOriginalFeeExactlyOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user := invoiceTestUser(t, client)
	client.User.UpdateOneID(user.ID).SetBalance(50).SetTotalRecharged(123).ExecX(ctx)
	order := invoiceTestOrder(t, client, user, 100)
	svc := invoiceTestService(client, true, "100")
	setInvoiceTestFees(svc, "10", "20")
	application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.NoError(t, err)
	require.Equal(t, 30.0, client.User.GetX(ctx, user.ID).Balance)

	setInvoiceTestFees(svc, "50", "40")
	rejected, err := svc.RejectInvoiceRequest(ctx, application.ID, "请修改开票抬头")
	require.NoError(t, err)
	require.Equal(t, "rejected", rejected.Status)
	require.Equal(t, 20.0, rejected.FeeAmount)
	require.True(t, rejected.FeeRefunded)
	require.True(t, client.InvoiceRequest.GetX(ctx, application.ID).FeeRefunded)
	require.Equal(t, 50.0, client.User.GetX(ctx, user.ID).Balance, "refund must use the original fee snapshot")
	require.Equal(t, 123.0, client.User.GetX(ctx, user.ID).TotalRecharged, "a fee refund is not a new recharge")
	require.Nil(t, client.PaymentOrder.GetX(ctx, order.ID).InvoiceRequestID)

	_, err = svc.RejectInvoiceRequest(ctx, application.ID, "重复驳回")
	require.Equal(t, "INVOICE_ALREADY_PROCESSED", infraerrors.Reason(err))
	require.Equal(t, 50.0, client.User.GetX(ctx, user.ID).Balance)

	reapplied, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.NoError(t, err)
	require.Equal(t, 50.0, reapplied.FeeAmount, "a new application uses the current configuration")
	require.False(t, reapplied.FeeRefunded)
	require.Zero(t, client.User.GetX(ctx, user.ID).Balance)
	require.Equal(t, 123.0, client.User.GetX(ctx, user.ID).TotalRecharged)
	issued, err := svc.IssueInvoiceRequest(ctx, reapplied.ID, "INV-FEE", "", "invoice.pdf", []byte("%PDF-1.7\nfixture"))
	require.NoError(t, err)
	require.Equal(t, "issued", issued.Status)
	require.Equal(t, 50.0, issued.FeeAmount)
	require.False(t, issued.FeeRefunded)
	require.Zero(t, client.User.GetX(ctx, user.ID).Balance, "issuing the invoice must not charge the fee again")
	_, err = svc.RejectInvoiceRequest(ctx, reapplied.ID, "already issued")
	require.Equal(t, "INVOICE_ALREADY_PROCESSED", infraerrors.Reason(err))
	require.Zero(t, client.User.GetX(ctx, user.ID).Balance)
}

func TestInvoiceFreeApplicationRemainsAvailableWithNegativeBalance(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user := invoiceTestUser(t, client)
	client.User.UpdateOneID(user.ID).SetBalance(-5).ExecX(ctx)
	order := invoiceTestOrder(t, client, user, 100)
	svc := invoiceTestService(client, true, "100")

	application, err := svc.CreateInvoiceRequest(ctx, user.ID, invoiceTestInput(order.ID))
	require.NoError(t, err)
	require.Zero(t, application.FeeAmount)
	require.False(t, application.FeeRefunded)
	require.Equal(t, -5.0, client.User.GetX(ctx, user.ID).Balance)
	rejected, err := svc.RejectInvoiceRequest(ctx, application.ID, "请修改开票抬头")
	require.NoError(t, err)
	require.False(t, rejected.FeeRefunded, "a free application must not report a fee refund")
	require.Equal(t, -5.0, client.User.GetX(ctx, user.ID).Balance)
}
