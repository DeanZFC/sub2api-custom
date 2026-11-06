//go:build integration

package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSchedulerOutboxPublicationReadsCurrentAndLocksThroughPublish(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	a := mustCreateAccount(t, client, &service.Account{Name: "retry-publication-lock", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "synthetic"}})
	g := mustCreateGroup(t, client, &service.Group{Name: "retry-publication-group", Platform: service.PlatformOpenAI})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id=$1", a.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", a.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", g.ID)
		require.NoError(t, err)
	})
	_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET concurrency=7 WHERE id=$1", a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.WithLatestSchedulerAccounts(ctx, []int64{a.ID, a.ID}, func(fresh []service.Account) error {
		require.Len(t, fresh, 1)
		require.Equal(t, 7, fresh[0].Concurrency)
		// A concurrent SQL writer cannot overtake the publication callback.
		tx, err := integrationDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout='50ms'")
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, "UPDATE accounts SET concurrency=9 WHERE id=$1", a.ID)
		require.ErrorContains(t, err, "lock timeout")
		// Production membership changes take the same account lock.
		bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		require.Error(t, repo.BindGroups(bounded, a.ID, []int64{g.ID}))
		return nil
	}))
	require.NoError(t, repo.BindGroups(ctx, a.ID, []int64{g.ID}))
	require.NoError(t, repo.WithLatestSchedulerAccounts(ctx, []int64{a.ID}, func(fresh []service.Account) error {
		require.Len(t, fresh, 1)
		require.Equal(t, []int64{g.ID}, fresh[0].GroupIDs)
		return nil
	}))
	require.NoError(t, repo.Delete(ctx, a.ID))
	require.NoError(t, repo.WithLatestSchedulerAccounts(ctx, []int64{a.ID}, func(fresh []service.Account) error {
		require.Empty(t, fresh, "deleted account cannot be resurrected by a delayed retry")
		return nil
	}))
}
