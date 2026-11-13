package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ovhipv6"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *proxyRepository) ovhIPv6Enabled(ctx context.Context) (bool, error) {
	if !r.ipv6Supported {
		return false, nil
	}
	var enabled bool
	var query sqlQueryer = r.sql
	if tx := dbent.TxFromContext(ctx); tx != nil {
		query = tx.Client()
	}
	err := scanSingleRow(ctx, query, `SELECT enabled FROM ovh_ipv6_settings WHERE id=1`, nil, &enabled)
	return enabled, err
}

func (r *proxyRepository) GetOVHIPv6(ctx context.Context) (*ovhipv6.Status, error) {
	enabled, err := r.ovhIPv6Enabled(ctx)
	if err != nil {
		return nil, err
	}
	s := &ovhipv6.Status{Configured: r.ipv6 != nil, Enabled: enabled, Issued: "0"}
	if r.ipv6 != nil {
		if err := r.ipv6.Call(ctx, http.MethodGet, "/v1/status", nil, s); err != nil {
			s.Error = err.Error()
			s.Available = false
		}
		s.Configured, s.Enabled = true, enabled
	}
	if r.ipv6Supported {
		err = scanSingleRow(ctx, r.sql, `SELECT COUNT(*) FILTER(WHERE state='bound'), COUNT(*) FILTER(WHERE state='retiring') FROM ovh_ipv6_leases`, nil, &s.Bound, &s.Pending)
	}
	return s, err
}

func (r *proxyRepository) SetOVHIPv6(ctx context.Context, enabled bool) (*ovhipv6.Status, error) {
	if enabled {
		status, err := r.GetOVHIPv6(ctx)
		if err != nil {
			return nil, err
		}
		if !status.Configured || !status.Available {
			return nil, fmt.Errorf("OVH IPv6 出口服务未就绪，无法开启自动分配")
		}
	}
	_, err := r.sql.ExecContext(ctx, `UPDATE ovh_ipv6_settings SET enabled=$1, updated_at=NOW() WHERE id=1`, enabled)
	if err != nil {
		return nil, err
	}
	return r.GetOVHIPv6(ctx)
}

// The host commits its monotonic counter before provisioning. A database
// rollback can orphan a pending lease, but can never return that ID to the pool.
func (r *proxyRepository) allocateOVHIPv6(ctx context.Context) (int64, error) {
	return r.allocateOVHIPv6Lease(ctx, "", false)
}

func (r *proxyRepository) AllocateOVHIPv6(ctx context.Context, name string) (*service.Proxy, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	id, err := r.allocateOVHIPv6Lease(txctx, name, true)
	if err != nil {
		return nil, err
	}
	p, err := tx.Client().Proxy.Get(txctx, id)
	if err != nil {
		return nil, err
	}
	out := proxyEntityToService(p)
	out.ManagedIPv6State = "reserved"
	addresses, err := r.ManagedIPv6Addresses(txctx, []int64{id})
	if err != nil {
		return nil, err
	}
	out.ManagedIPv6 = addresses[id]
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *proxyRepository) allocateOVHIPv6Lease(ctx context.Context, name string, reserved bool) (int64, error) {
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return 0, fmt.Errorf("OVH IPv6 分配需要账号写事务")
	}
	enabled, err := r.ovhIPv6Enabled(ctx)
	if err != nil {
		return 0, err
	}
	if !enabled {
		return 0, fmt.Errorf("OVH IPv6 新地址分配尚未启用")
	}
	if r.ipv6 == nil {
		return 0, errors.New("OVH IPv6 出口服务尚未配置")
	}
	var mismatched bool
	if err := scanSingleRow(ctx, tx.Client(), `SELECT EXISTS(SELECT 1 FROM ovh_ipv6_leases WHERE control_url<>$1)`, []any{r.ipv6.BaseURL}, &mismatched); err != nil {
		return 0, err
	}
	if mismatched {
		return 0, errors.New("存在其他控制服务的未清理租约，请恢复原控制服务配置后完成退役")
	}
	var lease ovhipv6.Lease
	if err := r.ipv6.Call(ctx, http.MethodPost, "/v1/leases", nil, &lease); err != nil {
		return 0, err
	}
	addr, err := netip.ParseAddr(lease.Address)
	if err != nil || !addr.Is6() || addr.Is4In6() || !strings.HasPrefix(lease.Username, "ovh6_") || len(lease.Password) < 32 || lease.ID == "" || lease.Host == "" || lease.Port < 1 || lease.Port > 65535 {
		return 0, fmt.Errorf("OVH IPv6 出口服务返回无效的代理配置")
	}
	if name == "" {
		name = "OVH IPv6 · " + lease.Address
	}
	p, err := tx.Client().Proxy.Create().SetName(name).
		SetProtocol("http").SetHost(lease.Host).SetPort(lease.Port).
		SetUsername(lease.Username).SetPassword(lease.Password).
		SetStatus(service.StatusActive).SetFallbackMode(service.FallbackModeNone).SetExpiryWarnDays(0).Save(ctx)
	if err != nil {
		return 0, err
	}
	state := "bound"
	if reserved {
		state = "reserved"
	}
	_, err = tx.Client().ExecContext(ctx, `INSERT INTO ovh_ipv6_leases(proxy_id,lease_id,address,state,control_url) VALUES($1,$2,$3::inet,$4,$5)`, p.ID, lease.ID, lease.Address, state, r.ipv6.BaseURL)
	if err != nil {
		return 0, err
	}
	// Claim as soon as the account transaction commits; the durable worker is the
	// retry path if the process exits here. No activation on rolled-back imports.
	service.AfterAccountCommit(ctx, func(context.Context) {
		claimCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var activated ovhipv6.Lease
		if r.ipv6.Call(claimCtx, http.MethodPost, "/v1/leases/"+lease.ID+"/activate", nil, &activated) == nil && activated.State == "active" {
			_, _ = r.sql.ExecContext(claimCtx, `UPDATE ovh_ipv6_leases SET confirmed=TRUE,last_error='' WHERE proxy_id=$1 AND state IN ('reserved','bound')`, p.ID)
		}
	})
	return p.ID, nil
}

