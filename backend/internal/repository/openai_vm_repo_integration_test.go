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

// integrationDB is created by integration_harness_test.go in a disposable
// testcontainers PostgreSQL instance. These tests never use an external DSN.
type openAIVMFixture struct {
	repo                               *openAIVMRepository
	parent, shadow, original, fallback int64
}

func newOpenAIVMFixture(t *testing.T) *openAIVMFixture {
	t.Helper()
	f := &openAIVMFixture{repo: &openAIVMRepository{db: integrationDB}}
	ctx := context.Background()
	for index, id := range []*int64{&f.original, &f.fallback} {
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO proxies(name,protocol,host,port,status,fallback_mode) VALUES($1,'socks5','127.0.0.1',1080,'active','none') RETURNING id`, fmt.Sprintf("vm-integration-%d-%d", time.Now().UnixNano(), index)).Scan(id))
	}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,extra,proxy_id,proxy_fallback_origin_id) VALUES('vm-parent','openai','oauth',jsonb_build_object('access_token','synthetic-test-token'),jsonb_build_object('preserved',true),$1,$2) RETURNING id`, f.original, f.fallback).Scan(&f.parent))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM openai_vm_bindings WHERE account_id=$1`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_proxies WHERE account_id IN(SELECT id FROM accounts WHERE id=$1 OR parent_account_id=$1)`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id IN(SELECT id FROM accounts WHERE id=$1 OR parent_account_id=$1)`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE parent_account_id=$1`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM proxies WHERE id IN($1,$2) OR name=$3`, f.original, f.fallback, fmt.Sprintf("OpenAI VM account %d", f.parent))
	})
	f.shadow = f.insertShadow(t, f.original, false)
	return f
}

func (f *openAIVMFixture) insertShadow(t *testing.T, proxyID int64, managed bool) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,parent_account_id,proxy_id,quota_dimension) VALUES('vm-shadow','openai','oauth','{}',jsonb_build_object('openai_vm_bound',$3::boolean),$1,$2,'spark') RETURNING id`, f.parent, proxyID, managed).Scan(&id))
	return id
}

func (f *openAIVMFixture) reserve(t *testing.T) *service.OpenAIVMBinding {
	t.Helper()
	var binding *service.OpenAIVMBinding
	err := f.repo.WithLock(context.Background(), f.parent, func() error {
		var err error
		binding, err = f.repo.Reserve(context.Background(), f.parent, "docker", &service.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 8081, Username: fmt.Sprint(f.parent), Password: "synthetic-proxy-auth"})
		return err
	})
	require.NoError(t, err)
	return binding
}

func requireOpenAIVMRoute(t *testing.T, accountID int64, proxyID, fallbackID *int64, managed bool) {
	t.Helper()
	var actualProxy, actualFallback *int64
	var marked bool
	require.NoError(t, integrationDB.QueryRow(`SELECT proxy_id,proxy_fallback_origin_id,COALESCE(extra->>'openai_vm_bound','false')::boolean FROM accounts WHERE id=$1`, accountID).Scan(&actualProxy, &actualFallback, &marked))
	require.Equal(t, proxyID, actualProxy)
	require.Equal(t, fallbackID, actualFallback)
	require.Equal(t, managed, marked)
}

func TestOpenAIVMRepositoryReserveRestoreParentAndShadows(t *testing.T) {
	f := newOpenAIVMFixture(t)
	ctx := context.Background()
	for _, id := range []int64{f.parent, f.shadow} {
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)`, id, f.original)
		require.NoError(t, err)
	}
	b := f.reserve(t)
	require.Equal(t, "provisioning", b.Status)
	require.Equal(t, &f.original, b.OriginalProxyID)
	require.Equal(t, &f.fallback, b.OriginalFallbackID)
	require.Equal(t, []int64{f.original}, b.OriginalProxyIDs)
	require.NotZero(t, b.ManagedProxyID)
	for _, id := range []int64{f.parent, f.shadow} {
		requireOpenAIVMRoute(t, id, &b.ManagedProxyID, nil, true)
	}
	var poolCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_proxies WHERE account_id IN($1,$2)`, f.parent, f.shadow).Scan(&poolCount))
	require.Zero(t, poolCount)
	require.NoError(t, f.repo.SetState(ctx, f.parent, "ready", "slot-test", ""))
	ready, err := f.repo.GetBinding(ctx, f.parent)
	require.NoError(t, err)
	require.Equal(t, "slot-test", ready.SlotID)
	require.Equal(t, "ready", ready.Status)
	require.NoError(t, f.repo.Restore(ctx, f.parent))
	require.NoError(t, f.repo.Restore(ctx, f.parent), "restore is idempotent")
	for _, id := range []int64{f.parent, f.shadow} {
		requireOpenAIVMRoute(t, id, &f.original, &f.fallback, false)
		var poolProxy int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT proxy_id FROM account_proxies WHERE account_id=$1 AND position=0`, id).Scan(&poolProxy))
		require.Equal(t, f.original, poolProxy)
	}
	remaining, err := f.repo.GetBinding(ctx, f.parent)
	require.NoError(t, err)
	require.Nil(t, remaining)
	var deleted bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL AND status='disabled' FROM proxies WHERE id=$1`, b.ManagedProxyID).Scan(&deleted))
	require.True(t, deleted)
	var preserved bool
	var token string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT (extra->>'preserved')::boolean,credentials->>'access_token' FROM accounts WHERE id=$1`, f.parent).Scan(&preserved, &token))
	require.True(t, preserved)
	require.Equal(t, "synthetic-test-token", token)
	var snapshots int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1`, f.parent).Scan(&snapshots))
	require.Equal(t, 2, snapshots, "reserve and restore both invalidate scheduler snapshots")
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, f.shadow)
	require.NoError(t, err)
	staleShadow := f.insertShadow(t, b.ManagedProxyID, true)
	requireOpenAIVMRoute(t, staleShadow, &f.original, &f.fallback, false)
}

