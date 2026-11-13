package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type openAIIPv6Repository struct{ db *sql.DB }

func NewOpenAIIPv6Repository(db *sql.DB) service.OpenAIIPv6Repository {
	return &openAIIPv6Repository{db: db}
}

// Transaction-scoped advisory locks cannot leak when a process/request dies.
// The global shared lock prevents config changes during account operations.
func (r *openAIIPv6Repository) WithLock(ctx context.Context, id int64, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep ownership until cancellation cleanup has finished. A transaction
	// using the HTTP request context would auto-unlock before fail() can persist
	// the operation outcome, allowing it to overwrite a newer retry.
	lockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 150*time.Second)
	defer cancel()
	tx, err := r.db.BeginTx(lockCtx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var ok bool
	globalSQL := `SELECT pg_try_advisory_xact_lock_shared(1828121102,0)`
	if id == 0 {
		globalSQL = `SELECT pg_try_advisory_xact_lock(1828121102,0)`
	}
	if err = tx.QueryRowContext(lockCtx, globalSQL).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return infraerrors.Conflict("OPENAI_IPV6_BUSY", "IPv6 configuration is being changed; retry shortly")
	}
	if id != 0 {
		if err = tx.QueryRowContext(lockCtx, `SELECT pg_try_advisory_xact_lock($1)`, int64(0x5636000000000000)+id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return infraerrors.Conflict("OPENAI_IPV6_BUSY", "An operation is already running for this account")
		}
	}
	if err = fn(); err != nil {
		return err
	}
	return tx.Commit()
}

const openAIIPv6BindingSelect = `SELECT account_id,managed_proxy_id,original_proxy_id,original_proxy_ids,original_fallback_id,lease_id,address,control_url,status,last_error,created_at,updated_at FROM openai_ipv6_bindings`

