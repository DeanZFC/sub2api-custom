package service

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestInvoiceConfigDefaultsAndPersistence(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := NewPaymentConfigService(nil, repo, nil)
	cfg, err := svc.GetPaymentConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.InvoiceEnabled)
	require.Equal(t, 300.0, cfg.InvoiceMinAmount)
	require.Zero(t, cfg.InvoiceFeeRate)
	require.Zero(t, cfg.InvoiceFeeMinAmount)

	enabled, minimum, instructions := true, 125.25, "  请填写完整开票抬头  "
	feeRate, feeMinimum := 10.25, 20.50
	err = svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		InvoiceEnabled: &enabled, InvoiceMinAmount: &minimum, InvoiceInstructions: &instructions,
		InvoiceFeeRate: &feeRate, InvoiceFeeMinAmount: &feeMinimum,
	})
	require.NoError(t, err)
	cfg, err = svc.GetPaymentConfig(context.Background())
	require.NoError(t, err)
	require.True(t, cfg.InvoiceEnabled)
	require.Equal(t, minimum, cfg.InvoiceMinAmount)
	require.Equal(t, feeRate, cfg.InvoiceFeeRate)
	require.Equal(t, feeMinimum, cfg.InvoiceFeeMinAmount)
	require.Equal(t, "请填写完整开票抬头", cfg.InvoiceInstructions)
	require.Equal(t, "125.25", repo.values[SettingInvoiceMinAmount])
	require.Equal(t, "10.25", repo.values[SettingInvoiceFeeRate])
	require.Equal(t, "20.50", repo.values[SettingInvoiceFeeMinAmount])

	// A separate payment-settings save must preserve the invoice configuration.
	paymentEnabled := true
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{Enabled: &paymentEnabled}))
	require.Equal(t, "125.25", repo.values[SettingInvoiceMinAmount])
	require.Equal(t, "10.25", repo.values[SettingInvoiceFeeRate])
	require.Equal(t, "20.50", repo.values[SettingInvoiceFeeMinAmount])

	// Zero explicitly disables both fees; omitted fields preserve stored values.
	zero := 0.0
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{InvoiceFeeRate: &zero, InvoiceFeeMinAmount: &zero}))
	cfg, err = svc.GetPaymentConfig(context.Background())
	require.NoError(t, err)
	require.Zero(t, cfg.InvoiceFeeRate)
	require.Zero(t, cfg.InvoiceFeeMinAmount)
	require.Equal(t, "0.00", repo.values[SettingInvoiceFeeRate])
	require.Equal(t, "0.00", repo.values[SettingInvoiceFeeMinAmount])
}

func TestInvoiceConfigRejectsInvalidFeesBeforeSaving(t *testing.T) {
	for _, test := range []struct {
		name   string
		reason string
		values []float64
		apply  func(*UpdatePaymentConfigRequest, *float64)
	}{
		{
			name: "rate", reason: "INVALID_INVOICE_FEE_RATE",
			values: []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), 0.001, 10.123, 100.01},
			apply:  func(req *UpdatePaymentConfigRequest, value *float64) { req.InvoiceFeeRate = value },
		},
		{
			name: "minimum", reason: "INVALID_INVOICE_FEE_MIN_AMOUNT",
			values: []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), 0.001, 20.123, 1e9 + 1},
			apply:  func(req *UpdatePaymentConfigRequest, value *float64) { req.InvoiceFeeMinAmount = value },
		},
	} {
		for _, value := range test.values {
			t.Run(test.name+"/"+strconv.FormatFloat(value, 'f', -1, 64), func(t *testing.T) {
				repo := &paymentConfigSettingRepoStub{values: map[string]string{SettingInvoiceEnabled: "false"}}
				svc := NewPaymentConfigService(nil, repo, nil)
				enabled := true
				req := UpdatePaymentConfigRequest{InvoiceEnabled: &enabled}
				test.apply(&req, &value)
				err := svc.UpdatePaymentConfig(context.Background(), req)
				require.Equal(t, test.reason, infraerrors.Reason(err))
				require.Nil(t, repo.updates)
				require.Equal(t, "false", repo.values[SettingInvoiceEnabled])
			})
		}
	}
}

func TestInvoiceFeeConfigNormalizesLegacyAndInvalidValues(t *testing.T) {
	for _, raw := range []string{"", "bad", "NaN", "Inf", "-1", "10.123"} {
		require.Zero(t, normalizedInvoiceFeeRate(raw), raw)
		require.Zero(t, normalizedInvoiceFeeMinAmount(raw), raw)
	}
	require.Zero(t, normalizedInvoiceFeeRate("100.01"))
	require.Zero(t, normalizedInvoiceFeeMinAmount("1000000001"))
	for _, value := range []float64{0, 0.29, 10, 99.99, 100} {
		require.True(t, validInvoiceFeeRate(value))
	}
	for _, value := range []float64{0, 0.29, 20, 1e9} {
		require.True(t, validInvoiceFeeMinAmount(value))
	}
}

func TestInvoiceConfigRejectsInvalidAmountsBeforeSaving(t *testing.T) {
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1), 0.001, 300.123, 1e9 + 1} {
		t.Run(strconv.FormatFloat(amount, 'f', -1, 64), func(t *testing.T) {
			repo := &paymentConfigSettingRepoStub{values: map[string]string{SettingInvoiceEnabled: "false"}}
			svc := NewPaymentConfigService(nil, repo, nil)
			enabled := true
			err := svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
				InvoiceEnabled: &enabled, InvoiceMinAmount: &amount,
			})
			require.Error(t, err)
			require.Nil(t, repo.updates)
			require.Equal(t, "false", repo.values[SettingInvoiceEnabled])
		})
	}
	for _, raw := range []string{"", "NaN", "Inf", "-10", "0", "bad", "300.123"} {
		require.Equal(t, 300.0, normalizedInvoiceMinAmount(raw), raw)
	}
	require.True(t, validInvoiceMinAmount(0.29))
	require.True(t, validInvoiceMinAmount(1e9))
}

func TestInvoiceConfigRejectsOversizedInstructions(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := NewPaymentConfigService(nil, repo, nil)
	instructions := strings.Repeat("开", 5001)
	require.Error(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{InvoiceInstructions: &instructions}))
	require.Nil(t, repo.updates)
}
