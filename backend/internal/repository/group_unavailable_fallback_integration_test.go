//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupUnavailableFallbackPersistenceAndAuthProjection(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	groups := newGroupRepositoryWithSQL(tx.Client(), tx)
	pool := &service.Group{Name: "unavailable-pool", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 9}
	require.NoError(t, groups.Create(ctx, pool))
	source := &service.Group{Name: "unavailable-primary", Platform: service.PlatformOpenAI, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard, RateMultiplier: 0.7, FallbackGroupIDOnUnavailable: &pool.ID}
	require.NoError(t, groups.Create(ctx, source))
	read, err := groups.GetByIDLite(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, &pool.ID, read.FallbackGroupIDOnUnavailable)
	user := mustCreateUser(t, tx.Client(), &service.User{})
	keys := newAPIKeyRepositoryWithSQL(tx.Client(), tx)
	key := &service.APIKey{UserID: user.ID, Key: "sk-group-unavailable-auth", Name: "test", GroupID: &source.ID, Status: service.StatusActive}
	require.NoError(t, keys.Create(ctx, key))
	loaded, err := keys.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, &pool.ID, loaded.Group.FallbackGroupIDOnUnavailable, "auth SELECT must carry the configuration, not only admin reads")
	require.Nil(t, loaded.FallbackGroupID, "admin pool fallback must not become API-key fallback")
	source.FallbackGroupIDOnUnavailable = nil
	require.NoError(t, groups.Update(ctx, source))
	loaded, err = keys.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Nil(t, loaded.Group.FallbackGroupIDOnUnavailable)
	// Exercise migration 330 constraints without leaving an aborted transaction.
	_, err = tx.ExecContext(ctx, "SAVEPOINT invalid_fallback")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE groups SET fallback_group_id_on_unavailable=id WHERE id=$1`, source.ID)
	require.Error(t, err)
	_, err = tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT invalid_fallback")
	require.NoError(t, err)
}
