//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCustomerUsageSyncAndSnapshot(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	account := mustCreateAccount(t, client, &service.Account{Name: "customer-sync"})
	a := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	b := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: a.ID, Name: "old,key", Status: "inactive"})
	t.Cleanup(func() {
		_, err := integrationDB.Exec("DELETE FROM users WHERE id IN ($1,$2)", a.ID, b.ID)
		require.NoError(t, err)
		_, err = integrationDB.Exec("DELETE FROM accounts WHERE id=$1", account.ID)
		require.NoError(t, err)
	})
	other := mustCreateApiKey(t, client, &service.APIKey{UserID: b.ID})
	repo := NewCustomerUsageRepository(integrationDB)
	p := pagination.PaginationParams{Page: 1, PageSize: 10}
	keys, total, err := repo.Keys(ctx, a.ID, p)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	require.EqualValues(t, 1, total)
	require.Equal(t, "inactive", keys[0].Status)
	ownerKey, err := repo.Key(ctx, a.ID, other.ID)
	require.NoError(t, err)
	require.Nil(t, ownerKey)
	_, err = integrationDB.Exec("UPDATE api_keys SET deleted_at=now() WHERE id=$1", key.ID)
	require.NoError(t, err)
	keys, _, err = repo.Keys(ctx, a.ID, p)
	require.NoError(t, err)
	require.NotNil(t, keys[0].DeletedAt)
	insert := func(db interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	}, userID, keyID int64, legacy bool) int64 {
		t.Helper()
		// Same event time, distinct commit order. Include pre-migration rows.
		generation := "DEFAULT"
		if legacy {
			generation = "NULL"
		}
		stmt := "INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,created_at,actual_cost,image_count,sync_xid) VALUES ($1,$2,$3,$4,'model','2026-01-01',0.12345678,1," + generation + ") RETURNING id"
		var id int64
		require.NoError(t, db.QueryRowContext(ctx, stmt, userID, keyID, account.ID, uuid.NewString()).Scan(&id))
		return id
	}
	legacy := insert(integrationDB, a.ID, key.ID, true)
	insert(integrationDB, b.ID, other.ID, false)
	delayed, err := integrationDB.Begin()
	require.NoError(t, err)
	defer delayed.Rollback()
	slow := insert(delayed, a.ID, key.ID, false)
	fast := insert(integrationDB, a.ID, key.ID, false)
	f := UsageLogFilters{UserID: a.ID, Model: "model", ModelFilterSource: "requested"}
	first, pos, more, err := repo.Sync(ctx, f, service.UsageSyncPosition{}, 1)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, first, 1)
	require.Equal(t, legacy, first[0].ID)
	require.Equal(t, key.Name, first[0].KeyName)
	require.Nil(t, first[0].HTTPStatus)
	require.Equal(t, "image", first[0].BillingMode)
	require.NoError(t, delayed.Commit())
	page, next, more, err := repo.Sync(ctx, f, pos, 1)
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, slow, page[0].ID)
	retry, retryPos, _, err := repo.Sync(ctx, f, pos, 1)
	require.NoError(t, err)
	require.Equal(t, page, retry)
	require.Equal(t, next, retryPos)
	page, last, more, err := repo.Sync(ctx, f, next, 1)
	require.NoError(t, err)
	require.False(t, more)
	require.Equal(t, fast, page[0].ID)
	// Compare with the authoritative count and exact-decimal sum at snapshot start.
	var want int
	var sum string
	require.NoError(t, integrationDB.QueryRow("SELECT count(*),sum(actual_cost)::text FROM usage_logs WHERE user_id=$1", a.ID).Scan(&want, &sum))
	require.Equal(t, 3, want)
	var ids []int64
	var costs []string
	var late int64
	err = repo.Export(ctx, f, p, func(row service.CustomerUsageRow) error {
		if len(ids) == 0 {
			require.ErrorIs(t, repo.Export(ctx, f, p, func(service.CustomerUsageRow) error { return nil }), service.ErrUsageExportBusy)
			late = insert(integrationDB, a.ID, key.ID, false)
		}
		ids = append(ids, row.ID)
		costs = append(costs, row.ActualCost)
		return nil
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{slow, fast, legacy}, ids)
	require.Len(t, ids, want)
	var actual string
	require.NoError(t, integrationDB.QueryRow("SELECT ($1::numeric+$2::numeric+$3::numeric)::text", costs[0], costs[1], costs[2]).Scan(&actual))
	require.Equal(t, sum, actual)
	page, _, more, err = repo.Sync(ctx, f, last, 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, page, 1)
	require.Equal(t, late, page[0].ID)
	foreign, _, _, err := repo.Sync(ctx, UsageLogFilters{UserID: b.ID, APIKeyID: key.ID}, service.UsageSyncPosition{}, 10)
	require.NoError(t, err)
	require.Empty(t, foreign)
}

func TestCustomerUsageLargeAccount(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID})
	account := mustCreateAccount(t, client, &service.Account{Name: "usage-perf"})
	t.Cleanup(func() {
		_, err := integrationDB.Exec("DELETE FROM users WHERE id=$1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.Exec("DELETE FROM accounts WHERE id=$1", account.ID)
		require.NoError(t, err)
	})
	const count = 25000
	_, err := integrationDB.Exec(`INSERT INTO usage_logs(user_id,api_key_id,account_id,model,created_at,actual_cost)
 SELECT $1,$2,$3,'sample',now()-(s||' seconds')::interval,0.01 FROM generate_series(1,$4::int) s`, user.ID, key.ID, account.ID, count)
	require.NoError(t, err)
	_, err = integrationDB.Exec("ANALYZE usage_logs")
	require.NoError(t, err)
	rows, err := integrationDB.Query(`EXPLAIN (ANALYZE,BUFFERS) SELECT id FROM usage_logs WHERE user_id=$1 AND (COALESCE(sync_xid,0),id) > ($2,$3) ORDER BY COALESCE(sync_xid,0),id LIMIT 101`, user.ID, 0, 0)
	require.NoError(t, err)
	var plan string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += line + "\n"
	}
	require.NoError(t, rows.Close())
	require.Contains(t, plan, "idx_usage_sync_user")
	t.Log(plan)
	var n int
	start := time.Now()
	err = NewCustomerUsageRepository(integrationDB).Export(ctx, UsageLogFilters{UserID: user.ID}, pagination.PaginationParams{SortBy: "created_at"}, func(row service.CustomerUsageRow) error {
		if row.KeyName != key.Name {
			return fmt.Errorf("incorrect key %d", row.APIKeyID)
		}
		n++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, count, n)
	t.Logf("export scanned %d rows in %s", n, time.Since(start))
}