func TestOpenAIVMRepositoryNewShadowInheritsManagedRoute(t *testing.T) {
	f := newOpenAIVMFixture(t)
	_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, f.shadow)
	require.NoError(t, err)
	b := f.reserve(t)
	shadow := f.insertShadow(t, f.original, false)
	requireOpenAIVMRoute(t, shadow, &b.ManagedProxyID, nil, true)
	require.NoError(t, f.repo.Restore(context.Background(), f.parent))
	requireOpenAIVMRoute(t, shadow, &f.original, &f.fallback, false)
}

func TestOpenAIVMRepositoryRestoresDirectRoute(t *testing.T) {
	f := newOpenAIVMFixture(t)
	_, err := integrationDB.Exec(`UPDATE accounts SET proxy_id=NULL,proxy_fallback_origin_id=NULL WHERE id=$1 OR parent_account_id=$1`, f.parent)
	require.NoError(t, err)
	b := f.reserve(t)
	require.Nil(t, b.OriginalProxyID)
	require.Empty(t, b.OriginalProxyIDs)
	require.NoError(t, f.repo.Restore(context.Background(), f.parent))
	requireOpenAIVMRoute(t, f.parent, nil, nil, false)
	requireOpenAIVMRoute(t, f.shadow, nil, nil, false)
}

func TestOpenAIVMRepositoryGuardsBoundRoutes(t *testing.T) {
	f := newOpenAIVMFixture(t)
	b := f.reserve(t)
	for _, id := range []int64{f.parent, f.shadow} {
		for _, update := range []string{
			"proxy_id=NULL", "deleted_at=NOW()", "platform='anthropic'", "type='apikey'",
			"extra=extra-'openai_vm_bound'", fmt.Sprintf("proxy_fallback_origin_id=%d", f.original),
		} {
			_, err := integrationDB.Exec(`UPDATE accounts SET `+update+` WHERE id=$1`, id)
			require.ErrorContains(t, err, "Detach the OpenAI VM slot", update)
		}
		_, err := integrationDB.Exec(`INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)`, id, f.original)
		require.ErrorContains(t, err, "Detach the OpenAI VM slot")
	}
	for _, update := range []string{
		"host='another-host'", "protocol='https'", "port=9090", "username='changed'", "password='changed'",
		"status='disabled'", "expires_at=NOW()", "fallback_mode='direct'", "deleted_at=NOW()",
		fmt.Sprintf("backup_proxy_id=%d", f.original),
	} {
		_, err := integrationDB.Exec(`UPDATE proxies SET `+update+` WHERE id=$1`, b.ManagedProxyID)
		require.ErrorContains(t, err, "Detach the OpenAI VM slot", update)
	}
	_, err := integrationDB.Exec(`UPDATE accounts SET name='renamed',credentials=credentials||jsonb_build_object('access_token','rotated-synthetic-token') WHERE id=$1`, f.parent)
	require.NoError(t, err, "normal metadata and token rotation remain possible")
	requireOpenAIVMRoute(t, f.parent, &b.ManagedProxyID, nil, true)
	require.NoError(t, f.repo.Restore(context.Background(), f.parent))
	_, err = integrationDB.Exec(`UPDATE accounts SET proxy_id=NULL WHERE id=$1`, f.parent)
	require.NoError(t, err, "detached accounts can change their route again")
}

