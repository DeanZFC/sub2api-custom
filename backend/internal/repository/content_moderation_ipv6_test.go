//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func auditKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func moderationIPv6Fixture(t *testing.T) (*sql.DB, *proxyRepository, *ovhTestHost, *service.ContentModerationService) {
	t.Helper()
	db, proxies, host := ovhRepoFixture(t)
	var previous string
	err := db.QueryRow(`SELECT value FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig).Scan(&previous)
	existed := err == nil
	require.True(t, existed || err == sql.ErrNoRows)
	_, err = db.Exec(`DELETE FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig)
	require.NoError(t, err)
	t.Cleanup(func() {
		if existed {
			_, err = db.Exec(`INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, service.SettingKeyContentModerationConfig, previous)
		} else {
			_, err = db.Exec(`DELETE FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig)
		}
		require.NoError(t, err)
	})
	svc := service.NewContentModerationService(NewSettingRepository(proxies.client), nil, nil, nil, nil, proxies, nil, nil)
	return db, proxies, host, svc
}

func TestContentModerationIPv6LifecyclePostgres(t *testing.T) {
	db, repo, host, svc := moderationIPv6Fixture(t)
	ctx := context.Background()
	keys := []string{"audit-first", "audit-second"}
	enabled := true
	view, err := svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{APIKeys: &keys, AutoAssignIPv6: &enabled})
	require.NoError(t, err)
	require.Len(t, view.APIKeyStatuses, 2)
	first, second := view.APIKeyStatuses[0], view.APIKeyStatuses[1]
	require.NotEqual(t, first.ManagedIPv6, second.ManagedIPv6)
	require.NotEmpty(t, first.ManagedIPv6)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases WHERE state='bound' AND confirmed AND owner_account_id IS NULL AND owner_moderation_key IS NOT NULL`).Scan(&count))
	require.Equal(t, 2, count)
	_, err = svc.AllocateAPIKeyIPv6(ctx, "openai", first.KeyHash)
	require.NoError(t, err)
	host.mu.Lock()
	require.Equal(t, 2, host.issued)
	host.mu.Unlock()
	_, err = svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{ProxyID: first.ProxyID})
	require.Error(t, err, "managed key proxies cannot be shared as a global route")
	_, err = ovhCreateAccount(t, repo.client, *first.ProxyID, nil)
	require.ErrorContains(t, err, "owned", "account must not steal key address")
	_, err = db.Exec(`UPDATE proxies SET deleted_at=NOW() WHERE id=$1`, *first.ProxyID)
	require.Error(t, err, "bound key address must not be deleted from proxy pool")
	disabled := false
	_, err = svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{AutoAssignIPv6: &disabled})
	require.NoError(t, err)
	deleteHashes := []string{first.KeyHash}
	view, err = svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{DeleteAPIKeyHashes: &deleteHashes})
	require.NoError(t, err)
	require.Len(t, view.APIKeyStatuses, 1)
	require.Equal(t, second.ManagedIPv6, view.APIKeyStatuses[0].ManagedIPv6)
	var state string
	require.NoError(t, db.QueryRow(`SELECT state FROM ovh_ipv6_leases WHERE proxy_id=$1`, *first.ProxyID).Scan(&state))
	require.Equal(t, "retiring", state)
	host.mu.Lock()
	host.draining = true
	host.mu.Unlock()
	require.NoError(t, repo.SyncOVHIPv6(ctx))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases WHERE proxy_id=$1`, *first.ProxyID).Scan(&count))
	require.Equal(t, 1, count)
	host.mu.Lock()
	host.draining = false
	host.mu.Unlock()
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET next_attempt_at=NOW()`)
	require.NoError(t, err)
	require.NoError(t, repo.SyncOVHIPv6(ctx))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases WHERE proxy_id=$1`, *first.ProxyID).Scan(&count))
	require.Zero(t, count)
	// Re-adding a deleted key must consume a new monotonic address.
	keys = []string{"audit-first"}
	view, err = svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{APIKeys: &keys, AutoAssignIPv6: &enabled})
	require.NoError(t, err)
	require.NotEqual(t, first.ManagedIPv6, view.APIKeyStatuses[1].ManagedIPv6)
	// Force an activation retry after commit; key-owned leases must be picked up.
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET confirmed=FALSE,next_attempt_at=NOW() WHERE state='bound'`)
	require.NoError(t, err)
	require.NoError(t, repo.SyncOVHIPv6(ctx))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases WHERE state='bound' AND NOT confirmed`).Scan(&count))
	require.Zero(t, count)
}

func TestContentModerationIPv6ConcurrentIdempotencyPostgres(t *testing.T) {
	db, _, host, svc := moderationIPv6Fixture(t)
	ctx := context.Background()
	keys := []string{"concurrent-key"}
	_, err := svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{APIKeys: &keys})
	require.NoError(t, err)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := svc.AllocateAPIKeyIPv6(ctx, "openai", auditKeyHash(keys[0]))
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		require.NoError(t, e)
	}
	host.mu.Lock()
	require.Equal(t, 1, host.issued)
	host.mu.Unlock()
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases`).Scan(&count))
	require.Equal(t, 1, count)
}

func TestContentModerationIPv6RollbackAndEngineIsolationPostgres(t *testing.T) {
	db, _, host, svc := moderationIPv6Fixture(t)
	ctx := context.Background()
	enabled := true
	keys := []string{"same-key"}
	view, err := svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{EngineConfigs: map[string]service.UpdateContentModerationEngineInput{
		"openai": {APIKeys: &keys, AutoAssignIPv6: &enabled}, "typesafe": {APIKeys: &keys, AutoAssignIPv6: &enabled},
	}})
	require.NoError(t, err)
	require.NotEqual(t, view.EngineConfigs["openai"].APIKeyStatuses[0].ManagedIPv6, view.EngineConfigs["typesafe"].APIKeyStatuses[0].ManagedIPv6)
	var before string
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig).Scan(&before))
	// Reject the setting write after provisioning to exercise the actual rollback.
	_, err = db.Exec(`CREATE FUNCTION reject_audit_write() RETURNS TRIGGER AS $$ BEGIN RAISE EXCEPTION 'test setting failure'; END; $$ LANGUAGE plpgsql;
CREATE TRIGGER reject_audit_write BEFORE UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION reject_audit_write();`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP TRIGGER IF EXISTS reject_audit_write ON settings; DROP FUNCTION IF EXISTS reject_audit_write();`)
	})
	keys = []string{"must-rollback"}
	_, err = svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{APIKeys: &keys})
	require.Error(t, err)
	var after string
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig).Scan(&after))
	require.Equal(t, before, after)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases`).Scan(&count))
	require.Equal(t, 2, count)
	host.mu.Lock()
	require.Equal(t, 3, host.issued, "rolled back addresses are never reissued")
	host.mu.Unlock()
	_, err = db.Exec(`DROP TRIGGER reject_audit_write ON settings`)
	require.NoError(t, err)
	host.mu.Lock()
	host.fail = true
	host.mu.Unlock()
	_, err = svc.UpdateConfig(ctx, service.UpdateContentModerationConfigInput{APIKeys: &keys})
	require.Error(t, err)
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key=$1`, service.SettingKeyContentModerationConfig).Scan(&after))
	require.Equal(t, before, after)
	_, err = svc.AllocateAPIKeyIPv6(ctx, "openai", auditKeyHash("missing"))
	require.Error(t, err)
}
