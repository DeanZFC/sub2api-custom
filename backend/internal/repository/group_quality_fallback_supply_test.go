package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityFallbackSupplyChecksLiveAccountsInOneBatch(t *testing.T) {
	capacityErr := errors.New("fresh capacity unavailable")
	for _, tt := range []struct {
		name   string
		supply bool
		err    error
	}{
		{name: "no request feasible supply"},
		{name: "request feasible supply", supply: true},
		{name: "capacity read error", err: capacityErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()

			proxyID, loadFactor, rate := int64(7), 12, 0.8
			reset := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
			live := groupQualityAccountRow{
				ID: 2, Name: "live account", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
				Status: service.StatusActive, Schedulable: true, Concurrency: 3, LoadFactor: &loadFactor,
				ProxyID: &proxyID, ProxyIDs: []int64{7, 8}, RateMultiplier: &rate,
				Proxies:          []groupQualityProxyRow{{ID: 7, Status: service.StatusActive}, {ID: 8, Status: service.StatusDisabled, ExpiresAt: &reset}},
				Credentials:      map[string]any{"model_mapping": map[string]any{"public-model": "live-upstream"}},
				Extra:            map[string]any{"privacy_mode": "training_off", "openai_apikey_responses_websockets_v2_enabled": true},
				RateLimitResetAt: &reset,
			}
			peer := live
			peer.ID = 3
			peer.Concurrency = 5
			manual := live
			manual.ID, manual.Schedulable = 4, false
			rows := sqlmock.NewRows([]string{"account"})
			for _, account := range []groupQualityAccountRow{live, peer, manual} {
				raw, e := json.Marshal(account)
				require.NoError(t, e)
				rows.AddRow(raw)
			}
			mock.ExpectQuery("SELECT to_jsonb\\(a\\).*jsonb_build_object").WithArgs(int64(43), "public-model").
				WillReturnRows(rows).RowsWillBeClosed()
			ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{GroupID: 43, AccountID: 1, Model: "public-model"})
			calls := 0
			ctx = service.WithGroupQualityFallbackSupplyChecker(ctx, func(_ context.Context, accounts []*service.Account) (bool, error) {
				calls++
				require.Len(t, accounts, 2, "all live eligible accounts are checked together; manual stops stay excluded")
				account := accounts[0]
				require.Equal(t, live.ID, account.ID)
				require.Equal(t, live.Name, account.Name)
				require.Equal(t, 3, account.Concurrency)
				require.Equal(t, &loadFactor, account.LoadFactor)
				require.Equal(t, []int64{7, 8}, account.ProxyIDs)
				require.Len(t, account.Proxies, 2)
				require.Equal(t, service.StatusDisabled, account.Proxies[1].Status)
				require.Equal(t, &reset, account.Proxies[1].ExpiresAt)
				require.Equal(t, &proxyID, account.ProxyID)
				require.Equal(t, 6, account.TotalConcurrency(), "live proxy pool size participates in ordinary concurrency")
				require.Equal(t, &rate, account.RateMultiplier)
				require.Equal(t, &reset, account.RateLimitResetAt)
				require.Equal(t, []int64{43}, account.GroupIDs)
				require.True(t, account.IsPrivacySet())
				require.Equal(t, live.Credentials, account.Credentials)
				require.Equal(t, live.Extra, account.Extra)
				require.Equal(t, int64(3), accounts[1].ID)
				require.Equal(t, 5, accounts[1].Concurrency)
				return tt.supply, tt.err
			})
			supply, err := groupQualityFallbackHasNormalSupply(ctx, tx, service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{GroupIDs: []int64{43}}}, "public-model")
			require.Equal(t, tt.supply, supply)
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, 1, calls)
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGroupQualityFallbackSupplyDoesNotHideLiveRowErrors(t *testing.T) {
	readErr := errors.New("live account row read failed")
	for _, tt := range []struct {
		name string
		rows *sqlmock.Rows
	}{
		{name: "malformed row", rows: sqlmock.NewRows([]string{"account"}).AddRow([]byte("invalid json"))},
		{name: "query stream error", rows: sqlmock.NewRows([]string{"account"}).AddRow([]byte(`{}`)).RowError(0, readErr)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			mock.ExpectBegin()
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			mock.ExpectQuery("SELECT to_jsonb\\(a\\).*jsonb_build_object").WithArgs(int64(43), "public-model").
				WillReturnRows(tt.rows).RowsWillBeClosed()
			ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{GroupID: 43, AccountID: 1, Model: "public-model"})
			called := false
			ctx = service.WithGroupQualityFallbackSupplyChecker(ctx, func(context.Context, []*service.Account) (bool, error) {
				called = true
				return false, nil
			})
			supply, err := groupQualityFallbackHasNormalSupply(ctx, tx, service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{GroupIDs: []int64{43}}}, "public-model")
			require.Error(t, err)
			require.False(t, supply)
			require.False(t, called, "a partial or failed live query cannot authorize fallback")
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestGroupQualityFallbackLiveEligibilityKeepsPublicProtectionAndForwardedModel(t *testing.T) {
	row := groupQualityAccountRow{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"forwarded-model": "upstream"}},
	}
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	ctx := service.WithGroupQualityFallback(context.Background(), service.GroupQualityFallbackRequest{GroupID: 43, AccountID: 1, Model: "public-model"})
	ctx = service.WithGroupQualityFallbackAdmissionModel(ctx, "forwarded-model")
	eligible, err := groupQualityAccountEligible(ctx, raw, "public-model", false, false, false)
	require.NoError(t, err)
	require.True(t, eligible, "forwarded model support counts as actual normal supply")
	row.Extra = map[string]any{"active_test_model_holds": map[string]any{"public-model": "blocked"}}
	raw, err = json.Marshal(row)
	require.NoError(t, err)
	eligible, err = groupQualityAccountEligible(ctx, raw, "public-model", false, false, false)
	require.NoError(t, err)
	require.False(t, eligible, "public model protection remains authoritative")
}
