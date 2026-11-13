//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ovhipv6"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type ovhTestHost struct {
	mu                           sync.Mutex
	issued, activated, deleted   int
	draining, fail, failActivate bool
	active                       map[string]bool
}

// Use the repository's disposable PostgreSQL harness with every migration,
// including the legacy IPv6/pool constraints. Never apply a reduced schema.
func ovhRepoFixture(t *testing.T) (*sql.DB, *proxyRepository, *ovhTestHost) {
	t.Helper()
	db, client := integrationDB, testEntClient(t)
	host := &ovhTestHost{active: map[string]bool{}}
	token := strings.Repeat("test-control-", 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host.mu.Lock()
		defer host.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		if host.fail || (host.failActivate && strings.HasSuffix(r.URL.Path, "/activate")) {
			w.WriteHeader(503)
			return
		}
		var out any
		switch {
		case r.URL.Path == "/v1/status":
			out = ovhipv6.Status{Available: true, Prefix: "2001:db8:205:b900::/56", Issued: strconv.Itoa(host.issued)}
		case r.Method == "POST" && r.URL.Path == "/v1/leases":
			host.issued++
			id := strconv.Itoa(host.issued)
			address, err := ovhipv6.Address("2001:db8:205:b900::/56", id)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			host.active[id] = true
			out = ovhipv6.Lease{ID: id, Address: address.String(), Username: "ovh6_" + id, Password: strings.Repeat("test-proxy-", 4), State: "pending", Host: "127.0.0.1", Port: 11880}
		case strings.HasSuffix(r.URL.Path, "/activate"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/leases/"), "/activate")
			if !host.active[id] {
				w.WriteHeader(404)
				return
			}
			host.activated++
			out = ovhipv6.Lease{ID: id, State: "active"}
		case r.Method == "DELETE":
			host.deleted++
			id := strings.TrimPrefix(r.URL.Path, "/v1/leases/")
			state := "retired"
			if host.draining {
				state = "retiring"
			} else {
				delete(host.active, id)
			}
			out = ovhipv6.Lease{ID: id, State: state}
		default:
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	var wasEnabled bool
	require.NoError(t, db.QueryRow(`SELECT enabled FROM ovh_ipv6_settings WHERE id=1`).Scan(&wasEnabled))
	repo := newProxyRepositoryWithSQL(client, db)
	repo.ipv6Supported = true
	repo.ipv6 = &ovhipv6.Client{BaseURL: server.URL, Token: token, HTTP: server.Client()}
	t.Cleanup(func() {
		// Only rows created by this fixture are removed, even after a failed test.
		_, err := db.Exec(`UPDATE accounts SET status='inactive' WHERE name=$1`, ovhAccountName(t))
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM scheduler_outbox WHERE account_id IN(SELECT id FROM accounts WHERE name=$1)`, ovhAccountName(t))
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM accounts WHERE name=$1 AND parent_account_id IS NOT NULL`, ovhAccountName(t))
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM accounts WHERE name=$1`, ovhAccountName(t))
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE ovh_ipv6_leases SET state='retiring' WHERE control_url=$1`, server.URL)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM proxies WHERE id IN(SELECT proxy_id FROM ovh_ipv6_leases WHERE control_url=$1)`, server.URL)
		require.NoError(t, err)
		_, err = db.Exec(`DELETE FROM ovh_ipv6_leases WHERE control_url=$1`, server.URL)
		require.NoError(t, err)
		_, err = db.Exec(`UPDATE ovh_ipv6_settings SET enabled=$1 WHERE id=1`, wasEnabled)
		require.NoError(t, err)
	})
	_, err := repo.SetOVHIPv6(context.Background(), true)
	require.NoError(t, err)
	return db, repo, host
}
func ovhAccountName(t *testing.T) string { return "ovh-integration-" + t.Name() }
func ovhCreateAccount(t *testing.T, client *dbent.Client, proxyID int64, parent *int64) (*dbent.Account, error) {
	t.Helper()
	b := client.Account.Create().SetName(ovhAccountName(t)).SetPlatform("openai").SetType("oauth").SetCredentials(map[string]any{"access_token": "synthetic"}).SetExtra(map[string]any{}).SetProxyID(proxyID).SetStatus("inactive").SetSchedulable(false)
	if parent != nil {
		b.SetParentAccountID(*parent).SetQuotaDimension("spark")
	}
	return b.Save(context.Background())
}
func ovhAllocateAccount(t *testing.T, repo *proxyRepository) (*dbent.Account, int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := repo.client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	id, err := repo.SelectAccountProxy(dbent.NewTxContext(ctx, tx), "ovh_ipv6")
	require.NoError(t, err)
	account, err := ovhCreateAccount(t, tx.Client(), id, nil)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return account, id
}
func ovhSnapshot(t *testing.T, repo *proxyRepository, id int64) *service.Account {
	t.Helper()
	a, err := repo.client.Account.Get(context.Background(), id)
	require.NoError(t, err)
	out := accountEntityToService(a)
	out.ProxyID = a.ProxyID
	out.Proxy, err = repo.GetByID(context.Background(), *a.ProxyID)
	require.NoError(t, err)
	return out
}

func TestOVHIPv6LifecycleFamilyDrainAndWorker(t *testing.T) {
	db, r, host := ovhRepoFixture(t)
	ctx := context.Background()
	account, first := ovhAllocateAccount(t, r)
	shadow, err := ovhCreateAccount(t, r.client, first+100000, &account.ID)
	require.NoError(t, err)
	require.NoError(t, r.CheckOVHIPv6Route(ctx, ovhSnapshot(t, r, account.ID)))
	require.NoError(t, r.CheckOVHIPv6Route(ctx, ovhSnapshot(t, r, shadow.ID)))
	replacement, err := r.AllocateOVHIPv6(ctx, "replacement")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET status='active' WHERE id=$1`, account.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET proxy_id=$2 WHERE id=$1`, account.ID, replacement.ID)
	require.ErrorContains(t, err, "Disable")
	_, err = db.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID)
	require.ErrorContains(t, err, "Disable")
	_, err = db.Exec(`UPDATE accounts SET status='inactive' WHERE id=$1`, account.ID)
	require.NoError(t, err)
	stale := ovhSnapshot(t, r, account.ID)
	_, err = db.Exec(`UPDATE accounts SET proxy_id=$2 WHERE id=$1`, account.ID, replacement.ID)
	require.NoError(t, err)
	require.Error(t, r.CheckOVHIPv6Route(ctx, stale))
	var shadowProxy int64
	require.NoError(t, db.QueryRow(`SELECT proxy_id FROM accounts WHERE id=$1`, shadow.ID).Scan(&shadowProxy))
	require.Equal(t, replacement.ID, shadowProxy)
	require.NoError(t, r.CheckOVHIPv6Route(ctx, ovhSnapshot(t, r, account.ID)))
	var state string
	require.NoError(t, db.QueryRow(`SELECT state FROM ovh_ipv6_leases WHERE proxy_id=$1`, first).Scan(&state))
	require.Equal(t, "retiring", state)
	host.mu.Lock()
	host.draining = true
	host.mu.Unlock()
	require.NoError(t, r.SyncOVHIPv6(ctx))
	require.NoError(t, db.QueryRow(`SELECT state FROM ovh_ipv6_leases WHERE proxy_id=$1`, first).Scan(&state))
	require.Equal(t, "retiring", state)
	host.mu.Lock()
	host.draining = false
	host.mu.Unlock()
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET next_attempt_at=NOW() WHERE proxy_id=$1`, first)
	require.NoError(t, err)
	require.NoError(t, r.SyncOVHIPv6(ctx))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases WHERE proxy_id=$1`, first).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE id=$1`, first).Scan(&count))
	require.Zero(t, count)
}

