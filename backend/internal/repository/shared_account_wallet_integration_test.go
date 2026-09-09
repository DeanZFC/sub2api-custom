package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

// Uses an isolated schema and an explicitly supplied disposable PostgreSQL DSN.
func TestSharedWalletPostgresConcurrentTransfer(t *testing.T) {
	dsn := os.Getenv("SHARED_POOL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SHARED_POOL_TEST_DATABASE_URL is not set")
	}
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("shared_pool_test_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + schema)
	require.NoError(t, err)
	defer admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE users(id BIGINT PRIMARY KEY,balance NUMERIC(20,8) NOT NULL DEFAULT 0,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 CREATE TABLE accounts(id BIGINT PRIMARY KEY,platform TEXT,status TEXT,schedulable BOOLEAN,proxy_id BIGINT,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT NOW());
 CREATE TABLE groups(id BIGSERIAL PRIMARY KEY,platform TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE proxies(id BIGSERIAL PRIMARY KEY,owner_user_id BIGINT,deleted_at TIMESTAMPTZ);
 CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT);
 CREATE UNIQUE INDEX ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
 INSERT INTO users(id,balance) VALUES(42,5),(43,100); INSERT INTO accounts(id,platform,status,schedulable) VALUES(1,'openai','active',true);`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("238_shared_account_pool.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	// Reapplying the new migration must remain safe.
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET account_scope='shared',schedulable=FALSE`)
	require.NoError(t, err)
	listing := &service.SharedAccountListing{AccountID: 1, OwnerUserID: 42, Platform: "openai", DisplayName: "test", Status: "active", ConcurrencyLimit: 1, ConcurrencyMultiplier: 1, SellRate: 1}
	require.NoError(t, (&sharedAccountPoolRepository{db: db}).CreateListing(context.Background(), listing))
	var published bool
	require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=1`).Scan(&published))
	require.True(t, published)
	_, err = db.Exec(`INSERT INTO shared_account_wallets(user_id,pending_amount,total_earned) VALUES(42,90.00000001,90.00000001);
 INSERT INTO shared_account_usage_ledger(request_id,listing_id,owner_user_id,consumer_user_id,gross_cost,fee_rate_percent,platform_fee,owner_amount,frozen_until)
 VALUES('request-1',1,42,43,100.00000001,10,10,90.00000001,NOW()-INTERVAL '1 hour');`)
	require.NoError(t, err)
	repo := sharedWalletRepository{db: db}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.TransferEarnings(context.Background(), 42, "same-transfer-key")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var balance string
	require.NoError(t, db.QueryRow(`SELECT balance::text FROM users WHERE id=42`).Scan(&balance))
	require.Equal(t, "95.00000001", balance)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM shared_account_withdrawals`).Scan(&count))
	require.Equal(t, 1, count)
	wallet, err := repo.GetWallet(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "0.00000000", wallet.Available)
	require.Equal(t, "0.00000000", wallet.Pending)
	require.Equal(t, "90.00000001", wallet.TotalTransferred)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM shared_account_wallet_ledger WHERE action='release'`).Scan(&count))
	require.Equal(t, 1, count)
	// The real card query must parse PostgreSQL JSON timestamps and tolerate no calls.
	cards, err := (&sharedAccountPoolRepository{db: db}).ListPublicCards(context.Background(), "", 50, 5)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.Empty(t, cards[0].RecentCalls)
	pool := &sharedAccountPoolRepository{db: db}
	require.NoError(t, pool.SetListingStatus(context.Background(), 42, 1, "paused"))
	cards, err = pool.ListPublicCards(context.Background(), "openai", 50, 5)
	require.NoError(t, err)
	require.Empty(t, cards)
	cards, err = pool.GetOwnerCards(context.Background(), 42, 50, 5)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.Equal(t, "paused", cards[0].Status)
	// A gateway health failure marks the underlying account unavailable; owner cards expose invalid.
	require.NoError(t, pool.SetListingStatus(context.Background(), 42, 1, "active"))
	_, err = db.Exec(`UPDATE accounts SET status='error',schedulable=FALSE WHERE id=1`)
	require.NoError(t, err)
	cards, err = pool.GetOwnerCards(context.Background(), 42, 50, 5)
	require.NoError(t, err)
	require.Equal(t, "invalid", cards[0].Status)

	// Exercise the production billing transaction, including its dedup claim.
	_, err = db.Exec(`CREATE TABLE usage_billing_dedup(id BIGSERIAL PRIMARY KEY,request_id TEXT,api_key_id BIGINT,request_fingerprint TEXT,UNIQUE(request_id,api_key_id));
 CREATE TABLE usage_billing_dedup_archive(request_id TEXT,api_key_id BIGINT,request_fingerprint TEXT);
 UPDATE users SET balance=200 WHERE id=43;`)
	require.NoError(t, err)
	fee := 10.0
	cmd := &service.UsageBillingCommand{RequestID: "paid-request", APIKeyID: 9, UserID: 43, AccountID: 1, Model: "model-a", BalanceCost: 100, SharedAccountFeeRatePercent: &fee, SharedAccountFreezeHours: 1}
	bill := &usageBillingRepository{db: db}
	// A request already served must still settle if the owner pauses before billing.
	applied, err := bill.Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.True(t, applied.Applied)
	applied, err = bill.Apply(context.Background(), cmd)
	require.NoError(t, err)
	require.False(t, applied.Applied)
	require.NoError(t, db.QueryRow(`SELECT balance::text FROM users WHERE id=43`).Scan(&balance))
	require.Equal(t, "100.00000000", balance)
	wallet, err = repo.GetWallet(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "90.00000000", wallet.Pending)
	cards, err = pool.GetOwnerCards(context.Background(), 42, 50, 5)
	require.NoError(t, err)
	require.EqualValues(t, 1, cards[0].TotalCallCount)
	require.Len(t, cards[0].RecentCalls, 1)
	require.Equal(t, "model-a", cards[0].RecentCalls[0].Model)
	var gross, commission, net string
	require.NoError(t, db.QueryRow(`SELECT gross_cost::text,platform_fee::text,owner_amount::text FROM shared_account_usage_ledger WHERE consumer_user_id=43 ORDER BY id DESC LIMIT 1`).Scan(&gross, &commission, &net))
	require.Equal(t, "100.00000000", gross)
	require.Equal(t, "10.00000000", commission)
	require.Equal(t, "90.00000000", net)
	// Free requests still count, but never increase the provider's earnings.
	cmd.RequestID = "free-request"
	cmd.RequestFingerprint = ""
	cmd.BalanceCost = 0
	_, err = bill.Apply(context.Background(), cmd)
	require.NoError(t, err)
	cards, err = pool.GetOwnerCards(context.Background(), 42, 50, 5)
	require.NoError(t, err)
	require.EqualValues(t, 2, cards[0].TotalCallCount)
	wallet, err = repo.GetWallet(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, "90.00000000", wallet.Pending)
	cmd.BalanceCost = 100

	// Real gateway-shaped acceptance: send an HTTP request to a disposable upstream,
	// parse its usage response, then run the production billing transaction.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "Bearer upstream-test", req.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"upstream-e2e-1","model":"model-e2e","usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer upstream.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, upstream.URL+"/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer upstream-test")
	httpResponse, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer httpResponse.Body.Close()
	var upstreamPayload struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
		} `json:"usage"`
	}
	require.NoError(t, json.NewDecoder(httpResponse.Body).Decode(&upstreamPayload))
	require.Equal(t, http.StatusOK, httpResponse.StatusCode)
	e2eFee := 10.0
	e2eCmd := &service.UsageBillingCommand{RequestID: upstreamPayload.ID, APIKeyID: 99, UserID: 43, AccountID: 1, Model: upstreamPayload.Model, BalanceCost: 5, SharedAccountFeeRatePercent: &e2eFee, SharedAccountFreezeHours: 1}
	e2eApplied, err := bill.Apply(context.Background(), e2eCmd)
	require.NoError(t, err)
	require.True(t, e2eApplied.Applied)
	require.Equal(t, 10, upstreamPayload.Usage.Input)
	require.Equal(t, 5, upstreamPayload.Usage.Output)
	cards, err = pool.GetOwnerCards(context.Background(), 42, 50, 5)
	require.NoError(t, err)
	require.EqualValues(t, 3, cards[0].TotalCallCount)

	// Failure to resolve the supplied account must roll back the consumer debit.
	cmd.RequestID = "missing-listing"
	cmd.RequestFingerprint = ""
	cmd.AccountID = 999
	_, err = bill.Apply(context.Background(), cmd)
	require.Error(t, err)
	require.NoError(t, db.QueryRow(`SELECT balance::text FROM users WHERE id=43`).Scan(&balance))
	require.Equal(t, "95.00000000", balance)

}