func TestOpenAIVMRepositoryRejectsProxyPoolWithoutPartialReservation(t *testing.T) {
	f := newOpenAIVMFixture(t)
	_, err := integrationDB.Exec(`INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0),($1,$3,1)`, f.parent, f.original, f.fallback)
	require.NoError(t, err)
	_, err = f.repo.Reserve(context.Background(), f.parent, "docker", &service.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 8081})
	require.ErrorContains(t, err, "Select one outbound proxy")
	b, err := f.repo.GetBinding(context.Background(), f.parent)
	require.NoError(t, err)
	require.Nil(t, b)
	requireOpenAIVMRoute(t, f.parent, &f.original, &f.fallback, false)
	var proxyCount, poolCount int
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM proxies WHERE name=$1`, fmt.Sprintf("OpenAI VM account %d", f.parent)).Scan(&proxyCount))
	require.Zero(t, proxyCount)
	require.NoError(t, integrationDB.QueryRow(`SELECT COUNT(*) FROM account_proxies WHERE account_id=$1`, f.parent).Scan(&poolCount))
	require.Equal(t, 2, poolCount)
}

func TestOpenAIVMRepositoryFailedRestoreKeepsBindingForRetry(t *testing.T) {
	f := newOpenAIVMFixture(t)
	b := f.reserve(t)
	_, err := integrationDB.Exec(`UPDATE proxies SET status='disabled' WHERE id=$1`, f.original)
	require.NoError(t, err)
	require.ErrorContains(t, f.repo.Restore(context.Background(), f.parent), "Restore the original proxy")
	retained, err := f.repo.GetBinding(context.Background(), f.parent)
	require.NoError(t, err)
	require.NotNil(t, retained)
	requireOpenAIVMRoute(t, f.parent, &b.ManagedProxyID, nil, true)
	_, err = integrationDB.Exec(`UPDATE proxies SET status='active' WHERE id=$1`, f.original)
	require.NoError(t, err)
	require.NoError(t, f.repo.Restore(context.Background(), f.parent))
	requireOpenAIVMRoute(t, f.parent, &f.original, &f.fallback, false)
}

func TestOpenAIVMRepositoryLockSurvivesRequestCancellation(t *testing.T) {
	r := &openAIVMRepository{db: integrationDB}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { done <- r.WithLock(ctx, 900001, func() error { close(entered); <-release; return nil }) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("account lock was not acquired")
	}
	cancel()
	concurrent := r.WithLock(context.Background(), 900001, func() error { return nil })
	global := r.WithLock(context.Background(), 0, func() error { return nil })
	independent := r.WithLock(context.Background(), 900002, func() error { return nil })
	close(release)
	require.NoError(t, <-done)
	require.ErrorContains(t, concurrent, "operation is already running")
	require.ErrorContains(t, global, "configuration is being changed")
	require.NoError(t, independent)
	require.NoError(t, r.WithLock(context.Background(), 0, func() error {
		err := r.WithLock(context.Background(), 900001, func() error { return nil })
		require.ErrorContains(t, err, "configuration is being changed")
		return nil
	}))
	require.ErrorIs(t, r.WithLock(ctx, 900001, func() error { return nil }), context.Canceled)
}

func TestOpenAIVMRepositoryConcurrentShadowUsesRestoredRoute(t *testing.T) {
	f := newOpenAIVMFixture(t)
	_, err := integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, f.shadow)
	require.NoError(t, err)
	b := f.reserve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer blocker.Rollback()
	var locked int64
	require.NoError(t, blocker.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, f.parent).Scan(&locked))
	restored := make(chan error, 1)
	go func() { restored <- f.repo.Restore(ctx, f.parent) }()
	require.Eventually(t, func() bool {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND strpos(query,$1)>0)`, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE").Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond, "restore must lock its parent before inspecting the binding")
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	var pid int
	require.NoError(t, conn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid))
	type shadowResult struct {
		id  int64
		err error
	}
	inserted := make(chan shadowResult, 1)
	go func() {
		var id int64
		err := conn.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,extra,parent_account_id,proxy_id,quota_dimension) VALUES('concurrent-vm-shadow','openai','oauth','{}',jsonb_build_object('openai_vm_bound',true),$1,$2,'spark') RETURNING id`, f.parent, b.ManagedProxyID).Scan(&id)
		inserted <- shadowResult{id: id, err: err}
	}()
	require.Eventually(t, func() bool {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting)
		return err == nil && waiting
	}, 3*time.Second, 10*time.Millisecond, "shadow insertion must serialize on its parent")
	require.NoError(t, blocker.Commit())
	require.NoError(t, <-restored)
	result := <-inserted
	require.NoError(t, result.err)
	requireOpenAIVMRoute(t, result.id, &f.original, &f.fallback, false)
}