func TestOVHIPv6ExclusiveOwnershipPoolAndBackup(t *testing.T) {
	db, r, _ := ovhRepoFixture(t)
	ctx := context.Background()
	owner, id := ovhAllocateAccount(t, r)
	_, err := ovhCreateAccount(t, r.client, id, nil)
	require.ErrorContains(t, err, "owned")
	reserved, err := r.AllocateOVHIPv6(ctx, "reserved")
	require.NoError(t, err)
	manual, err := r.client.Proxy.Create().SetName(ovhAccountName(t)).SetProtocol("http").SetHost("127.0.0.1").SetPort(8080).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM proxies WHERE id=$1`, manual.ID) })
	for _, mode := range []string{"random", "least_used"} {
		tx, err := r.client.Tx(ctx)
		require.NoError(t, err)
		picked, err := r.SelectAccountProxy(dbent.NewTxContext(ctx, tx), mode)
		require.NoError(t, err)
		require.NoError(t, tx.Rollback())
		p, err := r.GetByID(ctx, picked)
		require.NoError(t, err)
		require.False(t, p.IsManagedIPv6(), "ordinary selection must retain the manual pool")
	}
	for _, query := range []string{`INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)`, `UPDATE proxies SET backup_proxy_id=$2 WHERE id=$1`} {
		first := owner.ID
		if strings.HasPrefix(query, "UPDATE proxies") {
			first = manual.ID
		}
		_, err = db.Exec(query, first, reserved.ID)
		require.Error(t, err)
	}
	_, err = db.Exec(`INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)`, owner.ID, manual.ID)
	require.ErrorContains(t, err, "rotating")
	_, err = db.Exec(`UPDATE proxies SET fallback_mode='direct' WHERE id=$1`, id)
	require.ErrorContains(t, err, "immutable")
	_, err = db.Exec(`UPDATE proxies SET deleted_at=NOW() WHERE id=$1`, id)
	require.ErrorContains(t, err, "owned")
	// Final-state enforcement catches a pre-existing pool when an ordinary account binds.
	ordinary, err := ovhCreateAccount(t, r.client, manual.ID, nil)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO account_proxies(account_id,proxy_id,position) VALUES($1,$2,0)`, ordinary.ID, manual.ID)
	require.NoError(t, err)
	tx, err := r.client.Tx(ctx)
	require.NoError(t, err)
	_, err = tx.Client().ExecContext(ctx, `UPDATE accounts SET proxy_id=$2 WHERE id=$1`, ordinary.ID, reserved.ID)
	require.NoError(t, err)
	require.ErrorContains(t, tx.Commit(), "rotating")
	_, err = db.Exec(`DELETE FROM account_proxies WHERE account_id=$1`, ordinary.ID)
	require.NoError(t, err)
}