func (r *proxyRepository) ManagedIPv6Addresses(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	details, err := r.ManagedIPv6Details(ctx, ids)
	for id, detail := range details {
		out[id] = detail.Address
	}
	return out, err
}

func (r *proxyRepository) ManagedIPv6Details(ctx context.Context, ids []int64) (map[int64]service.ManagedIPv6Binding, error) {
	out := map[int64]service.ManagedIPv6Binding{}
	if !r.ipv6Supported || len(ids) == 0 {
		return out, nil
	}
	in, args := ovhIPv6IDArgs(ids)
	var query sqlQueryer = r.sql
	if tx := dbent.TxFromContext(ctx); tx != nil {
		query = tx.Client()
	}
	rows, err := query.QueryContext(ctx, `SELECT proxy_id,host(address),state FROM ovh_ipv6_leases WHERE proxy_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var detail service.ManagedIPv6Binding
		if err := rows.Scan(&id, &detail.Address, &detail.State); err != nil {
			return nil, err
		}
		out[id] = detail
	}
	return out, rows.Err()
}

// SyncOVHIPv6 only consumes durable unfinished work; it never enumerates the
// address space or writes a historical row for every retired IPv6.
func (r *proxyRepository) SyncOVHIPv6(ctx context.Context) error {
	if !r.ipv6Supported {
		return nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT proxy_id,lease_id,state,control_url FROM ovh_ipv6_leases
        WHERE (NOT confirmed OR state='retiring') AND next_attempt_at<=NOW()
          AND (owner_account_id IS NOT NULL OR owner_moderation_key IS NOT NULL OR state IN ('reserved','retiring'))
        ORDER BY next_attempt_at LIMIT 100`)
	if err != nil {
		return err
	}
	type job struct {
		proxyID            int64
		id, state, control string
	}
	jobs := []job{}
	for rows.Next() {
		var j job
		if err = rows.Scan(&j.proxyID, &j.id, &j.state, &j.control); err != nil {
			break
		}
		jobs = append(jobs, j)
	}
	readErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	var identityErr error
	for _, j := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if r.ipv6 == nil || r.ipv6.BaseURL != j.control {
			identityErr = errors.New("OVH IPv6 control origin differs from a lease; its cleanup is paused")
			if _, err := r.sql.ExecContext(ctx, `UPDATE ovh_ipv6_leases SET last_error=$2,next_attempt_at=NOW()+INTERVAL '1 minute' WHERE proxy_id=$1`, j.proxyID, identityErr.Error()); err != nil {
				return err
			}
			continue
		}
		method, path := http.MethodPost, "/v1/leases/"+j.id+"/activate"
		if j.state == "retiring" {
			method, path = http.MethodDelete, "/v1/leases/"+j.id
		}
		var result ovhipv6.Lease
		callErr := r.ipv6.Call(ctx, method, path, nil, &result)
		if callErr != nil {
			_, err = r.sql.ExecContext(ctx, `UPDATE ovh_ipv6_leases SET last_error=$2,next_attempt_at=NOW()+INTERVAL '15 seconds' WHERE proxy_id=$1`, j.proxyID, callErr.Error())
		} else if (j.state == "bound" || j.state == "reserved") && result.State == "active" {
			_, err = r.sql.ExecContext(ctx, `UPDATE ovh_ipv6_leases SET confirmed=TRUE,last_error='' WHERE proxy_id=$1 AND state IN ('reserved','bound')`, j.proxyID)
		} else if j.state == "retiring" && result.State == "retired" {
			// Purge only our retired rows; remaining live shadow references must
			// disappear before cleanup can complete. No manual proxy is affected.
			_, err = r.sql.ExecContext(ctx, `WITH doomed AS (
                SELECT proxy_id FROM ovh_ipv6_leases l WHERE proxy_id=$1 AND state='retiring'
                AND NOT EXISTS(SELECT 1 FROM accounts a WHERE a.proxy_id=l.proxy_id AND a.deleted_at IS NULL)
                AND NOT EXISTS(SELECT 1 FROM account_proxies ap WHERE ap.proxy_id=l.proxy_id)
 AND NOT EXISTS(SELECT 1 FROM proxies p WHERE p.backup_proxy_id=l.proxy_id AND p.deleted_at IS NULL)
 FOR UPDATE), removed AS (DELETE FROM proxies WHERE id IN(SELECT proxy_id FROM doomed) RETURNING id)
                DELETE FROM ovh_ipv6_leases WHERE proxy_id IN(SELECT proxy_id FROM doomed)`, j.proxyID)
		} else {
			_, err = r.sql.ExecContext(ctx, `UPDATE ovh_ipv6_leases SET next_attempt_at=NOW()+INTERVAL '5 seconds' WHERE proxy_id=$1`, j.proxyID)
		}
		if err != nil {
			return err
		}
	}
	return identityErr
}

