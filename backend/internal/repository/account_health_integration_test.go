//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountHealthIsolationRechecksPersistedCooldownAndPolicy(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	policy := map[string]any{"account_protection_policy": map[string]any{"enabled": true, "mode": "enforce"}}
	row, err := tx.Client().Account.Create().SetName("health-policy-cas").SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{}).SetExtra(policy).Save(ctx)
	require.NoError(t, err)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	until := time.Now().Add(time.Hour)
	// A cooldown arrived after health statistics were read.
	_, err = tx.Client().Account.UpdateOneID(row.ID).SetTempUnschedulableUntil(until).
		SetTempUnschedulableReason("official cooldown").Save(ctx)
	require.NoError(t, err)
	for _, automatic := range []bool{true, false} {
		reason := "health:manual"
		if automatic {
			reason = "health:auto err_rate=90%"
		}
		changed, err := repo.TrySetAccountHealthIsolation(ctx, row.ID, until.Add(time.Hour), reason, automatic)
		require.NoError(t, err)
		require.False(t, changed)
	}
	stored, err := tx.Client().Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "official cooldown", *stored.TempUnschedulableReason)

	// Policy was disabled after the observer selected the account.
	_, err = tx.Client().Account.UpdateOneID(row.ID).ClearTempUnschedulableUntil().ClearTempUnschedulableReason().
		SetExtra(map[string]any{"account_protection_policy": map[string]any{"enabled": false, "mode": "enforce"}}).Save(ctx)
	require.NoError(t, err)
	changed, err := repo.TrySetAccountHealthIsolation(ctx, row.ID, until, "health:auto err_rate=90%", true)
	require.NoError(t, err)
	require.False(t, changed)

	_, err = tx.Client().Account.UpdateOneID(row.ID).SetExtra(policy).Save(ctx)
	require.NoError(t, err)
	changed, err = repo.TrySetAccountHealthIsolation(ctx, row.ID, until, "health:auto err_rate=90%", true)
	require.NoError(t, err)
	require.True(t, changed)
	// A manual decision arriving before automatic recovery remains in force.
	changed, err = repo.TrySetAccountHealthIsolation(ctx, row.ID, until.Add(time.Hour), "health:manual", false)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = repo.ClearAccountHealthIsolation(ctx, row.ID, true)
	require.NoError(t, err)
	require.False(t, changed)
	stored, err = tx.Client().Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, "health:manual", *stored.TempUnschedulableReason)
	changed, err = repo.ClearAccountHealthIsolation(ctx, row.ID, false)
	require.NoError(t, err)
	require.True(t, changed)
}

func TestAccountHealthIsolationRollsBackWithOutboxFailure(t *testing.T) {
	tx := testEntTx(t)
	ctx := dbent.NewTxContext(context.Background(), tx)
	row, err := tx.Client().Account.Create().SetName("health-atomic-outbox").SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeOAuth).SetCredentials(map[string]any{}).Save(ctx)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE scheduler_outbox ADD CONSTRAINT reject_health_test CHECK (account_id IS DISTINCT FROM %d)", row.ID))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SAVEPOINT health_write")
	require.NoError(t, err)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	changed, err := repo.TrySetAccountHealthIsolation(ctx, row.ID, time.Now().Add(time.Hour), "health:manual", false)
	require.Error(t, err)
	require.False(t, changed)
	_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT health_write")
	require.NoError(t, err)
	stored, err := tx.Client().Account.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Nil(t, stored.TempUnschedulableUntil)
	require.Nil(t, stored.TempUnschedulableReason)
}
