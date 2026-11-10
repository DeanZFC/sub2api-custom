//go:build integration

package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIIPv6RepositoryLifecycle(t *testing.T) {
	f := newOpenAIVMFixture(t)
	ctx := context.Background()
	repo := &openAIIPv6Repository{db: integrationDB}
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM openai_ipv6_bindings WHERE account_id=$1", f.parent) })
	_, err := integrationDB.Exec("UPDATE accounts SET status='inactive' WHERE id=$1 OR parent_account_id=$1", f.parent)
	require.NoError(t, err)
	for _, id := range []int64{f.parent, f.shadow} {
		_, err = integrationDB.Exec("INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0),($1,$3,1)", id, f.original, f.fallback)
		require.NoError(t, err)
	}
	p := &service.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 18080, Username: "v6-sub2-test"}
	b, err := repo.Reserve(ctx, f.parent, "sub2-test", "http://127.0.0.1:18081", p)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM openai_ipv6_bindings WHERE account_id=$1", f.parent)
		_, _ = integrationDB.Exec("UPDATE accounts SET proxy_id=NULL,extra=extra-'openai_ipv6_bound' WHERE id=$1 OR parent_account_id=$1", f.parent)
		_, _ = integrationDB.Exec("DELETE FROM proxies WHERE id=$1", b.ManagedProxyID)
	})
	require.Equal(t, []int64{f.original, f.fallback}, b.OriginalProxyIDs)
	_, err = integrationDB.Exec("UPDATE accounts SET status='active' WHERE id=$1", f.parent)
	require.Error(t, err, "provisioning cannot be enabled")
	require.NoError(t, repo.Activate(ctx, f.parent, "2001:db8::7", strings.Repeat("p", 32)))
	// A new shadow inherits the reserved route even from a stale caller snapshot.
	_, err = integrationDB.Exec("DELETE FROM accounts WHERE id=$1", f.shadow)
	require.NoError(t, err)
	f.shadow = f.insertShadow(t, f.original, false)
	var shadowProxy int64
	var shadowMarker bool
	require.NoError(t, integrationDB.QueryRow("SELECT proxy_id,(extra->>'openai_ipv6_bound')::boolean FROM accounts WHERE id=$1", f.shadow).Scan(&shadowProxy, &shadowMarker))
	require.Equal(t, b.ManagedProxyID, shadowProxy)
	require.True(t, shadowMarker)
	_, err = integrationDB.Exec("UPDATE accounts SET status='inactive' WHERE id=$1", f.shadow)
	require.NoError(t, err)
	foreign := newOpenAIVMFixture(t)
	_, err = integrationDB.Exec("UPDATE accounts SET proxy_id=$2 WHERE id=$1", foreign.parent, b.ManagedProxyID)
	require.Error(t, err)
	_, err = integrationDB.Exec("INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)", foreign.parent, b.ManagedProxyID)
	require.Error(t, err)
	_, err = integrationDB.Exec("UPDATE proxies SET backup_proxy_id=$2 WHERE id=$1", foreign.original, b.ManagedProxyID)
	require.Error(t, err)

	for _, assignment := range []string{"proxy_id=NULL", "extra=extra-'openai_ipv6_bound'", "deleted_at=NOW()", "extra=extra||'{\"openai_excel_bps\":true}'::jsonb"} {
		_, err = integrationDB.Exec("UPDATE accounts SET "+assignment+" WHERE id=$1", f.parent)
		require.Error(t, err)
	}
	_, err = integrationDB.Exec("UPDATE proxies SET host='other' WHERE id=$1", b.ManagedProxyID)
	require.Error(t, err)
	_, err = integrationDB.Exec("INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)", f.parent, f.original)
	require.Error(t, err)
	_, err = integrationDB.Exec("UPDATE accounts SET status='active' WHERE id=$1", f.parent)
	require.NoError(t, err)
	require.Error(t, repo.SetState(ctx, f.parent, "deleting", ""))
	_, err = integrationDB.Exec("UPDATE accounts SET status='inactive' WHERE id=$1", f.parent)
	require.NoError(t, err)
	require.NoError(t, repo.SetState(ctx, f.parent, "deleting", ""))
	_, err = integrationDB.Exec("UPDATE accounts SET status='active' WHERE id=$1", f.shadow)
	require.Error(t, err)
	require.NoError(t, repo.Restore(ctx, f.parent))
	require.NoError(t, repo.Restore(ctx, f.parent))
	for _, id := range []int64{f.parent, f.shadow} {
		var proxy int64
		var marked bool
		require.NoError(t, integrationDB.QueryRow("SELECT proxy_id,COALESCE((extra->>'openai_ipv6_bound')::boolean,false) FROM accounts WHERE id=$1", id).Scan(&proxy, &marked))
		require.Equal(t, f.original, proxy)
		require.False(t, marked)
		var count int
		require.NoError(t, integrationDB.QueryRow("SELECT count(*) FROM account_proxies WHERE account_id=$1", id).Scan(&count))
		require.Equal(t, 2, count)
	}
	var deleted bool
	require.NoError(t, integrationDB.QueryRow("SELECT deleted_at IS NOT NULL FROM proxies WHERE id=$1", b.ManagedProxyID).Scan(&deleted))
	require.True(t, deleted)
}
func TestOpenAIIPv6RepositoryRequiresDisabledFamily(t *testing.T) {
	f := newOpenAIVMFixture(t)
	repo := &openAIIPv6Repository{db: integrationDB}
	_, err := repo.Reserve(context.Background(), f.parent, fmt.Sprint(f.parent), "http://127.0.0.1", &service.Proxy{})
	require.Error(t, err)
}