func TestOVHIPv6RollbackActivationRetryAndControlIdentity(t *testing.T) {
	db, r, host := ovhRepoFixture(t)
	ctx := context.Background()
	tx, err := r.client.Tx(ctx)
	require.NoError(t, err)
	orphan, err := r.allocateOVHIPv6Lease(dbent.NewTxContext(ctx, tx), "rollback", true)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	host.mu.Lock()
	require.Equal(t, 1, host.issued)
	require.Zero(t, host.activated)
	host.failActivate = true
	host.mu.Unlock()
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM ovh_ipv6_leases WHERE proxy_id=$1`, orphan).Scan(&count))
	require.Zero(t, count)
	account, id := ovhAllocateAccount(t, r)
	require.Error(t, r.CheckOVHIPv6Route(ctx, ovhSnapshot(t, r, account.ID)))
	var leaseID string
	require.NoError(t, db.QueryRow(`SELECT lease_id FROM ovh_ipv6_leases WHERE proxy_id=$1`, id).Scan(&leaseID))
	require.Equal(t, "2", leaseID)
	require.NoError(t, r.SyncOVHIPv6(ctx))
	var message string
	require.NoError(t, db.QueryRow(`SELECT last_error FROM ovh_ipv6_leases WHERE proxy_id=$1`, id).Scan(&message))
	require.NotEmpty(t, message)
	host.mu.Lock()
	host.failActivate = false
	host.mu.Unlock()
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET next_attempt_at=NOW() WHERE proxy_id=$1`, id)
	require.NoError(t, err)
	require.NoError(t, r.SyncOVHIPv6(ctx))
	snapshot := ovhSnapshot(t, r, account.ID)
	require.NoError(t, r.CheckOVHIPv6Route(ctx, snapshot))
	original := r.ipv6.BaseURL
	r.ipv6.BaseURL = "http://127.0.0.1:1"
	require.Error(t, r.CheckOVHIPv6Route(ctx, snapshot))
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET confirmed=FALSE,next_attempt_at=NOW() WHERE proxy_id=$1`, id)
	require.NoError(t, err)
	require.ErrorContains(t, r.SyncOVHIPv6(ctx), "control origin")
	r.ipv6.BaseURL = original
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET next_attempt_at=NOW() WHERE proxy_id=$1`, id)
	require.NoError(t, err)
	require.NoError(t, r.SyncOVHIPv6(ctx))
	host.mu.Lock()
	host.fail = true
	host.mu.Unlock()
	_, err = r.AllocateOVHIPv6(ctx, "outage")
	require.Error(t, err)
	require.NoError(t, r.CheckOVHIPv6Route(ctx, snapshot), "control outage must not interrupt a confirmed proxy data route")
}

