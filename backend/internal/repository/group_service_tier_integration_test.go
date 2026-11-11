//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupForcedServiceTierPersistenceAndAuthProjection(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	groups := NewGroupRepository(integrationEntClient, integrationDB)
	group := &service.Group{Name: fmt.Sprintf("ultrafast-%d", suffix), Platform: service.PlatformOpenAI,
		RateMultiplier: 1, ImageRateMultiplier: 1, Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeStandard,
		ForceOpenAIServiceTier: "ultrafast"}
	require.NoError(t, groups.Create(ctx, group))
	user := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("tier-%d@example.com", suffix), Concurrency: 5})
	keyValue := fmt.Sprintf("sk-tier-%d", suffix)
	keys := NewAPIKeyRepository(integrationEntClient, integrationDB)
	key := &service.APIKey{UserID: user.ID, GroupID: &group.ID, Key: keyValue, Name: "ultrafast", Status: service.StatusActive}
	require.NoError(t, keys.Create(ctx, key))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id=$1", key.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id=$1", user.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id=$1", group.ID)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=api_key_cache_digest($1)", keyValue)
	})

	read, err := groups.GetByID(ctx, group.ID)
	require.NoError(t, err)
	require.Equal(t, "ultrafast", read.ForceOpenAIServiceTier)
	auth, err := keys.GetByKeyForAuth(ctx, keyValue)
	require.NoError(t, err)
	require.Equal(t, "ultrafast", auth.Group.ForceOpenAIServiceTier)

	for _, tier := range []string{"priority", "", "ultrafast"} {
		_, err = integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key=api_key_cache_digest($1)", keyValue)
		require.NoError(t, err)
		read.ForceOpenAIServiceTier = tier
		read.ForceOpenAIFast = tier == "priority"
		require.NoError(t, groups.Update(ctx, read))
		auth, err = keys.GetByKeyForAuth(ctx, keyValue)
		require.NoError(t, err)
		require.Equal(t, tier, auth.Group.ForceOpenAIServiceTier)
		var invalidations int
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM auth_cache_invalidation_outbox WHERE cache_key=api_key_cache_digest($1)", keyValue).Scan(&invalidations))
		require.Positive(t, invalidations)
	}
}

func TestGroupForcedServiceTierMigrationBackfillAndIdempotency(t *testing.T) {
	ctx := context.Background()
	schema := pq.QuoteIdentifier(fmt.Sprintf("group_forced_tier_%d", time.Now().UnixNano()))
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL search_path TO "+schema)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `CREATE TABLE groups (id BIGINT PRIMARY KEY, force_openai_fast BOOLEAN NOT NULL DEFAULT FALSE);
		INSERT INTO groups VALUES (1, TRUE), (2, FALSE);`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("352_group_force_openai_service_tier.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var tier string
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT force_openai_service_tier FROM groups WHERE id=1").Scan(&tier))
	require.Equal(t, "priority", tier)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT force_openai_service_tier FROM groups WHERE id=2").Scan(&tier))
	require.Empty(t, tier)
	_, err = tx.ExecContext(ctx, "UPDATE groups SET force_openai_service_tier='ultrafast' WHERE id=1")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT force_openai_service_tier FROM groups WHERE id=1").Scan(&tier))
	require.Equal(t, "ultrafast", tier)
	_, err = tx.ExecContext(ctx, "INSERT INTO groups (id) VALUES (3)")
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT force_openai_service_tier FROM groups WHERE id=3").Scan(&tier))
	require.Empty(t, tier)
	_, err = tx.ExecContext(ctx, "UPDATE groups SET force_openai_service_tier='flex' WHERE id=1")
	require.Error(t, err, "database rejects unsupported forced service tiers")
}
