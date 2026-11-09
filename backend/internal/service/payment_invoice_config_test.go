package service

import (
	"context"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvoiceConfigDefaultsAndPersistence(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{}}
	svc := NewPaymentConfigService(nil, repo, nil)
	cfg, err := svc.GetPaymentConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.InvoiceEnabled)
	require.Equal(t, 300.0, cfg.InvoiceMinAmount)

	enabled, minimum, instructions := true, 125.25, "  请填写完整开票抬头  "
	err = svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{
		InvoiceEnabled: &enabled, InvoiceMinAmount: &minimum, InvoiceInstructions: &instructions,
	})
	require.NoError(t, err)
	cfg, err = svc.GetPaymentConfig(context.Background())
	require.NoError(t, err)
	require.True(t, cfg.InvoiceEnabled)
	require.Equal(t, minimum, cfg.InvoiceMinAmount)
	require.Equal(t, "请填写完整开票抬头", cfg.InvoiceInstructions)
	require.Equal(t, "125.25", repo.values[SettingInvoiceMinAmount])

	// A separate payment-settings save must preserve the invoice configuration.
	paymentEnabled := true
	require.NoError(t, svc.UpdatePaymentConfig(context.Background(), UpdatePaymentConfigRequest{Enabled: &paymentEnabled}))
	require.Equal(t, "125.25", repo.values[SettingInvoiceMinAmount])
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
