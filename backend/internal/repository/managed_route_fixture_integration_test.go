//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// integrationDB is created by integration_harness_test.go in a disposable
// testcontainers PostgreSQL instance. These tests never use an external DSN.
type managedRouteFixture struct {
	parent, shadow, original, fallback int64
}

func newManagedRouteFixture(t *testing.T) *managedRouteFixture {
	t.Helper()
	f := &managedRouteFixture{}
	ctx := context.Background()
	for index, id := range []*int64{&f.original, &f.fallback} {
		require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO proxies(name,protocol,host,port,status,fallback_mode) VALUES($1,'socks5','127.0.0.1',1080,'active','none') RETURNING id`, fmt.Sprintf("managed-route-integration-%d-%d", time.Now().UnixNano(), index)).Scan(id))
	}
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,extra,proxy_id,proxy_fallback_origin_id) VALUES('managed-route-parent','openai','oauth',jsonb_build_object('access_token','synthetic-test-token'),jsonb_build_object('preserved',true),$1,$2) RETURNING id`, f.original, f.fallback).Scan(&f.parent))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM account_proxies WHERE account_id IN(SELECT id FROM accounts WHERE id=$1 OR parent_account_id=$1)`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduler_outbox WHERE account_id IN(SELECT id FROM accounts WHERE id=$1 OR parent_account_id=$1)`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE parent_account_id=$1`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, f.parent)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM proxies WHERE id IN($1,$2)`, f.original, f.fallback)
	})
	f.shadow = f.insertShadow(t, f.original)
	return f
}

func (f *managedRouteFixture) insertShadow(t *testing.T, proxyID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,parent_account_id,proxy_id,quota_dimension) VALUES('managed-route-shadow','openai','oauth','{}','{}',$1,$2,'spark') RETURNING id`, f.parent, proxyID).Scan(&id))
	return id
}
