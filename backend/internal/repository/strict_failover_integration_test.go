//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestStrictFailoverPersistenceAndAuthProjection(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	groups := NewGroupRepository(integrationEntClient, integrationDB)
	g := &service.Group{Name: fmt.Sprintf("strict-%d", suffix), Platform: service.PlatformOpenAI, RateMultiplier: 1, Status: service.StatusActive, SubscriptionType: "standard"}
	require.NoError(t, groups.Create(ctx, g))
	primary := mustCreateAccount(t, integrationEntClient, &service.Account{Name: fmt.Sprintf("primary-%d", suffix), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	backup := mustCreateAccount(t, integrationEntClient, &service.Account{Name: fmt.Sprintf("backup-%d", suffix), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey})
	require.NoError(t, groups.BindAccountsToGroup(ctx, g.ID, []int64{primary.ID, backup.ID}))
	// Disabled primary must still be present in the authoritative GetByIDs read.
	require.NoError(t, integrationEntClient.Account.UpdateOneID(primary.ID).SetSchedulable(false).Exec(ctx))
	g.StrictFailover = service.StrictFailoverConfig{Enabled: true, Tiers: [][]int64{{primary.ID}, {backup.ID}}}
	require.NoError(t, groups.Update(ctx, g))
	user := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("strict-%d@example.com", suffix), Concurrency: 2})
	keys := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{UserID: user.ID, GroupID: &g.ID, Key: fmt.Sprintf("sk-strict-%d", suffix), Name: "strict", Status: service.StatusActive}
	require.NoError(t, keys.Create(ctx, key))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = encode(sha256(convert_to($1, 'UTF8')), 'hex')", key.Key)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id=$1", key.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM account_groups WHERE group_id=$1", g.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id IN ($1,$2)", primary.ID, backup.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", g.ID)
		require.NoError(t, err)
	})
	lite, err := groups.GetByIDLite(ctx, g.ID)
	require.NoError(t, err)
	require.Equal(t, g.StrictFailover, lite.StrictFailover)
	full, err := groups.GetByID(ctx, g.ID)
	require.NoError(t, err)
	require.Equal(t, g.StrictFailover, full.StrictFailover)
	auth, err := keys.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, auth.Group)
	require.Equal(t, g.StrictFailover, auth.Group.StrictFailover)
	accounts := NewAccountRepository(integrationEntClient, integrationDB, nil)
	got, err := accounts.GetByIDs(ctx, []int64{primary.ID, backup.ID})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, a := range got {
		require.Contains(t, a.GroupIDs, g.ID)
		if a.ID == primary.ID {
			require.False(t, a.Schedulable)
		}
	}
	g.StrictFailover.Enabled = false
	require.NoError(t, groups.Update(ctx, g))
	auth, err = keys.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.False(t, auth.Group.StrictFailover.Enabled)
	require.Equal(t, g.StrictFailover.Tiers, auth.Group.StrictFailover.Tiers)
}