func TestOVHIPv6ConcurrentReservationsClaimOnlyOneOwner(t *testing.T) {
	db, r, _ := ovhRepoFixture(t)
	ctx := context.Background()
	p, err := r.AllocateOVHIPv6(ctx, "exclusive")
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := ovhCreateAccount(t, r.client, p.ID, nil); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE proxy_id=$1`, p.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestOVHIPv6CoexistsWithLegacyBindings(t *testing.T) {
	db, r, _ := ovhRepoFixture(t)
	ctx := context.Background()
	f := newManagedRouteFixture(t)
	_, err := db.Exec(`UPDATE accounts SET status='inactive' WHERE id=$1 OR parent_account_id=$1`, f.parent)
	require.NoError(t, err)
	legacy := &openAIIPv6Repository{db: db}
	binding, err := legacy.Reserve(ctx, f.parent, fmt.Sprintf("sub2-coexist-%d", f.parent), "http://127.0.0.1:18081", &service.Proxy{Protocol: "http", Host: "127.0.0.1", Port: 18080, Username: fmt.Sprintf("v6-sub2-coexist-%d", f.parent)})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM openai_ipv6_bindings WHERE account_id=$1`, f.parent)
		_, _ = db.Exec(`UPDATE accounts SET proxy_id=NULL,extra=extra-'openai_ipv6_bound' WHERE id=$1 OR parent_account_id=$1`, f.parent)
		_, _ = db.Exec(`DELETE FROM proxies WHERE id=$1`, binding.ManagedProxyID)
	})
	require.NoError(t, legacy.Activate(ctx, f.parent, "2001:db8::ff", strings.Repeat("p", 32)))
	reserved, err := r.AllocateOVHIPv6(ctx, "independent")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET proxy_id=$2 WHERE id=$1`, f.parent, reserved.ID)
	require.Error(t, err)
	current, err := legacy.GetBinding(ctx, f.parent)
	require.NoError(t, err)
	require.Equal(t, binding.ManagedProxyID, current.ManagedProxyID)
	require.Equal(t, "ready", current.Status)
	_, newID := ovhAllocateAccount(t, r)
	require.NotEqual(t, binding.ManagedProxyID, newID)
}

func TestOVHIPv6AccountRepositoryUsesAllocationTransaction(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%t", commit), func(t *testing.T) {
			db, r, host := ovhRepoFixture(t)
			ctx := context.Background()
			accounts := newAccountRepositoryWithSQL(r.client, db, nil)
			group, err := r.client.Group.Create().SetName(ovhAccountName(t)).SetPlatform("openai").Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() {
				_, _ = db.Exec(`DELETE FROM account_groups WHERE group_id=$1`, group.ID)
				_, _ = db.Exec(`DELETE FROM groups WHERE id=$1`, group.ID)
			})
			tx, err := r.client.Tx(ctx)
			require.NoError(t, err)
			defer tx.Rollback()
			txctx := dbent.NewTxContext(ctx, tx)
			id, err := r.allocateOVHIPv6Lease(txctx, "transaction", false)
			require.NoError(t, err)
			proxy, err := r.GetByID(txctx, id)
			require.NoError(t, err)
			require.Equal(t, id, proxy.ID)
			_, err = r.GetByID(ctx, id)
			require.Error(t, err, "uncommitted allocation must remain invisible outside the transaction")
			account := &service.Account{Name: ovhAccountName(t), Platform: "openai", Type: "oauth", Credentials: map[string]any{"access_token": "synthetic"}, Extra: map[string]any{}, ProxyID: &id, Concurrency: 1, Priority: 50, Status: "inactive", Schedulable: false}
			require.NoError(t, accounts.CreateWithAccountGroups(txctx, account, []service.AccountGroup{{GroupID: group.ID, Priority: 50}}))
			if commit {
				require.NoError(t, tx.Commit())
			} else {
				require.NoError(t, tx.Rollback())
			}
			want := 0
			if commit {
				want = 1
			}
			for _, query := range []string{`SELECT COUNT(*) FROM accounts WHERE id=$1`, `SELECT COUNT(*) FROM account_groups WHERE account_id=$1`, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1`} {
				var n int
				require.NoError(t, db.QueryRow(query, account.ID).Scan(&n))
				require.Equal(t, want, n)
			}
			for _, query := range []string{`SELECT COUNT(*) FROM proxies WHERE id=$1`, `SELECT COUNT(*) FROM ovh_ipv6_leases WHERE proxy_id=$1`} {
				var n int
				require.NoError(t, db.QueryRow(query, id).Scan(&n))
				require.Equal(t, want, n)
			}
			host.mu.Lock()
			require.Equal(t, want, host.activated)
			host.mu.Unlock()
			if commit {
				require.NoError(t, r.CheckOVHIPv6Route(ctx, ovhSnapshot(t, r, account.ID)))
			}
		})
	}
}