func ovhIPv6IDArgs(ids []int64) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	return strings.Join(placeholders, ","), args
}

// Check both current DB ownership and the scheduler snapshot before dispatch.
// This also catches a pre-binding snapshot which has no managed marker yet.
func (r *proxyRepository) CheckOVHIPv6Route(ctx context.Context, a *service.Account) error {
	if a == nil {
		return nil
	}
	if !r.ipv6Supported {
		if a.IsOVHIPv6Bound() {
			return errors.New("OVH IPv6 route verification unavailable")
		}
		return nil
	}
	var managed, valid bool
	control := ""
	if r.ipv6 != nil {
		control = r.ipv6.BaseURL
	}
	var proxyID int64
	if a.ProxyID != nil {
		proxyID = *a.ProxyID
	}
	// Credentials are compared below against a freshly loaded proxy; never logged.
	err := scanSingleRow(ctx, r.sql, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN ovh_ipv6_leases l ON l.proxy_id=a.proxy_id WHERE a.id=$1),
 EXISTS(SELECT 1 FROM accounts a JOIN ovh_ipv6_leases l ON l.proxy_id=a.proxy_id JOIN proxies p ON p.id=l.proxy_id
 WHERE a.id=$1 AND a.deleted_at IS NULL AND a.proxy_id=$2 AND l.state='bound' AND l.confirmed
 AND l.owner_account_id=COALESCE(a.parent_account_id,a.id) AND l.owner_moderation_key IS NULL AND l.control_url=$3
 AND p.status='active' AND p.deleted_at IS NULL AND p.fallback_mode='none' AND p.backup_proxy_id IS NULL
 AND a.proxy_fallback_origin_id IS NULL AND NOT EXISTS(SELECT 1 FROM account_proxies ap WHERE ap.account_id=a.id))`, []any{a.ID, proxyID, control}, &managed, &valid)
	if err != nil {
		return errors.New("cannot verify OVH IPv6 route")
	}
	if !managed && !a.IsOVHIPv6Bound() {
		return nil
	}
	if !valid || r.ipv6 == nil || a.Proxy == nil || !a.Proxy.IsOVHIPv6() {
		return errors.New("OVH IPv6 route is stale, unconfirmed or unavailable; direct fallback disabled")
	}
	current, err := r.GetByID(ctx, proxyID)
	if err != nil || current.URL() != a.Proxy.URL() {
		return errors.New("OVH IPv6 proxy snapshot is stale")
	}
	return nil
}

func (r *proxyRepository) CheckModerationIPv6Route(ctx context.Context, hash string, id int64) error {
	if !r.ipv6Supported || r.ipv6 == nil {
		return errors.New("moderation IPv6 provider unavailable")
	}
	var valid bool
	err := scanSingleRow(ctx, r.sql, `SELECT EXISTS(SELECT 1 FROM ovh_ipv6_leases l JOIN proxies p ON p.id=l.proxy_id
 WHERE l.proxy_id=$1 AND l.owner_moderation_key=$2 AND l.owner_account_id IS NULL AND l.state='bound' AND l.confirmed AND l.control_url=$3
 AND p.status='active' AND p.deleted_at IS NULL AND p.fallback_mode='none' AND p.backup_proxy_id IS NULL)`, []any{id, hash, r.ipv6.BaseURL}, &valid)
	if err != nil || !valid {
		return errors.New("moderation IPv6 route unconfirmed, stale or unavailable")
	}
	return nil
}
