//go:build integration

package repository

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestRetiredVMProxyExcludedFromSharedPoolsAndAutomaticSelection(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	// Enable the same full-schema predicate as the public production constructor.
	repo := newProxyRepositoryWithSQL(client, tx)
	repo.ipv6Supported = true
	ordinary, err := client.Proxy.Create().SetName("ordinary-shared-exit").SetProtocol("socks5").SetHost("127.0.0.1").SetPort(1080).SetUsername("sub2-vm-unrelated").SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	owner, err := client.Account.Create().SetName("retired-vm-owner").SetPlatform("openai").SetType("oauth").SetCredentials(map[string]any{}).SetStatus("inactive").SetSchedulable(false).Save(ctx)
	require.NoError(t, err)
	managed, err := client.Proxy.Create().SetName("retired-vm-exit").SetProtocol("http").SetHost("127.0.0.1").SetPort(18090).SetUsername(strconv.FormatInt(owner.ID, 10)).SetStatus(service.StatusActive).Save(ctx)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_vm_bindings(account_id,managed_proxy_id,original_proxy_id,runtime_type,status) VALUES($1,$2,$3,'docker','ready')`, owner.ID, managed.ID, ordinary.ID)
	require.NoError(t, err)
	// The preserved managed proxy is active with a numeric username, exactly
	// like the old VM implementation. The binding must control exclusion.
	active, err := repo.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, ordinary.ID, active[0].ID)
	withCounts, err := repo.ListActiveWithAccountCount(ctx)
	require.NoError(t, err)
	require.Len(t, withCounts, 1)
	require.Equal(t, ordinary.ID, withCounts[0].ID)
	selectionCtx := dbent.NewTxContext(ctx, tx)
	for _, mode := range []string{"random", "least_used"} {
		selected, err := repo.SelectAccountProxy(selectionCtx, mode)
		require.NoError(t, err)
		require.Equal(t, ordinary.ID, selected, mode)
	}
	_, err = client.Proxy.UpdateOneID(ordinary.ID).SetStatus(service.StatusDisabled).Save(ctx)
	require.NoError(t, err)
	for _, mode := range []string{"random", "least_used"} {
		selected, err := repo.SelectAccountProxy(selectionCtx, mode)
		require.NoError(t, err)
		require.Zero(t, selected, "the retired proxy must not be the last-resort candidate: %s", mode)
	}
	// Recovery tooling can still inspect the retained record by explicit ID.
	preserved, err := repo.GetByID(ctx, managed.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, preserved.Status)
	require.Equal(t, strconv.FormatInt(owner.ID, 10), preserved.Username)
}

func TestRetiredVMRouteMigrationPreservesOrdinaryRoutes(t *testing.T) {
	ctx := context.Background()
	conn, err := integrationDB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	schema := fmt.Sprintf("retired_vm_%d", time.Now().UnixNano())
	_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer func() { _, _ = conn.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") }()
	_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	defer func() { _, _ = conn.ExecContext(ctx, "SET search_path TO public") }()
	_, err = conn.ExecContext(ctx, `
CREATE TABLE accounts (id BIGINT PRIMARY KEY,parent_account_id BIGINT,proxy_id BIGINT,proxy_fallback_origin_id BIGINT,
 platform TEXT DEFAULT 'openai',type TEXT DEFAULT 'oauth',extra JSONB DEFAULT '{}',credentials JSONB DEFAULT '{"access_token":"keep"}',
 status TEXT DEFAULT 'active',schedulable BOOLEAN DEFAULT TRUE,deleted_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE proxies (id BIGINT PRIMARY KEY,protocol TEXT DEFAULT 'socks5',host TEXT DEFAULT '127.0.0.1',port INT DEFAULT 1080,
 username TEXT DEFAULT '',password TEXT DEFAULT '',status TEXT DEFAULT 'active',expires_at TIMESTAMPTZ,
 fallback_mode TEXT DEFAULT 'none',backup_proxy_id BIGINT,deleted_at TIMESTAMPTZ);
CREATE TABLE account_proxies (account_id BIGINT,proxy_id BIGINT,position INT);
CREATE TABLE scheduler_outbox (event_type TEXT,account_id BIGINT);
INSERT INTO proxies(id) VALUES(7),(8),(20);
INSERT INTO accounts(id,proxy_id,parent_account_id,extra) VALUES
 (1,7,NULL,'{"keep":true}'), (2,20,NULL,'{}'), (3,20,2,'{}'),
 (4,8,NULL,'{"openai_vm_bound":true}'), (5,8,4,'{}'), (6,20,NULL,'{}');
`)
	require.NoError(t, err)
	for _, name := range []string{"308_openai_vm_bindings.sql"} {
		raw, readErr := dbmigrations.FS.ReadFile(name)
		require.NoError(t, readErr)
		_, err = conn.ExecContext(ctx, string(raw))
		require.NoError(t, err)
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO openai_vm_bindings(account_id,managed_proxy_id,original_proxy_id,original_proxy_ids,runtime_type,slot_id,status) VALUES(2,20,7,'[7]','docker','preserved-slot','ready'); UPDATE openai_vm_config SET enabled=TRUE,base_url='http://127.0.0.1:1234',encrypted_secret='preserved-ciphertext';`)
	require.NoError(t, err)
	var ordinaryBefore, bindingsBefore, configBefore string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT to_jsonb(a)::text FROM accounts a WHERE id=1").Scan(&ordinaryBefore))
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT to_jsonb(b)::text FROM openai_vm_bindings b").Scan(&bindingsBefore))
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT to_jsonb(c)::text FROM openai_vm_config c").Scan(&configBefore))
	raw, err := dbmigrations.FS.ReadFile("353_retire_openai_vm_bridge.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(raw))
	require.NoError(t, err)
	for query, want := range map[string]string{
		"SELECT to_jsonb(a)::text FROM accounts a WHERE id=1": ordinaryBefore,
		"SELECT to_jsonb(b)::text FROM openai_vm_bindings b":  bindingsBefore,
		"SELECT to_jsonb(c)::text FROM openai_vm_config c":    configBefore,
	} {
		var got string
		require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&got))
		require.JSONEq(t, want, got)
	}
	for query, want := range map[string]int{
		"SELECT count(*) FROM accounts WHERE id<>1 AND status='inactive' AND NOT schedulable AND extra->>'openai_vm_retired'='true'": 5,
		"SELECT count(*) FROM openai_vm_retirement_audit WHERE previous_status='active' AND previous_schedulable":                    5,
		"SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed'":                                                   5,
		"SELECT count(*) FROM accounts WHERE credentials->>'access_token'='keep'":                                                    6,
	} {
		var got int
		require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&got))
		require.Equal(t, want, got, query)
	}
	for _, query := range []string{
		"UPDATE accounts SET status='active' WHERE id=2",
		"UPDATE accounts SET schedulable=TRUE WHERE id=4",
		"UPDATE accounts SET extra=extra-'openai_vm_retired' WHERE id=4",
		"UPDATE accounts SET proxy_id=20 WHERE id=1",
		"INSERT INTO account_proxies(account_id,proxy_id,position) VALUES(1,20,0)",
		"UPDATE proxies SET backup_proxy_id=20 WHERE id=7",
	} {
		_, err = conn.ExecContext(ctx, query)
		require.Error(t, err, query)
	}
	// New shadows inherit the retired state; their normal default cannot enable VM.
	_, err = conn.ExecContext(ctx, "INSERT INTO accounts(id,parent_account_id) VALUES(9,2)")
	require.NoError(t, err)
	var disabled bool
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT status='inactive' AND NOT schedulable AND proxy_id=20 FROM accounts WHERE id=9").Scan(&disabled))
	require.True(t, disabled)
	// Explicit recovery is two stages: restore while disabled, then clear the
	// retirement marker after removing the legacy binding. No enable is implicit.
	_, err = conn.ExecContext(ctx, `DELETE FROM openai_vm_bindings WHERE account_id=2;
UPDATE accounts SET proxy_id=7,extra=extra-'openai_vm_bound' WHERE id IN(2,3,9);
UPDATE accounts SET extra=extra-'openai_vm_retired' WHERE id=2;
UPDATE accounts SET extra=extra-'openai_vm_retired' WHERE id IN(3,9);`)
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT status='inactive' AND NOT schedulable AND proxy_id=7 FROM accounts WHERE id=2").Scan(&disabled))
	require.True(t, disabled)
}