func TestOpenAIIPv6RepositoryRestoreRequiresOriginalFallback(t *testing.T) {
	f := newOpenAIVMFixture(t)
	ctx := context.Background()
	repo := &openAIIPv6Repository{db: integrationDB}
	_, err := integrationDB.Exec("UPDATE accounts SET status='inactive' WHERE id=$1 OR parent_account_id=$1", f.parent)
	require.NoError(t, err)
	b, err := repo.Reserve(ctx, f.parent, fmt.Sprintf("sub2-fallback-%d", f.parent), "http://127.0.0.1:18081", &service.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 18080, Username: "v6-sub2-fallback"})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM openai_ipv6_bindings WHERE account_id=$1", f.parent)
		_, _ = integrationDB.Exec("UPDATE accounts SET proxy_id=NULL,extra=extra-'openai_ipv6_bound' WHERE id=$1 OR parent_account_id=$1", f.parent)
		_, _ = integrationDB.Exec("DELETE FROM proxies WHERE id=$1", b.ManagedProxyID)
	})
	require.NoError(t, repo.Activate(ctx, f.parent, "2001:db8::8", strings.Repeat("p", 32)))
	require.NoError(t, repo.SetState(ctx, f.parent, "deleting", ""))
	_, err = integrationDB.Exec("UPDATE proxies SET status='disabled' WHERE id=$1", f.fallback)
	require.NoError(t, err)
	require.ErrorContains(t, repo.Restore(ctx, f.parent), "Restore the original proxy")
	stillBound, err := repo.GetBinding(ctx, f.parent)
	require.NoError(t, err)
	require.NotNil(t, stillBound, "failed restoration must retain the binding")
	_, err = integrationDB.Exec("UPDATE proxies SET status='active' WHERE id=$1", f.fallback)
	require.NoError(t, err)
	require.NoError(t, repo.Restore(ctx, f.parent))
	var restoredFallback int64
	require.NoError(t, integrationDB.QueryRow("SELECT proxy_fallback_origin_id FROM accounts WHERE id=$1", f.parent).Scan(&restoredFallback))
	require.Equal(t, f.fallback, restoredFallback)
}
