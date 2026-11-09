//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestInvoiceClaimBlocksNewRefundRequests(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createCompletedRefundableOrderForTest(t, ctx, client, "invoice-refund-locked")
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetInvoiceRequestID(42).Save(ctx)
	require.NoError(t, err)
	svc := &PaymentService{entClient: client}

	err = svc.RequestRefund(ctx, order.ID, order.UserID, "refund")
	require.Equal(t, "INVOICE_ORDER_LOCKED", infraerrors.Reason(err))
	plan, result, err := svc.PrepareRefund(ctx, order.ID, 100, "refund", true, false)
	require.Nil(t, plan)
	require.Nil(t, result)
	require.Equal(t, "INVOICE_ORDER_LOCKED", infraerrors.Reason(err))
}

// Override the user lookup to claim the invoice after refund validation and
// before the final status update, reproducing a competing application.
type invoiceClaimingRefundUserRepo struct {
	*mockUserRepo
	claim func()
}

func (r *invoiceClaimingRefundUserRepo) GetByID(ctx context.Context, id int64) (*User, error) {
	r.claim()
	return r.mockUserRepo.GetByID(ctx, id)
}

func TestRequestRefundRechecksInvoiceClaimAtStatusUpdate(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createCompletedRefundableOrderForTest(t, ctx, client, "invoice-refund-race")
	providerID, err := strconv.ParseInt(*order.ProviderInstanceID, 10, 64)
	require.NoError(t, err)
	_, err = client.PaymentProviderInstance.UpdateOneID(providerID).SetAllowUserRefund(true).Save(ctx)
	require.NoError(t, err)
	repo := &invoiceClaimingRefundUserRepo{
		mockUserRepo: &mockUserRepo{getByIDUser: &User{ID: order.UserID, Balance: 100}},
		claim: func() {
			_, err := client.PaymentOrder.UpdateOneID(order.ID).SetInvoiceRequestID(42).Save(ctx)
			require.NoError(t, err)
		},
	}

	err = (&PaymentService{entClient: client, userRepo: repo}).RequestRefund(ctx, order.ID, order.UserID, "refund")
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	assertInvoiceClaimPreserved(t, ctx, client, order.ID)
}

func TestExecuteRefundRejectsStalePlanAfterInvoiceClaim(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createCompletedRefundableOrderForTest(t, ctx, client, "invoice-refund-stale")
	deductionCalled := false
	repo := &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
		deductionCalled = true
		return 100, nil
	}}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}
	_, err := client.PaymentOrder.UpdateOneID(order.ID).SetInvoiceRequestID(42).Save(ctx)
	require.NoError(t, err)

	result, err := (&PaymentService{entClient: client, userRepo: repo}).ExecuteRefund(ctx, plan)
	require.Nil(t, result)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	require.False(t, deductionCalled)
	assertInvoiceClaimPreserved(t, ctx, client, order.ID)
}

func assertInvoiceClaimPreserved(t *testing.T, ctx context.Context, client *dbent.Client, orderID int64) {
	t.Helper()
	order, err := client.PaymentOrder.Get(ctx, orderID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, order.Status)
	require.NotNil(t, order.InvoiceRequestID)
	require.Equal(t, int64(42), *order.InvoiceRequestID)
}