func TestOVHIPv6AccountRepositoryUpdateRollsBackRouteAndAllocation(t *testing.T) {
	db, r, _ := ovhRepoFixture(t)
	ctx := context.Background()
	owner, first := ovhAllocateAccount(t, r)
	accounts := newAccountRepositoryWithSQL(r.client, db, nil)
	tx, err := r.client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()
	txctx := dbent.NewTxContext(ctx, tx)
	replacement, err := r.allocateOVHIPv6Lease(txctx, "replacement", false)
	require.NoError(t, err)
	_, err = r.GetByID(txctx, replacement)
	require.NoError(t, err)
	account, err := accounts.GetByID(txctx, owner.ID)
	require.NoError(t, err)
	account.ProxyID = &replacement
	account.ProxyIDs = nil
	account.Proxy = nil
	require.NoError(t, accounts.Update(txctx, account))
	require.NoError(t, tx.Rollback())
	actual := ovhSnapshot(t, r, owner.ID)
	require.Equal(t, first, *actual.ProxyID)
	require.NoError(t, r.CheckOVHIPv6Route(ctx, actual))
	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM proxies WHERE id=$1`, replacement).Scan(&n))
	require.Zero(t, n)
	var state string
	require.NoError(t, db.QueryRow(`SELECT state FROM ovh_ipv6_leases WHERE proxy_id=$1`, first).Scan(&state))
	require.Equal(t, "bound", state)
}

// The provider's triggers must not normalize unrelated historical JSON values
// or queue deferred events for ordinary account writes.
func TestOVHIPv6OrdinaryAccountExtraRemainsUnchanged(t *testing.T) {
	tx := testTx(t)
	_, err := tx.Exec(`ALTER TABLE accounts ALTER COLUMN extra DROP NOT NULL`)
	require.NoError(t, err)
	for _, raw := range []any{nil, `null`, `"legacy"`, `42`, `[1,"old"]`, `{"ordinary":true}`} {
		var id int64
		require.NoError(t, tx.QueryRow(`INSERT INTO accounts(name,platform,type,credentials,extra,status) VALUES($1,'grok','oauth','{}',$2::jsonb,'inactive') RETURNING id`, ovhAccountName(t), raw).Scan(&id))
		_, err = tx.Exec(`UPDATE accounts SET name=name WHERE id=$1`, id)
		require.NoError(t, err)
		var actual sql.NullString
		require.NoError(t, tx.QueryRow(`SELECT extra::text FROM accounts WHERE id=$1`, id).Scan(&actual))
		if raw == nil {
			require.False(t, actual.Valid)
		} else {
			require.True(t, actual.Valid)
			require.JSONEq(t, raw.(string), actual.String)
		}
	}
	_, err = tx.Exec(`ALTER TABLE accounts ALTER COLUMN extra DROP NOT NULL`)
	require.NoError(t, err, "ordinary writes must not leave managed-route deferred trigger events")
}

type ovhAdminConcurrencyCache struct {
	service.ConcurrencyCache
	counts map[int64]int
}

func (c *ovhAdminConcurrencyCache) GetAccountConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(ids))
	for _, id := range ids {
		out[id] = c.counts[id]
	}
	return out, nil
}

func TestOVHIPv6AdminServiceCreateEditDrainBulkAndDetach(t *testing.T) {
	db, proxies, host := ovhRepoFixture(t)
	ctx := context.Background()
	client := proxies.client
	accounts := newAccountRepositoryWithSQL(client, db, nil)
	admin := service.NewAdminService(nil, nil, NewAdminGroupRepository(client, db), accounts, proxies, nil, nil, nil, nil, nil, nil, nil, nil, client, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	cache := &ovhAdminConcurrencyCache{counts: map[int64]int{}}
	service.ConfigureManagedIPv6Concurrency(admin, service.NewConcurrencyService(cache))
	input := func() *service.CreateAccountInput {
		return &service.CreateAccountInput{Name: ovhAccountName(t), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic"}, Extra: map[string]any{}, Concurrency: 1, Priority: 50, SkipDefaultGroupBind: true}
	}
	firstInput := input()
	firstInput.ProxyMode = "ovh_ipv6"
	first, err := admin.CreateAccount(ctx, firstInput)
	require.NoError(t, err)
	require.NotNil(t, first.ProxyID)
	reserved, err := proxies.AllocateOVHIPv6(ctx, "reserved-create")
	require.NoError(t, err)
	secondInput := input()
	secondInput.ProxyID = &reserved.ID
	second, err := admin.CreateAccount(ctx, secondInput)
	require.NoError(t, err)
	require.Equal(t, reserved.ID, *second.ProxyID)
	first, err = accounts.GetByID(ctx, first.ID)
	require.NoError(t, err)
	second, err = accounts.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.NoError(t, proxies.CheckOVHIPv6Route(ctx, first))
	require.NoError(t, proxies.CheckOVHIPv6Route(ctx, second))
	notes := "keep same route"
	_, err = admin.UpdateAccount(ctx, first.ID, &service.UpdateAccountInput{Notes: &notes, ProxyID: first.ProxyID})
	require.NoError(t, err, "active same-route metadata edits must stay available")
	shadow, err := ovhCreateAccount(t, client, *first.ProxyID, &first.ID)
	require.NoError(t, err)
	_, err = admin.UpdateAccount(ctx, first.ID, &service.UpdateAccountInput{Status: service.StatusDisabled})
	require.NoError(t, err)
	cache.counts[first.ID] = 1
	_, err = admin.UpdateAccount(ctx, first.ID, &service.UpdateAccountInput{ProxyMode: "ovh_ipv6"})
	require.ErrorContains(t, err, "在途请求")
	zero := int64(0)
	_, err = admin.UpdateAccount(ctx, first.ID, &service.UpdateAccountInput{ProxyID: &zero})
	require.ErrorContains(t, err, "在途请求", "legacy proxy_id unbind requests must also drain")
	delete(cache.counts, first.ID)
	_, err = admin.UpdateAccount(ctx, second.ID, &service.UpdateAccountInput{Status: service.StatusDisabled})
	require.NoError(t, err)
	old := map[int64]int64{first.ID: *first.ProxyID, second.ID: *second.ProxyID}
	priority := 73
	result, err := admin.BulkUpdateAccounts(ctx, &service.BulkUpdateAccountsInput{AccountIDs: []int64{first.ID, second.ID}, ProxyMode: "ovh_ipv6", Priority: &priority})
	require.NoError(t, err)
	require.Equal(t, 2, result.Success, "%+v", result.Results)
	require.Zero(t, result.Failed)
	unique := map[int64]bool{}
	for _, id := range []int64{first.ID, second.ID} {
		current, err := accounts.GetByID(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, current.ProxyID)
		require.NotEqual(t, old[id], *current.ProxyID)
		require.False(t, unique[*current.ProxyID])
		unique[*current.ProxyID] = true
		require.Equal(t, priority, current.Priority)
		require.NoError(t, proxies.CheckOVHIPv6Route(ctx, current))
		var retired bool
		require.NoError(t, db.QueryRow(`SELECT state='retiring' FROM ovh_ipv6_leases WHERE proxy_id=$1`, old[id]).Scan(&retired))
		require.True(t, retired)
	}
	first, err = accounts.GetByID(ctx, first.ID)
	require.NoError(t, err)
	shadowNow, err := accounts.GetByID(ctx, shadow.ID)
	require.NoError(t, err)
	require.Equal(t, *first.ProxyID, *shadowNow.ProxyID)
	manual, err := client.Proxy.Create().SetName(ovhAccountName(t)).SetProtocol("http").SetHost("127.0.0.1").SetPort(8080).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM proxies WHERE id=$1`, manual.ID) })
	second, err = accounts.GetByID(ctx, second.ID)
	require.NoError(t, err)
	mixed := []int64{manual.ID, *second.ProxyID}
	_, err = admin.UpdateAccount(ctx, second.ID, &service.UpdateAccountInput{ProxyIDs: &mixed})
	require.Error(t, err, "mixed pools must reject, never silently shrink to one route")
	actual, err := accounts.GetByID(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, *second.ProxyID, *actual.ProxyID)
	mixedCreate := input()
	mixedCreate.ProxyIDs = mixed
	_, err = admin.CreateAccount(ctx, mixedCreate)
	require.Error(t, err)
	_, err = admin.BulkUpdateAccounts(ctx, &service.BulkUpdateAccountsInput{AccountIDs: []int64{second.ID}, ProxyIDs: &mixed})
	require.Error(t, err)
	// The custom fork's OAuth duplication prohibition remains intact.
	_, err = admin.DuplicateAccount(ctx, second.ID, "test-admin", "ovh-oauth-duplicate")
	require.ErrorContains(t, err, "rotating or unsupported credential types")
	cache.counts[shadow.ID] = 1
	_, err = admin.UpdateAccount(ctx, first.ID, &service.UpdateAccountInput{ProxyID: &manual.ID})
	require.ErrorContains(t, err, "在途请求")
	delete(cache.counts, shadow.ID)
	_, err = admin.UpdateAccount(ctx, first.ID, &service.UpdateAccountInput{ProxyID: &manual.ID})
	require.NoError(t, err)
	for _, id := range []int64{first.ID, shadow.ID} {
		current, err := accounts.GetByID(ctx, id)
		require.NoError(t, err)
		require.Equal(t, manual.ID, *current.ProxyID)
		require.False(t, current.IsOVHIPv6Bound())
	}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), strings.Repeat("test-proxy-", 4))
	host.mu.Lock()
	require.Equal(t, 4, host.issued, "rejected operations must not leak newly provisioned leases")
	host.mu.Unlock()
}