func scanOpenAIIPv6Binding(row interface{ Scan(...any) error }) (*service.OpenAIIPv6Binding, error) {
	b := &service.OpenAIIPv6Binding{}
	var ids []byte
	err := row.Scan(&b.AccountID, &b.ManagedProxyID, &b.OriginalProxyID, &ids, &b.OriginalFallbackID, &b.LeaseID, &b.Address, &b.ControlURL, &b.Status, &b.LastError, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(ids, &b.OriginalProxyIDs); err != nil {
		return nil, err
	}
	return b, nil
}
func (r *openAIIPv6Repository) GetBinding(ctx context.Context, id int64) (*service.OpenAIIPv6Binding, error) {
	return scanOpenAIIPv6Binding(r.db.QueryRowContext(ctx, openAIIPv6BindingSelect+` WHERE account_id=$1`, id))
}

func (r *openAIIPv6Repository) Reserve(ctx context.Context, id int64, leaseID, controlURL string, p *service.Proxy) (*service.OpenAIIPv6Binding, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var platform, kind, status string
	var vm bool
	var parent, original, fallback *int64
	err = tx.QueryRowContext(ctx, `SELECT platform,type,status,(COALESCE(extra->>'openai_vm_bound','false')='true' OR COALESCE(extra->>'openai_vm_retired','false')='true' OR COALESCE(extra->>'ovh_ipv6_bound','false')='true'),parent_account_id,proxy_id,proxy_fallback_origin_id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&platform, &kind, &status, &vm, &parent, &original, &fallback)
	if err != nil {
		return nil, err
	}
	if platform != service.PlatformOpenAI || kind != service.AccountTypeOAuth || parent != nil || vm || (status != service.StatusDisabled && status != "inactive") {
		return nil, infraerrors.BadRequest("OPENAI_IPV6_INELIGIBLE", "Only disabled OpenAI OAuth parent accounts without a VM support fixed IPv6")
	}
	var active bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE parent_account_id=$1 AND deleted_at IS NULL AND status NOT IN ('disabled','inactive'))", id).Scan(&active); err != nil {
		return nil, err
	}
	if active {
		return nil, infraerrors.Conflict("OPENAI_IPV6_DRAIN_REQUIRED", "Disable the account and all shadows before changing IPv6 egress")
	}
	rows, err := tx.QueryContext(ctx, `SELECT proxy_id FROM account_proxies WHERE account_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0)
	for rows.Next() {
		var pid int64
		if err = rows.Scan(&pid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, pid)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// Refuse to overwrite a shadow's independently edited route on detach.
	var divergent bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM accounts a WHERE a.parent_account_id=$1 AND a.deleted_at IS NULL AND (a.proxy_id IS DISTINCT FROM $2 OR a.proxy_fallback_origin_id IS DISTINCT FROM $3 OR ARRAY(SELECT proxy_id FROM account_proxies WHERE account_id=a.id ORDER BY position) IS DISTINCT FROM ARRAY(SELECT proxy_id FROM account_proxies WHERE account_id=$1 ORDER BY position)))", id, original, fallback).Scan(&divergent)
	if err != nil {
		return nil, err
	}
	if divergent {
		return nil, infraerrors.Conflict("OPENAI_IPV6_SHADOW_ROUTE_DIFFERS", "Align shadow routes with their parent before binding IPv6")
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO proxies(name,protocol,host,port,username,password,status,fallback_mode,expiry_warn_days,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,'active','none',7,NOW(),NOW()) RETURNING id`, fmt.Sprintf("OpenAI IPv6 account %d", id), p.Protocol, p.Host, p.Port, p.Username, p.Password).Scan(&p.ID)
	if err != nil {
		return nil, err
	}
	// Remove pools before installing the binding, whose trigger forbids pool edits.
	_, err = tx.ExecContext(ctx, `DELETE FROM account_proxies WHERE account_id IN(SELECT id FROM accounts WHERE id=$1 OR parent_account_id=$1)`, id)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO openai_ipv6_bindings(account_id,managed_proxy_id,original_proxy_id,original_proxy_ids,original_fallback_id,lease_id,control_url) VALUES($1,$2,$3,$4::jsonb,$5,$6,$7)`, id, p.ID, original, raw, fallback, leaseID, controlURL)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=NULL,extra=COALESCE(extra,'{}'::jsonb)||'{"openai_ipv6_bound":true}'::jsonb,updated_at=NOW() WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, p.ID)
	if err != nil {
		return nil, err
	}
	if err = enqueueOpenAIIPv6Snapshots(ctx, tx, id); err != nil {
		return nil, err
	}
	b, err := scanOpenAIIPv6Binding(tx.QueryRowContext(ctx, openAIIPv6BindingSelect+` WHERE account_id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return b, nil
}

func (r *openAIIPv6Repository) SetState(ctx context.Context, id int64, status, message string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if status == "deleting" {
		rows, err := tx.QueryContext(ctx, "SELECT status FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL ORDER BY id FOR UPDATE", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var state string
			if err = rows.Scan(&state); err != nil {
				_ = rows.Close()
				return err
			}
			if state != "disabled" && state != "inactive" {
				_ = rows.Close()
				return infraerrors.Conflict("OPENAI_IPV6_DRAIN_REQUIRED", "Disable account and shadows before detaching")
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE openai_ipv6_bindings SET status=$2,last_error=$3,updated_at=NOW() WHERE account_id=$1", id, status, message)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *openAIIPv6Repository) Activate(ctx context.Context, id int64, address, password string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "UPDATE proxies SET password=$2,updated_at=NOW() WHERE id=(SELECT managed_proxy_id FROM openai_ipv6_bindings WHERE account_id=$1)", id, password)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE openai_ipv6_bindings SET address=$2,status='ready',last_error='',updated_at=NOW() WHERE account_id=$1", id, address)
	if err != nil {
		return err
	}
	if err = enqueueOpenAIIPv6Snapshots(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *openAIIPv6Repository) Restore(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Same parent-first lock order as Reserve and the shadow INSERT guard.
	var lockedID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM accounts WHERE id=$1 FOR UPDATE`, id).Scan(&lockedID); err != nil {
		return err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL AND status NOT IN ('disabled','inactive'))", id).Scan(&active); err != nil {
		return err
	}
	if active {
		return infraerrors.Conflict("OPENAI_IPV6_DRAIN_REQUIRED", "Disable account and shadows before detaching")
	}
	b, err := scanOpenAIIPv6Binding(tx.QueryRowContext(ctx, openAIIPv6BindingSelect+` WHERE account_id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	originalRoutes := append(append([]int64{}, b.OriginalProxyIDs...), optionalIPv6ID(b.OriginalProxyID)...)
	originalRoutes = append(originalRoutes, optionalIPv6ID(b.OriginalFallbackID)...)
	for _, pid := range originalRoutes {
		var usable bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM proxies WHERE id=$1 AND deleted_at IS NULL AND status='active' AND (expires_at IS NULL OR expires_at>NOW()))`, pid).Scan(&usable)
		if err != nil {
			return err
		}
		if !usable {
			return infraerrors.Conflict("OPENAI_IPV6_ORIGINAL_PROXY_UNAVAILABLE", "Restore the original proxy before detaching fixed IPv6")
		}
	}
	// The route guard makes this CAS invariant explicit even for manual DB edits.
	var mismatch bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL AND proxy_id IS DISTINCT FROM $2)`, id, b.ManagedProxyID).Scan(&mismatch)
	if err != nil {
		return err
	}
	if mismatch {
		return infraerrors.Conflict("OPENAI_IPV6_ROUTE_CHANGED", "Account route changed; refusing to overwrite it")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM openai_ipv6_bindings WHERE account_id=$1`, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET proxy_id=$2,proxy_fallback_origin_id=$3,extra=COALESCE(extra,'{}'::jsonb)-'openai_ipv6_bound',updated_at=NOW() WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, b.OriginalProxyID, b.OriginalFallbackID)
	if err != nil {
		return err
	}
	for i, pid := range b.OriginalProxyIDs {
		_, err = tx.ExecContext(ctx, `INSERT INTO account_proxies(account_id,proxy_id,position) SELECT id,$2,$3 FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, pid, i)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE proxies SET deleted_at=NOW(),status='disabled',updated_at=NOW() WHERE id=$1`, b.ManagedProxyID)
	if err != nil {
		return err
	}
	if err = enqueueOpenAIIPv6Snapshots(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func enqueueOpenAIIPv6Snapshots(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO scheduler_outbox(event_type,account_id) SELECT $2,id FROM accounts WHERE (id=$1 OR parent_account_id=$1) AND deleted_at IS NULL`, id, service.SchedulerOutboxEventAccountChanged)
	return err
}

func optionalIPv6ID(id *int64) []int64 {
	if id == nil {
		return nil
	}
	return []int64{*id}
}