func TestOVHIPv6WorkerContinuesCurrentOriginAndBlocksUnsafeAllocation(t *testing.T) {
	db, r, host := ovhRepoFixture(t)
	ctx := context.Background()
	old, err := r.AllocateOVHIPv6(ctx, "old-origin")
	require.NoError(t, err)
	current, err := r.AllocateOVHIPv6(ctx, "current-origin")
	require.NoError(t, err)
	original := r.ipv6.BaseURL
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET control_url='http://127.0.0.1:1',confirmed=FALSE,next_attempt_at=NOW()-INTERVAL '1 minute' WHERE proxy_id=$1`, old.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`UPDATE ovh_ipv6_leases SET control_url=$2 WHERE proxy_id=$1`, old.ID, original)
	})
	_, err = db.Exec(`UPDATE ovh_ipv6_leases SET confirmed=FALSE,next_attempt_at=NOW() WHERE proxy_id=$1`, current.ID)
	require.NoError(t, err)
	_, err = r.AllocateOVHIPv6(ctx, "unsafe-new-origin")
	require.ErrorContains(t, err, "其他控制服务")
	host.mu.Lock()
	require.Equal(t, 2, host.issued, "control identity mismatch must reject before remote provisioning")
	host.mu.Unlock()
	require.ErrorContains(t, r.SyncOVHIPv6(ctx), "control origin")
	var confirmed bool
	require.NoError(t, db.QueryRow(`SELECT confirmed FROM ovh_ipv6_leases WHERE proxy_id=$1`, current.ID).Scan(&confirmed))
	require.True(t, confirmed, "an older foreign-origin job cannot starve matching jobs")
	var delayed bool
	var message string
	require.NoError(t, db.QueryRow(`SELECT confirmed,next_attempt_at>NOW(),last_error FROM ovh_ipv6_leases WHERE proxy_id=$1`, old.ID).Scan(&confirmed, &delayed, &message))
	require.False(t, confirmed)
	require.True(t, delayed)
	require.NotEmpty(t, message)
}
